// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectPullRequestUnavailableDoesNotDisclose(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	_, missingErr := InspectPullRequest(t.Context(), nil, InspectionRequest{Owner: "missing", Repository: "missing", Index: 1})
	_, deniedErr := InspectPullRequest(t.Context(), nil, InspectionRequest{Owner: "org3", Repository: "repo3", Index: 2})
	_, missingPullErr := InspectPullRequest(t.Context(), nil, InspectionRequest{Owner: "user2", Repository: "repo1", Index: 999})

	assert.ErrorIs(t, missingErr, ErrPullRequestInspectionUnavailable)
	assert.ErrorIs(t, deniedErr, ErrPullRequestInspectionUnavailable)
	assert.ErrorIs(t, missingPullErr, ErrPullRequestInspectionUnavailable)
	assert.Equal(t, missingErr.Error(), deniedErr.Error())
	assert.Equal(t, missingErr.Error(), missingPullErr.Error())
}

func TestInspectPullRequestPermissionsAndExactHead(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	pr, err := issues_model.GetPullRequestByIndex(t.Context(), repo.ID, 3)
	require.NoError(t, err)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	metadataOnly, err := InspectPullRequest(t.Context(), user, InspectionRequest{Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index})
	require.NoError(t, err)
	assert.True(t, metadataOnly.Revisions.InternalHeadAvailable)
	assert.True(t, metadataOnly.Revisions.TargetAvailable)
	assert.NotEmpty(t, metadataOnly.Revisions.ComparisonBase)
	assert.Equal(t, "open", metadataOnly.Metadata.State)

	withoutCode := *repo
	withoutCode.Units = []*repo_model.RepoUnit{{RepoID: repo.ID, Type: unit.TypePullRequests, Config: &repo_model.PullRequestsConfig{}}}
	_, err = inspectLoadedPullRequest(t.Context(), user, &withoutCode, pr, InspectionRequest{ChangedFiles: &InspectionPageRequest{}})
	assert.ErrorIs(t, err, ErrPullRequestInspectionUnavailable)

	_, err = InspectPullRequest(t.Context(), user, InspectionRequest{
		Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index,
		ExpectedHeadRevision: strings.Repeat("0", len(metadataOnly.Revisions.InternalHead)),
	})
	assert.ErrorIs(t, err, ErrPullRequestInspectionHeadChanged)
}

func TestInspectPullRequestRevisionStates(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
	require.NoError(t, err)
	defer gitRepo.Close()

	merged, err := InspectPullRequest(t.Context(), user, InspectionRequest{Owner: repo.OwnerName, Repository: repo.Name, Index: 2})
	require.NoError(t, err)
	assert.Equal(t, "merged", merged.Metadata.State)
	assert.NotEmpty(t, merged.Revisions.Merged)
	assert.NotEmpty(t, merged.Revisions.InternalHead)

	open, err := InspectPullRequest(t.Context(), user, InspectionRequest{Owner: repo.OwnerName, Repository: repo.Name, Index: 3})
	require.NoError(t, err)
	pr, err := issues_model.GetPullRequestByIndex(t.Context(), repo.ID, 3)
	require.NoError(t, err)
	originalSource, err := gitRepo.GetRefCommitID(git.RefNameFromBranch(pr.HeadBranch).String())
	require.NoError(t, err)
	originalTarget, err := gitRepo.GetRefCommitID(git.RefNameFromBranch(pr.BaseBranch).String())
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, gitrepo.UpdateRef(context.Background(), repo, git.RefNameFromBranch(pr.HeadBranch).String(), originalSource))
		require.NoError(t, gitrepo.UpdateRef(context.Background(), repo, git.RefNameFromBranch(pr.BaseBranch).String(), originalTarget))
	})

	advancedRevision := open.Revisions.ComparisonBase
	if advancedRevision == open.Revisions.InternalHead {
		advancedRevision = open.Revisions.Target
	}
	require.NotEqual(t, open.Revisions.InternalHead, advancedRevision)
	require.NoError(t, gitrepo.UpdateRef(t.Context(), repo, git.RefNameFromBranch(pr.HeadBranch).String(), advancedRevision))
	advanced, err := InspectPullRequest(t.Context(), user, InspectionRequest{Owner: repo.OwnerName, Repository: repo.Name, Index: 3})
	require.NoError(t, err)
	assert.Equal(t, open.Revisions.InternalHead, advanced.Revisions.InternalHead)
	assert.Equal(t, advancedRevision, advanced.Revisions.LiveSource)
	assert.True(t, advanced.Revisions.LiveSourceDiverged)

	require.NoError(t, gitrepo.RemoveRef(t.Context(), repo, git.RefNameFromBranch(pr.HeadBranch).String()))
	deletedSource, err := InspectPullRequest(t.Context(), user, InspectionRequest{Owner: repo.OwnerName, Repository: repo.Name, Index: 3})
	require.NoError(t, err)
	assert.True(t, deletedSource.Revisions.InternalHeadAvailable)
	assert.False(t, deletedSource.Revisions.LiveSourceAvailable)

	require.NoError(t, gitrepo.RemoveRef(t.Context(), repo, git.RefNameFromBranch(pr.BaseBranch).String()))
	missingTarget, err := InspectPullRequest(t.Context(), user, InspectionRequest{Owner: repo.OwnerName, Repository: repo.Name, Index: 3})
	require.NoError(t, err)
	assert.False(t, missingTarget.Revisions.TargetAvailable)
	assert.NotEmpty(t, missingTarget.Revisions.ComparisonBase)
	assert.Equal(t, open.Revisions.InternalHead, missingTarget.Revisions.InternalHead)
}

func TestPullRequestInspectionCursorBindingAndBounds(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.SecretKey, "inspection-test-key")()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	pr, err := issues_model.GetPullRequestByIndex(t.Context(), repo.ID, 3)
	require.NoError(t, err)
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})

	all, err := InspectPullRequest(t.Context(), user, InspectionRequest{
		Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index,
		ChangedFiles: &InspectionPageRequest{Limit: MaxPullRequestInspectionFileLimit},
	})
	require.NoError(t, err)
	require.NotEmpty(t, all.Files.Files)

	var pagedNames []string
	cursor := ""
	for {
		page, err := InspectPullRequest(t.Context(), user, InspectionRequest{
			Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index,
			ChangedFiles: &InspectionPageRequest{Limit: 1, Cursor: cursor},
		})
		require.NoError(t, err)
		for _, file := range page.Files.Files {
			pagedNames = append(pagedNames, file.Name)
		}
		cursor = page.Files.NextCursor
		if cursor == "" {
			break
		}
	}
	allNames := make([]string, 0, len(all.Files.Files))
	for _, file := range all.Files.Files {
		allNames = append(allNames, file.Name)
	}
	assert.Equal(t, allNames, pagedNames)

	revisions := all.Revisions
	encoded, err := encodePullRequestInspectionCursor("files", repo.ID, pr.ID, revisions, "README.md")
	require.NoError(t, err)
	replacement := "A"
	if strings.HasSuffix(encoded, replacement) {
		replacement = "B"
	}
	tampered := encoded[:len(encoded)-1] + replacement
	_, err = decodePullRequestInspectionCursor(tampered, "files", repo.ID, pr.ID, revisions)
	assert.ErrorIs(t, err, ErrPullRequestInspectionCursor)
	revisions.Target += "stale"
	_, err = decodePullRequestInspectionCursor(encoded, "files", repo.ID, pr.ID, revisions)
	assert.ErrorIs(t, err, ErrPullRequestInspectionCursorStale)

	diff, err := InspectPullRequest(t.Context(), user, InspectionRequest{
		Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index,
		Diff: &InspectionDiffRequest{FileLimit: 1, LinesPerFile: 2, MaxLineCharacters: 8},
	})
	require.NoError(t, err)
	require.LessOrEqual(t, len(diff.Diff.Files), 1)
	for _, file := range diff.Diff.Files {
		for _, section := range file.Sections {
			for _, line := range section.Lines {
				assert.LessOrEqual(t, len(line.Content), 8)
			}
		}
	}

	_, err = InspectPullRequest(t.Context(), user, InspectionRequest{
		Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index,
		Diff: &InspectionDiffRequest{FileLimit: MaxPullRequestInspectionDiffFiles + 1},
	})
	assert.ErrorIs(t, err, ErrPullRequestInspectionLimit)
}

func TestInspectPullRequestChecksUseExactRevisionAndHideActionsURL(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	revision := strings.Repeat("a", 40)
	actionsURL := repo.Link() + "/actions/runs/1/jobs/2"
	require.NoError(t, db.Insert(t.Context(), &git_model.CommitStatus{
		Index: 1, RepoID: repo.ID, SHA: revision, State: commitstatus.CommitStatusSuccess,
		Context: "ci/test", ContextHash: git_model.HashCommitStatusContext("ci/test"), TargetURL: actionsURL,
		Description: strings.Repeat("d", MaxPullRequestInspectionStatusText+1),
	}))

	hidden, err := inspectPullRequestChecks(t.Context(), repo, revision, false)
	require.NoError(t, err)
	require.Len(t, hidden.Checks, 1)
	assert.Equal(t, revision, hidden.Checks[0].Revision)
	assert.Empty(t, hidden.Checks[0].TargetURL)
	assert.Len(t, hidden.Checks[0].Description, MaxPullRequestInspectionStatusText)
	assert.True(t, hidden.Checks[0].Truncated)

	visible, err := inspectPullRequestChecks(t.Context(), repo, revision, true)
	require.NoError(t, err)
	require.Len(t, visible.Checks, 1)
	assert.Equal(t, actionsURL, visible.Checks[0].TargetURL)
}

func TestInspectPullRequestCheckBounds(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	revision := strings.Repeat("b", 40)
	statuses := make([]*git_model.CommitStatus, 0, MaxPullRequestInspectionStatuses+1)
	for i := 1; i <= MaxPullRequestInspectionStatuses+1; i++ {
		contextName := strings.Repeat("x", MaxPullRequestInspectionStatusText+1) + strconv.Itoa(i)
		statuses = append(statuses, &git_model.CommitStatus{
			Index: int64(i), RepoID: repo.ID, SHA: revision, State: commitstatus.CommitStatusSuccess,
			Context: contextName, ContextHash: git_model.HashCommitStatusContext(contextName),
		})
	}
	require.NoError(t, db.Insert(t.Context(), statuses))

	checks, err := inspectPullRequestChecks(t.Context(), repo, revision, true)
	assert.ErrorIs(t, err, ErrPullRequestInspectionLimit)
	require.NotNil(t, checks)
	assert.Equal(t, revision, checks.Revision)
	assert.Empty(t, checks.Checks)
	assert.Empty(t, checks.commitStatuses)
}

func TestInspectPullRequestOverLimitChecksPreservePolicy(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	pr, err := issues_model.GetPullRequestByID(t.Context(), 2)
	require.NoError(t, err)
	metadata, err := InspectPullRequest(t.Context(), user, InspectionRequest{Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index})
	require.NoError(t, err)

	requiredContext := strings.Repeat("required/", MaxPullRequestInspectionStatusText/len("required/")+1)
	require.Greater(t, len(requiredContext), MaxPullRequestInspectionStatusText)
	require.NoError(t, git_model.UpdateProtectBranch(t.Context(), repo, &git_model.ProtectedBranch{
		RepoID: repo.ID, RuleName: pr.BaseBranch, EnableStatusCheck: true, StatusCheckContexts: []string{requiredContext},
	}, git_model.WhitelistOptions{}))
	statuses := make([]*git_model.CommitStatus, 0, MaxPullRequestInspectionStatuses+1)
	statuses = append(statuses, &git_model.CommitStatus{
		Index: 10_000, RepoID: repo.ID, SHA: metadata.Revisions.InternalHead, State: commitstatus.CommitStatusSuccess,
		Context: requiredContext, ContextHash: git_model.HashCommitStatusContext(requiredContext),
	})
	for i := range MaxPullRequestInspectionStatuses {
		contextName := "optional/" + strconv.Itoa(i)
		statuses = append(statuses, &git_model.CommitStatus{
			Index: int64(i + 1), RepoID: repo.ID, SHA: metadata.Revisions.InternalHead, State: commitstatus.CommitStatusSuccess,
			Context: contextName, ContextHash: git_model.HashCommitStatusContext(contextName),
		})
	}
	require.NoError(t, db.Insert(t.Context(), statuses))

	inspection, err := InspectPullRequest(t.Context(), user, InspectionRequest{
		Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index, Checks: true, Policy: true,
	})
	assert.ErrorIs(t, err, ErrPullRequestInspectionLimit)
	require.NotNil(t, inspection)
	require.NotNil(t, inspection.Checks)
	assert.Empty(t, inspection.Checks.Checks)
	assert.Greater(t, len(inspection.Checks.commitStatuses), MaxPullRequestInspectionStatuses)
	require.NotNil(t, inspection.Policy)
	assert.Equal(t, commitstatus.CommitStatusSuccess, inspection.Policy.RequiredChecksState)
	assert.Empty(t, inspection.Policy.MissingRequiredContexts)
	assert.False(t, inspection.Policy.HasBlocker(PullRequestInspectionBlockerRequiredChecksMissing))
	assert.False(t, inspection.Policy.HasBlocker(PullRequestInspectionBlockerRequiredChecksFailing))
}

func TestInspectPullRequestPolicyNoProtectionRule(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	pr, err := issues_model.GetPullRequestByID(t.Context(), 2)
	require.NoError(t, err)

	policy, err := inspectPullRequestPolicy(t.Context(), pr, &InspectionChecks{})
	require.NoError(t, err)
	assert.False(t, policy.Protected)
	assert.Empty(t, policy.RequiredContexts)
	assert.Empty(t, policy.Blockers)
}

func TestInspectPullRequestProtectedBranchPolicy(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	pr, err := issues_model.GetPullRequestByID(t.Context(), 2)
	require.NoError(t, err)
	require.NoError(t, pr.LoadBaseRepo(t.Context()))

	pb := &git_model.ProtectedBranch{
		RepoID: repo.ID, RuleName: pr.BaseBranch, EnableStatusCheck: true,
		StatusCheckContexts: []string{"ci/pass", "ci/missing"}, RequiredApprovals: 2,
		BlockOnRejectedReviews: true, BlockOnOfficialReviewRequests: true,
	}
	require.NoError(t, git_model.UpdateProtectBranch(t.Context(), repo, pb, git_model.WhitelistOptions{}))

	inspection, err := resolvePullRequestInspectionRevisions(t.Context(), pr, openTestRepository(t, repo), true, false)
	require.NoError(t, err)
	revision := inspection.Revisions.InternalHead
	require.NoError(t, db.Insert(t.Context(), &git_model.CommitStatus{
		Index: 50, RepoID: repo.ID, SHA: revision, State: commitstatus.CommitStatusSuccess,
		Context: "ci/pass", ContextHash: git_model.HashCommitStatusContext("ci/pass"),
	}))
	_, err = db.GetEngine(t.Context()).Where("issue_id = ?", pr.IssueID).Delete(new(issues_model.Review))
	require.NoError(t, err)
	require.NoError(t, db.Insert(t.Context(),
		&issues_model.Review{IssueID: pr.IssueID, ReviewerID: 1, Type: issues_model.ReviewTypeApprove, Official: true},
		&issues_model.Review{IssueID: pr.IssueID, ReviewerID: 2, Type: issues_model.ReviewTypeReject, Official: true},
		&issues_model.Review{IssueID: pr.IssueID, ReviewerID: 3, Type: issues_model.ReviewTypeRequest, Official: true},
	))

	checks, err := inspectPullRequestChecks(t.Context(), repo, revision, true)
	require.NoError(t, err)
	policy, err := inspectPullRequestPolicy(t.Context(), pr, checks)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"ci/pass", "ci/missing"}, policy.RequiredContexts)
	assert.Equal(t, []string{"ci/missing"}, policy.MissingRequiredContexts)
	assert.Equal(t, commitstatus.CommitStatusPending, policy.RequiredChecksState)
	assert.EqualValues(t, 2, policy.RequiredApprovals)
	assert.EqualValues(t, 1, policy.GrantedApprovals)
	assert.True(t, policy.HasBlocker(PullRequestInspectionBlockerRequiredChecksMissing))
	assert.True(t, policy.HasBlocker(PullRequestInspectionBlockerApprovals))
	assert.True(t, policy.HasBlocker(PullRequestInspectionBlockerRejectedReview))
	assert.True(t, policy.HasBlocker(PullRequestInspectionBlockerOfficialReviewRequest))

	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	partial, err := evaluatePullRequestInspectionPolicy(canceled, pr, pb, checks)
	assert.Error(t, err)
	require.NotNil(t, partial)
	assert.True(t, partial.HasBlocker(PullRequestInspectionBlockerApprovals))
	assert.True(t, partial.HasBlocker(PullRequestInspectionBlockerRejectedReview))
	assert.True(t, partial.HasBlocker(PullRequestInspectionBlockerOfficialReviewRequest))
}

func TestInspectPullRequestCancellation(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := InspectPullRequest(canceled, nil, InspectionRequest{Owner: "user2", Repository: "repo1", Index: 3})
	assert.ErrorIs(t, err, context.Canceled)
}

func openTestRepository(t *testing.T, repo *repo_model.Repository) *git.Repository {
	t.Helper()
	gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
	require.NoError(t, err)
	t.Cleanup(func() { gitRepo.Close() })
	return gitRepo
}
