// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"context"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	activities_model "gitea.dev/models/activities"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm/contexts"
)

func TestNativeReadIsSideEffectFree(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	project := insertNativePlan(t, repo.ID, 95_001)
	closed := insertNativeIssue(t, repo.ID, 95_001, true)
	ready := insertNativeIssue(t, repo.ID, 95_002, false)
	require.NoError(t, db.Insert(t.Context(),
		&project_model.ProjectIssue{ProjectID: project.ID, IssueID: closed.ID, ProjectColumnID: 1},
		&project_model.ProjectIssue{ProjectID: project.ID, IssueID: ready.ID, ProjectColumnID: 1},
		&issues_model.IssueDependency{UserID: doer.ID, IssueID: ready.ID, DependencyID: closed.ID},
	))

	before := nativeReadRowCounts(t)
	service := NewReadService()
	plan, err := service.InspectPlan(t.Context(), doer, PlanRequest{
		Owner: repo.OwnerName, Repository: repo.Name, ProjectID: project.ID,
	})
	require.NoError(t, err)
	require.Len(t, plan.WorkPlan.ReadyFrontier, 1)
	assert.Equal(t, "project/"+nativeInt(project.ID)+"/issue/95002", plan.WorkPlan.ReadyFrontier[0].Ref)
	item, err := service.InspectItem(t.Context(), doer, ItemRequest{
		Owner: repo.OwnerName, Repository: repo.Name, IssueNumber: ready.Index, SelectedProjectID: project.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, item.SelectedContext)
	assert.Equal(t, "ready", item.SelectedContext.DerivedState)
	assert.Equal(t, before, nativeReadRowCounts(t))
}

func TestNativeReadQueryCountIsIndependentOfPlanWidth(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	installWorkSQLProbe()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	service := NewReadService()
	makePlan := func(projectID, firstIssue int64, count int) *project_model.Project {
		project := insertNativePlan(t, repo.ID, projectID)
		for i := range count {
			issue := insertNativeIssue(t, repo.ID, firstIssue+int64(i), false)
			require.NoError(t, db.Insert(t.Context(), &project_model.ProjectIssue{ProjectID: project.ID, IssueID: issue.ID, ProjectColumnID: 1}))
		}
		return project
	}
	small := makePlan(95_010, 95_100, 2)
	large := makePlan(95_011, 95_200, 50)
	countQueries := func(projectID int64) int64 {
		probe := &workSQLProbe{}
		ctx := context.WithValue(t.Context(), workSQLProbeKey{}, probe)
		_, err := service.InspectPlan(ctx, doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: projectID})
		require.NoError(t, err)
		return probe.count.Load()
	}
	smallQueries := countQueries(small.ID)
	largeQueries := countQueries(large.ID)
	t.Logf("Work plan composition used %d queries for 2 and 50 members", largeQueries)
	assert.Equal(t, smallQueries, largeQueries, "set-oriented composition must not add one query per member")
	assert.EqualValues(t, 9, largeQueries)
}

func TestNativeReadCancellationReachesDatabaseBoundary(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	installWorkSQLProbe()
	probe := &workSQLProbe{block: true, started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), workSQLProbeKey{}, probe))
	service := NewReadService()
	result := make(chan error, 1)
	go func() {
		_, err := service.InspectPlan(ctx, nil, PlanRequest{Owner: "user2", Repository: "repo1", ProjectID: 1})
		result <- err
	}()
	<-probe.started
	cancel()
	assert.ErrorIs(t, <-result, context.Canceled)
}

func insertNativePlan(t *testing.T, repoID, identity int64) *project_model.Project {
	t.Helper()
	project := &project_model.Project{
		ID: identity, RepoID: repoID, Type: project_model.TypeRepository, Title: "Work plan", CreatorID: 2,
		PlanningState: project_model.PlanningStateActive,
	}
	require.NoError(t, db.Insert(t.Context(), project))
	return project
}

func insertNativeIssue(t *testing.T, repoID, index int64, closed bool) *issues_model.Issue {
	t.Helper()
	issue := &issues_model.Issue{RepoID: repoID, Index: index, PosterID: 2, Title: "Work item", Content: "body", IsClosed: closed}
	require.NoError(t, db.Insert(t.Context(), issue))
	return issue
}

func nativeReadRowCounts(t *testing.T) map[string]int64 {
	t.Helper()
	beans := map[string]any{
		"issue":            new(issues_model.Issue),
		"project_issue":    new(project_model.ProjectIssue),
		"issue_dependency": new(issues_model.IssueDependency),
		"comment":          new(issues_model.Comment),
		"issue_user":       new(issues_model.IssueUser),
		"notification":     new(activities_model.Notification),
		"commit_status":    new(git_model.CommitStatus),
	}
	counts := make(map[string]int64, len(beans))
	for name, bean := range beans {
		count, err := db.GetEngine(t.Context()).Count(bean)
		require.NoError(t, err)
		counts[name] = count
	}
	return counts
}

func nativeInt(value int64) string {
	return strconv.FormatInt(value, 10)
}

type workSQLProbeKey struct{}

type workSQLProbe struct {
	count   atomic.Int64
	block   bool
	started chan struct{}
}

type workSQLHook struct{}

func (workSQLHook) BeforeProcess(hook *contexts.ContextHook) (context.Context, error) {
	probe, _ := hook.Ctx.Value(workSQLProbeKey{}).(*workSQLProbe)
	if probe == nil {
		return hook.Ctx, nil
	}
	probe.count.Add(1)
	if probe.block {
		close(probe.started)
		<-hook.Ctx.Done()
		return hook.Ctx, hook.Ctx.Err()
	}
	return hook.Ctx, nil
}

func (workSQLHook) AfterProcess(*contexts.ContextHook) error { return nil }

var installWorkSQLProbeOnce sync.Once

func installWorkSQLProbe() {
	installWorkSQLProbeOnce.Do(func() { unittest.GetXORMEngine().AddHook(workSQLHook{}) })
}
