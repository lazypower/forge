// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"testing"

	auth_model "gitea.dev/models/auth"
	mcpwork_model "gitea.dev/models/mcpwork"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/services/oauth2_provider"
	repo_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPWorkReplayAcrossGrantReplacement(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.MCP.ClientBootstrapEnabled, true)()
	defer test.MockVariableValue(&setting.MCP.WorkInspectionEnabled, true)()
	login := prepareMCPGrantBrowser(t)
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	require.NoError(t, repo_service.UpdateRepositoryUnits(t.Context(), repo, []repo_model.RepoUnit{
		{RepoID: repo.ID, Type: unit.TypeIssues, Config: &repo_model.IssuesConfig{EnableDependencies: true}},
		{RepoID: repo.ID, Type: unit.TypeProjects, Config: &repo_model.ProjectsConfig{ProjectsMode: repo_model.ProjectsModeRepo}},
	}, nil))
	first := bootstrapMCPGrantRegistration(t, "Example harness", "First installation")
	code, _ := consentMCPGrant(t, login, first, oauth2_provider.MCPWorkWriteScope, true)
	tokens := exchangeMCPGrantCode(t, first, code, http.StatusOK)
	firstGrant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ApplicationID: first.ID, UserID: 2})
	input := map[string]any{
		"repository": map[string]any{"owner": repo.OwnerName, "name": repo.Name}, "idempotencyKey": "onboarding-replay-plan-00001",
		"begin": map[string]any{"kind": "new", "title": "Onboarding replay plan"},
	}
	meta := mcpsdk.Meta{
		mcpsdk.MetaKeyProtocolVersion:      "2026-07-28",
		mcpsdk.MetaKeyClientCapabilities:   map[string]any{},
		mcpsdk.MetaKeyClientInfo:           map[string]any{"name": " First harness ", "version": " 1 "},
		"io.gitea.forge/clientAttribution": map[string]any{"model": " First model "},
	}
	original := mcpWorkHTTPStructured(t, callMCPWorkHTTP(t, tokens.AccessToken, "work_plan.begin", input, meta))
	require.Equal(t, "applied", original["status"])
	operation := original["operation"].(map[string]any)
	assert.Equal(t, map[string]any{"harness": "First harness", "harnessVersion": "1", "model": "First model", "source": "client-reported"}, operation["clientAttribution"])
	receipt := unittest.AssertExistsAndLoadBean(t, &mcpwork_model.Receipt{OperationUUID: operation["id"].(string)})
	assert.Equal(t, first.ID, receipt.ApplicationID)
	assert.Equal(t, firstGrant.ID, receipt.GrantID)
	counts := mcpWorkDogfoodNativeCounts(t)
	meta[mcpsdk.MetaKeyClientInfo] = map[string]any{"name": "Later harness", "version": "2"}
	meta["io.gitea.forge/clientAttribution"] = map[string]any{"model": "Later model"}
	assertReplay := func(token string) {
		t.Helper()
		replay := mcpWorkHTTPStructured(t, callMCPWorkHTTP(t, token, "work_plan.begin", input, meta))
		assertMCPWorkReplay(t, replay, original)
		assert.Equal(t, original["workPlan"].(map[string]any)["ref"], replay["workPlan"].(map[string]any)["ref"])
		assert.Equal(t, counts, mcpWorkDogfoodNativeCounts(t))
		assert.Equal(t, receipt, unittest.AssertExistsAndLoadBean(t, &mcpwork_model.Receipt{ID: receipt.ID}))
	}
	rotated := refreshMCPGrant(t, first, tokens.RefreshToken, http.StatusOK)
	assertReplay(rotated.AccessToken)

	readCode, _ := consentMCPGrant(t, login, first, oauth2_provider.MCPReadScope, true)
	readTokens := exchangeMCPGrantCode(t, first, readCode, http.StatusOK)
	assertMCPGrantLineageRejected(t, first, []string{code}, []*oauth2_provider.AccessTokenResponse{tokens, rotated})
	denied := mcpWorkHTTPStructured(t, callMCPWorkHTTP(t, readTokens.AccessToken, "work_plan.begin", input, meta))
	assert.Equal(t, "not_permitted", denied["problem"].(map[string]any)["code"])
	assert.NotContains(t, denied, "operation")
	assert.Equal(t, counts, mcpWorkDogfoodNativeCounts(t))

	replacementCode, _ := consentMCPGrant(t, login, first, oauth2_provider.MCPWorkWriteScope, true)
	replacementTokens := exchangeMCPGrantCode(t, first, replacementCode, http.StatusOK)
	replacementGrant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ApplicationID: first.ID, UserID: 2})
	require.NotEqual(t, firstGrant.ID, replacementGrant.ID)
	assertMCPGrantLineageRejected(t, first, []string{readCode}, []*oauth2_provider.AccessTokenResponse{readTokens})
	assertReplay(replacementTokens.AccessToken)

	login.MakeRequest(t, NewRequest(t, http.MethodPost, fmt.Sprintf("/user/settings/applications/oauth2/%d/revoke/%d", first.ID, replacementGrant.ID)), http.StatusOK)
	login.MakeRequest(t, NewRequest(t, http.MethodPost, fmt.Sprintf("/user/settings/applications/mcp/%d/delete", first.ID)), http.StatusOK)
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: first.ID})
	assertMCPGrantLineageRejected(t, first, []string{replacementCode}, []*oauth2_provider.AccessTokenResponse{replacementTokens})
	second := bootstrapMCPGrantRegistration(t, "Example harness", "Second installation")
	require.NotEqual(t, first.ClientID, second.ClientID)
	secondCode, _ := consentMCPGrant(t, login, second, oauth2_provider.MCPWorkWriteScope, true)
	secondTokens := exchangeMCPGrantCode(t, second, secondCode, http.StatusOK)
	assertReplay(secondTokens.AccessToken)
}
