// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testMCPApplication() *auth_model.OAuth2Application {
	builtin := auth_model.BuiltinApplications()[auth_model.MCPBuiltinOAuth2ApplicationClientID]
	return &auth_model.OAuth2Application{
		ClientID:     auth_model.MCPBuiltinOAuth2ApplicationClientID,
		RedirectURIs: builtin.RedirectURIs,
	}
}

func TestValidateMCPAuthorizationRequest(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	app := testMCPApplication()
	resource := setting.MCPResource()

	require.NoError(t, ValidateMCPAuthorizationRequest(app, resource, "read:repository", "S256", "challenge", "http://127.0.0.1:49152"))
	require.NoError(t, ValidateMCPAuthorizationRequest(app, resource, "read:repository", "S256", "challenge", "http://127.0.0.1:49152/callback"))
	require.NoError(t, ValidateMCPAuthorizationRequest(app, resource, "read:repository", "S256", "challenge", "https://127.0.0.1"))

	tests := []struct {
		name, resource, scope, method, challenge, redirect string
		mutate                                             func(*auth_model.OAuth2Application)
	}{
		{name: "missing resource", scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "wrong resource", resource: "https://forge.example/other", scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "empty scope", resource: resource, method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "unknown scope", resource: resource, scope: "read:mcp", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "extra scope", resource: resource, scope: "read:repository read:user", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "plain PKCE", resource: resource, scope: "read:repository", method: "plain", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "missing challenge", resource: resource, scope: "read:repository", method: "S256", redirect: "http://127.0.0.1:49152"},
		{name: "unregistered callback path", resource: resource, scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152/other"},
		{name: "non-loopback HTTP", resource: resource, scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://192.0.2.1"},
		{name: "confidential client", resource: resource, scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152", mutate: func(app *auth_model.OAuth2Application) { app.ConfidentialClient = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := *app
			if test.mutate != nil {
				test.mutate(&candidate)
			}
			assert.ErrorIs(t, ValidateMCPAuthorizationRequest(&candidate, test.resource, test.scope, test.method, test.challenge, test.redirect), ErrInvalidMCPProfileRequest)
		})
	}

	setting.MCP.Authentication = setting.MCPAuthenticationProfilePAT
	assert.ErrorIs(t, ValidateMCPAuthorizationRequest(app, resource, "read:repository", "S256", "challenge", "http://127.0.0.1:49152"), ErrInvalidMCPProfileRequest)
}

func TestMCPProfileRejectsResourceForOtherClients(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	app := &auth_model.OAuth2Application{ClientID: "other"}
	require.NoError(t, ValidateMCPAuthorizationRequest(app, "", "", "", "", "https://client.example/callback"))
	assert.ErrorIs(t, ValidateMCPAuthorizationRequest(app, setting.MCPResource(), "read:repository", "S256", "challenge", "https://client.example/callback"), ErrInvalidMCPProfileRequest)
}

func TestValidateMCPAuthorizationCodeExchange(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	app := testMCPApplication()
	grant := &auth_model.OAuth2Grant{Scope: "read:repository"}
	resource := setting.MCPResource()
	require.NoError(t, ValidateMCPAuthorizationCodeExchange(app, grant, resource, resource))
	assert.ErrorIs(t, ValidateMCPAuthorizationCodeExchange(app, grant, "", resource), ErrInvalidMCPProfileRequest)
	assert.ErrorIs(t, ValidateMCPAuthorizationCodeExchange(app, grant, resource, ""), ErrInvalidMCPProfileRequest)
	grant.Scope = ""
	assert.ErrorIs(t, ValidateMCPAuthorizationCodeExchange(app, grant, resource, resource), ErrInvalidMCPProfileRequest)
}

func TestValidateMCPRefresh(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "https://forge.example")()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	app := testMCPApplication()
	grant := &auth_model.OAuth2Grant{UserID: 42}
	resource := setting.MCPResource()
	token := &Token{
		Kind:    KindRefreshToken,
		Counter: 2,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:   TokenIssuer(),
			Subject:  "42",
			Audience: jwt.ClaimStrings{resource},
		},
	}

	got, err := ValidateMCPRefresh(app, grant, token, "")
	require.NoError(t, err)
	assert.Equal(t, resource, got)
	got, err = ValidateMCPRefresh(app, grant, token, resource)
	require.NoError(t, err)
	assert.Equal(t, resource, got)
	_, err = ValidateMCPRefresh(app, grant, token, "https://forge.example/other")
	assert.ErrorIs(t, err, ErrInvalidMCPProfileRequest)

	token.Audience = nil
	_, err = ValidateMCPRefresh(app, grant, token, "")
	assert.ErrorIs(t, err, ErrInvalidMCPProfileRequest)
}
