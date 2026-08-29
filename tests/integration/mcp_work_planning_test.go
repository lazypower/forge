// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/routers"
	"gitea.dev/services/oauth2_provider"
	repo_service "gitea.dev/services/repository"
	work_service "gitea.dev/services/work"
	"gitea.dev/tests"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPWorkPlanningDogfoodWithOfficialClient(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var productionRoutes http.Handler
	forgeServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		productionRoutes.ServeHTTP(w, req)
	}))
	forgeServer.StartTLS()
	defer forgeServer.Close()

	defer test.MockVariableValue(&setting.AppURL, forgeServer.URL+"/")()
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test.MockVariableValue(&setting.MCP.WorkInspectionEnabled, true)()
	defer test.MockVariableValue(&setting.MCP.WorkMutationEnabled, true)()
	defer test.MockVariableValue(&setting.OAuth2.Enabled, true)()
	defer test.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, forgeServer.URL)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, repo_service.UpdateRepositoryUnits(t.Context(), repo, []repo_model.RepoUnit{
		{RepoID: repo.ID, Type: unit.TypeIssues, Config: &repo_model.IssuesConfig{EnableDependencies: true}},
		{RepoID: repo.ID, Type: unit.TypeProjects, Config: &repo_model.ProjectsConfig{ProjectsMode: repo_model.ProjectsModeRepo}},
	}, nil))
	require.NoError(t, auth_model.Init(t.Context()))
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()
	productionRoutes = testWebRoutes

	readSession := connectMCPWorkProfile(t, forgeServer, auth_model.MCPBuiltinOAuth2ApplicationClientID, oauth2_provider.MCPReadScope)
	readTools, err := readSession.ListTools(t.Context(), nil)
	require.NoError(t, err)
	assertMCPWorkToolNames(t, readTools)
	readRejected := callMCPWorkTool(t, readSession, "work_plan.begin", map[string]any{
		"repository":     map[string]any{"owner": repo.OwnerName, "name": repo.Name},
		"idempotencyKey": "read-profile-rejection-000001", "begin": map[string]any{"kind": "new", "title": "Denied"},
	})
	assert.Equal(t, "error", readRejected["status"])
	assert.Equal(t, "not_permitted", readRejected["problem"].(map[string]any)["code"])

	writeSession := connectMCPWorkProfile(t, forgeServer, auth_model.MCPWorkWriteBuiltinOAuth2ApplicationClientID, oauth2_provider.MCPWorkWriteScope)
	writeTools, err := writeSession.ListTools(t.Context(), nil)
	require.NoError(t, err)
	assertMCPWorkToolNames(t, writeTools)

	nativeCountsBefore := mcpWorkDogfoodNativeCounts(t)

	beginInput := map[string]any{
		"repository":     map[string]any{"owner": repo.OwnerName, "name": repo.Name},
		"idempotencyKey": "dogfood-plan-begin-00000001",
		"begin":          map[string]any{"kind": "new", "title": "Dogfood plan", "markdown": "Synthetic MCP work-planning coverage."},
	}
	begin := callMCPWorkTool(t, writeSession, "work_plan.begin", beginInput)
	assertCommittedMCPWorkResult(t, begin, "applied", false, "available")
	plan := begin["workPlan"].(map[string]any)
	planRef := plan["ref"].(string)
	planToken := plan["planToken"].(string)
	assert.Equal(t, "draft", plan["planningState"])
	readInspection := callMCPWorkTool(t, readSession, "work_plan.inspect", map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef,
	})
	assert.Equal(t, "available", readInspection["status"])
	assert.Equal(t, planRef, readInspection["workPlan"].(map[string]any)["ref"])

	inspectPlan := callMCPWorkTool(t, writeSession, "work_plan.inspect", map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef,
	})
	assert.Equal(t, "available", inspectPlan["status"])
	assert.Equal(t, planRef, inspectPlan["workPlan"].(map[string]any)["ref"])

	revisionInput := map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef,
		"idempotencyKey": "dogfood-plan-revision-000001",
		"changes": []any{
			map[string]any{"kind": "create_member", "localReference": "first", "title": "First dogfood item"},
			map[string]any{"kind": "create_member", "localReference": "second", "title": "Second dogfood item"},
			map[string]any{"kind": "create_member", "localReference": "third", "title": "Third dogfood item"},
			map[string]any{"kind": "ensure_dependency", "blocked": "local/second", "prerequisite": "local/first", "presence": "present"},
			map[string]any{"kind": "ensure_dependency", "blocked": "local/third", "prerequisite": "local/second", "presence": "present"},
		},
	}
	revised := callMCPWorkTool(t, writeSession, "work_plan.revise", revisionInput)
	assertCommittedMCPWorkResult(t, revised, "applied", false, "available")
	created := revised["createdReferences"].(map[string]any)
	require.Len(t, created, 3)
	firstRef := created["first"].(string)
	secondRef := created["second"].(string)
	thirdRef := created["third"].(string)
	assert.Equal(t, "draft", revised["workPlan"].(map[string]any)["planningState"])

	countsBeforeRevisionReplay := mcpWorkDogfoodNativeCounts(t)
	replayed := callMCPWorkTool(t, writeSession, "work_plan.revise", revisionInput)
	assertCommittedMCPWorkResult(t, replayed, "applied", true, "available")
	assert.Equal(t, created, replayed["createdReferences"])
	assert.Equal(t, revised["operation"].(map[string]any)["id"], replayed["operation"].(map[string]any)["id"])
	assert.Equal(t, countsBeforeRevisionReplay, mcpWorkDogfoodNativeCounts(t))

	changedReplay := map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef,
		"idempotencyKey": revisionInput["idempotencyKey"],
		"changes":        []any{map[string]any{"kind": "ensure_member", "workItem": firstRef, "presence": "present"}},
	}
	conflict := callMCPWorkTool(t, writeSession, "work_plan.revise", changedReplay)
	assert.Equal(t, "error", conflict["status"])
	assert.Equal(t, "idempotency_conflict", conflict["problem"].(map[string]any)["code"])
	assert.NotContains(t, conflict, "operation")

	cycleInput := map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef, "idempotencyKey": "dogfood-cycle-rejection-0001",
		"changes": []any{map[string]any{"kind": "ensure_dependency", "blocked": firstRef, "prerequisite": thirdRef, "presence": "present"}},
	}
	cycle := callMCPWorkTool(t, writeSession, "work_plan.revise", cycleInput)
	assert.Equal(t, "rejected", cycle["status"])
	assert.Equal(t, "invalid_dependency", cycle["problem"].(map[string]any)["code"])
	require.NotNil(t, cycle["operation"])

	planToken = revised["workPlan"].(map[string]any)["planToken"].(string)
	activationInput := map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef, "idempotencyKey": "dogfood-plan-activate-000001",
		"expectedPlanToken": planToken,
		"changes":           []any{map[string]any{"kind": "set_planning_state", "expected": "draft", "desired": "active"}},
	}
	activated := callMCPWorkTool(t, writeSession, "work_plan.revise", activationInput)
	assertCommittedMCPWorkResult(t, activated, "applied", false, "available")
	assert.Equal(t, "active", activated["workPlan"].(map[string]any)["planningState"])
	assertMCPReadyFrontier(t, activated["workPlan"].(map[string]any), planRef+"/"+firstRef)

	staleActivationInput := map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef, "idempotencyKey": "dogfood-stale-plan-token-0001",
		"expectedPlanToken": planToken,
		"changes":           []any{map[string]any{"kind": "set_planning_state", "expected": "active", "desired": "draft"}},
	}
	staleActivation := callMCPWorkTool(t, writeSession, "work_plan.revise", staleActivationInput)
	assert.Equal(t, "rejected", staleActivation["status"])
	assert.Equal(t, "conflict", staleActivation["problem"].(map[string]any)["code"])

	inspectItem := callMCPWorkTool(t, writeSession, "work_item.inspect", map[string]any{
		"repository": beginInput["repository"], "workItem": firstRef, "selectedPlan": planRef,
	})
	assert.Equal(t, "available", inspectItem["status"])
	assert.Equal(t, "ready", inspectItem["selectedContext"].(map[string]any)["derivedState"])

	secondNumber, err := strconv.ParseInt(strings.TrimPrefix(secondRef, "issue/"), 10, 64)
	require.NoError(t, err)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 5})
	humanRevision, err := work_service.NewMutationService().ReviseItem(t.Context(), doer, work_service.ItemRevisionRequest{
		RepositoryID: repo.ID, IssueNumber: secondNumber,
		Title: &work_service.ConditionalText{Expected: "Second dogfood item", Desired: "Second item refined by a human path"},
	})
	require.NoError(t, err)
	require.Equal(t, "applied", string(humanRevision.Completion.Outcome))
	afterHumanChange := callMCPWorkTool(t, writeSession, "work_item.inspect", map[string]any{
		"repository": beginInput["repository"], "workItem": secondRef, "selectedPlan": planRef,
	})
	assert.Equal(t, "Second item refined by a human path", afterHumanChange["workItem"].(map[string]any)["title"])

	staleItemInput := map[string]any{
		"repository": beginInput["repository"], "workItem": firstRef, "idempotencyKey": "dogfood-stale-item-00000001",
		"title": map[string]any{"expected": "stale title", "desired": "must not apply"},
	}
	stale := callMCPWorkTool(t, writeSession, "work_item.revise", staleItemInput)
	assert.Equal(t, "rejected", stale["status"])
	assert.Equal(t, "conflict", stale["problem"].(map[string]any)["code"])

	closeInput := map[string]any{
		"repository": beginInput["repository"], "workItem": firstRef, "idempotencyKey": "dogfood-close-item-000000001",
		"state": map[string]any{"desired": "closed"},
	}
	closed := callMCPWorkTool(t, writeSession, "work_item.revise", closeInput)
	assertCommittedMCPWorkResult(t, closed, "applied", false, "available")
	assert.Equal(t, "closed", closed["workItem"].(map[string]any)["state"])

	frontier := callMCPWorkTool(t, writeSession, "work_plan.inspect", map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef, "pageKind": "ready_frontier",
	})
	assert.Equal(t, "available", frontier["status"])
	assertMCPReadyFrontier(t, frontier["workPlan"].(map[string]any), planRef+"/"+secondRef)

	missing := callMCPWorkTool(t, writeSession, "work_plan.begin", map[string]any{
		"repository":     map[string]any{"owner": "missing-owner", "name": "missing-repository"},
		"idempotencyKey": "dogfood-missing-target-00001", "begin": map[string]any{"kind": "new", "title": "Hidden"},
	})
	denied := callMCPWorkTool(t, writeSession, "work_plan.begin", map[string]any{
		"repository":     map[string]any{"owner": "user2", "name": "repo2"},
		"idempotencyKey": "dogfood-denied-target-000001", "begin": map[string]any{"kind": "new", "title": "Hidden"},
	})
	assert.Equal(t, "unavailable", missing["problem"].(map[string]any)["code"])
	assert.Equal(t, missing["problem"], denied["problem"])
	assert.NotContains(t, denied, "workPlan")
	assert.NotContains(t, denied, "repository")

	projectID, err := strconv.ParseInt(strings.TrimPrefix(planRef, "project/"), 10, 64)
	require.NoError(t, err)
	project := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: projectID, RepoID: repo.ID})
	assert.Equal(t, project_model.PlanningStateActive, project.PlanningState)
	for _, issueRef := range []string{firstRef, secondRef, thirdRef} {
		issueNumber, err := strconv.ParseInt(strings.TrimPrefix(issueRef, "issue/"), 10, 64)
		require.NoError(t, err)
		_, err = issues_model.GetIssueByIndex(t.Context(), repo.ID, issueNumber)
		require.NoError(t, err)
	}
	assert.Equal(t, nativeCountsBefore["projects"]+1, unittest.GetCount(t, new(project_model.Project)))
	assert.Equal(t, nativeCountsBefore["issues"]+3, unittest.GetCount(t, new(issues_model.Issue)))
	assert.Equal(t, nativeCountsBefore["memberships"]+3, unittest.GetCount(t, new(project_model.ProjectIssue)))
	assert.Equal(t, nativeCountsBefore["dependencies"]+2, unittest.GetCount(t, new(issues_model.IssueDependency)))
	assert.Greater(t, unittest.GetCount(t, new(mcpwork_model.Receipt)), nativeCountsBefore["receipts"])

	human := loginUser(t, doer.Name)
	humanResponse := human.MakeRequest(t, NewRequest(t, http.MethodGet, fmt.Sprintf("/%s/projects/%d", repo.FullName(), projectID)), http.StatusOK)
	humanHTML := NewHTMLParser(t, humanResponse.Body)
	assert.Equal(t, 1, humanHTML.Find(`.work-plan[data-work-plan-state="active"]`).Length())
	assert.Equal(t, 1, humanHTML.Find(`[data-work-context="`+planRef+`/`+secondRef+`"]`).Length())
	assert.Contains(t, humanHTML.doc.Text(), "Performed through MCP using @"+doer.Name+"'s authority; software actor unverified.")

	countsBeforeFullReplay := mcpWorkDogfoodNativeCounts(t)
	assertMCPWorkReplay(t, callMCPWorkTool(t, writeSession, "work_plan.begin", beginInput), begin)
	assertMCPWorkReplay(t, callMCPWorkTool(t, writeSession, "work_plan.revise", revisionInput), revised)
	conflictReplay := callMCPWorkTool(t, writeSession, "work_plan.revise", changedReplay)
	assert.Equal(t, conflict, conflictReplay)
	assertMCPWorkReplay(t, callMCPWorkTool(t, writeSession, "work_plan.revise", cycleInput), cycle)
	assertMCPWorkReplay(t, callMCPWorkTool(t, writeSession, "work_plan.revise", activationInput), activated)
	assertMCPWorkReplay(t, callMCPWorkTool(t, writeSession, "work_plan.revise", staleActivationInput), staleActivation)
	assertMCPWorkReplay(t, callMCPWorkTool(t, writeSession, "work_item.revise", staleItemInput), stale)
	assertMCPWorkReplay(t, callMCPWorkTool(t, writeSession, "work_item.revise", closeInput), closed)
	assert.Equal(t, countsBeforeFullReplay, mcpWorkDogfoodNativeCounts(t))
	readInspectionAfterReplay := callMCPWorkTool(t, readSession, "work_plan.inspect", map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef,
	})
	assert.Equal(t, "available", readInspectionAfterReplay["status"])
	assert.Equal(t, "active", readInspectionAfterReplay["workPlan"].(map[string]any)["planningState"])
	assertMCPReadyFrontier(t, readInspectionAfterReplay["workPlan"].(map[string]any), planRef+"/"+secondRef)
	humanReplayResponse := human.MakeRequest(t, NewRequest(t, http.MethodGet, fmt.Sprintf("/%s/projects/%d", repo.FullName(), projectID)), http.StatusOK)
	humanReplayHTML := NewHTMLParser(t, humanReplayResponse.Body)
	assert.Equal(t, 1, humanReplayHTML.Find(`.work-plan[data-work-plan-state="active"]`).Length())
	assert.Equal(t, 1, humanReplayHTML.Find(`[data-work-context="`+planRef+`/`+secondRef+`"]`).Length())
	assert.Contains(t, humanReplayHTML.doc.Text(), "Performed through MCP using @"+doer.Name+"'s authority; software actor unverified.")

	writeApp, err := auth_model.GetOAuth2ApplicationByClientID(t.Context(), auth_model.MCPWorkWriteBuiltinOAuth2ApplicationClientID)
	require.NoError(t, err)
	setting.MCP.WorkMutationEnabled = false
	productionRoutes = routers.NormalRoutes()
	readOnlyTools, err := readSession.ListTools(t.Context(), nil)
	require.NoError(t, err)
	readOnlyNames := make([]string, 0, len(readOnlyTools.Tools))
	for _, tool := range readOnlyTools.Tools {
		readOnlyNames = append(readOnlyNames, tool.Name)
	}
	assert.ElementsMatch(t, []string{"pull_request.inspect", "work_item.inspect", "work_plan.inspect"}, readOnlyNames)
	_, err = oauth2_provider.CanonicalMCPAuthorizationScope(writeApp, oauth2_provider.MCPWorkWriteScope)
	require.ErrorIs(t, err, oauth2_provider.ErrInvalidMCPProfileRequest)
	pullRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	pull, err := issues_model.GetPullRequestByIndex(t.Context(), pullRepo.ID, 3)
	require.NoError(t, err)
	pullResult, pullInspection := callMCPPullRequestInspect(t, readSession, map[string]any{
		"owner": pullRepo.OwnerName, "repository": pullRepo.Name, "number": pull.Index,
	})
	assert.False(t, pullResult.IsError)
	assert.Equal(t, "available", pullInspection["status"])

	assert.NotEqual(t, firstRef, secondRef)
	assert.NotEqual(t, secondRef, thirdRef)
}

func mcpWorkDogfoodNativeCounts(t *testing.T) map[string]int {
	t.Helper()
	return map[string]int{
		"projects":     unittest.GetCount(t, new(project_model.Project)),
		"issues":       unittest.GetCount(t, new(issues_model.Issue)),
		"memberships":  unittest.GetCount(t, new(project_model.ProjectIssue)),
		"dependencies": unittest.GetCount(t, new(issues_model.IssueDependency)),
		"comments":     unittest.GetCount(t, new(issues_model.Comment)),
		"receipts":     unittest.GetCount(t, new(mcpwork_model.Receipt)),
		"artifacts":    unittest.GetCount(t, new(mcpwork_model.ArtifactLink)),
		"events":       unittest.GetCount(t, new(mcpwork_model.EventLink)),
	}
}

func assertMCPWorkReplay(t *testing.T, replay, original map[string]any) {
	t.Helper()
	assert.Equal(t, original["status"], replay["status"])
	replayedOperation, ok := replay["operation"].(map[string]any)
	require.True(t, ok)
	originalOperation, ok := original["operation"].(map[string]any)
	require.True(t, ok)
	assert.True(t, replayedOperation["replayed"].(bool))
	assert.Equal(t, originalOperation["id"], replayedOperation["id"])
}

func connectMCPWorkProfile(t *testing.T, server *httptest.Server, clientID, scope string) *mcpsdk.ClientSession {
	t.Helper()
	app, err := auth_model.GetOAuth2ApplicationByClientID(t.Context(), clientID)
	require.NoError(t, err)
	grant, err := app.GetGrantByUserID(t.Context(), 5)
	require.NoError(t, err)
	if grant == nil {
		grant, err = app.CreateGrant(t.Context(), 5, scope)
		require.NoError(t, err)
	}
	token := signMCPConformanceAccessToken(t, grant.ID, oauth2_provider.TokenIssuer(), strconv.FormatInt(grant.UserID, 10), []string{setting.MCPResource()}, time.Now().Add(time.Hour))
	httpClient := &http.Client{Transport: mcpAuthorizationTransport{token: token, base: server.Client().Transport}}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "work-dogfood", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	t.Cleanup(cancel)
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint: server.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	return session
}

func callMCPWorkTool(t *testing.T, session *mcpsdk.ClientSession, name string, arguments map[string]any) map[string]any {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	return structured
}

func assertMCPWorkToolNames(t *testing.T, listed *mcpsdk.ListToolsResult) {
	t.Helper()
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	assert.ElementsMatch(t, []string{
		"pull_request.inspect", "work_item.inspect", "work_plan.inspect", "work_plan.begin", "work_item.revise", "work_plan.revise",
	}, names)
}

func assertCommittedMCPWorkResult(t *testing.T, result map[string]any, status string, replayed bool, current string) {
	t.Helper()
	require.Equal(t, status, result["status"])
	require.Equal(t, current, result["currentResultStatus"])
	operation, ok := result["operation"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, replayed, operation["replayed"])
	assert.NotEmpty(t, operation["id"])
	assert.NotEmpty(t, operation["committedAt"])
}

func assertMCPReadyFrontier(t *testing.T, plan map[string]any, expected string) {
	t.Helper()
	frontier, ok := plan["readyFrontier"].([]any)
	require.True(t, ok)
	require.Len(t, frontier, 1)
	assert.Equal(t, expected, frontier[0].(map[string]any)["ref"])
}

func TestMCPWorkMutationDiscoveryExcludesPATProfile(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	pat := newPersistedMCPPAT(t, 2, "mcp-work-mutation-discovery", auth_model.AccessTokenScopeReadRepository)
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfilePAT)()
	defer test.MockVariableValue(&setting.MCP.WorkInspectionEnabled, true)()
	defer test.MockVariableValue(&setting.MCP.WorkMutationEnabled, true)()
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

	server := httptest.NewServer(testWebRoutes)
	defer server.Close()
	httpClient := &http.Client{Transport: mcpAuthorizationTransport{token: pat, base: server.Client().Transport}}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "pat-discovery", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcpsdk.StreamableClientTransport{
		Endpoint: server.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	defer session.Close()
	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	assert.ElementsMatch(t, []string{"pull_request.inspect", "work_item.inspect", "work_plan.inspect"}, names)
	_, err = session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "work_plan.begin", Arguments: map[string]any{
		"repository": map[string]any{"owner": "user2", "name": "repo1"}, "idempotencyKey": "pat-mutation-rejection-00001",
		"begin": map[string]any{"kind": "new", "title": "Denied"},
	}})
	require.Error(t, err)
}
