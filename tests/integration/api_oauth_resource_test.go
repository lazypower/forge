// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"
	"gitea.dev/services/oauth2_provider"
	"gitea.dev/tests"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/require"
)

func TestOAuthAccessTokenResourceIsolation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test_module.MockVariableValue(&setting.DisableQueryAuthToken, false)()

	legacyToken := signIntegrationOAuthAccessToken(t, "")
	resourceToken := signIntegrationOAuthAccessToken(t, "https://forge.example/mcp")

	tests := []struct {
		name         string
		request      func(string) *RequestWrapper
		legacyStatus int
	}{
		{
			name: "API Bearer",
			request: func(token string) *RequestWrapper {
				return NewRequest(t, http.MethodGet, "/api/v1/user").AddTokenAuth(token)
			},
			legacyStatus: http.StatusOK,
		},
		{
			name: "API legacy token scheme",
			request: func(token string) *RequestWrapper {
				return NewRequest(t, http.MethodGet, "/api/v1/user").SetHeader("Authorization", "token "+token)
			},
			legacyStatus: http.StatusOK,
		},
		{
			name: "API query compatibility",
			request: func(token string) *RequestWrapper {
				return NewRequest(t, http.MethodGet, "/api/v1/user?access_token="+url.QueryEscape(token))
			},
			legacyStatus: http.StatusOK,
		},
		{
			name: "API Basic x-oauth-basic",
			request: func(token string) *RequestWrapper {
				return NewRequest(t, http.MethodGet, "/api/v1/user").AddBasicAuth(token, "x-oauth-basic")
			},
			legacyStatus: http.StatusOK,
		},
		{
			name: "OAuth userinfo",
			request: func(token string) *RequestWrapper {
				return NewRequest(t, http.MethodGet, "/login/oauth/userinfo").AddTokenAuth(token)
			},
			legacyStatus: http.StatusOK,
		},
		{
			name: "common package API",
			request: func(token string) *RequestWrapper {
				return NewRequest(t, http.MethodGet, "/api/packages/user1/generic/missing/1/missing").AddTokenAuth(token)
			},
			legacyStatus: http.StatusNotFound,
		},
		{
			name: "container token Basic password",
			request: func(token string) *RequestWrapper {
				return NewRequest(t, http.MethodGet, "/v2/token").AddBasicAuth("user1", token)
			},
			legacyStatus: http.StatusOK,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			MakeRequest(t, test.request(legacyToken), test.legacyStatus)
			MakeRequest(t, test.request(resourceToken), http.StatusUnauthorized)
		})
	}

	pat := getUserToken(t, "user1", auth_model.AccessTokenScopeReadUser)
	MakeRequest(t, NewRequest(t, http.MethodGet, "/api/v1/user").AddTokenAuth(pat), http.StatusOK)
}

func signIntegrationOAuthAccessToken(t *testing.T, resource string) string {
	t.Helper()
	claims := jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}
	if resource != "" {
		claims.Issuer = oauth2_provider.TokenIssuer()
		claims.Subject = "1"
		claims.Audience = jwt.ClaimStrings{resource}
	}
	token, err := (&oauth2_provider.Token{
		GrantID:          1,
		Kind:             oauth2_provider.KindAccessToken,
		RegisteredClaims: claims,
	}).SignToken(oauth2_provider.DefaultSigningKey)
	require.NoError(t, err)
	return token
}
