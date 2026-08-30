// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/modules/timeutil"
	"gitea.dev/routers"
	"gitea.dev/services/oauth2_provider"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newFinalizedMCPRegistration(t *testing.T, userID int64, scope, redirectURI string) (*auth_model.OAuth2Application, *auth_model.OAuth2Grant) {
	t.Helper()
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "MCP integration harness", "", []string{redirectURI}, auth_model.MCPRedirectClassLoopback, time.Now().Add(time.Minute), 1000)
	require.NoError(t, err)
	app, grant, _, err := auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, userID, scope, "", redirectURI, "mcp-integration-challenge", "S256", setting.MCPResource(), time.Now())
	require.NoError(t, err)
	return app, grant
}

func TestMCPClientRegistrationConsentLifecycle(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test.MockVariableValue(&setting.OAuth2.Enabled, true)()
	defer test.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "https://forge.example")()
	require.NoError(t, auth_model.Init(t.Context()))
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()
	login := loginUser(t, "user2")
	const callback = "http://127.0.0.1:49152/callback"
	const state = "mcp-consent-lifecycle"
	newProvisional := func(t *testing.T) *auth_model.OAuth2Application {
		t.Helper()
		app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Consent harness", "", []string{callback}, auth_model.MCPRedirectClassLoopback, time.Now().Add(30*time.Minute), 1000)
		require.NoError(t, err)
		return app
	}
	authorizationPath := func(app *auth_model.OAuth2Application) string {
		query := url.Values{
			"client_id": {app.ClientID}, "redirect_uri": {callback}, "response_type": {"code"},
			"state": {state}, "scope": {oauth2_provider.MCPReadScope}, "resource": {setting.MCPResource()},
			"code_challenge_method": {"S256"}, "code_challenge": {"mcp-consent-challenge"},
		}
		return "/login/oauth/authorize?" + query.Encode()
	}
	consentValues := func(app *auth_model.OAuth2Application, granted string) map[string]string {
		return map[string]string{
			"client_id": app.ClientID, "redirect_uri": callback, "state": state,
			"scope": oauth2_provider.MCPReadScope, "resource": setting.MCPResource(), "granted": granted,
		}
	}
	admissionCount := func(t *testing.T) int {
		t.Helper()
		return unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2MCPRegistrationAdmission{ID: 1}).Outstanding
	}
	expire := func(t *testing.T, app *auth_model.OAuth2Application) {
		t.Helper()
		_, err := db.GetEngine(t.Context()).ID(app.ID).Cols("mcp_expires_unix").Update(&auth_model.OAuth2Application{
			MCPExpiresUnix: timeutil.TimeStamp(time.Now().Add(-time.Minute).Unix()),
		})
		require.NoError(t, err)
	}
	assertRemovedWithoutAuthority := func(t *testing.T, app *auth_model.OAuth2Application, outstanding, codes int) {
		t.Helper()
		unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: app.ID})
		unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ApplicationID: app.ID})
		assert.Equal(t, outstanding, admissionCount(t))
		assert.Equal(t, codes, unittest.GetCount(t, new(auth_model.OAuth2AuthorizationCode)))
	}

	t.Run("denied provisional releases admission", func(t *testing.T) {
		outstanding, codes := admissionCount(t), unittest.GetCount(t, new(auth_model.OAuth2AuthorizationCode))
		app := newProvisional(t)
		login.MakeRequest(t, NewRequest(t, http.MethodGet, authorizationPath(app)), http.StatusOK)
		response := login.MakeRequest(t, NewRequestWithValues(t, http.MethodPost, "/login/oauth/grant", consentValues(app, "false")), http.StatusSeeOther)
		location, err := response.Result().Location()
		require.NoError(t, err)
		assert.Equal(t, "access_denied", location.Query().Get("error"))
		assertRemovedWithoutAuthority(t, app, outstanding, codes)
	})

	t.Run("denied finalized reconnect preserves authority", func(t *testing.T) {
		app, grant := newFinalizedMCPRegistration(t, 2, oauth2_provider.MCPReadScope, callback)
		outstanding, codes := admissionCount(t), unittest.GetCount(t, new(auth_model.OAuth2AuthorizationCode))
		login.MakeRequest(t, NewRequest(t, http.MethodGet, authorizationPath(app)), http.StatusOK)
		login.MakeRequest(t, NewRequestWithValues(t, http.MethodPost, "/login/oauth/grant", consentValues(app, "false")), http.StatusSeeOther)
		unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: app.ID, MCPRegistrationState: auth_model.MCPRegistrationStateFinalized, MCPBoundUserID: 2})
		unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: grant.ID})
		assert.Equal(t, outstanding, admissionCount(t))
		assert.Equal(t, codes, unittest.GetCount(t, new(auth_model.OAuth2AuthorizationCode)))
	})

	for _, duringConsent := range []bool{false, true} {
		name := "expired before authorization"
		if duringConsent {
			name = "expired during consent"
		}
		t.Run(name, func(t *testing.T) {
			outstanding, codes := admissionCount(t), unittest.GetCount(t, new(auth_model.OAuth2AuthorizationCode))
			app := newProvisional(t)
			if duringConsent {
				login.MakeRequest(t, NewRequest(t, http.MethodGet, authorizationPath(app)), http.StatusOK)
			}
			expire(t, app)
			request := NewRequest(t, http.MethodGet, authorizationPath(app))
			if duringConsent {
				request = NewRequestWithValues(t, http.MethodPost, "/login/oauth/grant", consentValues(app, "true"))
			}
			response := login.MakeRequest(t, request, http.StatusBadRequest)
			assert.Contains(t, response.Body.String(), "Client registration expired; bootstrap again")
			assert.Empty(t, response.Header().Get("Location"))
			assertRemovedWithoutAuthority(t, app, outstanding, codes)
		})
	}
}
