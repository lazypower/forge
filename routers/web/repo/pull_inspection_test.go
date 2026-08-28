// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"strconv"
	"strings"
	"testing"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/services/contexttest"
	pull_service "gitea.dev/services/pull"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullRequestViewUsesFrozenInspectionRevisions(t *testing.T) {
	for _, index := range []int64{2, 3} {
		t.Run(strconv.FormatInt(index, 10), func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			ctx, _ := contexttest.MockContext(t, "user2/repo1/pulls/3")
			contexttest.LoadUser(t, ctx, 2)
			contexttest.LoadRepo(t, ctx, 1)
			contexttest.LoadGitRepo(t, ctx)

			issue, err := issues_model.GetIssueByIndex(t.Context(), ctx.Repo.Repository.ID, index)
			require.NoError(t, err)
			require.NoError(t, issue.LoadPullRequest(t.Context()))
			view := newPullRequestViewInfo()
			view.prepareViewInfo(ctx, issue)

			assert.False(t, ctx.Written())
			require.NotNil(t, view.Inspection)
			assert.True(t, view.Inspection.Revisions.InternalHeadAvailable)
			assert.Equal(t, view.Inspection.Revisions.InternalHead, view.CompareInfo.HeadCommitID)
			assert.Equal(t, view.Inspection.Revisions.ComparisonBase, view.CompareInfo.CompareBase)
			assert.Equal(t, len(view.CompareInfo.Commits), ctx.Data["NumCommits"])
		})
	}
}

func TestPullRequestViewUsesInspectionPolicyWithoutProtectionRule(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx, _ := contexttest.MockContext(t, "user2/repo1/pulls/3")
	contexttest.LoadUser(t, ctx, 2)
	contexttest.LoadRepo(t, ctx, 1)
	contexttest.LoadGitRepo(t, ctx)

	issue, err := issues_model.GetIssueByIndex(t.Context(), ctx.Repo.Repository.ID, 3)
	require.NoError(t, err)
	require.NoError(t, issue.LoadPullRequest(t.Context()))
	view := newPullRequestViewInfo()
	view.prepareViewInfo(ctx, issue)
	require.False(t, ctx.Written())
	view.MergeBoxData = &pullMergeBoxData{}
	view.prepareMergeBoxProtectionChecks(ctx)

	assert.False(t, ctx.Written())
	require.NotNil(t, view.Inspection)
	require.NotNil(t, view.Inspection.Policy)
	assert.False(t, view.Inspection.Policy.Protected)
	assert.Nil(t, view.ProtectedBranchRule)
	assert.Empty(t, view.MergeBoxData.infoProtectionBlockers)
}

func TestPullRequestViewInspectionPermissions(t *testing.T) {
	t.Run("denied pull requests exposes no inspection", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())
		deletePullInspectionRepoUnit(t, unit.TypePullRequests)
		ctx, _ := contexttest.MockContext(t, "user2/repo1/pulls/3")
		contexttest.LoadUser(t, ctx, 2)
		contexttest.LoadRepo(t, ctx, 1)
		contexttest.LoadGitRepo(t, ctx)
		issue, err := issues_model.GetIssueByIndex(t.Context(), ctx.Repo.Repository.ID, 3)
		require.NoError(t, err)
		require.NoError(t, issue.LoadPullRequest(t.Context()))

		view := newPullRequestViewInfo()
		view.prepareViewInfo(ctx, issue)
		assert.Nil(t, view.Inspection)
		assert.Empty(t, view.HeadBranchCommitID)
		assert.Empty(t, view.CompareInfo.HeadCommitID)
	})

	t.Run("pull-only retains semantic inspection", func(t *testing.T) {
		require.NoError(t, unittest.PrepareTestDatabase())
		deletePullInspectionRepoUnit(t, unit.TypeCode)
		ctx, _ := contexttest.MockContext(t, "user2/repo1/pulls/3")
		contexttest.LoadUser(t, ctx, 2)
		contexttest.LoadRepo(t, ctx, 1)
		contexttest.LoadGitRepo(t, ctx)
		issue, err := issues_model.GetIssueByIndex(t.Context(), ctx.Repo.Repository.ID, 3)
		require.NoError(t, err)
		require.NoError(t, issue.LoadPullRequest(t.Context()))

		view := newPullRequestViewInfo()
		view.prepareViewInfo(ctx, issue)
		require.NotNil(t, view.Inspection)
		assert.Equal(t, issue.Title, view.Inspection.Metadata.Title)
		require.NotNil(t, view.Inspection.Checks)
		require.NotNil(t, view.Inspection.Policy)

		request := pull_service.InspectionRequest{Owner: "user2", Repository: "repo1", Index: 3}
		_, err = pull_service.InspectPullRequest(t.Context(), ctx.Doer, request)
		require.NoError(t, err)
		request.ChangedFiles = &pull_service.InspectionPageRequest{}
		_, err = pull_service.InspectPullRequest(t.Context(), ctx.Doer, request)
		assert.ErrorIs(t, err, pull_service.ErrPullRequestInspectionUnavailable)
		request.ChangedFiles = nil
		request.Diff = &pull_service.InspectionDiffRequest{}
		_, err = pull_service.InspectPullRequest(t.Context(), ctx.Doer, request)
		assert.ErrorIs(t, err, pull_service.ErrPullRequestInspectionUnavailable)
	})
}

func TestPullRequestViewPreservesStatusPresentationBounds(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx, _ := contexttest.MockContext(t, "user2/repo1/pulls/3")
	contexttest.LoadUser(t, ctx, 2)
	contexttest.LoadRepo(t, ctx, 1)
	contexttest.LoadGitRepo(t, ctx)
	issue, err := issues_model.GetIssueByIndex(t.Context(), ctx.Repo.Repository.ID, 3)
	require.NoError(t, err)
	require.NoError(t, issue.LoadPullRequest(t.Context()))
	revision, err := ctx.Repo.GitRepo.GetRefCommitID(issue.PullRequest.GetGitHeadRefName())
	require.NoError(t, err)
	requiredContext := strings.Repeat("required/", pull_service.MaxPullRequestInspectionStatusText/len("required/")+1)
	require.Greater(t, len(requiredContext), pull_service.MaxPullRequestInspectionStatusText)
	require.NoError(t, git_model.UpdateProtectBranch(t.Context(), ctx.Repo.Repository, &git_model.ProtectedBranch{
		RepoID: ctx.Repo.Repository.ID, RuleName: issue.PullRequest.BaseBranch,
		EnableStatusCheck: true, StatusCheckContexts: []string{requiredContext},
	}, git_model.WhitelistOptions{}))
	longDescription := strings.Repeat("d", pull_service.MaxPullRequestInspectionStatusText+50)
	statuses := make([]*git_model.CommitStatus, 0, pull_service.MaxPullRequestInspectionStatuses+1)
	statuses = append(statuses, &git_model.CommitStatus{
		Index: 10_000, RepoID: ctx.Repo.Repository.ID, SHA: revision,
		State: commitstatus.CommitStatusSuccess, Context: requiredContext,
		ContextHash: git_model.HashCommitStatusContext(requiredContext), Description: longDescription,
	})
	for i := range pull_service.MaxPullRequestInspectionStatuses {
		contextName := "presentation/" + strconv.Itoa(i)
		statuses = append(statuses, &git_model.CommitStatus{
			Index: int64(1000 + i), RepoID: ctx.Repo.Repository.ID, SHA: revision,
			State: commitstatus.CommitStatusSuccess, Context: contextName,
			ContextHash: git_model.HashCommitStatusContext(contextName), Description: longDescription,
		})
	}
	require.NoError(t, db.Insert(t.Context(), statuses))
	expectedStatuses, err := git_model.GetLatestCommitStatus(t.Context(), ctx.Repo.Repository.ID, revision, db.ListOptionsAll)
	require.NoError(t, err)

	view := newPullRequestViewInfo()
	view.prepareViewInfo(ctx, issue)
	require.NotNil(t, view.Inspection)
	require.NotNil(t, view.Inspection.Checks)
	assert.Empty(t, view.Inspection.Checks.Checks)
	require.NotNil(t, view.Inspection.Policy)
	assert.Equal(t, commitstatus.CommitStatusSuccess, view.Inspection.Policy.RequiredChecksState)
	assert.Empty(t, view.Inspection.Policy.MissingRequiredContexts)
	assert.False(t, view.Inspection.Policy.HasBlocker(pull_service.PullRequestInspectionBlockerRequiredChecksMissing))
	assert.False(t, view.Inspection.Policy.HasBlocker(pull_service.PullRequestInspectionBlockerRequiredChecksFailing))
	view.MergeBoxData = &pullMergeBoxData{}
	view.prepareMergeBoxProtectionChecks(ctx)
	require.False(t, ctx.Written())
	require.NotNil(t, view.MergeBoxData.StatusCheckData)
	assert.Len(t, view.MergeBoxData.StatusCheckData.PullCommitStatuses, len(expectedStatuses))
	assert.Equal(t, commitstatus.CommitStatusSuccess, view.MergeBoxData.StatusCheckData.RequiredChecksState)
	assert.Empty(t, view.MergeBoxData.StatusCheckData.MissingRequiredChecks)
	assert.Empty(t, view.MergeBoxData.infoProtectionBlockers)
	presentedRequired := false
	for _, status := range view.MergeBoxData.StatusCheckData.PullCommitStatuses {
		if status.Context == requiredContext {
			presentedRequired = true
			assert.Equal(t, longDescription, status.Description)
		}
	}
	assert.True(t, presentedRequired)
}

func deletePullInspectionRepoUnit(t *testing.T, unitType unit.Type) {
	t.Helper()
	deleted, err := db.DeleteByBean(t.Context(), &repo_model.RepoUnit{RepoID: 1, Type: unitType})
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
}
