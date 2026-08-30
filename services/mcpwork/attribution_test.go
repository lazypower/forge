// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcpwork

import (
	"context"
	"strings"
	"testing"

	mcpwork_model "gitea.dev/models/mcpwork"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClientAttributionBoundsAndNormalization(t *testing.T) {
	for _, tc := range []struct {
		name, harness, version, model string
		valid                         bool
	}{
		{"trim", " Harness ", " 1.0 ", " Model ", true},
		{"optional version", "Harness", "", "Model", true},
		{"optional model", "Harness", "1.0", "", true},
		{"unicode bounds", strings.Repeat("界", 128), strings.Repeat("界", 64), strings.Repeat("界", 128), true},
		{"missing harness", "", "", "Model", false},
		{"blank model", "Harness", "", "   ", false},
		{"blank version", "Harness", " ", "Model", false},
		{"long harness", strings.Repeat("界", 129), "", "Model", false},
		{"long model", "Harness", "", strings.Repeat("界", 129), false},
		{"long version", "Harness", strings.Repeat("界", 65), "Model", false},
		{"invalid utf8", "Harness", "", string([]byte{0xff}), false},
		{"leading control", "\nHarness", "", "Model", false},
		{"control", "Harness", "", "Model\x7f", false},
		{"version control", "Harness", "1\t2", "Model", false},
		{"spoofed direction", "Harness\u202e", "", "Model", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewClientAttribution(tc.harness, tc.version, tc.model)
			if !tc.valid {
				require.ErrorIs(t, err, ErrClientAttributionRequired)
				assert.Empty(t, got)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, strings.TrimSpace(tc.harness), got.Harness)
			assert.Equal(t, strings.TrimSpace(tc.version), got.HarnessVersion)
			assert.Equal(t, strings.TrimSpace(tc.model), got.Model)
			assert.Equal(t, "client-reported", got.Source)
		})
	}
}

func TestAttributionRequiredBeforeReceiptTransaction(t *testing.T) {
	s, err := NewService([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	s.runTransaction = func(context.Context, func(context.Context) error) error {
		t.Fatal("invalid attribution reached receipt lookup")
		return nil
	}
	request := testReceiptRequest(testReceiptKey, `{"idempotencyKey":"`+testReceiptKey+`"}`)
	for _, attribution := range []ClientAttribution{
		{},
		{Harness: "Harness", Model: "Model", Source: "verified"},
		{Harness: "Harness", Model: strings.Repeat("x", 129), Source: "client-reported"},
	} {
		request.ClientAttribution = attribution
		_, err := s.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) {
			t.Fatal("invalid attribution reached mutation")
			return Completion{}, nil
		})
		require.ErrorIs(t, err, ErrClientAttributionRequired)
	}
}

func TestRegisteredReceiptLabelsRemainBounded(t *testing.T) {
	s, err := NewService([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	s.runTransaction = func(context.Context, func(context.Context) error) error {
		t.Fatal("invalid registered label reached receipt lookup")
		return nil
	}
	for _, label := range []string{"", " padded ", strings.Repeat("x", 129), "bad\nlabel"} {
		request := testReceiptRequest(testReceiptKey, `{"idempotencyKey":"`+testReceiptKey+`"}`)
		request.Authority.RegisteredClientLabel = label
		_, err := s.Execute(t.Context(), request, func(context.Context, Operation) (Completion, error) { return Completion{}, nil })
		require.ErrorIs(t, err, ErrInvalidRequest)
	}
}

func TestReplayKeepsFirstAttribution(t *testing.T) {
	for _, tc := range []struct {
		name, firstModel, replayModel string
	}{
		{"modeled receipt", "First model", ""},
		{"model-less receipt", "", "Later model"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := prepareReceiptService(t)
			request := testReceiptRequest(testReceiptKey, `{"idempotencyKey":"`+testReceiptKey+`","title":"same"}`)
			var err error
			request.ClientAttribution, err = NewClientAttribution("First harness", "1", tc.firstModel)
			require.NoError(t, err)
			calls := 0
			mutate := func(context.Context, Operation) (Completion, error) {
				calls++
				return Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
			}
			first, err := s.Execute(t.Context(), request, mutate)
			require.NoError(t, err)
			request.ClientAttribution, err = NewClientAttribution("Other harness", "2", tc.replayModel)
			require.NoError(t, err)
			replayed, err := s.Execute(t.Context(), request, mutate)
			require.NoError(t, err)
			assert.True(t, replayed.Replayed)
			assert.Equal(t, first.OperationUUID, replayed.OperationUUID)
			assert.Equal(t, first.ClientAttribution, replayed.ClientAttribution)
			assert.Equal(t, tc.firstModel, replayed.ClientAttribution.Model)
			stored := unittest.AssertExistsAndLoadBean(t, &mcpwork_model.Receipt{OperationUUID: first.OperationUUID})
			assert.Equal(t, tc.firstModel, stored.Model)
			assert.NotEqual(t, request.ClientAttribution, replayed.ClientAttribution)
			assert.Equal(t, 1, calls)
			request.ExpandedInput = []byte(`{"idempotencyKey":"` + testReceiptKey + `","title":"different"}`)
			_, err = s.Execute(t.Context(), request, mutate)
			require.ErrorIs(t, err, ErrIdempotencyConflict)
			assert.Equal(t, 1, calls)
		})
	}
}
