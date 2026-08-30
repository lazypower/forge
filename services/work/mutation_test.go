// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"

	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	perm_model "gitea.dev/models/perm"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	issue_service "gitea.dev/services/issue"
	mcpwork_service "gitea.dev/services/mcpwork"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mutationReceiptSecret = "0123456789abcdef0123456789abcdef"

func TestBeginPlanCreatesDraftOrOptsInDisabledProject(t *testing.T) {
	t.Run("new draft", func(t *testing.T) {
		repo, doer, _ := prepareMutationPlan(t, project_model.PlanningStateDraft)
		commit, err := NewMutationService().BeginPlan(t.Context(), doer, BeginPlanRequest{
			RepositoryID: repo.ID, Title: "New plan", Markdown: "Plan context",
		})
		require.NoError(t, err)
		assert.Equal(t, mcpwork_model.OutcomeApplied, commit.Completion.Outcome)
		require.Len(t, commit.Completion.Artifacts, 1)
		created, err := project_model.GetProjectByID(t.Context(), commit.Completion.Artifacts[0].ArtifactID)
		require.NoError(t, err)
		assert.Equal(t, project_model.PlanningStateDraft, created.PlanningState)
		_, err = created.MustDefaultColumn(t.Context())
		require.NoError(t, err)
	})

	t.Run("existing disabled", func(t *testing.T) {
		repo, doer, _ := prepareMutationPlan(t, project_model.PlanningStateDraft)
		disabled := newMutationProject(t, repo.ID, project_model.PlanningStateDisabled)
		commit, err := NewMutationService().BeginPlan(t.Context(), doer, BeginPlanRequest{
			RepositoryID: repo.ID, ExistingProjectID: disabled.ID,
		})
		require.NoError(t, err)
		assert.Equal(t, mcpwork_model.OutcomeApplied, commit.Completion.Outcome)
		stored, err := project_model.GetProjectByID(t.Context(), disabled.ID)
		require.NoError(t, err)
		assert.Equal(t, project_model.PlanningStateDraft, stored.PlanningState)
	})
}

func TestPlanRevisionCommitsCreateMembershipDependencyActivationAndReceiptTogether(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	reader := NewReadService()
	before, err := reader.InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	request := PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: before.WorkPlan.PlanToken,
		Changes: []PlanChange{
			{Kind: PlanChangeCreateMember, LocalReference: "foundation", Title: "Foundation", Markdown: "first"},
			{Kind: PlanChangeCreateMember, LocalReference: "delivery", Title: "Delivery", Markdown: "second"},
			{Kind: PlanChangeEnsureDependency, Blocked: ItemSelector{LocalReference: "delivery"}, Prerequisite: ItemSelector{LocalReference: "foundation"}, Presence: PresencePresent},
			{Kind: PlanChangeSetPlanningState, ExpectedState: PlanningStateDraft, DesiredState: PlanningStateActive},
		},
	}
	commit, receipt := executePlanReceipt(t, doer, request, "atomic-plan-revision-000000000001")
	assert.Equal(t, mcpwork_model.OutcomeApplied, receipt.Outcome)
	assert.False(t, receipt.Replayed)
	assert.Len(t, commit.Effects, 2)
	require.Len(t, commit.CreatedReferences, 2)

	storedPlan, err := project_model.GetProjectByID(t.Context(), plan.ID)
	require.NoError(t, err)
	assert.Equal(t, project_model.PlanningStateActive, storedPlan.PlanningState)
	foundation := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: repo.ID, Index: createdIssueNumber(t, commit, "foundation")})
	delivery := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: repo.ID, Index: createdIssueNumber(t, commit, "delivery")})
	for _, issue := range []*issues_model.Issue{foundation, delivery} {
		membership := unittest.AssertExistsAndLoadBean(t, &project_model.ProjectIssue{ProjectID: plan.ID, IssueID: issue.ID})
		assert.Positive(t, membership.ProjectColumnID)
	}
	unittest.AssertExistsAndLoadBean(t, &issues_model.IssueDependency{IssueID: delivery.ID, DependencyID: foundation.ID})
	storedReceipt, artifacts, events, err := mcpwork_model.GetReceiptByUUID(t.Context(), receipt.OperationUUID)
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeApplied, storedReceipt.Outcome)
	assert.Len(t, artifacts, 3)
	assert.Len(t, events, 4, "two membership and two dependency timeline rows carry provenance")
}

func TestPlanRevisionRejectionRollsBackCreatedFactsAndFinalizesReceipt(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	issueCount := unittest.GetCount(t, new(issues_model.Issue))
	commentCount := unittest.GetCount(t, new(issues_model.Comment))
	receipts, err := mcpwork_service.NewService([]byte(mutationReceiptSecret))
	require.NoError(t, err)
	service := NewMutationService()
	request := PlanRevisionRequest{RepositoryID: repo.ID, ProjectID: plan.ID, Changes: []PlanChange{
		{Kind: PlanChangeCreateMember, LocalReference: "first", Title: "First"},
		{Kind: PlanChangeCreateMember, LocalReference: "second", Title: "Second"},
		{Kind: PlanChangeEnsureDependency, Blocked: ItemSelector{LocalReference: "first"}, Prerequisite: ItemSelector{LocalReference: "second"}, Presence: PresencePresent},
		{Kind: PlanChangeEnsureDependency, Blocked: ItemSelector{LocalReference: "second"}, Prerequisite: ItemSelector{LocalReference: "first"}, Presence: PresencePresent},
	}}
	result, err := receipts.Execute(t.Context(), receiptRequest("work_plan.revise", "rollback-plan-revision-0000000001"), func(ctx context.Context, operation mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		commit, err := service.RevisePlanInWorkTx(ctx, doer, request, operation)
		return commit.Completion, err
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, result.Outcome)
	assert.Equal(t, "invalid_dependency", result.ProblemCode)
	assert.Equal(t, issueCount, unittest.GetCount(t, new(issues_model.Issue)))
	assert.Equal(t, commentCount, unittest.GetCount(t, new(issues_model.Comment)))
	storedReceipt, artifacts, events, err := mcpwork_model.GetReceiptByUUID(t.Context(), result.OperationUUID)
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, storedReceipt.Outcome)
	assert.Empty(t, artifacts)
	assert.Empty(t, events)
	unittest.AssertNotExistsBean(t, &project_model.ProjectIssue{ProjectID: plan.ID})
}

func TestCombinedTitleMarkdownConflictChangesNeither(t *testing.T) {
	repo, doer, _ := prepareMutationPlan(t, project_model.PlanningStateDraft)
	issue := insertMutationIssue(t, repo.ID, doer.ID, "Original", "Original body")
	service := NewMutationService()
	staleVersion := issue.ContentVersion + 1
	desiredState := ItemStateClosed
	commit, err := service.ReviseItem(t.Context(), doer, ItemRevisionRequest{
		RepositoryID: repo.ID, IssueNumber: issue.Index,
		Title:        &ConditionalText{Expected: issue.Title, Desired: "Changed"},
		Markdown:     &ConditionalMarkdown{ExpectedContentVersion: staleVersion, Desired: "Changed body"},
		DesiredState: &desiredState,
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, commit.Completion.Outcome)
	assert.Equal(t, "conflict", commit.Completion.ProblemCode)
	stored := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
	assert.Equal(t, "Original", stored.Title)
	assert.Equal(t, "Original body", stored.Content)
	assert.Equal(t, issue.ContentVersion, stored.ContentVersion)
	assert.False(t, stored.IsClosed)
	unittest.AssertNotExistsBean(t, &issues_model.Comment{IssueID: issue.ID, Type: issues_model.CommentTypeChangeTitle})
}

func TestMembershipPresenceConvergesAndPreservesUnrelatedProjects(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	other := newMutationProject(t, repo.ID, project_model.PlanningStateDisabled)
	issue := insertMutationIssue(t, repo.ID, doer.ID, "Member", "")
	_, _, err := issues_model.EnsureIssueProjectInWorkTx(t.Context(), issue, doer, other, true)
	require.NoError(t, err)
	service := NewMutationService()
	request := PlanRevisionRequest{RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: "permitted-for-set-only-revision", Changes: []PlanChange{{
		Kind: PlanChangeEnsureMember, WorkItem: ItemSelector{IssueNumber: issue.Index}, Presence: PresencePresent,
	}}}
	first, err := service.RevisePlan(t.Context(), doer, request)
	require.NoError(t, err)
	second, err := service.RevisePlan(t.Context(), doer, request)
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeApplied, first.Completion.Outcome)
	assert.Equal(t, mcpwork_model.OutcomeUnchanged, second.Completion.Outcome)
	assert.Empty(t, second.Completion.Events)
	assert.Empty(t, second.Effects)
	unittest.AssertExistsAndLoadBean(t, &project_model.ProjectIssue{ProjectID: plan.ID, IssueID: issue.ID})
	unittest.AssertExistsAndLoadBean(t, &project_model.ProjectIssue{ProjectID: other.ID, IssueID: issue.ID})
	unittest.AssertCount(t, &issues_model.Comment{IssueID: issue.ID, Type: issues_model.CommentTypeProject}, 2)
}

func TestActivationRejectsStalePlanTokenWithoutChange(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	inspection, err := NewReadService().InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	issue := insertMutationIssue(t, repo.ID, doer.ID, "Later member", "")
	_, _, err = issues_model.EnsureIssueProjectInWorkTx(t.Context(), issue, doer, plan, true)
	require.NoError(t, err)
	commit, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: inspection.WorkPlan.PlanToken,
		Changes: []PlanChange{{Kind: PlanChangeSetPlanningState, ExpectedState: PlanningStateDraft, DesiredState: PlanningStateActive}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, commit.Completion.Outcome)
	assert.Equal(t, "conflict", commit.Completion.ProblemCode)
	stored, err := project_model.GetProjectByID(t.Context(), plan.ID)
	require.NoError(t, err)
	assert.Equal(t, project_model.PlanningStateDraft, stored.PlanningState)
}

func TestActivationRejectsOverBoundPlanWithoutChange(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	for _, title := range []string{"First", "Second"} {
		issue := insertMutationIssue(t, repo.ID, doer.ID, title, "")
		_, _, err := issues_model.EnsureIssueProjectInWorkTx(t.Context(), issue, doer, plan, true)
		require.NoError(t, err)
	}
	originalMax := setting.Work.MaxPlanItems
	setting.Work.MaxPlanItems = 1
	t.Cleanup(func() { setting.Work.MaxPlanItems = originalMax })
	inspection, err := NewReadService().InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	assert.Equal(t, "incomplete", inspection.WorkPlan.Integrity.Status)

	commit, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: inspection.WorkPlan.PlanToken,
		Changes: []PlanChange{{Kind: PlanChangeSetPlanningState, ExpectedState: PlanningStateDraft, DesiredState: PlanningStateActive}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, commit.Completion.Outcome)
	assert.Equal(t, "invalid_plan", commit.Completion.ProblemCode)
	stored, err := project_model.GetProjectByID(t.Context(), plan.ID)
	require.NoError(t, err)
	assert.Equal(t, project_model.PlanningStateDraft, stored.PlanningState)
}

func TestActivationRejectsOverBoundDependencyGraphWithoutChange(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	blocked := insertMutationIssue(t, repo.ID, doer.ID, "Blocked", "")
	prerequisite := insertMutationIssue(t, repo.ID, doer.ID, "Prerequisite", "")
	_, _, err := issues_model.EnsureIssueProjectInWorkTx(t.Context(), blocked, doer, plan, true)
	require.NoError(t, err)
	_, err = issue_service.EnsureDependencyInWorkTx(t.Context(), doer, blocked, prerequisite, issue_service.DependencyPresent, issue_service.WorkDependencyScope)
	require.NoError(t, err)
	originalMax := setting.Work.MaxGraphNodes
	setting.Work.MaxGraphNodes = 1
	t.Cleanup(func() { setting.Work.MaxGraphNodes = originalMax })
	inspection, err := NewReadService().InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	assert.NotEqual(t, "valid", inspection.WorkPlan.Integrity.Status)

	commit, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: inspection.WorkPlan.PlanToken,
		Changes: []PlanChange{{Kind: PlanChangeSetPlanningState, ExpectedState: PlanningStateDraft, DesiredState: PlanningStateActive}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, commit.Completion.Outcome)
	assert.Equal(t, "invalid_plan", commit.Completion.ProblemCode)
	stored, err := project_model.GetProjectByID(t.Context(), plan.ID)
	require.NoError(t, err)
	assert.Equal(t, project_model.PlanningStateDraft, stored.PlanningState)
}

func TestReturnToDraftAndDeleteUseCurrentPlanToken(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateActive)
	issue := insertMutationIssue(t, repo.ID, doer.ID, "Retained", "")
	_, _, err := issues_model.EnsureIssueProjectInWorkTx(t.Context(), issue, doer, plan, true)
	require.NoError(t, err)
	reader := NewReadService()
	active, err := reader.InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)

	commit, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: active.WorkPlan.PlanToken,
		Changes: []PlanChange{{Kind: PlanChangeSetPlanningState, ExpectedState: PlanningStateActive, DesiredState: PlanningStateDraft}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeApplied, commit.Completion.Outcome)

	staleDelete, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: active.WorkPlan.PlanToken,
		Changes: []PlanChange{{Kind: PlanChangeDeleteDraft}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, staleDelete.Completion.Outcome)
	unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: plan.ID})

	draft, err := reader.InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	deleted, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: draft.WorkPlan.PlanToken,
		Changes: []PlanChange{{Kind: PlanChangeDeleteDraft}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeApplied, deleted.Completion.Outcome)
	unittest.AssertNotExistsBean(t, &project_model.Project{ID: plan.ID})
	unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
}

func TestPlanLifecycleRequiresProjectWriteWithoutIssueWrite(t *testing.T) {
	repo, owner, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	issue := insertMutationIssue(t, repo.ID, owner.ID, "Lifecycle member", "")
	_, _, err := issues_model.EnsureIssueProjectInWorkTx(t.Context(), issue, owner, plan, true)
	require.NoError(t, err)

	issuesUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeIssues})
	issuesUnit.EveryoneAccessMode = perm_model.AccessModeRead
	require.NoError(t, repo_model.UpdateRepoUnitPublicAccess(t.Context(), issuesUnit))
	projectsUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeProjects})
	projectsUnit.EveryoneAccessMode = perm_model.AccessModeWrite
	require.NoError(t, repo_model.UpdateRepoUnitPublicAccess(t.Context(), projectsUnit))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})

	inspection, err := NewReadService().InspectPlan(t.Context(), doer, PlanRequest{
		Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID,
	})
	require.NoError(t, err)
	activated, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: inspection.WorkPlan.PlanToken,
		Changes: []PlanChange{{Kind: PlanChangeSetPlanningState, ExpectedState: PlanningStateDraft, DesiredState: PlanningStateActive}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeApplied, activated.Completion.Outcome)

	membership, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID,
		Changes: []PlanChange{{Kind: PlanChangeEnsureMember, WorkItem: ItemSelector{IssueNumber: issue.Index}, Presence: PresenceAbsent}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, membership.Completion.Outcome)
	assert.Equal(t, "not_permitted", membership.Completion.ProblemCode)
}

func TestExpiredPlanTokenRejectsLifecycleChange(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	inspection, err := NewReadService().InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	originalNow := planTokenNow
	planTokenNow = func() time.Time { return originalNow().Add(planTokenLifetime + time.Second) }
	t.Cleanup(func() { planTokenNow = originalNow })
	commit, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID, ExpectedPlanToken: inspection.WorkPlan.PlanToken,
		Changes: []PlanChange{{Kind: PlanChangeSetPlanningState, ExpectedState: PlanningStateDraft, DesiredState: PlanningStateActive}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, commit.Completion.Outcome)
	assert.Equal(t, "conflict", commit.Completion.ProblemCode)
}

func TestDependencyPresenceConvergesWithoutDuplicateTimeline(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	blocked := insertMutationIssue(t, repo.ID, doer.ID, "Blocked", "")
	prerequisite := insertMutationIssue(t, repo.ID, doer.ID, "Prerequisite", "")
	request := PlanRevisionRequest{RepositoryID: repo.ID, ProjectID: plan.ID, Changes: []PlanChange{{
		Kind: PlanChangeEnsureDependency, Blocked: ItemSelector{IssueNumber: blocked.Index},
		Prerequisite: ItemSelector{IssueNumber: prerequisite.Index}, Presence: PresencePresent,
	}}}
	first, err := NewMutationService().RevisePlan(t.Context(), doer, request)
	require.NoError(t, err)
	second, err := NewMutationService().RevisePlan(t.Context(), doer, request)
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeApplied, first.Completion.Outcome)
	assert.Len(t, first.Completion.Events, 2)
	assert.Equal(t, mcpwork_model.OutcomeUnchanged, second.Completion.Outcome)
	assert.Empty(t, second.Completion.Events)
	unittest.AssertCount(t, &issues_model.IssueDependency{IssueID: blocked.ID, DependencyID: prerequisite.ID}, 1)
	unittest.AssertCount(t, &issues_model.Comment{Type: issues_model.CommentTypeAddDependency}, 2)
}

func TestItemDesiredStateAndBodyNoOpsDoNotEmitEffects(t *testing.T) {
	repo, doer, _ := prepareMutationPlan(t, project_model.PlanningStateDraft)
	issue := insertMutationIssue(t, repo.ID, doer.ID, "Stable", "Stable body")
	desiredState := ItemStateOpen
	commit, err := NewMutationService().ReviseItem(t.Context(), doer, ItemRevisionRequest{
		RepositoryID: repo.ID, IssueNumber: issue.Index,
		Markdown:     &ConditionalMarkdown{ExpectedContentVersion: issue.ContentVersion, Desired: issue.Content},
		DesiredState: &desiredState,
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeUnchanged, commit.Completion.Outcome)
	assert.Empty(t, commit.Effects)
	stored := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
	assert.Equal(t, issue.ContentVersion, stored.ContentVersion)
}

func TestIssueCloseReopenConvergesAndDependencyFailureRollsBackRevision(t *testing.T) {
	repo, doer, _ := prepareMutationPlan(t, project_model.PlanningStateDraft)
	issue := insertMutationIssue(t, repo.ID, doer.ID, "Lifecycle", "Original body")
	closed := ItemStateClosed
	first, err := NewMutationService().ReviseItem(t.Context(), doer, ItemRevisionRequest{
		RepositoryID: repo.ID, IssueNumber: issue.Index, DesiredState: &closed,
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeApplied, first.Completion.Outcome)
	assert.Len(t, first.Effects, 1)
	repeated, err := NewMutationService().ReviseItem(t.Context(), doer, ItemRevisionRequest{
		RepositoryID: repo.ID, IssueNumber: issue.Index, DesiredState: &closed,
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeUnchanged, repeated.Completion.Outcome)
	assert.Empty(t, repeated.Effects)
	opened := ItemStateOpen
	reopened, err := NewMutationService().ReviseItem(t.Context(), doer, ItemRevisionRequest{
		RepositoryID: repo.ID, IssueNumber: issue.Index, DesiredState: &opened,
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeApplied, reopened.Completion.Outcome)

	prerequisite := insertMutationIssue(t, repo.ID, doer.ID, "Prerequisite", "")
	_, err = issue_service.EnsureDependencyInWorkTx(t.Context(), doer, issue, prerequisite, issue_service.DependencyPresent, issue_service.WorkDependencyScope)
	require.NoError(t, err)
	current := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
	rejected, err := NewMutationService().ReviseItem(t.Context(), doer, ItemRevisionRequest{
		RepositoryID: repo.ID, IssueNumber: issue.Index,
		Markdown:     &ConditionalMarkdown{ExpectedContentVersion: current.ContentVersion, Desired: "Tentative body"},
		DesiredState: &closed,
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, rejected.Completion.Outcome)
	assert.Equal(t, "invalid_dependency", rejected.Completion.ProblemCode)
	stored := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID})
	assert.Equal(t, "Original body", stored.Content)
	assert.Equal(t, current.ContentVersion, stored.ContentVersion)
	assert.False(t, stored.IsClosed)
}

func TestReceiptEffectsArePostCommitOnlyAndReplaySafe(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	receipts, err := mcpwork_service.NewService([]byte(mutationReceiptSecret))
	require.NoError(t, err)
	service := NewMutationService()
	request := PlanRevisionRequest{RepositoryID: repo.ID, ProjectID: plan.ID, Changes: []PlanChange{{
		Kind: PlanChangeCreateMember, LocalReference: "created", Title: "Created",
	}}}
	receiptInput := receiptRequest("work_plan.revise", "post-commit-effects-000000000001")
	callbackCalls := 0
	dispatches := 0
	result, commit, err := ApplyReceiptMutation(t.Context(), receipts, receiptInput, func(ctx context.Context, operation mcpwork_service.Operation) (MutationCommit, error) {
		callbackCalls++
		commit, err := service.RevisePlanInWorkTx(ctx, doer, request, operation)
		require.Zero(t, callbackCalls-1, "the first receipt callback owns the only mutation attempt")
		commit.dispatchEffect = func(ctx context.Context, _ issue_service.PostCommitEffect) {
			dispatches++
			stored, _, _, lookupErr := mcpwork_model.GetReceiptByUUID(ctx, operation.UUID)
			require.NoError(t, lookupErr)
			assert.Equal(t, mcpwork_model.OutcomeApplied, stored.Outcome)
		}
		assert.Zero(t, dispatches, "effects remain inert inside the receipt transaction")
		return commit, err
	})
	require.NoError(t, err)
	require.False(t, result.Replayed)
	require.Len(t, commit.Effects, 1)
	assert.Equal(t, 1, dispatches)

	replayed, _, err := ApplyReceiptMutation(t.Context(), receipts, receiptInput, func(context.Context, mcpwork_service.Operation) (MutationCommit, error) {
		callbackCalls++
		return MutationCommit{}, nil
	})
	require.NoError(t, err)
	assert.True(t, replayed.Replayed)
	assert.Equal(t, 1, callbackCalls)
	assert.Equal(t, 1, dispatches)
}

func TestArchivedRepositoryRejectsMutationAndUnarchiveRecomposes(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateActive)
	issue := insertMutationIssue(t, repo.ID, doer.ID, "Ready", "")
	_, _, err := issues_model.EnsureIssueProjectInWorkTx(t.Context(), issue, doer, plan, true)
	require.NoError(t, err)
	reader := NewReadService()
	ready, err := reader.InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	require.Len(t, ready.WorkPlan.ReadyFrontier, 1)

	require.NoError(t, repo_model.SetArchiveRepoState(t.Context(), repo, true))
	commit, err := NewMutationService().RevisePlan(t.Context(), doer, PlanRevisionRequest{
		RepositoryID: repo.ID, ProjectID: plan.ID,
		Changes: []PlanChange{{Kind: PlanChangeEnsureMember, WorkItem: ItemSelector{IssueNumber: issue.Index}, Presence: PresencePresent}},
	})
	require.NoError(t, err)
	assert.Equal(t, mcpwork_model.OutcomeRejected, commit.Completion.Outcome)
	assert.Equal(t, "not_permitted", commit.Completion.ProblemCode)
	archived, err := reader.InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	assert.Empty(t, archived.WorkPlan.ReadyFrontier)
	require.NoError(t, repo_model.SetArchiveRepoState(t.Context(), repo, false))
	recomposed, err := reader.InspectPlan(t.Context(), doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: plan.ID})
	require.NoError(t, err)
	require.Len(t, recomposed.WorkPlan.ReadyFrontier, 1)
	unittest.AssertExistsAndLoadBean(t, &project_model.ProjectIssue{ProjectID: plan.ID, IssueID: issue.ID})
}

func TestCancelledReceiptMutationCommitsNothing(t *testing.T) {
	repo, doer, plan := prepareMutationPlan(t, project_model.PlanningStateDraft)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	receipts, err := mcpwork_service.NewService([]byte(mutationReceiptSecret))
	require.NoError(t, err)
	service := NewMutationService()
	_, err = receipts.Execute(ctx, receiptRequest("work_plan.revise", "cancelled-plan-revision-000000001"), func(ctx context.Context, operation mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		commit, err := service.RevisePlanInWorkTx(ctx, doer, PlanRevisionRequest{
			RepositoryID: repo.ID, ProjectID: plan.ID,
			Changes: []PlanChange{{Kind: PlanChangeCreateMember, LocalReference: "cancelled", Title: "Cancelled"}},
		}, operation)
		return commit.Completion, err
	})
	require.ErrorIs(t, err, context.Canceled)
	unittest.AssertNotExistsBean(t, &issues_model.Issue{RepoID: repo.ID, Title: "Cancelled"})
	unittest.AssertNotExistsBean(t, &mcpwork_model.Receipt{Tool: "work_plan.revise"})
}

func executePlanReceipt(t *testing.T, doer *user_model.User, request PlanRevisionRequest, key string) (MutationCommit, *mcpwork_service.Result) {
	t.Helper()
	receipts, err := mcpwork_service.NewService([]byte(mutationReceiptSecret))
	require.NoError(t, err)
	service := NewMutationService()
	var commit MutationCommit
	result, err := receipts.Execute(t.Context(), receiptRequest("work_plan.revise", key), func(ctx context.Context, operation mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		commit = MutationCommit{}
		var err error
		commit, err = service.RevisePlanInWorkTx(ctx, doer, request, operation)
		return commit.Completion, err
	})
	require.NoError(t, err)
	return commit, result
}

func receiptRequest(tool, key string) mcpwork_service.Request {
	return mcpwork_service.Request{
		Tool: tool, SchemaVersion: "1", IdempotencyKey: key,
		ExpandedInput:     fmt.Appendf(nil, `{"idempotencyKey":%q}`, key),
		ClientAttribution: mcpwork_service.ClientAttribution{Harness: "Example Harness", HarnessVersion: "1.0", Model: "Example Model", Source: "client-reported"},
		Authority: mcpwork_service.Authority{
			Profile: "work-planning", RegisteredClientLabel: "Example Client", RegisteredInstallationLabel: "Example Installation",
			PrincipalID: 2, OAuthApplicationID: 101, OAuthGrantID: 102,
			CredentialJTI: "22222222-2222-4222-8222-222222222222", Audience: "https://forge.example/mcp",
			Scope: "read:repository write:issue write:repository",
		},
	}
}

func createdIssueNumber(t *testing.T, commit MutationCommit, localReference string) int64 {
	t.Helper()
	value, found := strings.CutPrefix(commit.CreatedReferences[localReference], "issue/")
	require.True(t, found)
	number, err := strconv.ParseInt(value, 10, 64)
	require.NoError(t, err)
	return number
}

func prepareMutationPlan(t *testing.T, state project_model.PlanningState) (*repo_model.Repository, *user_model.User, *project_model.Project) {
	t.Helper()
	require.NoError(t, unittest.PrepareTestDatabase())
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	issuesUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: repo.ID, Type: unit.TypeIssues})
	issuesUnit.IssuesConfig().EnableDependencies = true
	require.NoError(t, repo_model.UpdateRepoUnitConfig(t.Context(), issuesUnit))
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	return repo, doer, newMutationProject(t, repo.ID, state)
}

func newMutationProject(t *testing.T, repoID int64, state project_model.PlanningState) *project_model.Project {
	t.Helper()
	project := &project_model.Project{
		RepoID: repoID, Type: project_model.TypeRepository, CreatorID: 2,
		Title: "Mutation plan", PlanningState: state,
	}
	require.NoError(t, project_model.NewProject(t.Context(), project))
	return project
}

func insertMutationIssue(t *testing.T, repoID, posterID int64, title, content string) *issues_model.Issue {
	t.Helper()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repoID})
	poster := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: posterID})
	issue := &issues_model.Issue{RepoID: repoID, Repo: repo, PosterID: posterID, Poster: poster, Title: title, Content: content}
	require.NoError(t, issues_model.NewIssue(t.Context(), repo, issue, nil, nil))
	return issue
}
