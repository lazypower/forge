// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"strconv"
	"testing"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	work_service "gitea.dev/services/work"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkReadCompositionIsNativeAndSideEffectFree(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	project := &project_model.Project{
		RepoID: repo.ID, Type: project_model.TypeRepository, Title: "Read plan", CreatorID: doer.ID,
		PlanningState: project_model.PlanningStateActive,
	}
	require.NoError(t, project_model.NewProject(t.Context(), project))
	closed := &issues_model.Issue{RepoID: repo.ID, Index: 98_001, PosterID: doer.ID, Title: "Closed prerequisite", IsClosed: true}
	ready := &issues_model.Issue{RepoID: repo.ID, Index: 98_002, PosterID: doer.ID, Title: "Ready work"}
	require.NoError(t, db.Insert(t.Context(), closed, ready))
	require.NoError(t, db.Insert(t.Context(),
		&project_model.ProjectIssue{ProjectID: project.ID, IssueID: closed.ID, ProjectColumnID: 1},
		&project_model.ProjectIssue{ProjectID: project.ID, IssueID: ready.ID, ProjectColumnID: 1},
		&issues_model.IssueDependency{UserID: doer.ID, IssueID: ready.ID, DependencyID: closed.ID},
	))

	before := workReadIntegrationCounts(t)
	inspection, err := work_service.NewReadService().InspectPlan(t.Context(), doer, work_service.PlanRequest{
		Owner: repo.OwnerName, Repository: repo.Name, ProjectID: project.ID,
	})
	require.NoError(t, err)
	require.Len(t, inspection.WorkPlan.ReadyFrontier, 1)
	assert.Equal(t, "project/"+strconv.FormatInt(project.ID, 10)+"/issue/98002", inspection.WorkPlan.ReadyFrontier[0].Ref)
	assert.Equal(t, before, workReadIntegrationCounts(t))
}

func workReadIntegrationCounts(t *testing.T) map[string]int64 {
	t.Helper()
	beans := map[string]any{
		"issue": new(issues_model.Issue), "project_issue": new(project_model.ProjectIssue),
		"issue_dependency": new(issues_model.IssueDependency), "comment": new(issues_model.Comment),
		"issue_user": new(issues_model.IssueUser), "notification": new(activities_model.Notification),
		"commit_status": new(git_model.CommitStatus),
	}
	counts := make(map[string]int64, len(beans))
	for name, bean := range beans {
		count, err := db.GetEngine(t.Context()).Count(bean)
		require.NoError(t, err)
		counts[name] = count
	}
	return counts
}
