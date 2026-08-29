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
	return &auth_model.OAuth2Application{
		ID:                   9,
		ClientID:             "mcp_test_public_client",
		RedirectURIs:         []string{"http://127.0.0.1", "http://127.0.0.1/callback", "http://127.0.0.1/callback/kiPXe-El69xe"},
		MCPRegistrationState: auth_model.MCPRegistrationStateFinalized,
		MCPRedirectClass:     auth_model.MCPRedirectClassLoopback,
		MCPBoundUserID:       42,
	}
}

func TestValidateMCPAuthorizationRequest(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test_module.MockVariableValue(&setting.MCP.WorkMutationEnabled, false)()
	app := testMCPApplication()
	resource := setting.MCPResource()

	require.NoError(t, ValidateMCPAuthorizationRequest(app, resource, "read:repository", "S256", "challenge", "http://127.0.0.1:49152"))
	require.NoError(t, ValidateMCPAuthorizationRequest(app, resource, "read:repository", "S256", "challenge", "http://127.0.0.1:49152/callback"))
	require.NoError(t, ValidateMCPAuthorizationRequest(app, resource, "read:repository", "S256", "challenge", "http://127.0.0.1:49152/callback/kiPXe-El69xe"))

	tests := []struct {
		name, resource, scope, method, challenge, redirect string
		mutate                                             func(*auth_model.OAuth2Application)
	}{
		{name: "missing resource", scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "wrong resource", resource: "https://forge.example/other", scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "empty scope", resource: resource, method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "unknown scope", resource: resource, scope: "read:mcp", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "extra scope", resource: resource, scope: "read:repository read:user", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "duplicate scope", resource: resource, scope: "read:repository read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "plain PKCE", resource: resource, scope: "read:repository", method: "plain", challenge: "challenge", redirect: "http://127.0.0.1:49152"},
		{name: "missing challenge", resource: resource, scope: "read:repository", method: "S256", redirect: "http://127.0.0.1:49152"},
		{name: "unregistered callback path", resource: resource, scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152/other"},
		{name: "wrong callback ID", resource: resource, scope: "read:repository", method: "S256", challenge: "challenge", redirect: "http://127.0.0.1:49152/callback/not-this-forge"},
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

func TestCanonicalMCPWorkWriteScope(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test_module.MockVariableValue(&setting.MCP.WorkMutationEnabled, true)()
	app := testMCPApplication()

	for _, scope := range []string{
		"read:repository write:issue write:repository",
		"read:repository write:repository write:issue",
		"write:issue read:repository write:repository",
		"write:issue write:repository read:repository",
		"write:repository read:repository write:issue",
		"write:repository write:issue read:repository",
	} {
		t.Run(scope, func(t *testing.T) {
			canonical, err := CanonicalMCPAuthorizationScope(app, scope)
			require.NoError(t, err)
			assert.Equal(t, MCPWorkWriteScope, canonical)
			require.NoError(t, ValidateMCPAuthorizationRequest(app, setting.MCPResource(), scope, "S256", "challenge", "http://127.0.0.1:49152"))
		})
	}

	for _, scope := range []string{
		"",
		"read:repository write:issue",
		"read:repository write:repository",
		"write:issue write:repository",
		"read:repository read:repository write:issue write:repository",
		"read:repository write:issue write:user",
		"read:repository write:issue write:repository read:user",
		"read:repository,write:issue,write:repository",
		" read:repository write:issue write:repository",
		"read:repository write:issue write:repository ",
		"read:repository  write:issue write:repository",
		"read:repository\twrite:issue\twrite:repository",
	} {
		t.Run("reject "+scope, func(t *testing.T) {
			_, err := CanonicalMCPAuthorizationScope(app, scope)
			assert.ErrorIs(t, err, ErrInvalidMCPProfileRequest)
		})
	}
}

func TestValidateMCPWorkWriteExchangeAndRefresh(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "https://forge.example")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test_module.MockVariableValue(&setting.MCP.WorkMutationEnabled, true)()
	app := testMCPApplication()
	grant := &auth_model.OAuth2Grant{ApplicationID: app.ID, UserID: 42, Scope: MCPWorkWriteScope}
	resource := setting.MCPResource()
	token := &Token{Kind: KindRefreshToken, Counter: 2, RegisteredClaims: jwt.RegisteredClaims{
		Issuer: TokenIssuer(), Subject: "42", Audience: jwt.ClaimStrings{resource},
	}}

	require.NoError(t, ValidateMCPAuthorizationCodeExchange(app, grant, resource, resource))
	got, err := ValidateMCPRefresh(app, grant, token, resource)
	require.NoError(t, err)
	assert.Equal(t, resource, got)

	readApp := testMCPApplication()
	readApp.ID = app.ID + 1
	assert.ErrorIs(t, ValidateMCPAuthorizationCodeExchange(readApp, grant, resource, resource), ErrInvalidMCPProfileRequest)
	_, err = ValidateMCPRefresh(readApp, grant, token, resource)
	assert.ErrorIs(t, err, ErrInvalidMCPProfileRequest)

	for _, scope := range []string{
		"read:repository write:issue",
		"read:repository write:issue write:issue write:repository",
		"read:repository write:issue write:user",
		"read:repository write:issue write:repository read:user",
	} {
		grant.Scope = scope
		assert.ErrorIs(t, ValidateMCPAuthorizationCodeExchange(app, grant, resource, resource), ErrInvalidMCPProfileRequest)
		_, err = ValidateMCPRefresh(app, grant, token, resource)
		assert.ErrorIs(t, err, ErrInvalidMCPProfileRequest)
	}
}

func TestMCPWorkWriteProfileRequiresMutationEnablement(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test_module.MockVariableValue(&setting.MCP.WorkMutationEnabled, false)()
	app := testMCPApplication()
	grant := &auth_model.OAuth2Grant{ApplicationID: app.ID, UserID: 42, Scope: MCPWorkWriteScope}
	resource := setting.MCPResource()
	token := &Token{Kind: KindRefreshToken, Counter: 1, RegisteredClaims: jwt.RegisteredClaims{
		Issuer: TokenIssuer(), Subject: "42", Audience: jwt.ClaimStrings{resource},
	}}

	_, err := CanonicalMCPAuthorizationScope(app, MCPWorkWriteScope)
	assert.ErrorIs(t, err, ErrInvalidMCPProfileRequest)
	assert.ErrorIs(t, ValidateMCPAuthorizationRequest(app, resource, MCPWorkWriteScope, "S256", "challenge", "http://127.0.0.1:49152"), ErrInvalidMCPProfileRequest)
	assert.ErrorIs(t, ValidateMCPAuthorizationCodeExchange(app, grant, resource, resource), ErrInvalidMCPProfileRequest)
	_, err = ValidateMCPRefresh(app, grant, token, resource)
	assert.ErrorIs(t, err, ErrInvalidMCPProfileRequest)
	_, err = MCPProfileForAccessToken(app, grant)
	assert.ErrorIs(t, err, ErrInvalidMCPProfileRequest)
	assert.Equal(t, []string{MCPReadScope}, MCPScopesSupported())
}

func TestMCPProfileRejectsResourceForOtherClients(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test_module.MockVariableValue(&setting.MCP.WorkMutationEnabled, false)()
	app := &auth_model.OAuth2Application{ClientID: "other"}
	require.NoError(t, ValidateMCPAuthorizationRequest(app, "", "", "", "", "https://client.example/callback"))
	assert.ErrorIs(t, ValidateMCPAuthorizationRequest(app, setting.MCPResource(), "read:repository", "S256", "challenge", "https://client.example/callback"), ErrInvalidMCPProfileRequest)
}

func TestValidateMCPAuthorizationCodeExchange(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	app := testMCPApplication()
	grant := &auth_model.OAuth2Grant{ApplicationID: app.ID, UserID: 42, Scope: "read:repository"}
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
	grant := &auth_model.OAuth2Grant{ApplicationID: app.ID, UserID: 42, Scope: MCPReadScope}
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
