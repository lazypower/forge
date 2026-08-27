// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const (
	defaultMCPMaxRequestBodyBytes = 1 << 20
	defaultMCPMaxInFlightRequests = 8
	defaultMCPExecutionTimeout    = 30 * time.Second

	// MCPRoutePath is the instance-relative MCP endpoint path.
	MCPRoutePath = "/mcp"

	mcpProtectedResourceMetadataPath = "/.well-known/oauth-protected-resource/mcp"
)

// MCPAuthenticationProfile selects the only credential class accepted by MCP.
type MCPAuthenticationProfile string

const (
	MCPAuthenticationProfilePAT   MCPAuthenticationProfile = "pat"
	MCPAuthenticationProfileOAuth MCPAuthenticationProfile = "oauth"
)

var MCP = struct {
	Enabled             bool
	Authentication      MCPAuthenticationProfile
	MaxRequestBodyBytes int64         `ini:"MAX_REQUEST_BODY_BYTES"`
	MaxInFlightRequests int           `ini:"MAX_IN_FLIGHT_REQUESTS"`
	ExecutionTimeout    time.Duration `ini:"EXECUTION_TIMEOUT"`
}{
	Enabled:             false,
	Authentication:      MCPAuthenticationProfilePAT,
	MaxRequestBodyBytes: defaultMCPMaxRequestBodyBytes,
	MaxInFlightRequests: defaultMCPMaxInFlightRequests,
	ExecutionTimeout:    defaultMCPExecutionTimeout,
}

func loadMCPFrom(rootCfg ConfigProvider) error {
	mustMapSetting(rootCfg, "mcp", &MCP)
	switch MCP.Authentication {
	case MCPAuthenticationProfilePAT, MCPAuthenticationProfileOAuth:
	default:
		return fmt.Errorf("[mcp] AUTHENTICATION must be %q or %q", MCPAuthenticationProfilePAT, MCPAuthenticationProfileOAuth)
	}
	if MCP.MaxRequestBodyBytes <= 0 {
		MCP.MaxRequestBodyBytes = defaultMCPMaxRequestBodyBytes
	}
	if MCP.MaxInFlightRequests <= 0 {
		MCP.MaxInFlightRequests = defaultMCPMaxInFlightRequests
	}
	if MCP.ExecutionTimeout <= 0 {
		MCP.ExecutionTimeout = defaultMCPExecutionTimeout
	}
	if MCP.Enabled {
		appURL, err := url.Parse(AppURL)
		if err != nil || appURL.Scheme != "https" || appURL.Host == "" || appURL.User != nil || appURL.RawQuery != "" || appURL.Fragment != "" {
			return errors.New("[mcp] ENABLED requires an HTTPS ROOT_URL")
		}
		if MCP.Authentication == MCPAuthenticationProfileOAuth {
			if !OAuth2.Enabled {
				return errors.New("[mcp] OAuth authentication requires [oauth2] ENABLED")
			}
			if err := validateMCPIssuer(); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMCPIssuer() error {
	if OAuth2.JWTClaimIssuer == "" {
		return errors.New("[mcp] OAuth authentication requires [oauth2] JWT_CLAIM_ISSUER")
	}
	issuer, err := url.Parse(OAuth2.JWTClaimIssuer)
	if err != nil || issuer.Scheme != "https" || issuer.Host == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" {
		return errors.New("[mcp] OAuth JWT_CLAIM_ISSUER must be an HTTPS issuer URL without query or fragment")
	}
	if strings.TrimSuffix(OAuth2.JWTClaimIssuer, "/") != strings.TrimSuffix(AppURL, "/") {
		return errors.New("[mcp] OAuth JWT_CLAIM_ISSUER must match ROOT_URL so Forge OpenID discovery is authoritative")
	}
	return nil
}

// MCPResource returns the one canonical protected resource identifier.
func MCPResource() string {
	return strings.TrimSuffix(AppURL, "/") + MCPRoutePath
}

// MCPProtectedResourceMetadataURL returns the public metadata location advertised in bearer challenges.
func MCPProtectedResourceMetadataURL() string {
	return strings.TrimSuffix(AppURL, "/") + mcpProtectedResourceMetadataPath
}

// MCPProtectedResourceMetadataPath returns the instance-relative metadata route.
func MCPProtectedResourceMetadataPath() string {
	return mcpProtectedResourceMetadataPath
}
