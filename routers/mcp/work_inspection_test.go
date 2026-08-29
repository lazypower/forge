// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	pull_service "gitea.dev/services/pull"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWorkReadService struct {
	item func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error)
	plan func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error)
}

func (fake fakeWorkReadService) InspectWorkItem(ctx context.Context, doer *user_model.User, request WorkItemInspectRequest) (*WorkItemInspection, error) {
	return fake.item(ctx, doer, request)
}

func (fake fakeWorkReadService) InspectWorkPlan(ctx context.Context, doer *user_model.User, request WorkPlanInspectRequest) (*WorkPlanInspection, error) {
	return fake.plan(ctx, doer, request)
}

func TestDeclaredWorkToolContracts(t *testing.T) {
	contracts := declaredWorkToolContracts()
	require.Len(t, contracts, 5)
	for _, name := range []string{workItemInspectToolName, workPlanInspectToolName, workPlanBeginToolName, workItemReviseToolName, workPlanReviseToolName} {
		contract := contracts[name]
		require.NotNil(t, contract, name)
		assert.Equal(t, name, contract.Name)
		assertStandaloneClosedSchema(t, contract.InputSchema)
		assertStandaloneClosedSchema(t, contract.OutputSchema)
		require.NotNil(t, contract.Annotations)
		assert.True(t, contract.Annotations.IdempotentHint)
		require.NotNil(t, contract.Annotations.OpenWorldHint)
		assert.False(t, *contract.Annotations.OpenWorldHint)
	}
	for _, name := range []string{workItemInspectToolName, workPlanInspectToolName} {
		assert.True(t, contracts[name].Annotations.ReadOnlyHint)
		assert.Nil(t, contracts[name].Annotations.DestructiveHint)
	}
	assertMutationAnnotations(t, contracts[workPlanBeginToolName], false)
	assertMutationAnnotations(t, contracts[workItemReviseToolName], true)
	assertMutationAnnotations(t, contracts[workPlanReviseToolName], true)
}

func TestWorkToolSchemasWithOfficialSDK(t *testing.T) {
	validReadOutput := map[string]any{"schemaVersion": "1", "status": "unavailable"}
	validMutationOutput := map[string]any{
		"schemaVersion": "1", "status": "error",
		"problem": map[string]any{"code": "mutation_failed", "message": "failed", "retryable": false},
	}
	tests := []struct {
		name       string
		valid      map[string]any
		invalid    []map[string]any
		validOut   map[string]any
		invalidOut map[string]any
	}{
		{
			name:  workItemInspectToolName,
			valid: map[string]any{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7"},
			invalid: []map[string]any{
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/07"},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7", "page": map[string]any{"limit": 101}},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7", "page": map[string]any{"cursor": ""}},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7", "page": map[string]any{"cursor": strings.Repeat("c", 2049)}},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7", "resource": map[string]any{}},
			},
			validOut: validReadOutput,
			invalidOut: map[string]any{
				"schemaVersion": "1", "status": "unavailable", "repository": map[string]any{"owner": "hidden"},
			},
		},
		{
			name:  workPlanInspectToolName,
			valid: map[string]any{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workPlan": "project/9"},
			invalid: []map[string]any{
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workPlan": "project/0"},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workPlan": "project/9", "pageKind": "anything"},
			},
			validOut: validReadOutput,
			invalidOut: map[string]any{
				"schemaVersion": "1", "status": "error", "problem": map[string]any{"code": "timeout", "message": "late"},
			},
		},
		{
			name:  workPlanBeginToolName,
			valid: map[string]any{"repository": map[string]any{"owner": "octo", "name": "forge"}, "idempotencyKey": "abcdefghijklmnop", "begin": map[string]any{"kind": "new", "title": "Plan"}},
			invalid: []map[string]any{
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "idempotencyKey": "short", "begin": map[string]any{"kind": "new", "title": "Plan"}},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "idempotencyKey": "abcdefghijklmnop", "begin": map[string]any{"kind": "new", "title": "", "workPlan": "project/1"}},
			},
			validOut: validMutationOutput,
		},
		{
			name:  workItemReviseToolName,
			valid: map[string]any{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7", "idempotencyKey": "abcdefghijklmnop", "state": map[string]any{"desired": "open"}},
			invalid: []map[string]any{
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7", "idempotencyKey": "abcdefghijklmnop"},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7", "idempotencyKey": "abcdefghijklmnop", "markdown": map[string]any{"expectedContentVersion": -1, "desired": "x"}},
			},
			validOut: validMutationOutput,
		},
		{
			name:  workPlanReviseToolName,
			valid: map[string]any{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workPlan": "project/9", "idempotencyKey": "abcdefghijklmnop", "changes": []any{map[string]any{"kind": "ensure_member", "workItem": "issue/7", "presence": "present"}}},
			invalid: []map[string]any{
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workPlan": "project/9", "idempotencyKey": "abcdefghijklmnop", "changes": []any{}},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workPlan": "project/9", "idempotencyKey": "abcdefghijklmnop", "changes": []any{map[string]any{"kind": "create_member", "localReference": "1bad", "title": "x"}}},
				{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workPlan": "project/9", "idempotencyKey": "abcdefghijklmnop", "changes": []any{map[string]any{"kind": "delete_draft", "extra": true}}},
			},
			validOut: validMutationOutput,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contract := declaredWorkToolContracts()[test.name]
			assertSDKSchemaCall(t, contract, test.valid, test.validOut, false)
			for _, invalid := range test.invalid {
				assertSDKSchemaCall(t, contract, invalid, test.validOut, true)
			}
			if test.invalidOut != nil {
				assertSDKOutputRejected(t, contract, test.valid, test.invalidOut)
			}
		})
	}
}

func TestWorkReadOutputSchemaRejectsBoundsAndMismatchedPageItems(t *testing.T) {
	contract := declaredWorkToolContracts()[workItemInspectToolName]
	input := map[string]any{"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7"}
	inspection := validWorkItemInspection()
	base := workReadOutput{
		SchemaVersion: "1", Status: "available", Repository: &inspection.Repository, WorkItem: &inspection.WorkItem, Page: &inspection.Page,
	}
	for _, mutate := range []func(map[string]any){
		func(output map[string]any) {
			output["workItem"].(map[string]any)["contextSummaries"] = make([]any, 101)
		},
		func(output map[string]any) {
			output["page"].(map[string]any)["nextCursor"] = strings.Repeat("c", 2049)
		},
		func(output map[string]any) {
			output["page"].(map[string]any)["items"] = []any{map[string]any{"availability": "undisclosed"}}
		},
	} {
		output := workOutputMap(t, base)
		mutate(output)
		assertSDKOutputRejected(t, contract, input, output)
	}
}

func TestOfficialSDKAppliesWorkReadDefaults(t *testing.T) {
	captured := make(chan WorkItemInspectRequest, 1)
	executor := newToolExecutor(1, time.Second)
	reader := fakeWorkReadService{
		item: func(_ context.Context, _ *user_model.User, request WorkItemInspectRequest) (*WorkItemInspection, error) {
			captured <- request
			return validWorkItemInspection(), nil
		},
		plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
			return validWorkPlanInspection(), nil
		},
	}
	pullTool := newPullRequestInspectionTool(executor,
		func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
			return nil, pull_service.ErrPullRequestInspectionUnavailable
		}, testPrincipal)
	server := newServer(pullTool, newWorkInspectionTools(executor, reader, testPrincipal, 1<<20), true)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	defer serverSession.Close()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	defer clientSession.Close()

	result, err := clientSession.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: workItemInspectToolName,
		Arguments: map[string]any{
			"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7", "page": map[string]any{},
		},
	})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	request := <-captured
	assert.Equal(t, "contexts", request.PageKind)
	require.NotNil(t, request.Page)
	assert.Equal(t, 25, request.Page.Limit)
}

func TestWorkInspectionHandlersUseTypedReadContract(t *testing.T) {
	var capturedItem WorkItemInspectRequest
	var capturedPlan WorkPlanInspectRequest
	reader := fakeWorkReadService{
		item: func(_ context.Context, doer *user_model.User, request WorkItemInspectRequest) (*WorkItemInspection, error) {
			assert.EqualValues(t, 1, doer.ID)
			capturedItem = request
			result := validWorkItemInspection()
			result.SelectedContext = &WorkPlanContextResult{
				Ref: "project/9/issue/7", WorkPlan: "project/9", WorkItem: "issue/7", DerivedState: "blocked",
				Integrity:             WorkIntegrity{Status: "valid", Concerns: []WorkIntegrityConcern{}},
				PrerequisiteSummaries: []WorkReferenceSummary{}, DeliverySummaries: []WorkDeliverySummary{},
			}
			return result, nil
		},
		plan: func(_ context.Context, doer *user_model.User, request WorkPlanInspectRequest) (*WorkPlanInspection, error) {
			assert.EqualValues(t, 1, doer.ID)
			capturedPlan = request
			return validWorkPlanInspection(), nil
		},
	}
	tools := newWorkInspectionTools(newToolExecutor(1, time.Second), reader, testPrincipal, 1<<20)

	itemResult, itemOutput, err := tools.inspectItem(t.Context(), nil, WorkItemInspectRequest{
		Repository: WorkRepository{Owner: "octo", Name: "forge"}, WorkItem: "issue/7", SelectedPlan: "project/9", PageKind: "contexts",
	})
	require.NoError(t, err)
	assert.False(t, itemResult.IsError)
	assert.Equal(t, "available", itemOutput.Status)
	require.NotNil(t, itemOutput.SelectedContext)
	assert.Equal(t, "project/9/issue/7", itemOutput.SelectedContext.Ref)
	assert.Equal(t, "issue/7", capturedItem.WorkItem)

	planResult, planOutput, err := tools.inspectPlan(t.Context(), nil, WorkPlanInspectRequest{
		Repository: WorkRepository{Owner: "octo", Name: "forge"}, WorkPlan: "project/9", PageKind: "items",
	})
	require.NoError(t, err)
	assert.False(t, planResult.IsError)
	assert.Equal(t, "available", planOutput.Status)
	require.NotNil(t, planOutput.WorkPlan)
	assert.Equal(t, "project/9", capturedPlan.WorkPlan)
}

func TestWorkInspectionUnavailableIsNeutral(t *testing.T) {
	for _, resourceClass := range []string{"missing", "denied", "private"} {
		t.Run(resourceClass, func(t *testing.T) {
			reader := fakeWorkReadService{
				item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
					return nil, &WorkReadFailure{Kind: WorkReadUnavailable, Cause: errors.New(resourceClass + " secret")}
				},
			}
			tools := newWorkInspectionTools(newToolExecutor(1, time.Second), reader, testPrincipal, 1<<20)
			result, output, err := tools.inspectItem(t.Context(), nil, WorkItemInspectRequest{})
			require.NoError(t, err)
			assert.False(t, result.IsError)
			assert.Equal(t, workReadOutput{SchemaVersion: "1", Status: "unavailable"}, output)
			wire, err := json.Marshal(output)
			require.NoError(t, err)
			assert.NotContains(t, string(wire), resourceClass)
			assert.NotContains(t, string(wire), "secret")
		})
	}
}

func TestWorkInspectionPreservesUndisclosedNestedReferenceAndBoundConcern(t *testing.T) {
	item := validWorkItemInspection()
	item.WorkItem.PrerequisiteSummaries = []WorkReferenceSummary{{Availability: "undisclosed"}}
	item.Page = WorkPageResult{
		Kind: "prerequisites", Items: []WorkPageEntry{WorkReferenceSummary{Availability: "undisclosed"}},
		SnapshotConsistency: "none", ReinspectBeforeAction: true,
	}
	plan := validWorkPlanInspection()
	plan.WorkPlan.Integrity = WorkIntegrity{Status: "concern", Concerns: []WorkIntegrityConcern{{Code: "graph_bound", Message: "Plan exceeds the configured graph bound."}}}
	plan.WorkPlan.ReadyFrontier = []WorkContextSummary{}
	reader := fakeWorkReadService{
		item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
			return item, nil
		},
		plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
			return plan, nil
		},
	}
	tools := newWorkInspectionTools(newToolExecutor(1, time.Second), reader, testPrincipal, 1<<20)

	_, itemOutput, err := tools.inspectItem(t.Context(), nil, WorkItemInspectRequest{})
	require.NoError(t, err)
	require.Len(t, itemOutput.WorkItem.PrerequisiteSummaries, 1)
	assert.Equal(t, WorkReferenceSummary{Availability: "undisclosed"}, itemOutput.WorkItem.PrerequisiteSummaries[0])

	_, planOutput, err := tools.inspectPlan(t.Context(), nil, WorkPlanInspectRequest{})
	require.NoError(t, err)
	assert.Equal(t, "concern", planOutput.WorkPlan.Integrity.Status)
	assert.Empty(t, planOutput.WorkPlan.ReadyFrontier)
}

func TestWorkInspectionControlledErrorsAndOutputBound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "invalid input", err: &WorkReadFailure{Kind: WorkReadInvalidInput}, code: "invalid_input"},
		{name: "invalid cursor", err: &WorkReadFailure{Kind: WorkReadInvalidCursor}, code: "invalid_cursor"},
		{name: "semantic bound", err: &WorkReadFailure{Kind: WorkReadLimitExceeded}, code: "limit_exceeded"},
		{name: "internal", err: errors.New("database secret"), code: "mutation_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := fakeWorkReadService{item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
				return nil, test.err
			}}
			tools := newWorkInspectionTools(newToolExecutor(1, time.Second), reader, testPrincipal, 1<<20)
			result, output, err := tools.inspectItem(t.Context(), nil, WorkItemInspectRequest{})
			require.NoError(t, err)
			assert.True(t, result.IsError)
			require.NotNil(t, output.Problem)
			assert.Equal(t, test.code, output.Problem.Code)
			assert.NotContains(t, output.Problem.Message, "secret")
		})
	}

	reader := fakeWorkReadService{item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
		result := validWorkItemInspection()
		result.WorkItem.Markdown = strings.Repeat("x", 1024)
		return result, nil
	}}
	tools := newWorkInspectionTools(newToolExecutor(1, time.Second), reader, testPrincipal, 128)
	result, output, err := tools.inspectItem(t.Context(), nil, WorkItemInspectRequest{})
	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Equal(t, "limit_exceeded", output.Problem.Code)
}

func TestWorkInspectionTimeoutCancellationAndBusy(t *testing.T) {
	blocking := fakeWorkReadService{item: func(ctx context.Context, _ *user_model.User, _ WorkItemInspectRequest) (*WorkItemInspection, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	t.Run("timeout", func(t *testing.T) {
		tools := newWorkInspectionTools(newToolExecutor(1, time.Millisecond), blocking, testPrincipal, 1<<20)
		_, output, err := tools.inspectItem(t.Context(), nil, WorkItemInspectRequest{})
		require.NoError(t, err)
		assert.Equal(t, "timeout", output.Problem.Code)
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		tools := newWorkInspectionTools(newToolExecutor(1, time.Second), blocking, testPrincipal, 1<<20)
		_, output, err := tools.inspectItem(ctx, nil, WorkItemInspectRequest{})
		require.NoError(t, err)
		assert.Equal(t, "cancelled", output.Problem.Code)
	})
	t.Run("busy", func(t *testing.T) {
		executor := newToolExecutor(1, time.Second)
		_, release, err := executor.begin(t.Context())
		require.NoError(t, err)
		defer release()
		tools := newWorkInspectionTools(executor, blocking, testPrincipal, 1<<20)
		_, output, err := tools.inspectItem(t.Context(), nil, WorkItemInspectRequest{})
		require.NoError(t, err)
		assert.Equal(t, "busy", output.Problem.Code)
	})
}

func assertStandaloneClosedSchema(t *testing.T, value any) {
	t.Helper()
	schema, ok := value.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "https://json-schema.org/draft/2020-12/schema", schema["$schema"])
	assert.Equal(t, "object", schema["type"])
	assert.Equal(t, false, schema["additionalProperties"])
	assert.NotContains(t, schema, "$ref")
	wire, err := json.Marshal(schema)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), `"additionalProperties":{}`)
	assert.NotContains(t, string(wire), "http://")
}

func assertMutationAnnotations(t *testing.T, contract *mcpsdk.Tool, destructive bool) {
	t.Helper()
	assert.False(t, contract.Annotations.ReadOnlyHint)
	require.NotNil(t, contract.Annotations.DestructiveHint)
	assert.Equal(t, destructive, *contract.Annotations.DestructiveHint)
}

func assertSDKSchemaCall(t *testing.T, contract *mcpsdk.Tool, input, output map[string]any, wantError bool) {
	t.Helper()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "schema-test", Version: "1"}, nil)
	mcpsdk.AddTool(server, contract, func(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, map[string]any, error) {
		return nil, output, nil
	})
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serverSession.Close()) })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, clientSession.Close()) })
	result, err := clientSession.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: contract.Name, Arguments: input})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, wantError, result.IsError)
}

func assertSDKOutputRejected(t *testing.T, contract *mcpsdk.Tool, input, output map[string]any) {
	t.Helper()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "schema-test", Version: "1"}, nil)
	mcpsdk.AddTool(server, contract, func(context.Context, *mcpsdk.CallToolRequest, map[string]any) (*mcpsdk.CallToolResult, map[string]any, error) {
		return nil, output, nil
	})
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serverSession.Close()) })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, clientSession.Close()) })
	_, err = clientSession.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: contract.Name, Arguments: input})
	require.Error(t, err)
}

func workOutputMap(t *testing.T, output workReadOutput) map[string]any {
	t.Helper()
	wire, err := json.Marshal(output)
	require.NoError(t, err)
	var result map[string]any
	require.NoError(t, json.Unmarshal(wire, &result))
	return result
}

func validWorkItemInspection() *WorkItemInspection {
	return &WorkItemInspection{
		Repository: WorkRepositoryResult{Owner: "octo", Name: "forge", URL: "https://forge.example/octo/forge"},
		WorkItem: WorkItemResult{
			Ref: "issue/7", URL: "https://forge.example/octo/forge/issues/7", Title: "Work", Markdown: "Body", ContentVersion: 2,
			State: "open", Classification: "planned", ContextSummaries: []WorkContextSummary{}, ProjectMemberships: []WorkReferenceSummary{},
			PrerequisiteSummaries: []WorkReferenceSummary{}, DependentSummaries: []WorkReferenceSummary{}, DeliverySummaries: []WorkDeliverySummary{},
		},
		Page: WorkPageResult{Kind: "contexts", Items: []WorkPageEntry{}, SnapshotConsistency: "none", ReinspectBeforeAction: true},
	}
}

func validWorkPlanInspection() *WorkPlanInspection {
	return &WorkPlanInspection{
		Repository: WorkRepositoryResult{Owner: "octo", Name: "forge", URL: "https://forge.example/octo/forge"},
		WorkPlan: WorkPlanResult{
			Ref: "project/9", URL: "https://forge.example/octo/forge/projects/9", Title: "Plan", Markdown: "Body", PlanningState: "active", ProjectState: "open",
			Integrity: WorkIntegrity{Status: "valid", Concerns: []WorkIntegrityConcern{}}, ItemSummaries: []WorkContextSummary{}, EdgeSummaries: []WorkEdgeSummary{},
			ReadyFrontier: []WorkContextSummary{}, ExcludedMembers: []WorkReferenceSummary{}, PlanToken: "signed-token",
		},
		Page: WorkPageResult{Kind: "items", Items: []WorkPageEntry{}, SnapshotConsistency: "none", ReinspectBeforeAction: true},
	}
}
