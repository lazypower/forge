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

	"gitea.dev/modules/setting"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testProtocolVersion = "2026-07-28"
	testDiscoverBody    = `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`
)

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

	httpServer := httptest.NewServer(NewEndpoint())
	defer httpServer.Close()

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	require.NoError(t, err)
	defer session.Close()

	result := session.InitializeResult()
	require.NotNil(t, result)
	require.NotNil(t, result.ServerInfo)
	assert.Equal(t, "forge", result.ServerInfo.Name)
	assert.Equal(t, "compatibility-spike", result.ServerInfo.Version)
	assert.Nil(t, result.Capabilities.Tools)

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, tools.Tools)
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
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
			resp := httptest.NewRecorder()

			newEndpoint(server, test.bodyLimit).ServeHTTP(resp, test.request(t))

			assert.Equal(t, test.wantStatus, resp.Code)
			assert.Equal(t, test.wantAllow, resp.Header().Get("Allow"))
		})
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
	endpoint := newEndpoint(server, 1024)
	ctx, cancel := context.WithCancel(t.Context())
	req := newDiscoverRequest(ctx, t, testDiscoverBody)
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
