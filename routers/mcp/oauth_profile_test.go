// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtectedResourceMetadata(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/forge/")()
	defer test_module.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "https://forge.example/forge")()
	req := httptest.NewRequest(http.MethodGet, setting.MCPProtectedResourceMetadataPath(), nil)
	resp := httptest.NewRecorder()

	ProtectedResourceMetadata().ServeHTTP(resp, req)

	require.Equal(t, http.StatusOK, resp.Code)
	assert.Equal(t, "*", resp.Header().Get("Access-Control-Allow-Origin"))
	var metadata oauthex.ProtectedResourceMetadata
	require.NoError(t, json.Unmarshal(resp.Body.Bytes(), &metadata))
	assert.Equal(t, "https://forge.example/forge/mcp", metadata.Resource)
	assert.Equal(t, []string{"https://forge.example/forge"}, metadata.AuthorizationServers)
	assert.Equal(t, []string{"read:repository"}, metadata.ScopesSupported)
	assert.Equal(t, []string{"header"}, metadata.BearerMethodsSupported)
	assert.Equal(t, "Forge MCP", metadata.ResourceName)
}

func TestOAuthBearerChallenges(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/forge/")()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
	verifier := func(_ context.Context, token string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		switch token {
		case "credential.invalid":
			return nil, errInvalidBearerToken
		case "credential.expired":
			return &mcpauth.TokenInfo{Scopes: []string{"read:repository"}, Expiration: time.Now().Add(-time.Minute)}, nil
		case "credential.scope":
			return &mcpauth.TokenInfo{Scopes: []string{"read:user"}, Expiration: time.Now().Add(time.Minute)}, nil
		case "credential.valid":
			return &mcpauth.TokenInfo{Scopes: []string{"read:repository"}, Expiration: time.Now().Add(time.Minute)}, nil
		default:
			return nil, errors.New("unexpected token")
		}
	}
	endpoint := newOAuthAuthenticatedEndpoint(server, 1024, verifier)
	tests := []struct {
		name, token, oauthError string
		status                  int
	}{
		{name: "missing", status: http.StatusUnauthorized, oauthError: `error="invalid_token"`},
		{name: "invalid", token: "credential.invalid", status: http.StatusUnauthorized, oauthError: `error="invalid_token"`},
		{name: "expired", token: "credential.expired", status: http.StatusUnauthorized, oauthError: `error="invalid_token"`},
		{name: "insufficient scope", token: "credential.scope", status: http.StatusForbidden, oauthError: `error="insufficient_scope"`},
		{name: "accepted", token: "credential.valid", status: http.StatusOK},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := newDiscoverRequest(t.Context(), t, testDiscoverBody)
			if test.token != "" {
				req.Header.Set("Authorization", "Bearer "+test.token)
			}
			resp := httptest.NewRecorder()
			endpoint.ServeHTTP(resp, req)
			assert.Equal(t, test.status, resp.Code)
			if test.oauthError == "" {
				return
			}
			challenge := resp.Header().Get("WWW-Authenticate")
			assert.Contains(t, challenge, `resource_metadata="https://forge.example/forge/.well-known/oauth-protected-resource/mcp"`)
			assert.Contains(t, challenge, `scope="read:repository"`)
			assert.Contains(t, challenge, test.oauthError)
			if test.token != "" {
				assert.NotContains(t, resp.Body.String(), test.token)
			}
		})
	}
}
