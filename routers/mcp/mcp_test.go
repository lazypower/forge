// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	pull_service "gitea.dev/services/pull"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProtocolVersion = "2026-07-28"
	testDiscoverBody    = `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
)

type testAuthorizationTransport struct {
	base http.RoundTripper
}

func (transport testAuthorizationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	request := req.Clone(req.Context())
	request.Header.Set("Authorization", "Bearer valid")
	return transport.base.RoundTrip(request)
}

func testAuthenticatedEndpoint(server *mcpsdk.Server, maxRequestBodyBytes int64) http.Handler {
	verifier := func(context.Context, string, *http.Request) (*mcpauth.TokenInfo, error) {
		return &mcpauth.TokenInfo{Scopes: []string{string(readRepositoryScope)}}, nil
	}
	return newAuthenticatedEndpoint(server, maxRequestBodyBytes, verifier)
}

func authorizeTestRequest(req *http.Request) *http.Request {
	req.Header.Set("Authorization", "Bearer valid")
	return req
}

func newDiscoverRequest(ctx context.Context, t *testing.T, body string) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://forge.example/mcp", strings.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("MCP-Protocol-Version", testProtocolVersion)
	req.Header.Set("MCP-Method", "server/discover")
	return req
}

func TestEndpointDiscovery(t *testing.T) {
	originalVersion := setting.AppVer
	setting.AppVer = "compatibility-spike"
	defer func() { setting.AppVer = originalVersion }()

	tool := newPullRequestInspectionTool(1, time.Second,
		func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error) {
			return nil, pull_service.ErrPullRequestInspectionUnavailable
		},
		func(context.Context) (*user_model.User, error) { return &user_model.User{ID: 1, IsActive: true}, nil },
	)
	httpServer := httptest.NewServer(testAuthenticatedEndpoint(newServer(tool), 1024))
	defer httpServer.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:   httpServer.URL,
		HTTPClient: &http.Client{Transport: testAuthorizationTransport{base: http.DefaultTransport}},
	}, nil)
	require.NoError(t, err)
	defer session.Close()

	result := session.InitializeResult()
	require.NotNil(t, result)
	require.NotNil(t, result.ServerInfo)
	assert.Equal(t, "forge", result.ServerInfo.Name)
	assert.Equal(t, "compatibility-spike", result.ServerInfo.Version)
	require.NotNil(t, result.Capabilities.Tools)

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, pullRequestInspectToolName, tools.Tools[0].Name)
	require.NotNil(t, tools.Tools[0].Annotations)
	assert.True(t, tools.Tools[0].Annotations.ReadOnlyHint)
}

func TestEndpointHTTPRules(t *testing.T) {
	tests := []struct {
		name       string
		request    func(*testing.T) *http.Request
		bodyLimit  int64
		wantStatus int
		wantAllow  string
	}{
		{
			name: "method",
			request: func(t *testing.T) *http.Request {
				return httptest.NewRequest(http.MethodGet, "https://forge.example/mcp", nil)
			},
			bodyLimit:  1024,
			wantStatus: http.StatusMethodNotAllowed,
			wantAllow:  http.MethodPost,
		},
		{
			name: "content type",
			request: func(t *testing.T) *http.Request {
				return httptest.NewRequest(http.MethodPost, "https://forge.example/mcp", strings.NewReader(testDiscoverBody))
			},
			bodyLimit:  1024,
			wantStatus: http.StatusUnsupportedMediaType,
		},
		{
			name: "accept",
			request: func(t *testing.T) *http.Request {
				req := httptest.NewRequest(http.MethodPost, "https://forge.example/mcp", strings.NewReader(testDiscoverBody))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
			bodyLimit:  1024,
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "body limit",
			request: func(t *testing.T) *http.Request {
				return newDiscoverRequest(t.Context(), t, strings.Repeat(" ", 65))
			},
			bodyLimit:  64,
			wantStatus: http.StatusRequestEntityTooLarge,
		},
		{
			name: "cross origin",
			request: func(t *testing.T) *http.Request {
				req := newDiscoverRequest(t.Context(), t, testDiscoverBody)
				req.Header.Set("Origin", "https://untrusted.example")
				return req
			},
			bodyLimit:  1024,
			wantStatus: http.StatusForbidden,
		},
		{
			name: "cross-site fetch metadata",
			request: func(t *testing.T) *http.Request {
				req := newDiscoverRequest(t.Context(), t, testDiscoverBody)
				req.Header.Set("Sec-Fetch-Site", "cross-site")
				return req
			},
			bodyLimit:  1024,
			wantStatus: http.StatusForbidden,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
			resp := httptest.NewRecorder()

			testAuthenticatedEndpoint(server, test.bodyLimit).ServeHTTP(resp, authorizeTestRequest(test.request(t)))

			assert.Equal(t, test.wantStatus, resp.Code)
			assert.Equal(t, test.wantAllow, resp.Header().Get("Allow"))
		})
	}
}

func TestEndpointAuthenticatesBeforeTransport(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
	endpoint := testAuthenticatedEndpoint(server, 1)
	for _, req := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "https://forge.example/mcp", nil),
		newDiscoverRequest(t.Context(), t, testDiscoverBody),
	} {
		resp := httptest.NewRecorder()
		endpoint.ServeHTTP(resp, req)
		assert.Equal(t, http.StatusUnauthorized, resp.Code)
		assert.Equal(t, "invalid bearer token\n", resp.Body.String())
	}
}

func TestEndpointPropagatesRequestCancellation(t *testing.T) {
	started := make(chan struct{})
	cancelled := make(chan struct{})
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
	server.AddReceivingMiddleware(func(mcpsdk.MethodHandler) mcpsdk.MethodHandler {
		return func(ctx context.Context, _ string, _ mcpsdk.Request) (mcpsdk.Result, error) {
			close(started)
			<-ctx.Done()
			close(cancelled)
			return nil, ctx.Err()
		}
	})
	endpoint := testAuthenticatedEndpoint(server, 1024)
	ctx, cancel := context.WithCancel(t.Context())
	req := authorizeTestRequest(newDiscoverRequest(ctx, t, testDiscoverBody))
	done := make(chan struct{})
	resp := httptest.NewRecorder()
	go func() {
		endpoint.ServeHTTP(resp, req)
		close(done)
	}()

	select {
	case <-started:
	case <-done:
		t.Fatalf("MCP request finished before reaching the server: status %d, body %q", resp.Code, resp.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("MCP request did not reach the server")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("HTTP cancellation did not reach the MCP request context")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("MCP request did not finish after cancellation")
	}
}
