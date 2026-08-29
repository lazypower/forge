// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"testing"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/references"
	mcpwork_service "gitea.dev/services/mcpwork"
	work_service "gitea.dev/services/work"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHumanWorkViewsUsePermissionFilteredProjection(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	publicRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	hiddenRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 6})
	principal := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	viewer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})
	require.True(t, hiddenRepo.IsPrivate)

	plan := &project_model.Project{
		RepoID: publicRepo.ID, Type: project_model.TypeRepository, CreatorID: principal.ID,
		Title: "Human symmetry plan", PlanningState: project_model.PlanningStateActive,
	}
	require.NoError(t, project_model.NewProject(t.Context(), plan))
	column, err := plan.MustDefaultColumn(t.Context())
	require.NoError(t, err)

	item := &issues_model.Issue{
		RepoID: publicRepo.ID, Repo: publicRepo, Index: 98_501, PosterID: principal.ID,
		Title: "Visible planned work", Content: "Visible work body",
	}
	hiddenPrerequisite := &issues_model.Issue{
		RepoID: hiddenRepo.ID, Repo: hiddenRepo, Index: 98_502, PosterID: principal.ID,
		Title: "HIDDEN-PREREQUISITE-MARKER",
	}
	hiddenPullIssue := &issues_model.Issue{
		RepoID: hiddenRepo.ID, Repo: hiddenRepo, Index: 98_503, PosterID: principal.ID,
		Title: "HIDDEN-DELIVERY-MARKER", IsPull: true,
	}
	require.NoError(t, db.Insert(t.Context(), item, hiddenPrerequisite, hiddenPullIssue))
	require.NoError(t, db.Insert(t.Context(),
		&project_model.ProjectIssue{ProjectID: plan.ID, IssueID: item.ID, ProjectColumnID: column.ID},
		&issues_model.IssueDependency{UserID: principal.ID, IssueID: item.ID, DependencyID: hiddenPrerequisite.ID},
		&issues_model.PullRequest{
			IssueID: hiddenPullIssue.ID, Index: hiddenPullIssue.Index,
			HeadRepoID: hiddenRepo.ID, BaseRepoID: hiddenRepo.ID, HeadBranch: "hidden", BaseBranch: "main",
		},
		&issues_model.Comment{
			Type: issues_model.CommentTypePullRef, PosterID: principal.ID, IssueID: item.ID,
			RefRepoID: hiddenRepo.ID, RefIssueID: hiddenPullIssue.ID, RefIsPull: true, RefAction: references.XRefActionCloses,
		},
		&issues_model.Comment{
			Type: issues_model.CommentTypePullRef, PosterID: principal.ID, IssueID: item.ID,
			RefRepoID: publicRepo.ID, RefIssueID: 2, RefIsPull: true, RefAction: references.XRefActionCloses,
		},
	))

	provenanceEvent, err := issues_model.CreateComment(t.Context(), &issues_model.CreateCommentOptions{
		Type: issues_model.CommentTypeChangeTitle, Doer: principal, Repo: publicRepo, Issue: item,
		OldTitle: "Earlier title", NewTitle: item.Title,
	})
	require.NoError(t, err)
	receipts, err := mcpwork_service.NewService([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	receipt, err := receipts.Execute(t.Context(), mcpwork_service.Request{
		Tool: "work_plan.revise", SchemaVersion: "1", IdempotencyKey: "human-work-view-receipt-000000000001",
		ExpandedInput: []byte(`{"idempotencyKey":"human-work-view-receipt-000000000001","marker":"human-view"}`),
		Authority: mcpwork_service.Authority{
			PrincipalID: principal.ID, OAuthApplicationID: 701, OAuthGrantID: 702,
			CredentialJTI: "77777777-7777-4777-8777-777777777777",
			Audience:      "https://forge.example/mcp", Scope: "read:repository write:issue write:repository",
		},
	}, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		return mcpwork_service.Completion{
			Outcome: mcpwork_model.OutcomeApplied,
			Artifacts: []mcpwork_service.ArtifactReference{
				{RepositoryID: publicRepo.ID, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: plan.ID},
				{RepositoryID: publicRepo.ID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: item.ID, ArtifactNumber: item.Index},
				{RepositoryID: hiddenRepo.ID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: hiddenPrerequisite.ID, ArtifactNumber: hiddenPrerequisite.Index},
			},
			Events: []mcpwork_service.EventReference{{
				RepositoryID: publicRepo.ID, Kind: mcpwork_model.EventKindIssueComment, EventID: provenanceEvent.ID,
				ArtifactKind: mcpwork_model.ArtifactKindIssue, ArtifactID: item.ID,
			}},
		}, nil
	})
	require.NoError(t, err)

	planProjection, err := work_service.NewReadService().InspectPlan(t.Context(), viewer, work_service.PlanRequest{
		Owner: publicRepo.OwnerName, Repository: publicRepo.Name, ProjectID: plan.ID,
	})
	require.NoError(t, err)
	assert.Equal(t, "concern", planProjection.WorkPlan.Integrity.Status)
	assert.Empty(t, planProjection.WorkPlan.ReadyFrontier)
	itemProjection, err := work_service.NewReadService().InspectItem(t.Context(), viewer, work_service.ItemRequest{
		Owner: publicRepo.OwnerName, Repository: publicRepo.Name, IssueNumber: item.Index, SelectedProjectID: plan.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, itemProjection.SelectedContext)
	assert.Equal(t, "blocked", itemProjection.SelectedContext.DerivedState)
	require.Len(t, itemProjection.SelectedContext.PrerequisiteSummaries, 1)
	assert.Equal(t, "undisclosed", itemProjection.SelectedContext.PrerequisiteSummaries[0].Availability)
	require.Len(t, itemProjection.WorkItem.DeliverySummaries, 1)
	assert.Equal(t, "pull/2", itemProjection.WorkItem.DeliverySummaries[0].Ref)

	sess := loginUser(t, viewer.Name)
	projectURL := fmt.Sprintf("/%s/projects/%d", publicRepo.FullName(), plan.ID)
	resp := sess.MakeRequest(t, NewRequest(t, "GET", projectURL), http.StatusOK)
	projectHTML := NewHTMLParser(t, resp.Body)
	assert.Equal(t, 1, projectHTML.Find(`.work-plan[data-work-plan-state="active"][data-work-plan-integrity="concern"]`).Length())
	assert.Equal(t, 1, projectHTML.Find(`[data-work-integrity-code="unresolved_prerequisite"]`).Length())
	assert.Equal(t, 0, projectHTML.Find(".work-ready-frontier [data-work-context]").Length())
	assert.Contains(t, projectHTML.doc.Text(), "Performed through MCP using @"+principal.Name+"'s authority; software actor unverified.")
	assert.NotContains(t, projectHTML.doc.Text(), "performed this")
	assertHumanWorkMarkupHides(t, projectHTML,
		hiddenPrerequisite.Title, hiddenPullIssue.Title, hiddenRepo.Name,
		fmt.Sprintf("/%s/issues/%d", hiddenRepo.FullName(), hiddenPrerequisite.Index),
		fmt.Sprintf("/%s/pulls/%d", hiddenRepo.FullName(), hiddenPullIssue.Index),
		receipt.OperationUUID, "human-work-view-receipt-000000000001", "human-view",
		"77777777-7777-4777-8777-777777777777", "https://forge.example/mcp",
	)
	assert.Equal(t, 0, projectHTML.Find(`[data-url*="/planning/active"]`).Length())
	assert.Equal(t, 0, projectHTML.Find(`[data-url*="/planning/draft"]`).Length())
	assert.Equal(t, 0, projectHTML.Find(`[data-url*="/delete?plan_token="]`).Length())

	issueURL := fmt.Sprintf("/%s/issues/%d?work_plan=%d", publicRepo.FullName(), item.Index, plan.ID)
	resp = sess.MakeRequest(t, NewRequest(t, "GET", issueURL), http.StatusOK)
	issueHTML := NewHTMLParser(t, resp.Body)
	contextSelector := fmt.Sprintf(`.selected-work-context[data-work-context="project/%d/issue/%d"][data-work-state="blocked"][data-work-integrity="concern"]`, plan.ID, item.Index)
	assert.Equal(t, 1, issueHTML.Find(contextSelector).Length())
	assert.Equal(t, 1, issueHTML.Find(".work-undisclosed-prerequisite").Length())
	assert.Equal(t, 1, issueHTML.Find(`[data-work-delivery="pull/2"]`).Length())
	assert.Contains(t, issueHTML.doc.Text(), "Performed through MCP using @"+principal.Name+"'s authority; software actor unverified.")
	assertHumanWorkMarkupHides(t, issueHTML,
		hiddenPrerequisite.Title, hiddenPullIssue.Title, hiddenRepo.Name,
		fmt.Sprintf("/%s/issues/%d", hiddenRepo.FullName(), hiddenPrerequisite.Index),
		fmt.Sprintf("/%s/pulls/%d", hiddenRepo.FullName(), hiddenPullIssue.Index),
		receipt.OperationUUID, "human-work-view-receipt-000000000001", "human-view",
		"77777777-7777-4777-8777-777777777777", "https://forge.example/mcp",
	)

	pullURL := fmt.Sprintf("/%s/pulls/2", publicRepo.FullName())
	resp = sess.MakeRequest(t, NewRequest(t, "GET", pullURL), http.StatusOK)
	pullHTML := NewHTMLParser(t, resp.Body)
	assert.Equal(t, 1, pullHTML.Find(`[data-work-item="issue/`+strconv.FormatInt(item.Index, 10)+`"]`).Length())
	assertHumanWorkMarkupHides(t, pullHTML,
		hiddenPrerequisite.Title, hiddenPullIssue.Title, hiddenRepo.Name,
		fmt.Sprintf("/%s/issues/%d", hiddenRepo.FullName(), hiddenPrerequisite.Index),
		fmt.Sprintf("/%s/pulls/%d", hiddenRepo.FullName(), hiddenPullIssue.Index),
		receipt.OperationUUID, "human-work-view-receipt-000000000001", "human-view",
		"77777777-7777-4777-8777-777777777777", "https://forge.example/mcp",
	)
}

func assertHumanWorkMarkupHides(t *testing.T, html *HTMLDoc, identities ...string) {
	t.Helper()
	markup, err := html.doc.Html()
	require.NoError(t, err)
	for _, identity := range identities {
		assert.NotContains(t, markup, identity)
	}
}

func TestDisabledProjectRetainsOrdinaryControls(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	project := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: 1, RepoID: 1})
	repository := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.Equal(t, project_model.PlanningStateDisabled, project.PlanningState)
	ordinaryIssue := &issues_model.Issue{
		RepoID: repository.ID, Repo: repository, Index: 98_505, PosterID: 2,
		Title: "Ordinary issue without planning context",
	}
	require.NoError(t, db.Insert(t.Context(), ordinaryIssue))

	sess := loginUser(t, "user2")
	resp := sess.MakeRequest(t, NewRequest(t, "GET", "/user2/repo1/projects/1"), http.StatusOK)
	html := NewHTMLParser(t, resp.Body)
	assert.Equal(t, 0, html.Find(".work-plan").Length())
	assert.Equal(t, 1, html.Find(`[data-url$="/projects/1/delete?id=1"]`).Length())
	assert.GreaterOrEqual(t, html.Find(".show-project-column-modal-edit").Length(), 1)
	assert.Equal(t, 1, html.Find(`[data-url$="/projects/1/planning/begin"]`).Length())
	assert.Equal(t, 0, html.Find(`[data-url*="/planning/active"]`).Length())
	assert.NotContains(t, html.doc.Text(), "Ready work")
	for _, forbidden := range []string{"Adopt work", "Claim work", "Lease work", "Executor", "Dispatcher", "Scheduler"} {
		assert.NotContains(t, html.doc.Text(), forbidden)
	}

	resp = sess.MakeRequest(t, NewRequest(t, "GET", fmt.Sprintf("/%s/issues/%d", repository.FullName(), ordinaryIssue.Index)), http.StatusOK)
	html = NewHTMLParser(t, resp.Body)
	assert.Equal(t, 0, html.Find(".work-item-context").Length())
	assert.NotContains(t, html.doc.Text(), "Work planning")
}

func TestHumanWorkPlanningLifecycleUsesGuardedMutations(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	repository := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	owner := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	project := &project_model.Project{
		RepoID: repository.ID, Type: project_model.TypeRepository, CreatorID: owner.ID,
		Title: "Guarded human lifecycle", PlanningState: project_model.PlanningStateDisabled,
	}
	require.NoError(t, project_model.NewProject(t.Context(), project))
	column, err := project.MustDefaultColumn(t.Context())
	require.NoError(t, err)
	item := &issues_model.Issue{
		RepoID: repository.ID, Repo: repository, Index: 98_504, PosterID: owner.ID,
		Title: "Ready human lifecycle item",
	}
	require.NoError(t, db.Insert(t.Context(), item))
	require.NoError(t, db.Insert(t.Context(), &project_model.ProjectIssue{
		ProjectID: project.ID, IssueID: item.ID, ProjectColumnID: column.ID,
	}))

	sess := loginUser(t, owner.Name)
	projectURL := fmt.Sprintf("/%s/projects/%d", repository.FullName(), project.ID)
	planningURL := projectURL + "/planning/"
	sess.MakeRequest(t, NewRequest(t, "POST", planningURL+"begin"), http.StatusOK)
	stored := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: project.ID})
	assert.Equal(t, project_model.PlanningStateDraft, stored.PlanningState)

	draftInspection, err := work_service.NewReadService().InspectPlan(t.Context(), owner, work_service.PlanRequest{
		Owner: repository.OwnerName, Repository: repository.Name, ProjectID: project.ID,
	})
	require.NoError(t, err)

	// A missing optimistic token is rejected by the shared mutation authority.
	sess.MakeRequest(t, NewRequest(t, "POST", planningURL+"active"), http.StatusOK)
	stored = unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: project.ID})
	assert.Equal(t, project_model.PlanningStateDraft, stored.PlanningState)

	sess.MakeRequest(t, NewRequestWithValues(t, "POST", planningURL+"active", map[string]string{
		"plan_token": draftInspection.WorkPlan.PlanToken,
	}), http.StatusOK)
	stored = unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: project.ID})
	assert.Equal(t, project_model.PlanningStateActive, stored.PlanningState)

	resp := sess.MakeRequest(t, NewRequest(t, "GET", projectURL), http.StatusOK)
	html := NewHTMLParser(t, resp.Body)
	assert.Equal(t, 1, html.Find(`.work-plan[data-work-plan-state="active"]`).Length())
	assert.Equal(t, 1, html.Find(`[data-url*="/planning/draft?plan_token="]`).Length())
	assert.Equal(t, 0, html.Find(`[data-url*="/delete?plan_token="]`).Length())

	activeInspection, err := work_service.NewReadService().InspectPlan(t.Context(), owner, work_service.PlanRequest{
		Owner: repository.OwnerName, Repository: repository.Name, ProjectID: project.ID,
	})
	require.NoError(t, err)
	require.Len(t, activeInspection.WorkPlan.ReadyFrontier, 1)
	assert.Equal(t, fmt.Sprintf("project/%d/issue/%d", project.ID, item.Index), activeInspection.WorkPlan.ReadyFrontier[0].Ref)
	assert.Equal(t, 1, html.Find(`[data-work-context="`+activeInspection.WorkPlan.ReadyFrontier[0].Ref+`"]`).Length())
	sess.MakeRequest(t, NewRequestWithValues(t, "POST", planningURL+"draft", map[string]string{
		"plan_token": activeInspection.WorkPlan.PlanToken,
	}), http.StatusOK)
	stored = unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: project.ID})
	assert.Equal(t, project_model.PlanningStateDraft, stored.PlanningState)

	refreshedDraft, err := work_service.NewReadService().InspectPlan(t.Context(), owner, work_service.PlanRequest{
		Owner: repository.OwnerName, Repository: repository.Name, ProjectID: project.ID,
	})
	require.NoError(t, err)
	sess.MakeRequest(t, NewRequestWithValues(t, "POST", projectURL+"/delete", map[string]string{
		"plan_token": activeInspection.WorkPlan.PlanToken,
	}), http.StatusOK)
	unremoved := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: project.ID})
	assert.Equal(t, project_model.PlanningStateDraft, unremoved.PlanningState)

	sess.MakeRequest(t, NewRequestWithValues(t, "POST", projectURL+"/delete", map[string]string{
		"plan_token": refreshedDraft.WorkPlan.PlanToken,
	}), http.StatusOK)
	_, err = project_model.GetProjectByID(t.Context(), project.ID)
	assert.True(t, project_model.IsErrProjectNotExist(err))
	unmodifiedItem := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: item.ID})
	assert.Equal(t, item.Title, unmodifiedItem.Title)
}
