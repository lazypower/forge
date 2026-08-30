// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	mcpwork_model "gitea.dev/models/mcpwork"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	mcpwork_service "gitea.dev/services/mcpwork"
	"gitea.dev/services/oauth2_provider"
	pull_service "gitea.dev/services/pull"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeWorkMutationService struct {
	begin func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error)
	item  func(context.Context, *user_model.User, workMutationAuthority, workItemReviseRequest) (*workMutationExecution, error)
	plan  func(context.Context, *user_model.User, workMutationAuthority, workPlanReviseRequest) (*workMutationExecution, error)
}

func (fake fakeWorkMutationService) BeginPlan(ctx context.Context, doer *user_model.User, authority workMutationAuthority, request workPlanBeginRequest) (*workMutationExecution, error) {
	return fake.begin(ctx, doer, authority, request)
}

func (fake fakeWorkMutationService) ReviseItem(ctx context.Context, doer *user_model.User, authority workMutationAuthority, request workItemReviseRequest) (*workMutationExecution, error) {
	return fake.item(ctx, doer, authority, request)
}

func (fake fakeWorkMutationService) RevisePlan(ctx context.Context, doer *user_model.User, authority workMutationAuthority, request workPlanReviseRequest) (*workMutationExecution, error) {
	return fake.plan(ctx, doer, authority, request)
}

func TestOfficialSDKCallsAllWorkMutationHandlers(t *testing.T) {
	var beginCalls, itemCalls, planCalls int
	mutations := fakeWorkMutationService{
		begin: func(_ context.Context, doer *user_model.User, authority workMutationAuthority, request workPlanBeginRequest) (*workMutationExecution, error) {
			beginCalls++
			assert.EqualValues(t, 1, doer.ID)
			assert.EqualValues(t, 8, authority.OAuthApplicationID)
			assert.Equal(t, "test", authority.ClientAttribution.Harness)
			assert.Equal(t, "1", authority.ClientAttribution.HarnessVersion)
			assert.Empty(t, authority.ClientAttribution.Model)
			assert.Equal(t, "Plan", request.Begin.Title)
			execution := committedMutation(mcpwork_model.OutcomeApplied, false, projectMutationArtifact(9))
			execution.Receipt.ClientAttribution = authority.ClientAttribution
			return execution, nil
		},
		item: func(_ context.Context, _ *user_model.User, _ workMutationAuthority, request workItemReviseRequest) (*workMutationExecution, error) {
			itemCalls++
			require.NotNil(t, request.Markdown)
			assert.Equal(t, 2, request.Markdown.ExpectedContentVersion)
			return committedMutation(mcpwork_model.OutcomeApplied, false, issueMutationArtifact(7, "")), nil
		},
		plan: func(_ context.Context, _ *user_model.User, _ workMutationAuthority, request workPlanReviseRequest) (*workMutationExecution, error) {
			planCalls++
			require.Len(t, request.Changes, 1)
			assert.Equal(t, "ensure_member", request.Changes[0].Kind)
			return committedMutation(mcpwork_model.OutcomeUnchanged, true, projectMutationArtifact(9)), nil
		},
	}
	reader := fakeWorkReadService{
		item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
			return validWorkItemInspection(), nil
		},
		plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
			return validWorkPlanInspection(), nil
		},
	}
	session := connectWorkMutationTestClient(t, mutations, reader, 1<<20)

	begin, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: workPlanBeginToolName, Arguments: map[string]any{
		"repository": map[string]any{"owner": "octo", "name": "forge"}, "idempotencyKey": "begin-plan-key-0000000001",
		"begin": map[string]any{"kind": "new", "title": "Plan"},
	}})
	require.NoError(t, err)
	assert.False(t, begin.IsError)
	assertMutationStructuredResult(t, begin, "applied", false, "available")
	assert.NotContains(t, structuredWorkOutput(t, begin)["operation"].(map[string]any)["clientAttribution"], "model")

	item, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Meta: testClientAttributionMeta(), Name: workItemReviseToolName, Arguments: map[string]any{
		"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7",
		"idempotencyKey": "revise-item-key-000000001", "markdown": map[string]any{"expectedContentVersion": 2, "desired": "updated"},
	}})
	require.NoError(t, err)
	assert.False(t, item.IsError)
	assertMutationStructuredResult(t, item, "applied", false, "available")

	plan, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Meta: testClientAttributionMeta(), Name: workPlanReviseToolName, Arguments: map[string]any{
		"repository": map[string]any{"owner": "octo", "name": "forge"}, "workPlan": "project/9",
		"idempotencyKey": "revise-plan-key-000000001", "changes": []any{map[string]any{"kind": "ensure_member", "workItem": "issue/7", "presence": "present"}},
	}})
	require.NoError(t, err)
	assert.False(t, plan.IsError)
	assertMutationStructuredResult(t, plan, "unchanged", true, "available")
	assert.Equal(t, 1, beginCalls)
	assert.Equal(t, 1, itemCalls)
	assert.Equal(t, 1, planCalls)
}

func TestOfficialSDKReopenKeepsDeliveryProjection(t *testing.T) {
	mutations := fakeWorkMutationService{
		begin: func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error) {
			return nil, errors.New("unexpected begin mutation")
		},
		item: func(context.Context, *user_model.User, workMutationAuthority, workItemReviseRequest) (*workMutationExecution, error) {
			return committedMutation(mcpwork_model.OutcomeApplied, false, issueMutationArtifact(7, "")), nil
		},
		plan: func(context.Context, *user_model.User, workMutationAuthority, workPlanReviseRequest) (*workMutationExecution, error) {
			return nil, errors.New("unexpected plan mutation")
		},
	}
	inspection := validWorkItemInspection()
	inspection.WorkItem.State = "open"
	inspection.WorkItem.DeliverySummaries = []WorkDeliverySummary{{
		Repository: inspection.Repository, Ref: "pull/12", URL: "https://forge.example/octo/forge/pulls/12",
		State: "open", Revision: "0123456789012345678901234567890123456789", CheckState: "success",
	}}
	reader := fakeWorkReadService{
		item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
			return inspection, nil
		},
		plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
			return nil, errors.New("unexpected plan read")
		},
	}
	session := connectWorkMutationTestClient(t, mutations, reader, 1<<20)
	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Meta: testClientAttributionMeta(), Name: workItemReviseToolName, Arguments: map[string]any{
		"repository": map[string]any{"owner": "octo", "name": "forge"}, "workItem": "issue/7",
		"idempotencyKey": "validation-reopen-00000001", "state": map[string]any{"desired": "open"},
	}})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	output := structuredWorkOutput(t, result)
	workItem := output["workItem"].(map[string]any)
	assert.Equal(t, "open", workItem["state"])
	deliveries := workItem["deliverySummaries"].([]any)
	require.Len(t, deliveries, 1)
	assert.Equal(t, "pull/12", deliveries[0].(map[string]any)["ref"])
}

func TestWorkMutationRequiresExactWriteProfileBeforeCallingService(t *testing.T) {
	called := false
	mutations := fakeWorkMutationService{
		begin: func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error) {
			called = true
			return nil, errors.New("unexpected begin mutation")
		},
		item: func(context.Context, *user_model.User, workMutationAuthority, workItemReviseRequest) (*workMutationExecution, error) {
			return nil, errors.New("unexpected item mutation")
		},
		plan: func(context.Context, *user_model.User, workMutationAuthority, workPlanReviseRequest) (*workMutationExecution, error) {
			return nil, errors.New("unexpected plan mutation")
		},
	}
	tools := newWorkMutationTools(newToolExecutor(1, time.Second), mutations, inertWorkReader(), testPrincipal, 1<<20)
	tools.credential = func(context.Context) (*verifiedOAuthCredential, error) {
		credential := testWriteCredential()
		credential.Profile = auth_model.MCPProfileRead
		credential.CanonicalScope = oauth2_provider.MCPReadScope
		return credential, nil
	}
	_, output, err := tools.beginPlan(t.Context(), testAttributedRequest(), workPlanBeginRequest{})
	require.NoError(t, err)
	assert.Equal(t, "error", output.Status)
	require.NotNil(t, output.Problem)
	assert.Equal(t, "not_permitted", output.Problem.Code)
	assert.False(t, called)
}

func TestWorkMutationCommittedProjectionFailuresPreserveReceipt(t *testing.T) {
	mutations := fakeWorkMutationService{
		begin: func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error) {
			return committedMutation(mcpwork_model.OutcomeApplied, false, projectMutationArtifact(9)), nil
		},
		item: func(context.Context, *user_model.User, workMutationAuthority, workItemReviseRequest) (*workMutationExecution, error) {
			return nil, errors.New("unexpected item mutation")
		},
		plan: func(context.Context, *user_model.User, workMutationAuthority, workPlanReviseRequest) (*workMutationExecution, error) {
			return nil, errors.New("unexpected plan mutation")
		},
	}
	invalidProjection := validWorkPlanInspection()
	invalidProjection.WorkPlan.PlanningState = "native-numeric-state"
	for _, test := range []struct {
		name   string
		reader WorkReadService
		want   string
	}{
		{name: "permission changed", reader: fakeWorkReadService{
			item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
				return nil, errors.New("unexpected item read")
			},
			plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
				return nil, &WorkReadFailure{Kind: WorkReadUnavailable}
			},
		}, want: "unavailable"},
		{name: "composition failed", reader: fakeWorkReadService{
			item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
				return nil, errors.New("unexpected item read")
			},
			plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
				return nil, errors.New("projection failed")
			},
		}, want: "projection_unavailable"},
		{name: "response schema construction failed", reader: fakeWorkReadService{
			item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
				return nil, errors.New("unexpected item read")
			},
			plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
				return invalidProjection, nil
			},
		}, want: "projection_unavailable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tools := newWorkMutationTools(newToolExecutor(1, time.Second), mutations, test.reader, testPrincipal, 1<<20)
			tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
			_, output, err := tools.beginPlan(t.Context(), testAttributedRequest(), workPlanBeginRequest{
				Repository: WorkRepository{Owner: "octo", Name: "forge"}, IdempotencyKey: "projection-key-000000001",
				Begin: workPlanBegin{Kind: "new", Title: "Plan"},
			})
			require.NoError(t, err)
			assert.Equal(t, "applied", output.Status)
			require.NotNil(t, output.Operation)
			assert.Equal(t, testOperationUUID, output.Operation.ID)
			assert.Equal(t, test.want, output.CurrentResultStatus)
			assert.Nil(t, output.WorkPlan)
		})
	}
}

func TestWorkMutationOutcomeAndRecoveryErrorMapping(t *testing.T) {
	tests := []struct {
		name      string
		execution *workMutationExecution
		err       error
		status    string
		code      string
		retryable bool
	}{
		{name: "durable stale rejection", execution: rejectedMutation("conflict"), status: "rejected", code: "conflict"},
		{name: "outcome unknown", err: mcpwork_service.ErrOutcomeUnknown, status: "error", code: "outcome_unknown", retryable: true},
		{name: "serialization retries exhausted", err: &db.WorkTransactionConflict{}, status: "error", code: "conflict", retryable: true},
		{name: "different request", err: mcpwork_service.ErrIdempotencyConflict, status: "error", code: "idempotency_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mutations := fakeWorkMutationService{
				begin: func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error) {
					return test.execution, test.err
				},
				item: func(context.Context, *user_model.User, workMutationAuthority, workItemReviseRequest) (*workMutationExecution, error) {
					return nil, errors.New("unexpected item mutation")
				},
				plan: func(context.Context, *user_model.User, workMutationAuthority, workPlanReviseRequest) (*workMutationExecution, error) {
					return nil, errors.New("unexpected plan mutation")
				},
			}
			tools := newWorkMutationTools(newToolExecutor(1, time.Second), mutations, inertWorkReader(), testPrincipal, 1<<20)
			tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
			_, output, err := tools.beginPlan(t.Context(), testAttributedRequest(), workPlanBeginRequest{})
			require.NoError(t, err)
			assert.Equal(t, test.status, output.Status)
			require.NotNil(t, output.Problem)
			assert.Equal(t, test.code, output.Problem.Code)
			assert.Equal(t, test.retryable, output.Problem.Retryable)
			if test.status == "rejected" {
				require.NotNil(t, output.Operation)
			} else {
				assert.Nil(t, output.Operation)
			}
		})
	}
}

func TestWorkMutationCommittedEnvelopeMatchesOutputSchema(t *testing.T) {
	for _, execution := range []*workMutationExecution{
		committedMutation(mcpwork_model.OutcomeApplied, false, projectMutationArtifact(9)),
		committedMutation(mcpwork_model.OutcomeUnchanged, true, projectMutationArtifact(9)),
		rejectedMutation("conflict"),
	} {
		output := mutationReceiptOutput(execution)
		if output.Status != "rejected" {
			output.CurrentResultStatus = "available"
		}
		wire, err := json.Marshal(output)
		require.NoError(t, err)
		var value any
		require.NoError(t, json.Unmarshal(wire, &value))
		require.NoError(t, compiledWorkMutationOutputSchema.Validate(value), string(wire))
	}
}

func TestWorkMutationCancellationBoundary(t *testing.T) {
	t.Run("before known commit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		mutations := fakeWorkMutationService{
			begin: func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error) {
				cancel()
				return nil, context.Canceled
			},
		}
		tools := newWorkMutationTools(newToolExecutor(1, time.Second), mutations, inertWorkReader(), testPrincipal, 1<<20)
		tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
		_, output, err := tools.beginPlan(ctx, testAttributedRequest(), workPlanBeginRequest{})
		require.NoError(t, err)
		assert.Equal(t, "error", output.Status)
		assert.Equal(t, "cancelled", output.Problem.Code)
		assert.Nil(t, output.Operation)
	})

	t.Run("after commit", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		mutations := fakeWorkMutationService{
			begin: func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error) {
				cancel()
				return committedMutation(mcpwork_model.OutcomeApplied, false, projectMutationArtifact(9)), nil
			},
		}
		reader := fakeWorkReadService{
			item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
				return nil, errors.New("unexpected item read")
			},
			plan: func(ctx context.Context, _ *user_model.User, _ WorkPlanInspectRequest) (*WorkPlanInspection, error) {
				return nil, ctx.Err()
			},
		}
		tools := newWorkMutationTools(newToolExecutor(1, time.Second), mutations, reader, testPrincipal, 1<<20)
		tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
		_, output, err := tools.beginPlan(ctx, testAttributedRequest(), workPlanBeginRequest{})
		require.NoError(t, err)
		assert.Equal(t, "applied", output.Status)
		require.NotNil(t, output.Operation)
		assert.Equal(t, testOperationUUID, output.Operation.ID)
		assert.Equal(t, "projection_unavailable", output.CurrentResultStatus)
	})

	t.Run("ambiguous commit remains unknown after cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		mutations := fakeWorkMutationService{
			begin: func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error) {
				cancel()
				return nil, mcpwork_service.ErrOutcomeUnknown
			},
		}
		tools := newWorkMutationTools(newToolExecutor(1, time.Second), mutations, inertWorkReader(), testPrincipal, 1<<20)
		tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
		_, output, err := tools.beginPlan(ctx, testAttributedRequest(), workPlanBeginRequest{})
		require.NoError(t, err)
		assert.Equal(t, "error", output.Status)
		assert.Equal(t, "outcome_unknown", output.Problem.Code)
		assert.True(t, output.Problem.Retryable)
		assert.Nil(t, output.Operation)
	})
}

func TestWorkMutationOutputLimitDropsOnlyPostCommitProjection(t *testing.T) {
	large := validWorkPlanInspection()
	large.WorkPlan.Markdown = string(make([]byte, 4096))
	mutations := fakeWorkMutationService{
		begin: func(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error) {
			return committedMutation(mcpwork_model.OutcomeApplied, false, projectMutationArtifact(9)), nil
		},
		item: func(context.Context, *user_model.User, workMutationAuthority, workItemReviseRequest) (*workMutationExecution, error) {
			return nil, errors.New("unexpected item mutation")
		},
		plan: func(context.Context, *user_model.User, workMutationAuthority, workPlanReviseRequest) (*workMutationExecution, error) {
			return nil, errors.New("unexpected plan mutation")
		},
	}
	reader := fakeWorkReadService{
		item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
			return nil, errors.New("unexpected item read")
		},
		plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
			return large, nil
		},
	}
	tools := newWorkMutationTools(newToolExecutor(1, time.Second), mutations, reader, testPrincipal, 512)
	tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
	_, output, err := tools.beginPlan(t.Context(), testAttributedRequest(), workPlanBeginRequest{})
	require.NoError(t, err)
	assert.Equal(t, "applied", output.Status)
	require.NotNil(t, output.Operation)
	assert.Nil(t, output.WorkPlan)
	assert.Equal(t, "projection_unavailable", output.CurrentResultStatus)
}

func TestWorkMutationDiscoveryFlags(t *testing.T) {
	executor := newToolExecutor(1, time.Second)
	pullTool := newPullRequestInspectionTool(executor,
		func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
			return nil, pull_service.ErrPullRequestInspectionUnavailable
		}, testPrincipal)
	mutationTools := newWorkMutationTools(executor, fakeWorkMutationService{}, inertWorkReader(), testPrincipal, 1<<20)
	server := newServerWithWorkMutations(pullTool, newWorkInspectionTools(executor, inertWorkReader(), testPrincipal, 1<<20), mutationTools, false, true)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serverSession.Close()) })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	listed, err := session.ListTools(t.Context(), nil)
	require.NoError(t, err)
	names := make([]string, 0, len(listed.Tools))
	for _, tool := range listed.Tools {
		names = append(names, tool.Name)
	}
	assert.ElementsMatch(t, []string{pullRequestInspectToolName, workPlanBeginToolName, workItemReviseToolName, workPlanReviseToolName}, names)
}

const testOperationUUID = "11111111-1111-4111-8111-111111111111"

func committedMutation(outcome mcpwork_model.Outcome, replayed bool, artifacts ...mcpwork_service.ArtifactReference) *workMutationExecution {
	return &workMutationExecution{
		Receipt: &mcpwork_service.Result{
			OperationUUID: testOperationUUID, Outcome: outcome, Replayed: replayed,
			CommittedAt:       time.Date(2026, time.August, 29, 12, 0, 0, 0, time.UTC),
			ClientAttribution: mcpwork_service.ClientAttribution{Harness: "Example Harness", HarnessVersion: "1", Model: "Example Model", Source: "client-reported"},
		},
		Artifacts: artifacts,
	}
}

func rejectedMutation(code string) *workMutationExecution {
	result := committedMutation(mcpwork_model.OutcomeRejected, false)
	result.Receipt.ProblemCode = code
	return result
}

func projectMutationArtifact(id int64) mcpwork_service.ArtifactReference {
	return mcpwork_service.ArtifactReference{RepositoryID: 1, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: id}
}

func issueMutationArtifact(number int64, local string) mcpwork_service.ArtifactReference {
	return mcpwork_service.ArtifactReference{RepositoryID: 1, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: number, ArtifactNumber: number, LocalReference: local}
}

func testWriteCredential() *verifiedOAuthCredential {
	return &verifiedOAuthCredential{
		Principal:   &user_model.User{ID: 1, IsActive: true},
		Application: &auth_model.OAuth2Application{ID: 8, Name: "Registered Client", MCPInstallationLabel: "Registered Installation"}, Grant: &auth_model.OAuth2Grant{ID: 9},
		CredentialID: "22222222-2222-4222-8222-222222222222",
		Profile:      auth_model.MCPProfileWorkPlanning, CanonicalScope: oauth2_provider.MCPWorkWriteScope,
		Scopes: []string{"read:repository", "write:issue", "write:repository"},
	}
}

func inertWorkReader() WorkReadService {
	return fakeWorkReadService{
		item: func(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
			return nil, errors.New("unexpected item read")
		},
		plan: func(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
			return nil, errors.New("unexpected plan read")
		},
	}
}

func connectWorkMutationTestClient(t *testing.T, mutations workMutationService, reader WorkReadService, maxOutputBytes int64) *mcpsdk.ClientSession {
	t.Helper()
	executor := newToolExecutor(2, time.Second)
	pullTool := newPullRequestInspectionTool(executor,
		func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
			return nil, pull_service.ErrPullRequestInspectionUnavailable
		}, testPrincipal)
	tools := newWorkMutationTools(executor, mutations, reader, testPrincipal, maxOutputBytes)
	tools.credential = func(context.Context) (*verifiedOAuthCredential, error) { return testWriteCredential(), nil }
	server := newServerWithWorkMutations(pullTool, nil, tools, false, true)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serverSession.Close()) })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	return session
}

func assertMutationStructuredResult(t *testing.T, result *mcpsdk.CallToolResult, status string, replayed bool, current string) {
	t.Helper()
	output := structuredWorkOutput(t, result)
	assert.Equal(t, status, output["status"])
	assert.Equal(t, current, output["currentResultStatus"])
	operation, ok := output["operation"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, replayed, operation["replayed"])
}

func TestWorkMutationProductionFlagsRemainOffByDefault(t *testing.T) {
	assert.False(t, setting.MCP.Enabled)
	assert.False(t, setting.MCP.WorkInspectionEnabled)
	assert.False(t, setting.MCP.WorkMutationEnabled)
}
