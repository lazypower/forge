// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"strings"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPClientBootstrapRedirectValidation(t *testing.T) {
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapMaxRedirectURIs, 5)()
	valid := []struct {
		name      string
		request   MCPClientRegistrationRequest
		wantClass auth_model.MCPRedirectClass
	}{
		{name: "https", request: MCPClientRegistrationRequest{ClientName: "Harness", RedirectURIs: []string{"https://client.example/callback?channel=A"}}, wantClass: auth_model.MCPRedirectClassHTTPS},
		{name: "ipv4 loopback", request: MCPClientRegistrationRequest{ClientName: "Harness", ApplicationType: "native", RedirectURIs: []string{"http://127.0.0.1/callback"}}, wantClass: auth_model.MCPRedirectClassLoopback},
		{name: "ipv6 loopback", request: MCPClientRegistrationRequest{ClientName: "Harness", TokenEndpointAuthMethod: "none", GrantTypes: []string{"refresh_token", "authorization_code"}, ResponseTypes: []string{"code"}, RedirectURIs: []string{"http://[::1]:49200/callback"}}, wantClass: auth_model.MCPRedirectClassLoopback},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			got, err := validateMCPClientRegistrationRequest(&test.request)
			require.NoError(t, err)
			assert.Equal(t, test.wantClass, got)
		})
	}

	invalid := []MCPClientRegistrationRequest{
		{ClientName: "", RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: " Harness", RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: "Harness\nForged", RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: "Harness\u202eforged", RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: "Harness\u2028forged", RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: strings.Repeat("x", 129), RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: "Harness", InstallationName: strings.Repeat("x", 129), RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: "Harness", TokenEndpointAuthMethod: "client_secret_basic", RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: "Harness", GrantTypes: []string{"client_credentials"}, RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: "Harness", ResponseTypes: []string{"token"}, RedirectURIs: []string{"https://client.example/callback"}},
		{ClientName: "Harness", ApplicationType: "web", RedirectURIs: []string{"http://127.0.0.1/callback"}},
		{ClientName: "Harness", RedirectURIs: nil},
		{ClientName: "Harness", RedirectURIs: []string{"https://user@client.example/callback"}},
		{ClientName: "Harness", RedirectURIs: []string{"https://client.example/callback#fragment"}},
		{ClientName: "Harness", RedirectURIs: []string{"http://client.example/callback"}},
		{ClientName: "Harness", RedirectURIs: []string{"http://localhost/callback"}},
		{ClientName: "Harness", RedirectURIs: []string{"http://127.0.0.1:bad/callback"}},
		{ClientName: "Harness", RedirectURIs: []string{"https://client.example/callback", "http://127.0.0.1/callback"}},
		{ClientName: "Harness", RedirectURIs: []string{"http://127.0.0.1/callback", "http://127.0.0.1:49152/callback"}},
		{ClientName: "Harness", RedirectURIs: []string{"https://client.example/callback", "https://client.example/callback"}},
	}
	for i, request := range invalid {
		t.Run(string(rune('a'+i)), func(t *testing.T) {
			_, err := validateMCPClientRegistrationRequest(&request)
			assert.ErrorIs(t, err, ErrInvalidMCPClientMetadata)
		})
	}
}

func TestMCPClientBootstrapCreatesNoAuthority(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapProvisionalLifetime, 30*time.Minute)()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapMaxOutstanding, 10)()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapCleanupBatchSize, 5)()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapMaxRedirectURIs, 5)()
	now := time.Now().UTC().Truncate(time.Second)
	response, err := CreateMCPClientBootstrap(t.Context(), MCPClientRegistrationRequest{
		ClientName:       "Planning harness",
		InstallationName: "laptop",
		RedirectURIs:     []string{"http://127.0.0.1/callback"},
	}, now)
	require.NoError(t, err)
	assert.NotEmpty(t, response.ClientID)
	assert.Equal(t, "none", response.TokenEndpointAuthMethod)
	app, err := auth_model.GetOAuth2ApplicationByClientID(t.Context(), response.ClientID)
	require.NoError(t, err)
	assert.Equal(t, auth_model.MCPRegistrationStateProvisional, app.MCPRegistrationState)
	assert.Equal(t, "laptop", app.MCPInstallationLabel)
	assert.False(t, app.ConfidentialClient)
	grant, err := app.GetGrantByUserID(t.Context(), 1)
	require.NoError(t, err)
	assert.Nil(t, grant)
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2AuthorizationCode{})
}

func TestMCPConsentCallbackContext(t *testing.T) {
	loopback := &auth_model.OAuth2Application{
		MCPRegistrationState: auth_model.MCPRegistrationStateProvisional,
		MCPRedirectClass:     auth_model.MCPRedirectClassLoopback,
		RedirectURIs:         []string{"http://127.0.0.1/callback"},
	}
	got, err := MCPConsentCallbackContext(loopback, "http://127.0.0.1:49152/callback")
	require.NoError(t, err)
	assert.Equal(t, "Local application (loopback)", got)
	httpsApp := &auth_model.OAuth2Application{
		MCPRegistrationState: auth_model.MCPRegistrationStateProvisional,
		MCPRedirectClass:     auth_model.MCPRedirectClassHTTPS,
		RedirectURIs:         []string{"https://client.example:8443/Callback?channel=A"},
	}
	got, err = MCPConsentCallbackContext(httpsApp, "https://client.example:8443/Callback?channel=A")
	require.NoError(t, err)
	assert.Equal(t, "https://client.example:8443", got)
	_, err = MCPConsentCallbackContext(httpsApp, "https://client.example:8443/callback?channel=A")
	assert.ErrorIs(t, err, ErrInvalidMCPClientMetadata)
}
