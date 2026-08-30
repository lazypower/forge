// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"strings"
	"testing"
	"time"

	mcpwork_model "gitea.dev/models/mcpwork"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	mcpwork_service "gitea.dev/services/mcpwork"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testClientAttributionMeta() mcpsdk.Meta {
	return mcpsdk.Meta{clientAttributionMetaKey: map[string]any{"model": "Example Model"}}
}

func testAttributedRequest() *mcpsdk.CallToolRequest {
	meta := testClientAttributionMeta()
	meta[mcpsdk.MetaKeyClientInfo] = map[string]any{"name": "Example Harness", "version": "1"}
	return &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Meta: meta}}
}

func TestMutationSchemaRequiresClosedClientAttribution(t *testing.T) {
	for _, value := range []any{
		nil,
		map[string]any{},
		map[string]any{"harness": "Harness", "model": "Model", "source": "verified"},
		map[string]any{"harness": "Harness", "model": "", "source": "client-reported"},
		map[string]any{"harness": "Harness", "model": "Model", "source": "client-reported", "prompt": "PRIVATE"},
		map[string]any{"harness": strings.Repeat("界", 129), "model": "Model", "source": "client-reported"},
		map[string]any{"harness": "Harness", "harnessVersion": "", "model": "Model", "source": "client-reported"},
	} {
		output := mutationReceiptOutput(committedMutation(mcpwork_model.OutcomeApplied, false))
		output.CurrentResultStatus = "unavailable"
		wire, err := json.Marshal(output)
		require.NoError(t, err)
		var fields map[string]any
		require.NoError(t, json.Unmarshal(wire, &fields))
		operation := fields["operation"].(map[string]any)
		if value == nil {
			delete(operation, "clientAttribution")
		} else {
			operation["clientAttribution"] = value
		}
		wire, err = json.Marshal(fields)
		require.NoError(t, err)
		assert.False(t, validWorkMutationOutput(wire))
	}
	output := mutationReceiptOutput(committedMutation(mcpwork_model.OutcomeApplied, false))
	output.CurrentResultStatus = "unavailable"
	output.Operation.ClientAttribution.Model = ""
	wire, err := json.Marshal(output)
	require.NoError(t, err)
	assert.True(t, validWorkMutationOutput(wire))
}

func TestWorkClientAttributionMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(mcpsdk.Meta)
	}{
		{"missing info", func(m mcpsdk.Meta) { delete(m, mcpsdk.MetaKeyClientInfo) }},
		{"null info", func(m mcpsdk.Meta) { m[mcpsdk.MetaKeyClientInfo] = nil }},
		{"scalar info", func(m mcpsdk.Meta) { m[mcpsdk.MetaKeyClientInfo] = "hidden-client" }},
		{"array info", func(m mcpsdk.Meta) { m[mcpsdk.MetaKeyClientInfo] = []any{} }},
		{"numeric name", func(m mcpsdk.Meta) { m[mcpsdk.MetaKeyClientInfo] = map[string]any{"name": 1} }},
		{"missing name", func(m mcpsdk.Meta) { m[mcpsdk.MetaKeyClientInfo] = map[string]any{} }},
		{"empty version", func(m mcpsdk.Meta) { m[mcpsdk.MetaKeyClientInfo] = map[string]any{"name": "Harness", "version": ""} }},
		{"null version", func(m mcpsdk.Meta) { m[mcpsdk.MetaKeyClientInfo] = map[string]any{"name": "Harness", "version": nil} }},
		{"null entry", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = nil }},
		{"scalar entry", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = "hidden-model" }},
		{"array entry", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = []any{"model"} }},
		{"null model", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = map[string]any{"model": nil} }},
		{"numeric model", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = map[string]any{"model": 1} }},
		{"empty model", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = map[string]any{"model": ""} }},
		{"blank model", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = map[string]any{"model": " "} }},
		{"control model", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = map[string]any{"model": "bad\nmodel"} }},
		{"overbound model", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = map[string]any{"model": strings.Repeat("界", 129)} }},
		{"unknown field", func(m mcpsdk.Meta) {
			m[clientAttributionMetaKey] = map[string]any{"model": "Model", "prompt": "PRIVATE-PROMPT"}
		}},
		{"wrong field", func(m mcpsdk.Meta) { m[clientAttributionMetaKey] = map[string]any{"prompt": "PRIVATE-PROMPT"} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := testAttributedRequest()
			tc.mutate(req.Params.Meta)
			got, err := workClientAttribution(req)
			require.ErrorIs(t, err, mcpwork_service.ErrClientAttributionRequired)
			assert.Empty(t, got)
			// No receipt service or reader exists: every entry must reject before either is reached.
			tools := newWorkMutationTools(newToolExecutor(1, time.Second), nil, nil, testPrincipal, 1<<20)
			tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
			_, begin, err := tools.beginPlan(t.Context(), req, workPlanBeginRequest{Repository: WorkRepository{Owner: "private-owner", Name: "private-repo"}})
			require.NoError(t, err)
			_, item, err := tools.reviseItem(t.Context(), req, workItemReviseRequest{WorkItem: "issue/99"})
			require.NoError(t, err)
			_, plan, err := tools.revisePlan(t.Context(), req, workPlanReviseRequest{WorkPlan: "project/99"})
			require.NoError(t, err)
			for _, output := range []workMutationOutput{begin, item, plan} {
				assert.Equal(t, "error", output.Status)
				require.NotNil(t, output.Problem)
				assert.Equal(t, "client_attribution_required", output.Problem.Code)
				assert.False(t, output.Problem.Retryable)
				assert.Nil(t, output.Operation)
				wire, err := json.Marshal(output)
				require.NoError(t, err)
				for _, secret := range []string{"PRIVATE-PROMPT", "hidden-", "private-", "issue/", "project/"} {
					assert.NotContains(t, string(wire), secret)
				}
			}
		})
	}
	req := testAttributedRequest()
	delete(req.Params.Meta, clientAttributionMetaKey)
	got, err := workClientAttribution(req)
	require.NoError(t, err)
	assert.Equal(t, "Example Harness", got.Harness)
	assert.Equal(t, "1", got.HarnessVersion)
	assert.Empty(t, got.Model)
	assert.Equal(t, "client-reported", got.Source)

	req = testAttributedRequest()
	req.Params.Meta[mcpsdk.MetaKeyClientInfo] = map[string]any{"name": " Harness ", "title": "Display title", "websiteUrl": "https://client.example"}
	req.Params.Meta["example.com/unrelated"] = map[string]any{"extra": true}
	got, err = workClientAttribution(req)
	require.NoError(t, err)
	assert.Equal(t, "Harness", got.Harness)
	assert.Empty(t, got.HarnessVersion)
}

func TestOfficialSDKLegacyAttributionFallback(t *testing.T) {
	for _, version := range []string{"1.0", "", " ", "1\t2", strings.Repeat("x", 65)} {
		t.Run("version="+version, func(t *testing.T) {
			calls := 0
			mutations := fakeWorkMutationService{begin: func(_ context.Context, _ *user_model.User, authority workMutationAuthority, _ workPlanBeginRequest) (*workMutationExecution, error) {
				calls++
				assert.Equal(t, "Legacy Harness", authority.ClientAttribution.Harness)
				assert.Equal(t, version, authority.ClientAttribution.HarnessVersion)
				assert.Equal(t, "Registered Client", authority.RegisteredClientLabel)
				assert.Equal(t, "Registered Installation", authority.RegisteredInstallationLabel)
				return committedMutation(mcpwork_model.OutcomeApplied, false), nil
			}}
			tools := newWorkMutationTools(newToolExecutor(1, time.Second), mutations, nil, testPrincipal, 1<<20)
			tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
			server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
			registerWorkMutationTools(server, tools)
			clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
			ss, err := server.Connect(t.Context(), serverTransport, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, ss.Close()) })
			client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: " Legacy Harness ", Version: version}, nil)
			// Simulate a peer without server/discover before a real legacy initialize handshake.
			client.AddSendingMiddleware(func(next mcpsdk.MethodHandler) mcpsdk.MethodHandler {
				return func(ctx context.Context, method string, req mcpsdk.Request) (mcpsdk.Result, error) {
					if method == "server/discover" {
						return nil, &jsonrpc.Error{Code: jsonrpc.CodeMethodNotFound, Message: "legacy peer"}
					}
					return next(ctx, method, req)
				}
			})
			session, err := client.Connect(t.Context(), clientTransport, nil)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, session.Close()) })
			require.NotNil(t, ss.InitializeParams())
			assert.Equal(t, "2025-11-25", ss.InitializeParams().ProtocolVersion)
			args := map[string]any{"repository": map[string]any{"owner": "example", "name": "repo"}, "idempotencyKey": "legacy-attribution-0001", "begin": map[string]any{"kind": "new", "title": "Plan"}}
			result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: workPlanBeginToolName, Arguments: args, Meta: testClientAttributionMeta()})
			require.NoError(t, err)
			if version == "1.0" || version == "" {
				// The SDK loses omitted/empty/null legacy version presence; empty means optional absence.
				assert.False(t, result.IsError)
				assert.Equal(t, 1, calls)
			} else {
				assert.Equal(t, "client_attribution_required", structuredWorkOutput(t, result)["problem"].(map[string]any)["code"])
				assert.Zero(t, calls)
			}
			before := calls
			for _, info := range []any{"malformed", map[string]any{"name": "Override", "version": ""}, map[string]any{"name": "Override", "version": 1}} {
				meta := testClientAttributionMeta()
				meta[mcpsdk.MetaKeyClientInfo] = info
				result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: workPlanBeginToolName, Arguments: args, Meta: meta})
				require.NoError(t, err)
				assert.Equal(t, "client_attribution_required", structuredWorkOutput(t, result)["problem"].(map[string]any)["code"])
				assert.Equal(t, before, calls)
			}
		})
	}
}
