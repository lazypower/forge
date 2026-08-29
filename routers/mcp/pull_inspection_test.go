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
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/json"
	pull_service "gitea.dev/services/pull"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testPrincipal(context.Context) (*user_model.User, error) {
	return &user_model.User{ID: 1, IsActive: true}, nil
}

func TestPullRequestInspectionToolTypedHappyPath(t *testing.T) {
	var captured pull_service.InspectionRequest
	operation := func(_ context.Context, doer *user_model.User, request pull_service.InspectionRequest) (*pull_service.Inspection, error) {
		assert.EqualValues(t, 1, doer.ID)
		captured = request
		return &pull_service.Inspection{
			Repository: pull_service.InspectionRepository{ID: 99, Owner: "octo", Name: "forge", FullName: "octo/forge"},
			Metadata: pull_service.InspectionMetadata{
				ID: 101, Index: 7, Title: "Inspect me", Description: "raw **Markdown**", DescriptionTruncated: true,
				Author: "pat", State: "open", BaseBranch: "main", HeadBranch: "topic",
			},
			Revisions: pull_service.InspectionRevisions{InternalHead: "head", InternalHeadAvailable: true, Target: "target", TargetAvailable: true, ComparisonBase: "base"},
			Files:     &pull_service.InspectionFilePage{Files: []pull_service.InspectionFile{{Name: "README.md", Addition: 2}}},
			Checks:    &pull_service.InspectionChecks{Revision: "head", State: commitstatus.CommitStatusSuccess, Checks: []pull_service.InspectionCheck{{ID: 501, RepositoryID: 99, Revision: "head", Context: "ci", TargetURL: "https://forge.example/actions/1"}}},
			Policy:    &pull_service.InspectionPolicy{Protected: true, RequiredContexts: []string{"ci"}},
		}, nil
	}
	tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second), operation, testPrincipal)
	input := pullRequestInspectionInput{
		Owner: "octo", Repository: "forge", Number: 7, ExpectedHeadRevision: "head",
		ChangedFiles: &pullRequestInspectionPageInput{Limit: 3},
		Diff:         &pullRequestInspectionDiffInput{FileLimit: 2, LinesPerFile: 4, MaxLineCharacters: 80},
		Checks:       true, Policy: true,
	}

	result, output, err := tool.call(t.Context(), nil, input)

	require.NoError(t, err)
	assertPullRequestInspectionResultContent(t, result)
	assert.Equal(t, "available", output.Status)
	require.NotNil(t, output.Inspection)
	assert.EqualValues(t, 7, output.Inspection.Metadata.Number)
	assert.Equal(t, "raw **Markdown**", output.Inspection.Metadata.Description)
	assert.True(t, output.Inspection.Metadata.DescriptionTruncated)
	assert.Equal(t, "README.md", output.Inspection.Files.Files[0].Name)
	assert.Equal(t, "https://forge.example/actions/1", output.Inspection.Checks.Checks[0].TargetURL)
	assert.Equal(t, pull_service.InspectionRequest{
		Owner: "octo", Repository: "forge", Index: 7, ExpectedHeadRevision: "head",
		ChangedFiles: &pull_service.InspectionPageRequest{Limit: 3},
		Diff:         &pull_service.InspectionDiffRequest{FileLimit: 2, LinesPerFile: 4, MaxLineCharacters: 80},
		Checks:       true, Policy: true,
	}, captured)
	wire, err := json.Marshal(output)
	require.NoError(t, err)
	assert.NotContains(t, string(wire), `"ID"`)
	assert.NotContains(t, string(wire), `"id"`)
	assert.NotContains(t, string(wire), "commitStatuses")
}

func TestPullRequestInspectionToolUnavailableIsNeutral(t *testing.T) {
	for _, resourceClass := range []string{"missing", "private", "denied"} {
		t.Run(resourceClass, func(t *testing.T) {
			tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second),
				func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
					return nil, pull_service.ErrPullRequestInspectionUnavailable
				}, testPrincipal)

			result, output, err := tool.call(t.Context(), nil, pullRequestInspectionInput{Owner: "hidden", Repository: resourceClass, Number: 1})

			require.NoError(t, err)
			assertPullRequestInspectionResultContent(t, result)
			assert.Equal(t, pullRequestInspectionOutput{Status: "unavailable"}, output)
		})
	}
}

func TestPullRequestInspectionToolRejectsOversizedProjection(t *testing.T) {
	tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second),
		func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
			return &pull_service.Inspection{
				Diff: &pull_service.InspectionDiffPage{Files: []pull_service.InspectionDiffFile{{
					Sections: []pull_service.InspectionDiffSection{{Lines: []pull_service.InspectionDiffLine{{
						Content: strings.Repeat("x", pull_service.MaxPullRequestInspectionDocumentBytes),
					}}}},
				}}},
			}, nil
		}, testPrincipal)

	result, output, err := tool.call(t.Context(), nil, pullRequestInspectionInput{})

	require.NoError(t, err)
	assert.True(t, result.IsError)
	assert.Equal(t, "limit_exceeded", output.Failure.Code)
}

func TestPullRequestInspectionSerializedResultBound(t *testing.T) {
	payload := strings.Repeat("x", pull_service.MaxPullRequestInspectionDocumentBytes-pull_service.MaxPullRequestInspectionDescriptionBytes-(8<<10))
	description := strings.Repeat("d", pull_service.MaxPullRequestInspectionDescriptionBytes)
	tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second),
		func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
			return &pull_service.Inspection{
				Metadata: pull_service.InspectionMetadata{Description: description},
				Diff: &pull_service.InspectionDiffPage{Files: []pull_service.InspectionDiffFile{{
					Sections: []pull_service.InspectionDiffSection{{Lines: []pull_service.InspectionDiffLine{{Content: payload}}}},
				}}},
			}, nil
		}, testPrincipal)
	server := newServer(tool, nil, false)
	clientTransport, serverTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := server.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, serverSession.Close()) })
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	clientSession, err := client.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, clientSession.Close()) })

	result, err := clientSession.CallTool(t.Context(), &mcpsdk.CallToolParams{
		Name: pullRequestInspectToolName,
		Arguments: map[string]any{
			"owner": "octo", "repository": "forge", "number": 1,
		},
	})
	require.NoError(t, err)
	assertPullRequestInspectionResultContent(t, result)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	inspection, ok := structured["inspection"].(map[string]any)
	require.True(t, ok)
	metadata, ok := inspection["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, description, metadata["description"])
	assert.Equal(t, false, metadata["descriptionTruncated"])
	structuredJSON, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(structuredJSON), pull_service.MaxPullRequestInspectionDocumentBytes)
	serialized, err := json.Marshal(result)
	require.NoError(t, err)
	assert.LessOrEqual(t, len(serialized), pull_service.MaxPullRequestInspectionResponseBytes)
}

func TestPullRequestInspectionToolPreservesExpansionPermissionBoundary(t *testing.T) {
	operation := func(_ context.Context, _ *user_model.User, request pull_service.InspectionRequest) (*pull_service.Inspection, error) {
		if request.ChangedFiles != nil || request.Diff != nil {
			return nil, pull_service.ErrPullRequestInspectionUnavailable
		}
		return &pull_service.Inspection{}, nil
	}
	tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second), operation, testPrincipal)

	_, metadata, err := tool.call(t.Context(), nil, pullRequestInspectionInput{Owner: "octo", Repository: "forge", Number: 1})
	require.NoError(t, err)
	assert.Equal(t, "available", metadata.Status)
	for _, input := range []pullRequestInspectionInput{
		{Owner: "octo", Repository: "forge", Number: 1, ChangedFiles: &pullRequestInspectionPageInput{}},
		{Owner: "octo", Repository: "forge", Number: 1, Diff: &pullRequestInspectionDiffInput{}},
	} {
		_, output, err := tool.call(t.Context(), nil, input)
		require.NoError(t, err)
		assert.Equal(t, pullRequestInspectionOutput{Status: "unavailable"}, output)
	}
}

func TestPullRequestInspectionToolPreservesActionsURLProjection(t *testing.T) {
	for _, test := range []struct {
		name      string
		targetURL string
	}{
		{name: "Actions permitted", targetURL: "https://forge.example/actions/1"},
		{name: "Actions denied"},
	} {
		t.Run(test.name, func(t *testing.T) {
			tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second),
				func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
					return &pull_service.Inspection{Checks: &pull_service.InspectionChecks{Checks: []pull_service.InspectionCheck{{TargetURL: test.targetURL}}}}, nil
				}, testPrincipal)
			_, output, err := tool.call(t.Context(), nil, pullRequestInspectionInput{Owner: "octo", Repository: "forge", Number: 1, Checks: true})
			require.NoError(t, err)
			assert.Equal(t, test.targetURL, output.Inspection.Checks.Checks[0].TargetURL)
		})
	}
}

func TestPullRequestInspectionToolMapsControlledErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		code string
	}{
		{name: "output overflow", err: pull_service.ErrPullRequestInspectionLimit, code: "limit_exceeded"},
		{name: "head changed", err: pull_service.ErrPullRequestInspectionHeadChanged, code: "head_changed"},
		{name: "cursor", err: pull_service.ErrPullRequestInspectionCursor, code: "invalid_cursor"},
		{name: "internal", err: errors.New("database contains secret"), code: "inspection_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second),
				func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
					return nil, test.err
				},
				testPrincipal)
			result, output, err := tool.call(t.Context(), nil, pullRequestInspectionInput{})
			require.NoError(t, err)
			require.NotNil(t, result)
			assert.True(t, result.IsError)
			require.NotNil(t, output.Failure)
			assert.Equal(t, test.code, output.Failure.Code)
			assert.NotContains(t, output.Failure.Message, "secret")
		})
	}
}

func TestPullRequestInspectionToolTimeoutAndCancellation(t *testing.T) {
	operation := func(ctx context.Context, _ *user_model.User, _ pull_service.InspectionRequest) (*pull_service.Inspection, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	t.Run("timeout", func(t *testing.T) {
		tool := newPullRequestInspectionTool(newToolExecutor(1, time.Millisecond), operation, testPrincipal)
		_, output, err := tool.call(t.Context(), nil, pullRequestInspectionInput{})
		require.NoError(t, err)
		assert.Equal(t, "timeout", output.Failure.Code)
	})
	t.Run("caller cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second), operation, testPrincipal)
		_, output, err := tool.call(ctx, nil, pullRequestInspectionInput{})
		require.NoError(t, err)
		assert.Equal(t, "cancelled", output.Failure.Code)
	})
}

func TestPullRequestInspectionToolNonBlockingCapacityAndRecovery(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	operation := func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
		close(started)
		<-release
		return &pull_service.Inspection{}, nil
	}
	tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second), operation, testPrincipal)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _, _ = tool.call(t.Context(), nil, pullRequestInspectionInput{})
	}()
	<-started

	_, rejected, err := tool.call(t.Context(), nil, pullRequestInspectionInput{})
	require.NoError(t, err)
	assert.Equal(t, "busy", rejected.Failure.Code)
	close(release)
	<-done

	tool.inspect = func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
		return &pull_service.Inspection{}, nil
	}
	_, recovered, err := tool.call(t.Context(), nil, pullRequestInspectionInput{})
	require.NoError(t, err)
	assert.Equal(t, "available", recovered.Status)
}

func TestPullRequestInspectionToolReleasesCapacityAfterPanic(t *testing.T) {
	tool := newPullRequestInspectionTool(newToolExecutor(1, time.Second),
		func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
			panic("boom")
		},
		testPrincipal)
	func() {
		defer func() { assert.Equal(t, "boom", recover()) }()
		_, _, _ = tool.call(t.Context(), nil, pullRequestInspectionInput{})
	}()
	tool.inspect = func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
		return &pull_service.Inspection{}, nil
	}

	_, output, err := tool.call(t.Context(), nil, pullRequestInspectionInput{})

	require.NoError(t, err)
	assert.Equal(t, "available", output.Status)
}

func assertPullRequestInspectionResultContent(t *testing.T, result *mcpsdk.CallToolResult) {
	t.Helper()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	content, ok := result.Content[0].(*mcpsdk.TextContent)
	require.True(t, ok)
	assert.Equal(t, pullRequestInspectionContent, content.Text)
}
