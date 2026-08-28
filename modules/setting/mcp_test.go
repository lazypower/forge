// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMCPFrom(t *testing.T) {
	original := MCP
	originalAppURL := AppURL
	originalOAuth2 := OAuth2
	defer func() {
		MCP = original
		AppURL = originalAppURL
		OAuth2 = originalOAuth2
	}()

	t.Run("disabled defaults", func(t *testing.T) {
		MCP = original
		AppURL = "http://forge.example/"
		OAuth2 = originalOAuth2
		OAuth2.Enabled = false
		OAuth2.JWTClaimIssuer = ""
		cfg, err := NewConfigProviderFromData("")
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))

		assert.False(t, MCP.Enabled)
		assert.Equal(t, MCPAuthenticationProfileOAuth, MCP.Authentication)
		assert.EqualValues(t, defaultMCPMaxRequestBodyBytes, MCP.MaxRequestBodyBytes)
		assert.Equal(t, defaultMCPMaxInFlightRequests, MCP.MaxInFlightRequests)
		assert.Equal(t, defaultMCPExecutionTimeout, MCP.ExecutionTimeout)
	})

	t.Run("enabled default OAuth", func(t *testing.T) {
		MCP = original
		AppURL = "https://forge.example/"
		OAuth2 = originalOAuth2
		OAuth2.Enabled = true
		OAuth2.JWTClaimIssuer = "https://forge.example"
		cfg, err := NewConfigProviderFromData(`
[mcp]
ENABLED = true
MAX_REQUEST_BODY_BYTES = 2048
MAX_IN_FLIGHT_REQUESTS = 4
EXECUTION_TIMEOUT = 15s
`)
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))

		assert.True(t, MCP.Enabled)
		assert.Equal(t, MCPAuthenticationProfileOAuth, MCP.Authentication)
		assert.EqualValues(t, 2048, MCP.MaxRequestBodyBytes)
		assert.Equal(t, 4, MCP.MaxInFlightRequests)
		assert.Equal(t, 15*time.Second, MCP.ExecutionTimeout)
	})

	t.Run("explicit PAT fallback", func(t *testing.T) {
		MCP = original
		AppURL = "https://forge.example/"
		OAuth2 = originalOAuth2
		OAuth2.Enabled = false
		OAuth2.JWTClaimIssuer = ""
		cfg, err := NewConfigProviderFromData("[mcp]\nENABLED = true\nAUTHENTICATION = pat\n")
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))
		assert.True(t, MCP.Enabled)
		assert.Equal(t, MCPAuthenticationProfilePAT, MCP.Authentication)
	})

	t.Run("non-positive body limit", func(t *testing.T) {
		MCP = original
		AppURL = originalAppURL
		cfg, err := NewConfigProviderFromData(`
[mcp]
MAX_REQUEST_BODY_BYTES = -1
MAX_IN_FLIGHT_REQUESTS = -1
EXECUTION_TIMEOUT = -1s
`)
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))

		assert.EqualValues(t, defaultMCPMaxRequestBodyBytes, MCP.MaxRequestBodyBytes)
		assert.Equal(t, defaultMCPMaxInFlightRequests, MCP.MaxInFlightRequests)
		assert.Equal(t, defaultMCPExecutionTimeout, MCP.ExecutionTimeout)
	})

	t.Run("requires HTTPS", func(t *testing.T) {
		MCP = original
		AppURL = "http://forge.example/"
		cfg, err := NewConfigProviderFromData(`
[mcp]
ENABLED = true
`)
		require.NoError(t, err)

		err = loadMCPFrom(cfg)

		assert.EqualError(t, err, "[mcp] ENABLED requires an HTTPS ROOT_URL")
	})

	t.Run("canonical resource and metadata include subpath", func(t *testing.T) {
		AppURL = "https://forge.example/forge/"
		assert.Equal(t, "https://forge.example/forge/mcp", MCPResource())
		assert.Equal(t, "https://forge.example/forge/.well-known/oauth-protected-resource/mcp", MCPProtectedResourceMetadataURL())
		assert.Equal(t, "/.well-known/oauth-protected-resource/mcp", MCPProtectedResourceMetadataPath())
	})

	t.Run("invalid authentication profile", func(t *testing.T) {
		MCP = original
		cfg, err := NewConfigProviderFromData("[mcp]\nAUTHENTICATION = both\n")
		require.NoError(t, err)
		assert.EqualError(t, loadMCPFrom(cfg), `[mcp] AUTHENTICATION must be "pat" or "oauth"`)
	})

	t.Run("enabled default OAuth requires configured discoverable issuer", func(t *testing.T) {
		for _, test := range []struct {
			name    string
			appURL  string
			oauth   bool
			issuer  string
			wantErr string
		}{
			{name: "OAuth disabled", appURL: "https://forge.example/", wantErr: "[mcp] OAuth authentication requires [oauth2] ENABLED"},
			{name: "issuer absent", appURL: "https://forge.example/", oauth: true, wantErr: "[mcp] OAuth authentication requires [oauth2] JWT_CLAIM_ISSUER"},
			{name: "issuer HTTP", appURL: "https://forge.example/", oauth: true, issuer: "http://forge.example", wantErr: "[mcp] OAuth JWT_CLAIM_ISSUER must be an HTTPS issuer URL without query or fragment"},
			{name: "issuer alias", appURL: "https://forge.example/forge/", oauth: true, issuer: "https://login.example/forge", wantErr: "[mcp] OAuth JWT_CLAIM_ISSUER must match ROOT_URL so Forge OpenID discovery is authoritative"},
			{name: "matching trailing slash", appURL: "https://forge.example/forge/", oauth: true, issuer: "https://forge.example/forge/"},
		} {
			t.Run(test.name, func(t *testing.T) {
				MCP = original
				AppURL = test.appURL
				OAuth2 = originalOAuth2
				OAuth2.Enabled = test.oauth
				OAuth2.JWTClaimIssuer = test.issuer
				cfg, err := NewConfigProviderFromData("[mcp]\nENABLED = true\n")
				require.NoError(t, err)
				err = loadMCPFrom(cfg)
				if test.wantErr == "" {
					require.NoError(t, err)
					return
				}
				assert.EqualError(t, err, test.wantErr)
			})
		}
	})
}
