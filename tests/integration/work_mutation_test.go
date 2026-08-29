// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"testing"
	"time"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	mcpwork_service "gitea.dev/services/mcpwork"
	work_service "gitea.dev/services/work"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkMutationSavepointRejection(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issuesUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeIssues})
	issuesUnit.IssuesConfig().EnableDependencies = true
	require.NoError(t, repo_model.UpdateRepoUnitConfig(t.Context(), issuesUnit))
	plan := &project_model.Project{
		RepoID: repo.ID, Type: project_model.TypeRepository, CreatorID: doer.ID,
		Title: "Savepoint plan", PlanningState: project_model.PlanningStateDraft,
	}
	require.NoError(t, project_model.NewProject(t.Context(), plan))
	receipts, err := mcpwork_service.NewService([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	request := work_service.PlanRevisionRequest{RepositoryID: repo.ID, ProjectID: plan.ID, Changes: []work_service.PlanChange{
		{Kind: work_service.PlanChangeCreateMember, LocalReference: "first", Title: "Savepoint first"},
		{Kind: work_service.PlanChangeCreateMember, LocalReference: "second", Title: "Savepoint second"},
		{Kind: work_service.PlanChangeEnsureDependency, Blocked: work_service.ItemSelector{LocalReference: "first"}, Prerequisite: work_service.ItemSelector{LocalReference: "second"}, Presence: work_service.PresencePresent},
		{Kind: work_service.PlanChangeEnsureDependency, Blocked: work_service.ItemSelector{LocalReference: "second"}, Prerequisite: work_service.ItemSelector{LocalReference: "first"}, Presence: work_service.PresencePresent},
	}}
	result, err := receipts.Execute(t.Context(), mcpwork_service.Request{
		Tool: "work_plan.revise", SchemaVersion: "1", IdempotencyKey: "integration-savepoint-rejection",
		ExpandedInput: []byte(`{"idempotencyKey":"integration-savepoint-rejection"}`),
		Authority: mcpwork_service.Authority{
			PrincipalID: doer.ID, OAuthApplicationID: 501, OAuthGrantID: 502,
			CredentialJTI: "55555555-5555-4555-8555-555555555555", Audience: "https://forge.example/mcp",
			Scope: "read:repository write:issue write:repository",
		},
	}, func(ctx context.Context, operation mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		commit, err := work_service.NewMutationService().RevisePlanInWorkTx(ctx, doer, request, operation)
		return commit.Completion, err
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, result.Outcome)
	assert.Equal(t, "invalid_dependency", result.ProblemCode)
	unittest.AssertNotExistsBean(t, &issues_model.Issue{RepoID: repo.ID, Title: "Savepoint first"})
	unittest.AssertNotExistsBean(t, &issues_model.Issue{RepoID: repo.ID, Title: "Savepoint second"})
	stored, artifacts, events, err := mcpwork_model.GetReceiptByUUID(t.Context(), result.OperationUUID)
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, stored.Outcome)
	assert.Empty(t, artifacts)
	assert.Empty(t, events)
}

func TestIssueDeletionSerializesWithActivePlanMembership(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	plan := &project_model.Project{
		RepoID: repo.ID, Type: project_model.TypeRepository, CreatorID: doer.ID,
		Title: "Active plan", PlanningState: project_model.PlanningStateActive,
	}
	require.NoError(t, project_model.NewProject(t.Context(), plan))

	guarded := make(chan struct{})
	releaseDeletion := make(chan struct{})
	deletionDone := make(chan error, 1)
	membershipStarted := make(chan struct{})
	membershipDone := make(chan error, 1)
	go func() {
		deletionDone <- db.WithTx(t.Context(), func(ctx context.Context) error {
			if err := project_model.RequireIssueOutsideActivePlan(ctx, repo.ID, issue.ID); err != nil {
				return err
			}
			close(guarded)
			<-releaseDeletion
			_, err := db.GetEngine(ctx).ID(issue.ID).Delete(new(issues_model.Issue))
			return err
		})
	}()
	select {
	case <-guarded:
	case err := <-deletionDone:
		require.NoError(t, err)
		require.FailNow(t, "deletion completed before the active-plan guard")
	}
	go func() {
		close(membershipStarted)
		membershipDone <- db.WithWorkTx(t.Context(), func(ctx context.Context) error {
			if err := project_model.StabilizePlanningStates(ctx, []int64{plan.ID}); err != nil {
				return err
			}
			storedIssue, err := issues_model.GetIssueByID(ctx, issue.ID)
			if err != nil {
				return err
			}
			storedIssue.Repo = repo
			_, _, err = issues_model.EnsureIssueProjectInWorkTx(ctx, storedIssue, doer, plan, true)
			return err
		})
	}()
	<-membershipStarted
	select {
	case err := <-membershipDone:
		require.Failf(t, "membership mutation did not serialize", "returned before deletion released its plan lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseDeletion)
	require.NoError(t, <-deletionDone)
	require.Error(t, <-membershipDone)
	unittest.AssertNotExistsBean(t, &issues_model.Issue{ID: issue.ID})
	unittest.AssertNotExistsBean(t, &project_model.ProjectIssue{ProjectID: plan.ID, IssueID: issue.ID})
}

func TestIssueDeletionSerializesWithPlanCreation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})

	guarded := make(chan struct{})
	releaseDeletion := make(chan struct{})
	deletionDone := make(chan error, 1)
	type beginResult struct {
		commit work_service.MutationCommit
		err    error
	}
	beginStarted := make(chan struct{})
	beginDone := make(chan beginResult, 1)
	go func() {
		deletionDone <- db.WithTx(t.Context(), func(ctx context.Context) error {
			if err := project_model.RequireIssueOutsideActivePlan(ctx, repo.ID, issue.ID); err != nil {
				return err
			}
			close(guarded)
			<-releaseDeletion
			_, err := db.GetEngine(ctx).ID(issue.ID).Delete(new(issues_model.Issue))
			return err
		})
	}()
	select {
	case <-guarded:
	case err := <-deletionDone:
		require.NoError(t, err)
		require.FailNow(t, "deletion completed before the repository planning guard")
	}
	go func() {
		close(beginStarted)
		commit, err := work_service.NewMutationService().BeginPlan(t.Context(), doer, work_service.BeginPlanRequest{
			RepositoryID: repo.ID, Title: "Concurrent plan",
		})
		beginDone <- beginResult{commit: commit, err: err}
	}()
	<-beginStarted
	select {
	case result := <-beginDone:
		require.Failf(t, "plan creation did not serialize", "returned before deletion released its repository lock: %v", result.err)
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseDeletion)
	require.NoError(t, <-deletionDone)
	result := <-beginDone
	require.NoError(t, result.err)
	require.Equal(t, mcpwork_model.OutcomeApplied, result.commit.Completion.Outcome)
	require.Len(t, result.commit.Completion.Artifacts, 1)
	planID := result.commit.Completion.Artifacts[0].ArtifactID

	revision, err := work_service.NewMutationService().RevisePlan(t.Context(), doer, work_service.PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: planID,
		Changes: []work_service.PlanChange{{
			Kind: work_service.PlanChangeEnsureMember, WorkItem: work_service.ItemSelector{IssueNumber: issue.Index}, Presence: work_service.PresencePresent,
		}},
	})
	require.NoError(t, err)
	require.Equal(t, mcpwork_model.OutcomeRejected, revision.Completion.Outcome)
	require.Equal(t, "unavailable", revision.Completion.ProblemCode)
	unittest.AssertNotExistsBean(t, &project_model.ProjectIssue{ProjectID: planID, IssueID: issue.ID})
}
