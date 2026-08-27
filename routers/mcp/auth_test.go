// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	auth_model "gitea.dev/models/auth"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPATVerifier(t *testing.T) {
	validUser := &user_model.User{ID: 1, IsActive: true, Type: user_model.UserTypeIndividual}
	tests := []struct {
		name       string
		tokenValue string
		token      *auth_model.AccessToken
		tokenErr   error
		user       *user_model.User
		userErr    error
		accepted   bool
	}{
		{name: "read repository PAT", tokenValue: "valid", token: &auth_model.AccessToken{UID: 1, Scope: auth_model.AccessTokenScopeReadRepository}, user: validUser, accepted: true},
		{name: "missing", tokenErr: errors.New("missing")},
		{name: "unknown", tokenValue: "unknown", tokenErr: errors.New("unknown")},
		{name: "OAuth access JWT", tokenValue: "header.payload.signature", tokenErr: errors.New("not a PAT")},
		{name: "OAuth refresh JWT", tokenValue: "refresh.payload.signature", tokenErr: errors.New("not a PAT")},
		{name: "Actions credential", tokenValue: "actions-token", tokenErr: errors.New("not a PAT")},
		{name: "all scope", tokenValue: "all", token: &auth_model.AccessToken{UID: 1, Scope: auth_model.AccessTokenScopeAll}, user: validUser},
		{name: "write scope", tokenValue: "write", token: &auth_model.AccessToken{UID: 1, Scope: auth_model.AccessTokenScopeWriteRepository}, user: validUser},
		{name: "empty scope", tokenValue: "empty", token: &auth_model.AccessToken{UID: 1}, user: validUser},
		{name: "invalid scope", tokenValue: "invalid-scope", token: &auth_model.AccessToken{UID: 1, Scope: "invalid"}, user: validUser},
		{name: "public only", tokenValue: "public", token: &auth_model.AccessToken{UID: 1, Scope: auth_model.AccessTokenScopePublicOnly}, user: validUser},
		{name: "read and public only", tokenValue: "mixed", token: &auth_model.AccessToken{UID: 1, Scope: "read:repository,public-only"}, user: validUser},
		{name: "duplicate read scope", tokenValue: "duplicate", token: &auth_model.AccessToken{UID: 1, Scope: "read:repository,read:repository"}, user: validUser},
		{name: "missing user", tokenValue: "missing-user", token: &auth_model.AccessToken{UID: 1, Scope: auth_model.AccessTokenScopeReadRepository}, userErr: errors.New("missing")},
		{name: "inactive user", tokenValue: "inactive", token: &auth_model.AccessToken{UID: 1, Scope: auth_model.AccessTokenScopeReadRepository}, user: &user_model.User{ID: 1}},
		{name: "prohibited user", tokenValue: "prohibited", token: &auth_model.AccessToken{UID: 1, Scope: auth_model.AccessTokenScopeReadRepository}, user: &user_model.User{ID: 1, IsActive: true, ProhibitLogin: true}},
		{name: "ghost principal", tokenValue: "ghost", token: &auth_model.AccessToken{UID: user_model.GhostUserID, Scope: auth_model.AccessTokenScopeReadRepository}, user: user_model.NewGhostUser()},
		{name: "Actions principal", tokenValue: "actions", token: &auth_model.AccessToken{UID: user_model.ActionsUserID, Scope: auth_model.AccessTokenScopeReadRepository}, user: user_model.NewActionsUser()},
		{name: "system principal", tokenValue: "system", token: &auth_model.AccessToken{UID: -99, Scope: auth_model.AccessTokenScopeReadRepository}, user: &user_model.User{ID: -99, IsActive: true}},
		{name: "organization principal", tokenValue: "organization", token: &auth_model.AccessToken{UID: 2, Scope: auth_model.AccessTokenScopeReadRepository}, user: &user_model.User{ID: 2, IsActive: true, Type: user_model.UserTypeOrganization}},
		{name: "reserved principal", tokenValue: "reserved", token: &auth_model.AccessToken{UID: 3, Scope: auth_model.AccessTokenScopeReadRepository}, user: &user_model.User{ID: 3, IsActive: true, Type: user_model.UserTypeUserReserved}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verifier := newPATVerifierWithLookups(
				func(context.Context, string) (*auth_model.AccessToken, error) { return test.token, test.tokenErr },
				func(context.Context, int64) (*user_model.User, error) { return test.user, test.userErr },
			)
			info, err := verifier(t.Context(), test.tokenValue, httptest.NewRequest(http.MethodPost, "/mcp", nil))
			if test.accepted {
				require.NoError(t, err)
				require.NotNil(t, info)
				assert.Equal(t, []string{"read:repository"}, info.Scopes)
				assert.True(t, info.Expiration.IsZero())
				assert.Same(t, validUser, info.Extra[authenticatedUserKey])
				return
			}
			assert.Nil(t, info)
			assert.ErrorIs(t, err, mcpauth.ErrInvalidToken)
			assert.EqualError(t, err, "invalid bearer token")
		})
	}
}

func TestBearerHeaderBoundary(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
	verifier := func(context.Context, string, *http.Request) (*mcpauth.TokenInfo, error) {
		return &mcpauth.TokenInfo{Scopes: []string{"read:repository"}}, nil
	}
	endpoint := QueryCredentialBoundary(newAuthenticatedEndpoint(server, 1024, verifier))
	tests := []struct {
		name   string
		mutate func(*http.Request)
		want   int
	}{
		{name: "valid bearer", mutate: func(req *http.Request) { req.Header.Set("Authorization", "Bearer abc_DEF-123~+/") }, want: http.StatusOK},
		{name: "missing", mutate: func(*http.Request) {}, want: http.StatusUnauthorized},
		{name: "empty", mutate: func(req *http.Request) { req.Header.Set("Authorization", "Bearer ") }, want: http.StatusUnauthorized},
		{name: "Basic", mutate: func(req *http.Request) { req.SetBasicAuth("user", "secret") }, want: http.StatusUnauthorized},
		{name: "HTTP signature", mutate: func(req *http.Request) { req.Header.Set("Authorization", `Signature keyId="secret"`) }, want: http.StatusUnauthorized},
		{name: "malformed spacing", mutate: func(req *http.Request) { req.Header.Set("Authorization", "Bearer  secret") }, want: http.StatusUnauthorized},
		{name: "malformed token", mutate: func(req *http.Request) { req.Header.Set("Authorization", "Bearer sec,ret") }, want: http.StatusUnauthorized},
		{name: "multiple headers", mutate: func(req *http.Request) {
			req.Header.Add("Authorization", "Bearer one")
			req.Header.Add("Authorization", "Bearer two")
		}, want: http.StatusUnauthorized},
		{name: "conflicting headers", mutate: func(req *http.Request) {
			req.Header.Add("Authorization", "Bearer one")
			req.Header.Add("Authorization", "Basic two")
		}, want: http.StatusUnauthorized},
		{name: "query token", mutate: func(req *http.Request) {
			req.URL.RawQuery = "token=secret"
			req.Header.Set("Authorization", "Bearer valid")
		}, want: http.StatusUnauthorized},
		{name: "query access token", mutate: func(req *http.Request) {
			req.URL.RawQuery = "access_token=secret"
			req.Header.Set("Authorization", "Bearer valid")
		}, want: http.StatusUnauthorized},
		{name: "cookie only", mutate: func(req *http.Request) { req.AddCookie(&http.Cookie{Name: "token", Value: "secret"}) }, want: http.StatusUnauthorized},
		{name: "form token only", mutate: func(req *http.Request) {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.Body = io.NopCloser(strings.NewReader("access_token=secret"))
		}, want: http.StatusUnauthorized},
		{name: "reverse proxy identity only", mutate: func(req *http.Request) { req.Header.Set("X-Webauth-User", "user") }, want: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			req := newDiscoverRequest(t.Context(), t, testDiscoverBody)
			test.mutate(req)
			resp := httptest.NewRecorder()
			endpoint.ServeHTTP(resp, req)
			assert.Equal(t, test.want, resp.Code)
			assert.NotContains(t, resp.Body.String(), "secret")
		})
	}
}

func TestAuthenticationFailuresAreNeutral(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
	verifier := func(context.Context, string, *http.Request) (*mcpauth.TokenInfo, error) {
		return nil, errInvalidBearerToken
	}
	endpoint := QueryCredentialBoundary(newAuthenticatedEndpoint(server, 1024, verifier))
	for _, authorization := range []string{"", "Basic secret", "Bearer unknown"} {
		req := newDiscoverRequest(t.Context(), t, testDiscoverBody)
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
		resp := httptest.NewRecorder()
		endpoint.ServeHTTP(resp, req)
		assert.Equal(t, http.StatusUnauthorized, resp.Code)
		assert.Equal(t, "invalid bearer token\n", resp.Body.String())
	}
}

func TestQueryCredentialsAreRemovedBeforeAccessLogging(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
	verifier := func(context.Context, string, *http.Request) (*mcpauth.TokenInfo, error) {
		t.Fatal("query credentials must be rejected before token verification")
		return nil, errInvalidBearerToken
	}
	endpoint := QueryCredentialBoundary(newAuthenticatedEndpoint(server, 1024, verifier))
	req := newDiscoverRequest(t.Context(), t, testDiscoverBody)
	req.Header.Set("Authorization", "Bearer valid")
	req.URL.RawQuery = "keep=visible&access_token=TOP-SECRET&token=SECOND-SECRET"
	req.RequestURI = req.URL.RequestURI()
	var accessLogURI string
	logged := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		endpoint.ServeHTTP(w, req)
		accessLogURI = req.URL.RequestURI()
	})
	resp := httptest.NewRecorder()

	logged.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
	assert.Equal(t, "keep=visible", req.URL.RawQuery)
	assert.Equal(t, "/mcp?keep=visible", req.RequestURI)
	assert.Equal(t, "/mcp?keep=visible", accessLogURI)
	assert.NotContains(t, resp.Body.String(), "SECRET")
}

func TestQueryCredentialBoundaryScope(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppSubURL, "/forge")()
	tests := []struct {
		name         string
		path         string
		wantQuery    string
		wantRemoved  bool
		wantURIQuery string
	}{
		{name: "root MCP route", path: "/mcp", wantQuery: "keep=visible", wantRemoved: true, wantURIQuery: "/mcp?keep=visible"},
		{name: "configured subpath", path: "/forge/mcp", wantQuery: "keep=visible", wantRemoved: true, wantURIQuery: "/forge/mcp?keep=visible"},
		{name: "non-MCP route", path: "/api/v1/version", wantQuery: "keep=visible&access_token=TOP-SECRET", wantURIQuery: "/api/v1/version?keep=visible&access_token=TOP-SECRET"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var gotQuery, gotRequestURI string
			var gotRemoved bool
			boundary := QueryCredentialBoundary(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				gotQuery = req.URL.RawQuery
				gotRequestURI = req.RequestURI
				gotRemoved = queryCredentialWasRemoved(req.Context())
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodGet, test.path+"?keep=visible&access_token=TOP-SECRET", nil)
			resp := httptest.NewRecorder()

			boundary.ServeHTTP(resp, req)

			assert.Equal(t, http.StatusNoContent, resp.Code)
			assert.Equal(t, test.wantQuery, gotQuery)
			assert.Equal(t, test.wantURIQuery, gotRequestURI)
			assert.Equal(t, test.wantRemoved, gotRemoved)
		})
	}
}

func TestPATMissingExpirationIsLimitedToAuthenticatedEndpoint(t *testing.T) {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "forge", Version: "test"}, nil)
	verifier := func(context.Context, string, *http.Request) (*mcpauth.TokenInfo, error) {
		return &mcpauth.TokenInfo{Scopes: []string{"read:repository"}}, nil
	}
	req := newDiscoverRequest(t.Context(), t, testDiscoverBody)
	req.Header.Set("Authorization", "Bearer valid")
	resp := httptest.NewRecorder()

	newAuthenticatedEndpoint(server, 1024, verifier).ServeHTTP(resp, req)

	assert.Equal(t, http.StatusOK, resp.Code)
	assert.Contains(t, resp.Body.String(), `"name":"forge"`)
}
