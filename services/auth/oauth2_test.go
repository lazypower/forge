// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/reqctx"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"
	"gitea.dev/services/actions"
	"gitea.dev/services/oauth2_provider"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUserIDFromToken(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	t.Run("Actions JWT", func(t *testing.T) {
		const RunningTaskID int64 = 47
		token, err := actions.CreateAuthorizationToken(RunningTaskID, 1, 2)
		assert.NoError(t, err)

		ds := make(reqctx.ContextData)

		o := OAuth2{}
		u, err := o.userFromToken(t.Context(), token, ds)
		require.NoError(t, err)
		assert.Equal(t, user_model.ActionsUserID, u.ID)
		taskID, ok := user_model.GetActionsUserTaskID(u)
		assert.True(t, ok)
		assert.Equal(t, RunningTaskID, taskID)
	})
}

func TestCheckTaskIsRunning(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	cases := map[string]struct {
		TaskID   int64
		Expected bool
	}{
		"Running":   {TaskID: 47, Expected: true},
		"Missing":   {TaskID: 1, Expected: false},
		"Cancelled": {TaskID: 46, Expected: false},
	}

	for name := range cases {
		c := cases[name]
		t.Run(name, func(t *testing.T) {
			actual := CheckTaskIsRunning(t.Context(), c.TaskID)
			assert.Equal(t, c.Expected, actual)
		})
	}
}

func TestOAuthAccessTokenResourceIsolationAcrossSharedConsumers(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test_module.MockVariableValue(&setting.OAuth2.Enabled, true)()
	defer test_module.MockVariableValue(&setting.DisableQueryAuthToken, false)()

	signingKey, err := oauth2_provider.CreateJWTSigningKey("HS256", []byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	defer test_module.MockVariableValue(&oauth2_provider.DefaultSigningKey, signingKey)()

	legacyToken := signAuthTestToken(t, signingKey, "")
	resourceToken := signAuthTestToken(t, signingKey, "https://forge.example/mcp")

	type verifyCredential func(*testing.T, string) (*user_model.User, reqctx.ContextData, error)
	tests := []struct {
		name   string
		verify verifyCredential
	}{
		{
			name: "Bearer header",
			verify: func(t *testing.T, token string) (*user_model.User, reqctx.ContextData, error) {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/user", nil)
				req.Header.Set("Authorization", "Bearer "+token)
				data := make(reqctx.ContextData)
				user, err := (&OAuth2{}).Verify(req, httptest.NewRecorder(), data, nil)
				return user, data, err
			},
		},
		{
			name: "legacy token header scheme",
			verify: func(t *testing.T, token string) (*user_model.User, reqctx.ContextData, error) {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/user", nil)
				req.Header.Set("Authorization", "token "+token)
				data := make(reqctx.ContextData)
				user, err := (&OAuth2{}).Verify(req, httptest.NewRecorder(), data, nil)
				return user, data, err
			},
		},
		{
			name: "query token",
			verify: func(t *testing.T, token string) (*user_model.User, reqctx.ContextData, error) {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/user?token="+token, nil)
				data := make(reqctx.ContextData)
				user, err := (&OAuth2{}).Verify(req, httptest.NewRecorder(), data, nil)
				return user, data, err
			},
		},
		{
			name: "query access token",
			verify: func(t *testing.T, token string) (*user_model.User, reqctx.ContextData, error) {
				req := httptest.NewRequest(http.MethodGet, "/api/v1/user?access_token="+token, nil)
				data := make(reqctx.ContextData)
				user, err := (&OAuth2{}).Verify(req, httptest.NewRecorder(), data, nil)
				return user, data, err
			},
		},
		{
			name: "Basic x-oauth-basic",
			verify: func(t *testing.T, token string) (*user_model.User, reqctx.ContextData, error) {
				req := httptest.NewRequest(http.MethodGet, "/v2/", nil)
				req.SetBasicAuth(token, "x-oauth-basic")
				data := make(reqctx.ContextData)
				user, err := (&Basic{}).Verify(req, httptest.NewRecorder(), data, nil)
				return user, data, err
			},
		},
		{
			name: "Basic password token",
			verify: func(t *testing.T, token string) (*user_model.User, reqctx.ContextData, error) {
				req := httptest.NewRequest(http.MethodGet, "/api/packages/user1", nil)
				req.SetBasicAuth("user1", token)
				data := make(reqctx.ContextData)
				user, err := (&Basic{}).Verify(req, httptest.NewRecorder(), data, nil)
				return user, data, err
			},
		},
		{
			name: "direct package API key verification",
			verify: func(t *testing.T, token string) (*user_model.User, reqctx.ContextData, error) {
				req := httptest.NewRequest(http.MethodPut, "/api/packages/user1/nuget", nil)
				data := make(reqctx.ContextData)
				user, err := (&Basic{}).VerifyAuthToken(req, httptest.NewRecorder(), data, nil, token)
				return user, data, err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			user, data, err := test.verify(t, legacyToken)
			require.NoError(t, err)
			require.NotNil(t, user)
			assert.Equal(t, int64(1), user.ID)
			assert.Equal(t, true, data["IsApiToken"])
			assert.Equal(t, auth_model.AccessTokenScopeAll, data["ApiTokenScope"])

			user, data, _ = test.verify(t, resourceToken)
			assert.Nil(t, user)
			assert.NotEqual(t, true, data["IsApiToken"])
		})
	}
}

func TestOAuthAccessTokenLegacyPrincipalGateAndPATCompatibility(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test_module.MockVariableValue(&setting.OAuth2.Enabled, true)()
	signingKey, err := oauth2_provider.CreateJWTSigningKey("HS256", []byte("01234567890123456789012345678901"))
	require.NoError(t, err)
	defer test_module.MockVariableValue(&oauth2_provider.DefaultSigningKey, signingKey)()

	inactiveLegacyToken := signAuthTestTokenForGrant(t, signingKey, 2, "")
	scope, userID := GetOAuthAccessTokenScopeAndUserID(t.Context(), inactiveLegacyToken)
	assert.Equal(t, auth_model.AccessTokenScopeAll, scope)
	assert.Equal(t, int64(3), userID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/user", nil)
	data := make(reqctx.ContextData)
	user, err := (&Basic{}).VerifyAuthToken(req, httptest.NewRecorder(), data, nil, "d2c6c1ba3890b309189a8e618c72a162e4efbf36")
	require.NoError(t, err)
	require.NotNil(t, user)
	assert.Equal(t, int64(1), user.ID)
	assert.Equal(t, auth_model.AccessTokenScope(""), data["ApiTokenScope"])
}

func signAuthTestToken(t *testing.T, signingKey oauth2_provider.JWTSigningKey, resource string) string {
	t.Helper()
	return signAuthTestTokenForGrant(t, signingKey, 1, resource)
}

func signAuthTestTokenForGrant(t *testing.T, signingKey oauth2_provider.JWTSigningKey, grantID int64, resource string) string {
	t.Helper()
	claims := jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}
	if resource != "" {
		claims.Issuer = oauth2_provider.TokenIssuer()
		claims.Subject = "1"
		if grantID == 2 {
			claims.Subject = "3"
		}
		claims.Audience = jwt.ClaimStrings{resource}
	}
	token, err := (&oauth2_provider.Token{
		GrantID:          grantID,
		Kind:             oauth2_provider.KindAccessToken,
		RegisteredClaims: claims,
	}).SignToken(signingKey)
	require.NoError(t, err)
	return token
}
