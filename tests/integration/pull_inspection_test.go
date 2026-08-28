// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"sync/atomic"
	"testing"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/test"
	pull_service "gitea.dev/services/pull"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullRequestInspectionIsReadOnly(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	pr, err := issues_model.GetPullRequestByIndex(t.Context(), repo.ID, 3)
	require.NoError(t, err)
	issueBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: pr.IssueID})
	prBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
	repoBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	require.NoError(t, db.Insert(t.Context(), &issues_model.IssueUser{IssueID: pr.IssueID, UID: doer.ID}))
	issueUserBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.IssueUser{IssueID: pr.IssueID, UID: doer.ID})

	rowCountsBefore := map[string]int64{}
	for name, bean := range map[string]any{
		"issue": &issues_model.Issue{}, "pull_request": &issues_model.PullRequest{},
		"issue_user": &issues_model.IssueUser{}, "notification": &activities_model.Notification{},
		"commit_status": &git_model.CommitStatus{},
	} {
		rowCountsBefore[name], err = db.GetEngine(t.Context()).Count(bean)
		require.NoError(t, err)
	}

	gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
	require.NoError(t, err)
	internalHeadBefore, err := gitRepo.GetRefCommitID(pr.GetGitHeadRefName())
	require.NoError(t, err)
	targetBefore, err := gitRepo.GetBranchCommitID(pr.BaseBranch)
	require.NoError(t, err)
	sourceBefore, err := gitRepo.GetBranchCommitID(pr.HeadBranch)
	require.NoError(t, err)
	gitRepo.Close()

	var queued atomic.Int64
	defer test.MockVariableValue(&pull_service.AddPullRequestToCheckQueue, func(int64) { queued.Add(1) })()
	inspection, err := pull_service.InspectPullRequest(t.Context(), doer, pull_service.InspectionRequest{
		Owner: repo.OwnerName, Repository: repo.Name, Index: pr.Index,
		ChangedFiles: &pull_service.InspectionPageRequest{Limit: 1},
		Diff:         &pull_service.InspectionDiffRequest{FileLimit: 1, LinesPerFile: 10, MaxLineCharacters: 100},
		Checks:       true, Policy: true,
	})
	require.NoError(t, err)
	require.NotNil(t, inspection)
	assert.Equal(t, internalHeadBefore, inspection.Revisions.InternalHead)
	assert.Zero(t, queued.Load())

	issueAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: pr.IssueID})
	prAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
	repoAfter := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	issueUserAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.IssueUser{IssueID: pr.IssueID, UID: doer.ID})
	assert.Equal(t, issueBefore.UpdatedUnix, issueAfter.UpdatedUnix)
	assert.Equal(t, prBefore.MergedUnix, prAfter.MergedUnix)
	assert.Equal(t, prBefore.Status, prAfter.Status)
	assert.Equal(t, repoBefore.UpdatedUnix, repoAfter.UpdatedUnix)
	assert.Equal(t, issueUserBefore.IsRead, issueUserAfter.IsRead)

	for name, bean := range map[string]any{
		"issue": &issues_model.Issue{}, "pull_request": &issues_model.PullRequest{},
		"issue_user": &issues_model.IssueUser{}, "notification": &activities_model.Notification{},
		"commit_status": &git_model.CommitStatus{},
	} {
		count, err := db.GetEngine(t.Context()).Count(bean)
		require.NoError(t, err)
		assert.Equal(t, rowCountsBefore[name], count, name)
	}

	gitRepo, err = gitrepo.OpenRepository(t.Context(), repo)
	require.NoError(t, err)
	defer gitRepo.Close()
	internalHeadAfter, err := gitRepo.GetRefCommitID(pr.GetGitHeadRefName())
	require.NoError(t, err)
	targetAfter, err := gitRepo.GetBranchCommitID(pr.BaseBranch)
	require.NoError(t, err)
	sourceAfter, err := gitRepo.GetBranchCommitID(pr.HeadBranch)
	require.NoError(t, err)
	assert.Equal(t, internalHeadBefore, internalHeadAfter)
	assert.Equal(t, targetBefore, targetAfter)
	assert.Equal(t, sourceBefore, sourceAfter)
}
