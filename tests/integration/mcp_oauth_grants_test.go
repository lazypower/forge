// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/routers"
	"gitea.dev/services/oauth2_provider"
	"gitea.dev/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

const (
	mcpGrantCallback = "http://127.0.0.1:49152/callback"
	mcpGrantVerifier = "mcp-grant-verifier-012345678901234567890123456789012345678901234567"
)

func prepareMCPGrantBrowser(t *testing.T) *TestSession {
	t.Helper()
	t.Cleanup(test.MockVariableValue(&setting.AppURL, "https://forge.example/"))
	t.Cleanup(test.MockVariableValue(&setting.MCP.Enabled, true))
	t.Cleanup(test.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth))
	t.Cleanup(test.MockVariableValue(&setting.MCP.WorkMutationEnabled, true))
	t.Cleanup(test.MockVariableValue(&setting.OAuth2.Enabled, true))
	t.Cleanup(test.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "https://forge.example"))
	require.NoError(t, auth_model.Init(t.Context()))
	t.Cleanup(test.MockVariableValue(&testWebRoutes, routers.NormalRoutes()))
	return loginUser(t, "user2")
}

func newMCPGrantRegistration(t *testing.T, label, installation string) *auth_model.OAuth2Application {
	t.Helper()
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), label, installation, []string{mcpGrantCallback}, auth_model.MCPRedirectClassLoopback, time.Now().Add(30*time.Minute), 1000)
	require.NoError(t, err)
	return app
}

func bootstrapMCPGrantRegistration(t *testing.T, label, installation string) *auth_model.OAuth2Application {
	t.Helper()
	response := MakeRequest(t, NewRequestWithJSON(t, http.MethodPost, "/login/oauth/register", oauth2_provider.MCPClientRegistrationRequest{
		ClientName: label, InstallationName: installation, RedirectURIs: []string{mcpGrantCallback},
		TokenEndpointAuthMethod: "none", ApplicationType: "native",
	}), http.StatusCreated)
	registration := DecodeJSON(t, response, &oauth2_provider.MCPClientRegistrationResponse{})
	require.NotEmpty(t, registration.ClientID)
	assert.NotContains(t, response.Body.String(), "client_secret")
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ClientID: registration.ClientID})
	assert.Equal(t, auth_model.MCPRegistrationStateProvisional, app.MCPRegistrationState)
	assert.Zero(t, app.MCPBoundUserID)
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ApplicationID: app.ID})
	return app
}

func consentMCPGrant(t *testing.T, login *TestSession, app *auth_model.OAuth2Application, scope string, approve bool) (string, *goquery.Document) {
	t.Helper()
	query := url.Values{
		"client_id": {app.ClientID}, "redirect_uri": {mcpGrantCallback}, "response_type": {"code"},
		"state": {"mcp-grant-consent"}, "scope": {scope}, "resource": {setting.MCPResource()},
		"code_challenge_method": {"S256"}, "code_challenge": {oauth2.S256ChallengeFromVerifier(mcpGrantVerifier)},
	}
	response := login.MakeRequest(t, NewRequest(t, http.MethodGet, "/login/oauth/authorize?"+query.Encode()), http.StatusOK)
	document, err := goquery.NewDocumentFromReader(response.Body)
	require.NoError(t, err)
	canonicalScope, exists := document.Find(`input[name="scope"]`).Attr("value")
	require.True(t, exists)
	response = login.MakeRequest(t, NewRequestWithValues(t, http.MethodPost, "/login/oauth/grant", map[string]string{
		"client_id": app.ClientID, "redirect_uri": mcpGrantCallback, "state": query.Get("state"),
		"scope": canonicalScope, "resource": setting.MCPResource(), "granted": strconv.FormatBool(approve),
	}), http.StatusSeeOther)
	location, err := response.Result().Location()
	require.NoError(t, err)
	assert.Equal(t, query.Get("state"), location.Query().Get("state"))
	if !approve {
		assert.Equal(t, "access_denied", location.Query().Get("error"))
		assert.Empty(t, location.Query().Get("code"))
		return "", document
	}
	code := location.Query().Get("code")
	require.NotEmpty(t, code)
	return code, document
}

func exchangeMCPGrantCode(t *testing.T, app *auth_model.OAuth2Application, code string, status int) *oauth2_provider.AccessTokenResponse {
	t.Helper()
	response := MakeRequest(t, NewRequestWithValues(t, http.MethodPost, "/login/oauth/access_token", map[string]string{
		"grant_type": "authorization_code", "client_id": app.ClientID, "redirect_uri": mcpGrantCallback,
		"code": code, "code_verifier": mcpGrantVerifier, "resource": setting.MCPResource(),
	}), status)
	if status != http.StatusOK {
		assert.NotContains(t, response.Body.String(), code)
		return nil
	}
	tokens := DecodeJSON(t, response, &oauth2_provider.AccessTokenResponse{})
	require.NotEmpty(t, tokens.AccessToken)
	require.NotEmpty(t, tokens.RefreshToken)
	return tokens
}

func refreshMCPGrant(t *testing.T, app *auth_model.OAuth2Application, token string, status int) *oauth2_provider.AccessTokenResponse {
	t.Helper()
	response := MakeRequest(t, NewRequestWithValues(t, http.MethodPost, "/login/oauth/access_token", map[string]string{
		"grant_type": "refresh_token", "client_id": app.ClientID, "refresh_token": token, "resource": setting.MCPResource(),
	}), status)
	if status != http.StatusOK {
		assert.NotContains(t, response.Body.String(), token)
		return nil
	}
	tokens := DecodeJSON(t, response, &oauth2_provider.AccessTokenResponse{})
	require.NotEmpty(t, tokens.AccessToken)
	require.NotEmpty(t, tokens.RefreshToken)
	assert.NotEqual(t, token, tokens.RefreshToken)
	return tokens
}

func assertMCPGrantLineageRejected(t *testing.T, app *auth_model.OAuth2Application, codes []string, tokens []*oauth2_provider.AccessTokenResponse) {
	t.Helper()
	for _, code := range codes {
		exchangeMCPGrantCode(t, app, code, http.StatusBadRequest)
	}
	for _, pair := range tokens {
		response := MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(pair.AccessToken), http.StatusUnauthorized)
		assert.Contains(t, response.Header().Get("WWW-Authenticate"), `error="invalid_token"`)
		assert.NotContains(t, response.Body.String(), pair.AccessToken)
		refreshMCPGrant(t, app, pair.RefreshToken, http.StatusBadRequest)
	}
}

func TestMCPGrantProfileReplacement(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	login := prepareMCPGrantBrowser(t)
	app := newMCPGrantRegistration(t, "Profile transition harness", "installation")
	control := newMCPGrantRegistration(t, "Profile transition harness", "installation")
	controlCode, _ := consentMCPGrant(t, login, control, oauth2_provider.MCPReadScope, true)
	controlTokens := exchangeMCPGrantCode(t, control, controlCode, http.StatusOK)
	var oldCodes []string
	var oldTokens []*oauth2_provider.AccessTokenResponse
	var previousGrantID int64
	for _, scope := range []string{oauth2_provider.MCPReadScope, oauth2_provider.MCPWorkWriteScope, oauth2_provider.MCPReadScope} {
		code, _ := consentMCPGrant(t, login, app, scope, true)
		grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ApplicationID: app.ID, UserID: 2})
		assert.Equal(t, scope, grant.Scope)
		assert.NotEqual(t, previousGrantID, grant.ID)
		assert.Zero(t, grant.CredentialRotatedUnix)
		if previousGrantID != 0 {
			unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ID: previousGrantID})
			assertMCPGrantLineageRejected(t, app, oldCodes, oldTokens)
		}
		tokens := exchangeMCPGrantCode(t, app, code, http.StatusOK)
		MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(tokens.AccessToken), http.StatusOK)
		rotated := refreshMCPGrant(t, app, tokens.RefreshToken, http.StatusOK)
		oldTokens = append(oldTokens, tokens, rotated)
		oldCodes = append(oldCodes, code)

		// Same-profile consent preserves the grant and leaves another unspent code to invalidate.
		pendingCode, _ := consentMCPGrant(t, login, app, scope, true)
		oldCodes = append(oldCodes, pendingCode)
		beforeDenial := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: grant.ID})
		assert.Positive(t, beforeDenial.CredentialRotatedUnix)
		otherScope := oauth2_provider.MCPReadScope
		if scope == oauth2_provider.MCPReadScope {
			otherScope = oauth2_provider.MCPWorkWriteScope
		}
		consentMCPGrant(t, login, app, otherScope, false)
		afterDenial := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: grant.ID})
		assert.Equal(t, beforeDenial, afterDenial)
		unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2AuthorizationCode{Code: pendingCode, GrantID: grant.ID})
		MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(rotated.AccessToken), http.StatusOK)
		oldTokens = append(oldTokens, refreshMCPGrant(t, app, rotated.RefreshToken, http.StatusOK))
		previousGrantID = grant.ID
		MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(controlTokens.AccessToken), http.StatusOK)
		controlTokens = refreshMCPGrant(t, control, controlTokens.RefreshToken, http.StatusOK)
	}
}

func TestMCPGrantInstallationIsolation(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.MCP.ClientBootstrapEnabled, true)()
	login := prepareMCPGrantBrowser(t)
	first := bootstrapMCPGrantRegistration(t, "Same harness", "Same installation label")
	second := bootstrapMCPGrantRegistration(t, "Same harness", "Same installation label")
	require.NotEqual(t, first.ID, second.ID)
	require.NotEqual(t, first.ClientID, second.ClientID)
	firstCode, _ := consentMCPGrant(t, login, first, oauth2_provider.MCPReadScope, true)
	secondCode, _ := consentMCPGrant(t, login, second, oauth2_provider.MCPReadScope, true)
	firstTokens := exchangeMCPGrantCode(t, first, firstCode, http.StatusOK)
	secondTokens := exchangeMCPGrantCode(t, second, secondCode, http.StatusOK)
	assert.NotEqual(t, firstTokens.RefreshToken, secondTokens.RefreshToken)
	firstGrant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ApplicationID: first.ID, UserID: 2})
	secondGrant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ApplicationID: second.ID, UserID: 2})
	require.NotEqual(t, firstGrant.ID, secondGrant.ID)

	rotated := refreshMCPGrant(t, first, firstTokens.RefreshToken, http.StatusOK)
	assert.Equal(t, secondGrant, unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: secondGrant.ID}))
	MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(secondTokens.AccessToken), http.StatusOK)
	pendingCode, _ := consentMCPGrant(t, login, first, oauth2_provider.MCPReadScope, true)
	login.MakeRequest(t, NewRequest(t, http.MethodPost, fmt.Sprintf("/user/settings/applications/oauth2/%d/revoke/%d", first.ID, firstGrant.ID)), http.StatusOK)
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ID: firstGrant.ID})
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: first.ID, MCPRegistrationState: auth_model.MCPRegistrationStateFinalized})
	assertMCPGrantLineageRejected(t, first, []string{firstCode, pendingCode}, []*oauth2_provider.AccessTokenResponse{firstTokens, rotated})
	secondRotated := refreshMCPGrant(t, second, secondTokens.RefreshToken, http.StatusOK)

	reconnectedCode, _ := consentMCPGrant(t, login, first, oauth2_provider.MCPReadScope, true)
	reconnected := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ApplicationID: first.ID, UserID: 2})
	assert.NotEqual(t, firstGrant.ID, reconnected.ID)
	reconnectedTokens := exchangeMCPGrantCode(t, first, reconnectedCode, http.StatusOK)
	MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(reconnectedTokens.AccessToken), http.StatusOK)
	assertMCPGrantLineageRejected(t, first, []string{firstCode, pendingCode}, []*oauth2_provider.AccessTokenResponse{firstTokens, rotated})
	refreshMCPGrant(t, second, secondRotated.RefreshToken, http.StatusOK)
}

func TestMCPGrantAuthorityInspection(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	login := prepareMCPGrantBrowser(t)
	const clientLabel = `<img src=x onerror="alert('client')">`
	const installationLabel = `<script>alert('installation')</script>`
	app := newMCPGrantRegistration(t, clientLabel, installationLabel)
	assertEscaped := func(document *goquery.Document) {
		t.Helper()
		assert.Zero(t, document.Find(`img[src="x"], script:contains("installation")`).Length())
		visible := strings.Join(strings.Fields(document.Find("main, [role=main]").Text()), " ")
		assert.Contains(t, visible, clientLabel)
		assert.Contains(t, visible, installationLabel)
		assert.NotContains(t, visible, app.ClientID)
	}
	code, consent := consentMCPGrant(t, login, app, oauth2_provider.MCPWorkWriteScope, true)
	assertEscaped(consent)
	consentText := strings.Join(strings.Fields(consent.Text()), " ")
	for _, expected := range []string{"Work Planning", oauth2_provider.MCPWorkWriteScope, "client-provided", "not verified", "Local application (loopback)", "cannot push or merge code, administer repositories, or run agents"} {
		assert.Contains(t, consentText, expected)
	}
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ApplicationID: app.ID, UserID: 2})
	settingsDocument := func() *goquery.Document {
		t.Helper()
		response := login.MakeRequest(t, NewRequest(t, http.MethodGet, "/user/settings/applications"), http.StatusOK)
		document, err := goquery.NewDocumentFromReader(response.Body)
		require.NoError(t, err)
		assertEscaped(document)
		return document
	}
	grantRow := func(document *goquery.Document) *goquery.Selection {
		t.Helper()
		row := document.Find(fmt.Sprintf(`button[data-url="/user/settings/applications/oauth2/%d/revoke/%d"]`, app.ID, grant.ID)).Closest(".item")
		require.Equal(t, 1, row.Length())
		return row
	}
	row := grantRow(settingsDocument())
	rowText := strings.Join(strings.Fields(row.Text()), " ")
	for _, expected := range []string{"Work Planning", oauth2_provider.MCPWorkWriteScope, "Active", "Authorized on", "Credentials last issued or rotated", "Not yet issued", "Client-provided, not verified", "PKCE", "loopback", "Revoke"} {
		assert.Contains(t, rowText, expected)
	}
	for _, unexpected := range []string{"Last used", "Last use", "Client ID", "Grant ID", "Token ID", "Model"} {
		assert.NotContains(t, rowText, unexpected)
	}
	tokens := exchangeMCPGrantCode(t, app, code, http.StatusOK)
	row = grantRow(settingsDocument())
	assert.NotContains(t, row.Text(), "Not yet issued")
	assert.NotContains(t, row.Text(), tokens.AccessToken)
	assert.NotContains(t, row.Text(), tokens.RefreshToken)
	assert.NotContains(t, row.Text(), code)

	setting.MCP.WorkMutationEnabled = false
	disabled := grantRow(settingsDocument())
	assert.Contains(t, disabled.Text(), "Work Planning")
	assert.Contains(t, disabled.Text(), "Grant retained; MCP profile currently disabled")
	setting.MCP.WorkMutationEnabled = true

	deletePath := fmt.Sprintf("/user/settings/applications/mcp/%d/delete", app.ID)
	login.MakeRequest(t, NewRequest(t, http.MethodPost, deletePath), http.StatusOK)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: app.ID})
	login.MakeRequest(t, NewRequest(t, http.MethodPost, fmt.Sprintf("/user/settings/applications/oauth2/%d/revoke/%d", app.ID, grant.ID)), http.StatusOK)
	inert := settingsDocument()
	assert.Equal(t, 1, inert.Find(fmt.Sprintf(`button[data-url="%s"]`, deletePath)).Length())
	assert.Zero(t, inert.Find(fmt.Sprintf(`button[data-url="/user/settings/applications/oauth2/%d/revoke/%d"]`, app.ID, grant.ID)).Length())
	otherPrincipal := loginUser(t, "user5")
	otherPrincipal.MakeRequest(t, NewRequest(t, http.MethodPost, deletePath), http.StatusOK)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: app.ID})
	login.MakeRequest(t, NewRequest(t, http.MethodPost, deletePath), http.StatusOK)
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: app.ID})
	assertMCPGrantLineageRejected(t, app, []string{code}, []*oauth2_provider.AccessTokenResponse{tokens})
}
