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
	defaultMCPMaxRequestBodyBytes          = 1 << 20
	defaultMCPMaxInFlightRequests          = 8
	defaultMCPExecutionTimeout             = 30 * time.Second
	defaultMCPBootstrapMaxRequestBodyBytes = 32 << 10
	defaultMCPBootstrapMaxInFlightRequests = 4
	defaultMCPBootstrapProvisionalLifetime = 30 * time.Minute
	defaultMCPBootstrapMaxRedirectURIs     = 5
	defaultMCPBootstrapMaxOutstanding      = 1000
	defaultMCPBootstrapCleanupBatchSize    = 100
	defaultMCPBootstrapPerSourceRate       = 10
	defaultMCPBootstrapInstanceRate        = 100
	defaultMCPBootstrapMaxSourceBuckets    = 1024
	defaultMCPBootstrapRateWindow          = time.Minute
	maxMCPBootstrapRequestBodyBytes        = 1 << 20
	maxMCPBootstrapInFlightRequests        = 64
	maxMCPBootstrapRedirectURIs            = 20
	maxMCPBootstrapOutstanding             = 100_000
	maxMCPBootstrapCleanupBatchSize        = 10_000
	maxMCPBootstrapInstanceRate            = 100_000
	maxMCPBootstrapSourceBuckets           = 100_000

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
	Enabled                            bool
	WorkInspectionEnabled              bool `ini:"WORK_INSPECTION_ENABLED"`
	WorkMutationEnabled                bool `ini:"WORK_MUTATION_ENABLED"`
	ClientBootstrapEnabled             bool `ini:"CLIENT_BOOTSTRAP_ENABLED"`
	Authentication                     MCPAuthenticationProfile
	MaxRequestBodyBytes                int64         `ini:"MAX_REQUEST_BODY_BYTES"`
	MaxInFlightRequests                int           `ini:"MAX_IN_FLIGHT_REQUESTS"`
	ExecutionTimeout                   time.Duration `ini:"EXECUTION_TIMEOUT"`
	ClientBootstrapMaxRequestBodyBytes int64         `ini:"CLIENT_BOOTSTRAP_MAX_REQUEST_BODY_BYTES"`
	ClientBootstrapMaxInFlightRequests int           `ini:"CLIENT_BOOTSTRAP_MAX_IN_FLIGHT_REQUESTS"`
	ClientBootstrapProvisionalLifetime time.Duration `ini:"CLIENT_BOOTSTRAP_PROVISIONAL_LIFETIME"`
	ClientBootstrapMaxRedirectURIs     int           `ini:"CLIENT_BOOTSTRAP_MAX_REDIRECT_URIS"`
	ClientBootstrapMaxOutstanding      int           `ini:"CLIENT_BOOTSTRAP_MAX_OUTSTANDING"`
	ClientBootstrapCleanupBatchSize    int           `ini:"CLIENT_BOOTSTRAP_CLEANUP_BATCH_SIZE"`
	ClientBootstrapPerSourceRate       int           `ini:"CLIENT_BOOTSTRAP_PER_SOURCE_RATE"`
	ClientBootstrapInstanceRate        int           `ini:"CLIENT_BOOTSTRAP_INSTANCE_RATE"`
	ClientBootstrapMaxSourceBuckets    int           `ini:"CLIENT_BOOTSTRAP_MAX_SOURCE_BUCKETS"`
	ClientBootstrapRateWindow          time.Duration `ini:"CLIENT_BOOTSTRAP_RATE_WINDOW"`
}{
	Enabled:                            false,
	WorkInspectionEnabled:              false,
	WorkMutationEnabled:                false,
	Authentication:                     MCPAuthenticationProfileOAuth,
	MaxRequestBodyBytes:                defaultMCPMaxRequestBodyBytes,
	MaxInFlightRequests:                defaultMCPMaxInFlightRequests,
	ExecutionTimeout:                   defaultMCPExecutionTimeout,
	ClientBootstrapMaxRequestBodyBytes: defaultMCPBootstrapMaxRequestBodyBytes,
	ClientBootstrapMaxInFlightRequests: defaultMCPBootstrapMaxInFlightRequests,
	ClientBootstrapProvisionalLifetime: defaultMCPBootstrapProvisionalLifetime,
	ClientBootstrapMaxRedirectURIs:     defaultMCPBootstrapMaxRedirectURIs,
	ClientBootstrapMaxOutstanding:      defaultMCPBootstrapMaxOutstanding,
	ClientBootstrapCleanupBatchSize:    defaultMCPBootstrapCleanupBatchSize,
	ClientBootstrapPerSourceRate:       defaultMCPBootstrapPerSourceRate,
	ClientBootstrapInstanceRate:        defaultMCPBootstrapInstanceRate,
	ClientBootstrapMaxSourceBuckets:    defaultMCPBootstrapMaxSourceBuckets,
	ClientBootstrapRateWindow:          defaultMCPBootstrapRateWindow,
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
	if MCP.ClientBootstrapMaxRequestBodyBytes <= 0 || MCP.ClientBootstrapMaxInFlightRequests <= 0 ||
		MCP.ClientBootstrapMaxRedirectURIs <= 0 || MCP.ClientBootstrapMaxOutstanding <= 0 ||
		MCP.ClientBootstrapCleanupBatchSize <= 0 || MCP.ClientBootstrapCleanupBatchSize > MCP.ClientBootstrapMaxOutstanding ||
		MCP.ClientBootstrapPerSourceRate <= 0 || MCP.ClientBootstrapInstanceRate <= 0 ||
		MCP.ClientBootstrapPerSourceRate > MCP.ClientBootstrapInstanceRate || MCP.ClientBootstrapMaxSourceBuckets <= 0 ||
		MCP.ClientBootstrapRateWindow < time.Second ||
		MCP.ClientBootstrapMaxRequestBodyBytes > maxMCPBootstrapRequestBodyBytes ||
		MCP.ClientBootstrapMaxInFlightRequests > maxMCPBootstrapInFlightRequests ||
		MCP.ClientBootstrapMaxRedirectURIs > maxMCPBootstrapRedirectURIs ||
		MCP.ClientBootstrapMaxOutstanding > maxMCPBootstrapOutstanding ||
		MCP.ClientBootstrapCleanupBatchSize > maxMCPBootstrapCleanupBatchSize ||
		MCP.ClientBootstrapInstanceRate > maxMCPBootstrapInstanceRate ||
		MCP.ClientBootstrapMaxSourceBuckets > maxMCPBootstrapSourceBuckets ||
		MCP.ClientBootstrapRateWindow > time.Hour {
		return errors.New("[mcp] client bootstrap bounds must be positive, ordered, and finite")
	}
	if MCP.ClientBootstrapProvisionalLifetime < 10*time.Minute || MCP.ClientBootstrapProvisionalLifetime > 60*time.Minute {
		return errors.New("[mcp] CLIENT_BOOTSTRAP_PROVISIONAL_LIFETIME must be between 10m and 60m")
	}
	if MCP.ClientBootstrapEnabled && (!MCP.Enabled || MCP.Authentication != MCPAuthenticationProfileOAuth) {
		return errors.New("[mcp] CLIENT_BOOTSTRAP_ENABLED requires ENABLED with OAuth authentication")
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
