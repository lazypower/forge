// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"fmt"
	"maps"
	"net/http"
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
	"gitea.dev/modules/json"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/modules/util"
	"gitea.dev/routers"
	"gitea.dev/services/oauth2_provider"
	repo_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPWorkAttributionHTTPBoundary(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "https://forge.example")()
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test.MockVariableValue(&setting.MCP.WorkInspectionEnabled, true)()
	defer test.MockVariableValue(&setting.MCP.WorkMutationEnabled, true)()
	defer test.MockVariableValue(&setting.OAuth2.Enabled, true)()
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 4})
	require.NoError(t, repo_service.UpdateRepositoryUnits(t.Context(), repo, []repo_model.RepoUnit{
		{RepoID: repo.ID, Type: unit.TypeIssues, Config: &repo_model.IssuesConfig{EnableDependencies: true}},
		{RepoID: repo.ID, Type: unit.TypeProjects, Config: &repo_model.ProjectsConfig{ProjectsMode: repo_model.ProjectsModeRepo}},
	}, nil))
	require.NoError(t, auth_model.Init(t.Context()))
	defer test.MockVariableValue(&setting.Log.AccessLogTemplate, `{{.Ctx.Req.Method}} {{.Ctx.Req.URL.RequestURI}}`)()
	capturedLogs := map[string]*bytes.Buffer{}
	var stopLogging []func()
	for _, name := range []string{log.DEFAULT, "access"} {
		buffer := new(bytes.Buffer)
		capturedLogs[name] = buffer
		writer := log.NewEventWriterBase("mcp-attribution-"+name, "test", log.WriterMode{Level: log.TRACE})
		writer.OutputWriteCloser = util.NopCloser{Writer: buffer}
		logger := log.GetManager().GetLogger(name)
		logger.AddWriters(writer)
		removed := false
		stop := func() {
			if !removed {
				removed = true
				require.NoError(t, logger.RemoveWriter(writer.GetWriterName()))
			}
		}
		t.Cleanup(stop)
		stopLogging = append(stopLogging, stop)
	}
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Registered HTTP harness", "Example installation", []string{"http://127.0.0.1/callback"}, auth_model.MCPRedirectClassLoopback, time.Now().Add(time.Minute), 1000)
	require.NoError(t, err)
	app, grant, _, err := auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, 5, oauth2_provider.MCPWorkWriteScope, "", "http://127.0.0.1/callback", "mcp-integration-challenge", "S256", setting.MCPResource(), time.Now())
	require.NoError(t, err)
	token := signMCPConformanceAccessToken(t, grant.ID, oauth2_provider.TokenIssuer(), strconv.FormatInt(grant.UserID, 10), []string{setting.MCPResource()}, time.Now().Add(time.Hour))

	validMeta := mcpsdk.Meta{
		mcpsdk.MetaKeyProtocolVersion:      "2026-07-28",
		mcpsdk.MetaKeyClientCapabilities:   map[string]any{},
		mcpsdk.MetaKeyClientInfo:           map[string]any{"name": " HTTP harness ", "version": " 1 "},
		"io.gitea.forge/clientAttribution": map[string]any{"model": " Example Model "},
	}
	beginInput := map[string]any{
		"repository": map[string]any{"owner": repo.OwnerName, "name": repo.Name}, "idempotencyKey": "attribution-http-begin-00001",
		"begin": map[string]any{"kind": "new", "title": "Attribution boundary plan"},
	}
	begin := mcpWorkHTTPStructured(t, callMCPWorkHTTP(t, token, "work_plan.begin", beginInput, validMeta))
	require.Equal(t, "applied", begin["status"])
	planRef := begin["workPlan"].(map[string]any)["ref"].(string)
	revisionInput := map[string]any{
		"repository": beginInput["repository"], "workPlan": planRef, "idempotencyKey": "attribution-http-plan-000001",
		"changes": []any{map[string]any{"kind": "create_member", "localReference": "first", "title": "Attribution boundary item"}},
	}
	revised := mcpWorkHTTPStructured(t, callMCPWorkHTTP(t, token, "work_plan.revise", revisionInput, validMeta))
	require.Equal(t, "applied", revised["status"])
	itemRef := revised["createdReferences"].(map[string]any)["first"].(string)
	itemInput := map[string]any{
		"repository": beginInput["repository"], "workItem": itemRef, "idempotencyKey": "attribution-http-item-000001",
		"title": map[string]any{"expected": "Attribution boundary item", "desired": "Attribution item revised"},
	}
	item := mcpWorkHTTPStructured(t, callMCPWorkHTTP(t, token, "work_item.revise", itemInput, validMeta))
	require.Equal(t, "applied", item["status"])
	expectedAttribution := map[string]any{"harness": "HTTP harness", "harnessVersion": "1", "model": "Example Model", "source": "client-reported"}
	var receiptsBefore []*mcpwork_model.Receipt
	for _, result := range []map[string]any{begin, revised, item} {
		operation := result["operation"].(map[string]any)
		assert.Equal(t, expectedAttribution, operation["clientAttribution"])
		receipt := unittest.AssertExistsAndLoadBean(t, &mcpwork_model.Receipt{OperationUUID: operation["id"].(string)})
		assert.Equal(t, grant.UserID, receipt.PrincipalID)
		assert.Equal(t, app.ID, receipt.ApplicationID)
		assert.Equal(t, grant.ID, receipt.GrantID)
		assert.NotEmpty(t, receipt.CredentialID)
		assert.Equal(t, string(auth_model.MCPProfileWorkPlanning), receipt.Profile)
		assert.Equal(t, oauth2_provider.MCPWorkWriteScope, receipt.Scope)
		assert.Equal(t, "mcp", receipt.Origin)
		assert.Equal(t, mcpwork_model.OutcomeApplied, receipt.Outcome)
		assert.Equal(t, "HTTP harness", receipt.Harness)
		assert.Equal(t, "1", receipt.HarnessVersion)
		assert.Equal(t, "Example Model", receipt.Model)
		assert.Equal(t, "client-reported", receipt.AttributionSource)
		assert.Equal(t, app.Name, receipt.RegisteredClientLabel)
		assert.Equal(t, app.MCPInstallationLabel, receipt.RegisteredInstallationLabel)
		receiptsBefore = append(receiptsBefore, receipt)
	}

	counts := mcpWorkDogfoodNativeCounts(t)
	planID, err := strconv.ParseInt(strings.TrimPrefix(planRef, "project/"), 10, 64)
	require.NoError(t, err)
	planBefore := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: planID})
	itemNumber, err := strconv.ParseInt(strings.TrimPrefix(itemRef, "issue/"), 10, 64)
	require.NoError(t, err)
	itemBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: repo.ID, Index: itemNumber})
	mutations := []struct {
		name  string
		input map[string]any
	}{
		{"work_plan.begin", beginInput}, {"work_plan.revise", revisionInput}, {"work_item.revise", itemInput},
	}
	invalid := []struct {
		name          string
		key           string
		value         any
		absent        bool
		protocolError bool
	}{
		{name: "missing harness", key: mcpsdk.MetaKeyClientInfo, absent: true},
		{name: "null standard clientInfo", key: mcpsdk.MetaKeyClientInfo, protocolError: true},
		{name: "missing name", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"version": "1"}},
		{name: "empty name", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": ""}},
		{name: "whitespace name", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": "  "}},
		{name: "control name", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": "bad\x7fname"}},
		{name: "overbound name", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": strings.Repeat("界", 129)}},
		{name: "empty version", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": "Valid", "version": ""}},
		{name: "null version", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": "Valid", "version": nil}},
		{name: "whitespace version", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": "Valid", "version": " "}},
		{name: "control version", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": "Valid", "version": "1\n2"}},
		{name: "overbound version", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": "Valid", "version": strings.Repeat("界", 65)}},
		{name: "null model metadata", key: "io.gitea.forge/clientAttribution"},
		{name: "scalar model metadata", key: "io.gitea.forge/clientAttribution", value: "not an object"},
		{name: "array model metadata", key: "io.gitea.forge/clientAttribution", value: []any{"model"}},
		{name: "missing model", key: "io.gitea.forge/clientAttribution", value: map[string]any{}},
		{name: "numeric model", key: "io.gitea.forge/clientAttribution", value: map[string]any{"model": 5}},
		{name: "empty model", key: "io.gitea.forge/clientAttribution", value: map[string]any{"model": ""}},
		{name: "whitespace model", key: "io.gitea.forge/clientAttribution", value: map[string]any{"model": "  "}},
		{name: "control model", key: "io.gitea.forge/clientAttribution", value: map[string]any{"model": "bad\u0085model"}},
		{name: "overbound model", key: "io.gitea.forge/clientAttribution", value: map[string]any{"model": strings.Repeat("界", 129)}},
		{name: "unknown model field", key: "io.gitea.forge/clientAttribution", value: map[string]any{"model": "Example Model", "prompt": "must-not-leak-private-prompt"}},
		{name: "scalar standard clientInfo", key: mcpsdk.MetaKeyClientInfo, value: "invalid", protocolError: true},
		{name: "array standard clientInfo", key: mcpsdk.MetaKeyClientInfo, value: []any{}, protocolError: true},
		{name: "numeric standard name", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": 3}, protocolError: true},
		{name: "array standard version", key: mcpsdk.MetaKeyClientInfo, value: map[string]any{"name": "Valid", "version": []any{}}, protocolError: true},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			for _, invalid := range invalid {
				t.Run(invalid.name, func(t *testing.T) {
					meta := maps.Clone(validMeta)
					if invalid.absent {
						delete(meta, invalid.key)
					} else {
						meta[invalid.key] = invalid.value
					}
					var firstProblem any
					for _, repository := range []any{
						mutation.input["repository"],
						map[string]any{"owner": "user2", "name": "repo2"},
						map[string]any{"owner": "absent-owner", "name": "absent-repository"},
					} {
						input := maps.Clone(mutation.input)
						input["repository"] = repository
						status := http.StatusOK
						if invalid.protocolError {
							status = http.StatusBadRequest
						}
						response := callMCPWorkHTTP(t, token, mutation.name, input, meta, status)
						var problem any
						if invalid.protocolError {
							assert.NotContains(t, response, "result")
							problem = response["error"]
							require.NotNil(t, problem)
							assert.EqualValues(t, -32602, problem.(map[string]any)["code"])
						} else {
							result := mcpWorkHTTPStructured(t, response)
							require.Equal(t, "error", result["status"])
							problem = result["problem"]
							require.NotNil(t, problem)
							assert.Equal(t, "client_attribution_required", problem.(map[string]any)["code"])
							assert.Equal(t, false, problem.(map[string]any)["retryable"])
							for _, field := range []string{"operation", "repository", "workPlan", "workItem", "createdReferences"} {
								assert.NotContains(t, result, field)
							}
						}
						if firstProblem == nil {
							firstProblem = problem
						} else {
							assert.Equal(t, firstProblem, problem)
						}
						encoded, err := json.Marshal(response)
						require.NoError(t, err)
						for _, private := range []string{"must-not-leak-private-prompt", "Attribution boundary plan", "Attribution item revised", "absent-owner", token, app.ClientID} {
							if strings.Contains(string(encoded), private) {
								t.Error("response must not disclose sensitive operation data")
							}
						}
					}
					assert.Equal(t, counts, mcpWorkDogfoodNativeCounts(t))
					assert.Equal(t, planBefore, unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: planID}))
					assert.Equal(t, itemBefore, unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: itemBefore.ID}))
				})
			}
		})
	}

	t.Run("character bounds and optional version", func(t *testing.T) {
		for i, version := range []any{nil, strings.Repeat("版", 64)} {
			meta := maps.Clone(validMeta)
			info := map[string]any{"name": strings.Repeat("界", 128), "title": "Standard MCP metadata remains open"}
			if version != nil {
				info["version"] = version
			}
			meta[mcpsdk.MetaKeyClientInfo] = info
			meta["io.gitea.forge/clientAttribution"] = map[string]any{"model": strings.Repeat("模", 128)}
			input := maps.Clone(beginInput)
			input["idempotencyKey"] = fmt.Sprintf("attribution-utf8-bounds-%04d", i)
			result := mcpWorkHTTPStructured(t, callMCPWorkHTTP(t, token, "work_plan.begin", input, meta))
			require.Equal(t, "applied", result["status"])
			attribution := result["operation"].(map[string]any)["clientAttribution"].(map[string]any)
			assert.Equal(t, info["name"], attribution["harness"])
			assert.Equal(t, strings.Repeat("模", 128), attribution["model"])
			if version == nil {
				assert.NotContains(t, attribution, "harnessVersion")
			} else {
				assert.Equal(t, version, attribution["harnessVersion"])
			}
		}
	})

	t.Run("SDK decoded Unicode attribution", func(t *testing.T) {
		for i, tc := range []struct {
			name, wireModel string
			rejected        bool
		}{
			{name: "invalid UTF8 byte", wireModel: "\"\xff\""},
			{name: "lone surrogate", wireModel: `"\ud800"`},
			{name: "legitimate replacement character", wireModel: `"�"`},
			{name: "decoded control", wireModel: `"\u0001"`, rejected: true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				meta := maps.Clone(validMeta)
				meta["io.gitea.forge/clientAttribution"] = map[string]any{"model": "wire-model-marker"}
				input := maps.Clone(beginInput)
				input["idempotencyKey"] = fmt.Sprintf("attribution-wire-unicode-%04d", i)
				body, err := json.Marshal(map[string]any{
					"jsonrpc": "2.0", "id": 1, "method": "tools/call",
					"params": map[string]any{"name": "work_plan.begin", "arguments": input, "_meta": meta},
				})
				require.NoError(t, err)
				body = bytes.Replace(body, []byte(`"wire-model-marker"`), []byte(tc.wireModel), 1)
				before := mcpWorkDogfoodNativeCounts(t)
				result := mcpWorkHTTPStructured(t, callMCPWorkHTTPBody(t, token, "work_plan.begin", body, http.StatusOK))
				if tc.rejected {
					assert.Equal(t, "client_attribution_required", result["problem"].(map[string]any)["code"])
					assert.Equal(t, before, mcpWorkDogfoodNativeCounts(t))
					return
				}
				// The pinned SDK normalizes malformed wire Unicode before Forge sees the annotation.
				require.Equal(t, "applied", result["status"])
				operation := result["operation"].(map[string]any)
				assert.Equal(t, "�", operation["clientAttribution"].(map[string]any)["model"])
				receipt := unittest.AssertExistsAndLoadBean(t, &mcpwork_model.Receipt{OperationUUID: operation["id"].(string)})
				assert.Equal(t, "�", receipt.Model)
			})
		}
	})

	require.NoError(t, auth_model.RevokeOAuth2Grant(t.Context(), grant.ID, grant.UserID))
	require.NoError(t, auth_model.DeleteFinalizedMCPRegistration(t.Context(), app.ID, grant.UserID))
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: app.ID})
	for _, original := range receiptsBefore {
		assert.Equal(t, original, unittest.AssertExistsAndLoadBean(t, &mcpwork_model.Receipt{ID: original.ID}))
	}
	for _, stop := range stopLogging {
		stop()
	}
	for name, captured := range capturedLogs {
		assert.NotEmpty(t, captured.String(), "the production logger must have captured requests")
		for _, private := range []string{
			token, app.ClientID, beginInput["idempotencyKey"].(string), revisionInput["idempotencyKey"].(string), itemInput["idempotencyKey"].(string),
			"must-not-leak-private-prompt", "Attribution boundary plan", "Attribution item revised", "absent-owner",
		} {
			if strings.Contains(captured.String(), private) {
				t.Errorf("sensitive operation data must not enter %s logs", name)
			}
		}
	}
}

func callMCPWorkHTTP(t *testing.T, token, name string, arguments map[string]any, meta mcpsdk.Meta, expectedStatus ...int) map[string]any {
	t.Helper()
	status := http.StatusOK
	if len(expectedStatus) != 0 {
		status = expectedStatus[0]
	}
	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "tools/call",
		"params": map[string]any{"name": name, "arguments": arguments, "_meta": meta},
	})
	require.NoError(t, err)
	return callMCPWorkHTTPBody(t, token, name, body, status)
}

func callMCPWorkHTTPBody(t *testing.T, token, name string, body []byte, status int) map[string]any {
	t.Helper()
	response := MakeRequest(t, NewRequestWithBody(t, http.MethodPost, "/mcp", bytes.NewReader(body)).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "application/json, text/event-stream").
		SetHeader("MCP-Protocol-Version", "2026-07-28").
		SetHeader("MCP-Method", "tools/call").
		SetHeader("MCP-Name", name).AddTokenAuth(token), status)
	encoded := response.Body.Bytes()
	if strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
		for line := range strings.SplitSeq(string(encoded), "\n") {
			if data, ok := strings.CutPrefix(line, "data: "); ok {
				encoded = []byte(data)
				break
			}
		}
	}
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	return result
}

func mcpWorkHTTPStructured(t *testing.T, response map[string]any) map[string]any {
	t.Helper()
	require.NotContains(t, response, "error")
	result, ok := response["result"].(map[string]any)
	require.True(t, ok)
	structured, ok := result["structuredContent"].(map[string]any)
	require.True(t, ok)
	return structured
}
