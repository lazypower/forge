// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"testing"

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
