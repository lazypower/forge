// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcpwork

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gitea.dev/models/db"
	mcpwork_model "gitea.dev/models/mcpwork"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testReceiptKey = "receipt-key-000000000000000000000001"
	testMarkdown   = "PRIVATE-MARKDOWN-CONTENT"
)

func TestExecuteCanonicalReplayAndConflict(t *testing.T) {
	service := prepareReceiptService(t)
	var calls atomic.Int64
	mutate := func(context.Context, Operation) (Completion, error) {
		calls.Add(1)
		return Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
	}

	first, err := service.Execute(t.Context(), testReceiptRequest(testReceiptKey, fmt.Sprintf(`{
		"idempotencyKey": %q, "repository": {"owner":"example","name":"repo"},
		"count": 1.0, "markdown": %q}`, testReceiptKey, testMarkdown)), mutate)
	require.NoError(t, err)
	replayed, err := service.Execute(t.Context(), testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"markdown":%q,"count":1e0,"repository":{"name":"repo","owner":"example"},"idempotencyKey":%q}`, testMarkdown, testReceiptKey)), mutate)
	require.NoError(t, err)
	assert.Equal(t, first.OperationUUID, replayed.OperationUUID)
	assert.True(t, replayed.Replayed)
	assert.EqualValues(t, 1, calls.Load())

	_, err = service.Execute(t.Context(), testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"repository":{"owner":"example","name":"other"}}`, testReceiptKey)), mutate)
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	assert.NotContains(t, err.Error(), "example")
	assert.NotContains(t, err.Error(), "work_plan.begin")
	assert.EqualValues(t, 1, calls.Load())

	independent := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"repository":{"owner":"example","name":"other"}}`, testReceiptKey))
	independent.Authority.PrincipalID++
	_, err = service.Execute(t.Context(), independent, mutate)
	require.NoError(t, err)
	assert.EqualValues(t, 2, calls.Load())
}

func TestRequestDigestBindsToolSchemaAndCanonicalKeylessInput(t *testing.T) {
	service := prepareReceiptService(t)
	request := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"value":1}`, testReceiptKey))
	var calls atomic.Int64
	mutate := func(context.Context, Operation) (Completion, error) {
		calls.Add(1)
		return Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
	}
	_, err := service.Execute(t.Context(), request, mutate)
	require.NoError(t, err)

	differentTool := request
	differentTool.Tool = "work_plan.revise"
	_, err = service.Execute(t.Context(), differentTool, mutate)
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	differentSchema := request
	differentSchema.SchemaVersion = "2"
	_, err = service.Execute(t.Context(), differentSchema, mutate)
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	differentInput := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"value":2}`, testReceiptKey))
	_, err = service.Execute(t.Context(), differentInput, mutate)
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	assert.EqualValues(t, 1, calls.Load())
}

func TestExecuteConcurrentDuplicate(t *testing.T) {
	service := prepareReceiptService(t)
	request := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"title":"same"}`, testReceiptKey))
	var calls atomic.Int64
	mutate := func(context.Context, Operation) (Completion, error) {
		calls.Add(1)
		time.Sleep(5 * time.Millisecond)
		return Completion{
			Outcome:   mcpwork_model.OutcomeApplied,
			Artifacts: []ArtifactReference{{RepositoryID: 11, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: 22, ArtifactNumber: 7}},
		}, nil
	}

	const workers = 8
	results := make(chan *Result, workers)
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Go(func() {
			result, err := service.Execute(t.Context(), request, mutate)
			results <- result
			errorsFound <- err
		})
	}
	group.Wait()
	close(results)
	close(errorsFound)

	for err := range errorsFound {
		require.NoError(t, err)
	}
	var operationUUID string
	for result := range results {
		require.NotNil(t, result)
		if operationUUID == "" {
			operationUUID = result.OperationUUID
		}
		assert.Equal(t, operationUUID, result.OperationUUID)
	}
	assert.EqualValues(t, 1, calls.Load())
	assert.EqualValues(t, 1, countModel(t, new(mcpwork_model.Receipt)))
	assert.EqualValues(t, 1, countModel(t, new(mcpwork_model.ArtifactLink)))
}

func TestExecuteRollbackRemovesDomainFactAndReceipt(t *testing.T) {
	service := prepareReceiptService(t)
	prepareReceiptEffectTable(t)
	errMutation := errors.New("domain mutation rejected")
	_, err := service.Execute(t.Context(), testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"title":"rollback"}`, testReceiptKey)), func(ctx context.Context, _ Operation) (Completion, error) {
		_, err := db.Exec(ctx, "INSERT INTO mcp_work_receipt_effect (id) VALUES (?)", 1)
		require.NoError(t, err)
		return Completion{}, errMutation
	})
	require.ErrorIs(t, err, errMutation)
	assert.Zero(t, countReceiptEffects(t))
	assert.Zero(t, countModel(t, new(mcpwork_model.Receipt)))
}

func TestExecuteAmbiguousCommitAndResponseLossRecovery(t *testing.T) {
	service := prepareReceiptService(t)
	request := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"title":"ambiguous"}`, testReceiptKey))
	var calls atomic.Int64
	injected := errors.New("commit acknowledgement lost")
	original := service.runTransaction
	var loseCommit atomic.Bool
	service.runTransaction = func(ctx context.Context, callback func(context.Context) error) error {
		err := original(ctx, callback)
		if err == nil && loseCommit.CompareAndSwap(false, true) {
			return injected
		}
		return err
	}
	result, err := service.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) {
		calls.Add(1)
		return Completion{Outcome: mcpwork_model.OutcomeUnchanged}, nil
	})
	require.NoError(t, err)
	assert.True(t, result.Replayed)
	assert.EqualValues(t, 1, calls.Load())

	// Simulate a complete response loss by discarding the first result and
	// issuing the identical request again.
	replayed, err := service.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) {
		t.Fatal("response-loss replay invoked mutation")
		return Completion{}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, result.OperationUUID, replayed.OperationUUID)
	assert.True(t, replayed.Replayed)
}

func TestExecuteDefinitelyAbsentAmbiguousCommitRetriesWholeMutation(t *testing.T) {
	service := prepareReceiptService(t)
	prepareReceiptEffectTable(t)
	original := service.runTransaction
	injected := errors.New("transaction result unavailable")
	var attempts atomic.Int64
	service.runTransaction = func(ctx context.Context, callback func(context.Context) error) error {
		if attempts.Add(1) == 1 {
			return original(ctx, func(txCtx context.Context) error {
				if err := callback(txCtx); err != nil {
					return err
				}
				return injected
			})
		}
		return original(ctx, callback)
	}
	var callbacks atomic.Int64
	result, err := service.Execute(t.Context(), testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"title":"retry"}`, testReceiptKey)), func(ctx context.Context, _ Operation) (Completion, error) {
		callbacks.Add(1)
		_, err := db.Exec(ctx, "INSERT INTO mcp_work_receipt_effect (id) VALUES (?)", 1)
		if err != nil {
			return Completion{}, err
		}
		return Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
	})
	require.NoError(t, err)
	assert.False(t, result.Replayed)
	assert.EqualValues(t, 2, callbacks.Load())
	assert.EqualValues(t, 1, countReceiptEffects(t))
	assert.EqualValues(t, 1, countModel(t, new(mcpwork_model.Receipt)))
}

func TestExecuteAmbiguousLookupFailureIsOutcomeUnknown(t *testing.T) {
	service := prepareReceiptService(t)
	original := service.runTransaction
	service.runTransaction = func(ctx context.Context, callback func(context.Context) error) error {
		if err := original(ctx, callback); err != nil {
			return err
		}
		return errors.New("commit acknowledgement lost")
	}
	service.recoverReceipt = func(context.Context, int64, string, string) (*mcpwork_model.Receipt, error) {
		return nil, errors.New("lookup unavailable")
	}
	_, err := service.Execute(t.Context(), testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"title":"unknown"}`, testReceiptKey)), func(context.Context, Operation) (Completion, error) {
		return Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
	})
	require.ErrorIs(t, err, ErrOutcomeUnknown)
	assert.NotContains(t, err.Error(), "lookup unavailable")
}

func TestReplayAndPresentationRecheckPermissions(t *testing.T) {
	service := prepareReceiptService(t)
	request := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"title":"visible"}`, testReceiptKey))
	completion := Completion{
		Outcome:   mcpwork_model.OutcomeApplied,
		Artifacts: []ArtifactReference{{RepositoryID: 11, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: 22, ArtifactNumber: 7, LocalReference: "created"}},
		Events:    []EventReference{{RepositoryID: 11, Kind: mcpwork_model.EventKindIssueComment, EventID: 33, ArtifactKind: mcpwork_model.ArtifactKindIssue, ArtifactID: 22}},
	}
	first, err := service.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) { return completion, nil })
	require.NoError(t, err)
	replayed, err := service.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) {
		t.Fatal("replay invoked mutation")
		return Completion{}, nil
	})
	require.NoError(t, err)
	assert.Equal(t, first.OperationUUID, replayed.OperationUUID)
	assert.True(t, replayed.Replayed)

	presentation, err := service.Present(t.Context(), first.OperationUUID, func(context.Context, ArtifactReference) (bool, error) { return false, nil })
	require.NoError(t, err)
	require.NotNil(t, presentation)
	assert.False(t, presentation.Available)
	assert.Empty(t, presentation.Artifacts)
	assert.Zero(t, presentation.PrincipalID)
	presentation, err = service.Present(t.Context(), first.OperationUUID, func(context.Context, ArtifactReference) (bool, error) { return true, nil })
	require.NoError(t, err)
	require.NotNil(t, presentation)
	assert.True(t, presentation.Available)
	assert.Equal(t, "mcp", presentation.Origin)
	assert.Equal(t, "unverified", presentation.ActorTrust)
	assert.Equal(t, completion.Artifacts, presentation.Artifacts)
	assert.Equal(t, completion.Events, presentation.Events)
	artifactPresentations, err := service.PresentArtifact(t.Context(), completion.Artifacts[0], func(context.Context, ArtifactReference) (bool, error) { return true, nil })
	require.NoError(t, err)
	require.Len(t, artifactPresentations, 1)
	assert.Equal(t, first.OperationUUID, artifactPresentations[0].OperationUUID)
	eventPresentations, err := service.PresentEvent(t.Context(), completion.Events[0], completion.Artifacts[0], func(context.Context, ArtifactReference) (bool, error) { return true, nil })
	require.NoError(t, err)
	require.Len(t, eventPresentations, 1)
	assert.Equal(t, first.OperationUUID, eventPresentations[0].OperationUUID)
	deniedPresentations, err := service.PresentArtifact(t.Context(), completion.Artifacts[0], func(context.Context, ArtifactReference) (bool, error) { return false, nil })
	require.NoError(t, err)
	assert.Empty(t, deniedPresentations)
}

func TestRetirementPreventsKeyReuseAndClearsDetail(t *testing.T) {
	service := prepareReceiptService(t)
	request := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"markdown":%q}`, testReceiptKey, testMarkdown))
	var calls atomic.Int64
	_, err := service.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) {
		calls.Add(1)
		return Completion{
			Outcome:   mcpwork_model.OutcomeApplied,
			Artifacts: []ArtifactReference{{RepositoryID: 11, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: 44, ArtifactNumber: 7}},
			Events:    []EventReference{{RepositoryID: 11, Kind: mcpwork_model.EventKindIssueComment, EventID: 45, ArtifactKind: mcpwork_model.ArtifactKindIssue, ArtifactID: 44}},
		}, nil
	})
	require.NoError(t, err)

	err = mcpwork_model.RetireIssue(t.Context(), 11, 44)
	require.NoError(t, err)
	assert.Zero(t, countModel(t, new(mcpwork_model.ArtifactLink)))
	assert.Zero(t, countModel(t, new(mcpwork_model.EventLink)))
	prepared, err := service.prepare(request)
	require.NoError(t, err)
	stored, err := mcpwork_model.FindByKey(t.Context(), request.Authority.PrincipalID, prepared.audienceDigest, prepared.keyDigest)
	require.NoError(t, err)
	require.NotNil(t, stored)
	assert.Equal(t, request.Authority.PrincipalID, stored.PrincipalID)
	assert.Len(t, stored.AudienceDigest, 64)
	assert.Len(t, stored.KeyDigest, 64)
	assert.Len(t, stored.RequestDigest, 64)
	assert.Positive(t, stored.TombstonedUnix)
	assert.Empty(t, stored.OperationUUID)
	assert.Empty(t, stored.Tool)
	assert.Zero(t, stored.ApplicationID)
	assert.Empty(t, stored.CredentialID)
	assert.Empty(t, stored.Scope)
	assert.Empty(t, stored.Origin)

	_, err = service.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) {
		calls.Add(1)
		return Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
	})
	require.ErrorIs(t, err, ErrReceiptTombstoned)
	changed := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"markdown":"different"}`, testReceiptKey))
	_, err = service.Execute(t.Context(), changed, func(context.Context, Operation) (Completion, error) {
		calls.Add(1)
		return Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
	})
	require.ErrorIs(t, err, ErrIdempotencyConflict)
	assert.EqualValues(t, 1, calls.Load())
}

func TestReceiptNeverPersistsSecretsOrRequestContent(t *testing.T) {
	service := prepareReceiptService(t)
	request := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"markdown":%q,"nested":{"token":"RAW-TOKEN-MATERIAL"}}`, testReceiptKey, testMarkdown))
	result, err := service.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) {
		return Completion{Outcome: mcpwork_model.OutcomeRejected, ProblemCode: "conflict"}, nil
	})
	require.NoError(t, err)
	receipt, _, _, err := mcpwork_model.GetReceiptByUUID(t.Context(), result.OperationUUID)
	require.NoError(t, err)
	persisted := fmt.Sprintf("%+v", receipt)
	assert.NotContains(t, persisted, testReceiptKey)
	assert.NotContains(t, persisted, testMarkdown)
	assert.NotContains(t, persisted, "RAW-TOKEN-MATERIAL")
	assert.NotContains(t, persisted, `"markdown"`)
	assert.Len(t, receipt.KeyDigest, 64)
	assert.Len(t, receipt.RequestDigest, 64)
	assert.Equal(t, request.Authority.PrincipalID, receipt.PrincipalID)
	assert.Equal(t, request.Authority.OAuthApplicationID, receipt.ApplicationID)
	assert.Equal(t, request.Authority.OAuthGrantID, receipt.GrantID)
	assert.Equal(t, request.Authority.CredentialJTI, receipt.CredentialID)
	assert.Equal(t, request.Authority.Scope, receipt.Scope)
	assert.Equal(t, "unverified", receipt.ActorTrust)
	assert.Equal(t, "mcp", receipt.Origin)
}

func TestReceiptDigestDomainsAndDedicatedSecrets(t *testing.T) {
	first, err := NewService([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	second, err := NewService([]byte("fedcba9876543210fedcba9876543210"))
	require.NoError(t, err)
	request := testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"value":1}`, testReceiptKey))
	firstPrepared, err := first.prepare(request)
	require.NoError(t, err)
	secondPrepared, err := second.prepare(request)
	require.NoError(t, err)
	assert.NotEqual(t, firstPrepared.keyDigest, firstPrepared.requestDigest)
	assert.NotEqual(t, firstPrepared.keyDigest, secondPrepared.keyDigest)
	assert.NotEqual(t, firstPrepared.requestDigest, secondPrepared.requestDigest)
	assert.Len(t, firstPrepared.audienceDigest, 64)
}

func TestReceiptRejectsAmbiguousOrInvalidCanonicalInput(t *testing.T) {
	service, err := NewService([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	for _, input := range []string{
		fmt.Sprintf(`{"idempotencyKey":%q,"idempotencyKey":%q}`, testReceiptKey, testReceiptKey),
		fmt.Sprintf(`[%q]`, testReceiptKey),
		fmt.Sprintf(`{"idempotencyKey":%q`, testReceiptKey),
	} {
		request := testReceiptRequest(testReceiptKey, input)
		_, err := service.prepare(request)
		require.ErrorIs(t, err, ErrInvalidRequest)
	}
}

func TestExecuteCancellationRollsBackReceipt(t *testing.T) {
	service := prepareReceiptService(t)
	ctx, cancel := context.WithCancel(t.Context())
	_, err := service.Execute(ctx, testReceiptRequest(testReceiptKey, fmt.Sprintf(`{"idempotencyKey":%q,"title":"cancelled"}`, testReceiptKey)), func(context.Context, Operation) (Completion, error) {
		cancel()
		return Completion{}, context.Canceled
	})
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, countModel(t, new(mcpwork_model.Receipt)))
}

func prepareReceiptService(t *testing.T) *Service {
	t.Helper()
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, db.TruncateBeans(t.Context(), new(mcpwork_model.EventLink), new(mcpwork_model.ArtifactLink), new(mcpwork_model.Receipt)))
	service, err := NewService([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	service.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	service.newUUID = func() string { return "11111111-1111-4111-8111-111111111111" }
	return service
}

func testReceiptRequest(key, input string) Request {
	return Request{
		Tool: "work_plan.begin", SchemaVersion: "1", IdempotencyKey: key, ExpandedInput: []byte(input),
		Authority: Authority{
			PrincipalID: 1, OAuthApplicationID: 2, OAuthGrantID: 3,
			CredentialJTI: "22222222-2222-4222-8222-222222222222",
			Audience:      "https://forge.example/mcp",
			Scope:         "read:repository write:issue write:repository",
		},
	}
}

func countModel(t *testing.T, bean any) int64 {
	t.Helper()
	count, err := db.GetEngine(t.Context()).Count(bean)
	require.NoError(t, err)
	return count
}

func prepareReceiptEffectTable(t *testing.T) {
	t.Helper()
	_, err := db.Exec(t.Context(), "DROP TABLE IF EXISTS mcp_work_receipt_effect")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "CREATE TABLE mcp_work_receipt_effect (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS mcp_work_receipt_effect")
	})
}

func countReceiptEffects(t *testing.T) int64 {
	t.Helper()
	var count int64
	has, err := db.GetEngine(t.Context()).SQL("SELECT COUNT(*) FROM mcp_work_receipt_effect").Get(&count)
	require.NoError(t, err)
	require.True(t, has)
	return count
}
