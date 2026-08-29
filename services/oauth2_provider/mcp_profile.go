// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"errors"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/setting"
)

const (
	// MCPReadScope is the canonical scope for the fixed MCP read profile.
	MCPReadScope = "read:repository"
	// MCPWorkWriteScope is the canonical scope for the fixed MCP work-write profile.
	MCPWorkWriteScope = "read:repository write:issue write:repository"
)

// ErrInvalidMCPProfileRequest is returned when a request violates a fixed MCP OAuth profile.
var ErrInvalidMCPProfileRequest = errors.New("invalid MCP OAuth profile request")

// MCPProfile is the verified fixed OAuth profile and its exact scope snapshot.
type MCPProfile struct {
	Name           auth_model.MCPBuiltinOAuth2ApplicationProfile
	CanonicalScope string
	Scopes         []string
}

// MCPProfileForAccessToken validates the application and grant as one fixed profile.
func MCPProfileForAccessToken(app *auth_model.OAuth2Application, grant *auth_model.OAuth2Grant) (MCPProfile, error) {
	profile, err := mcpProfileForApplication(app)
	if err != nil || grant == nil || grant.ApplicationID != app.ID {
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
	canonical, err := canonicalMCPProfileScope(profile, grant.Scope)
	if err != nil || canonical != profile.CanonicalScope {
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
	return profile, nil
}

// CanonicalMCPAuthorizationScope canonicalizes a fixed MCP profile scope.
// Non-MCP applications retain their existing scope behavior.
func CanonicalMCPAuthorizationScope(app *auth_model.OAuth2Application, scope string) (string, error) {
	if !auth_model.IsMCPBuiltinOAuth2Application(app) {
		return scope, nil
	}
	profile, err := mcpProfileForApplication(app)
	if err != nil {
		return "", err
	}
	return canonicalMCPProfileScope(profile, scope)
}

// ValidateMCPAuthorizationRequest protects the fixed client and resource boundary.
func ValidateMCPAuthorizationRequest(app *auth_model.OAuth2Application, resource, scope, codeChallengeMethod, codeChallenge, redirectURI string) error {
	if !auth_model.IsMCPBuiltinOAuth2Application(app) {
		if resource != "" {
			return ErrInvalidMCPProfileRequest
		}
		return nil
	}
	profile, err := mcpProfileForApplication(app)
	if err != nil {
		return err
	}
	canonicalScope, err := canonicalMCPProfileScope(profile, scope)
	if err != nil || canonicalScope != profile.CanonicalScope || resource != setting.MCPResource() || codeChallengeMethod != "S256" || codeChallenge == "" {
		return ErrInvalidMCPProfileRequest
	}
	if !validMCPRedirectURI(redirectURI) || !app.ContainsRedirectURI(redirectURI) {
		return ErrInvalidMCPProfileRequest
	}
	return nil
}

// ValidateMCPAuthorizationCodeExchange validates the immutable code resource at token exchange.
func ValidateMCPAuthorizationCodeExchange(app *auth_model.OAuth2Application, grant *auth_model.OAuth2Grant, requestedResource, codeResource string) error {
	if !auth_model.IsMCPBuiltinOAuth2Application(app) {
		if requestedResource != "" || codeResource != "" {
			return ErrInvalidMCPProfileRequest
		}
		return nil
	}
	if _, err := MCPProfileForAccessToken(app, grant); err != nil {
		return err
	}
	if requestedResource != setting.MCPResource() || codeResource != setting.MCPResource() {
		return ErrInvalidMCPProfileRequest
	}
	return nil
}

// ValidateMCPRefresh validates the signed refresh audience and an optional resource assertion.
func ValidateMCPRefresh(app *auth_model.OAuth2Application, grant *auth_model.OAuth2Grant, token *Token, requestedResource string) (string, error) {
	if !auth_model.IsMCPBuiltinOAuth2Application(app) {
		if requestedResource != "" || len(token.Audience) != 0 {
			return "", ErrInvalidMCPProfileRequest
		}
		return "", nil
	}
	if _, err := MCPProfileForAccessToken(app, grant); err != nil {
		return "", err
	}
	resource := setting.MCPResource()
	if token.Kind != KindRefreshToken || token.Issuer != TokenIssuer() || token.Subject != strconv.FormatInt(grant.UserID, 10) || len(token.Audience) != 1 || token.Audience[0] != resource || token.Counter == 0 {
		return "", ErrInvalidMCPProfileRequest
	}
	if requestedResource != "" && requestedResource != resource {
		return "", ErrInvalidMCPProfileRequest
	}
	return resource, nil
}

// MCPScopesSupported returns the exact individual scopes enabled for the MCP resource.
func MCPScopesSupported() []string {
	scopes := []string{string(auth_model.AccessTokenScopeReadRepository)}
	if setting.MCP.WorkMutationEnabled {
		scopes = append(scopes,
			string(auth_model.AccessTokenScopeWriteIssue),
			string(auth_model.AccessTokenScopeWriteRepository),
		)
	}
	return scopes
}

func mcpProfileForApplication(app *auth_model.OAuth2Application) (MCPProfile, error) {
	profileName, ok := auth_model.MCPBuiltinOAuth2ApplicationProfileOf(app)
	if !ok || !setting.MCP.Enabled || setting.MCP.Authentication != setting.MCPAuthenticationProfileOAuth || app.ConfidentialClient {
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
	if profileName == auth_model.MCPBuiltinOAuth2ApplicationProfileWorkWrite && !setting.MCP.WorkMutationEnabled {
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
	builtin := auth_model.BuiltinApplications()[app.ClientID]
	if builtin == nil || !slices.Equal(app.RedirectURIs, builtin.RedirectURIs) {
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
	switch profileName {
	case auth_model.MCPBuiltinOAuth2ApplicationProfileRead:
		return MCPProfile{
			Name:           profileName,
			CanonicalScope: MCPReadScope,
			Scopes:         []string{string(auth_model.AccessTokenScopeReadRepository)},
		}, nil
	case auth_model.MCPBuiltinOAuth2ApplicationProfileWorkWrite:
		return MCPProfile{
			Name:           profileName,
			CanonicalScope: MCPWorkWriteScope,
			Scopes: []string{
				string(auth_model.AccessTokenScopeReadRepository),
				string(auth_model.AccessTokenScopeWriteIssue),
				string(auth_model.AccessTokenScopeWriteRepository),
			},
		}, nil
	default:
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
}

func canonicalMCPProfileScope(profile MCPProfile, scope string) (string, error) {
	members := strings.Split(scope, " ")
	if scope == "" || slices.Contains(members, "") || len(members) != len(profile.Scopes) {
		return "", ErrInvalidMCPProfileRequest
	}
	want := make(map[string]struct{}, len(profile.Scopes))
	for _, member := range profile.Scopes {
		want[member] = struct{}{}
	}
	seen := make(map[string]struct{}, len(members))
	for _, member := range members {
		if _, ok := want[member]; !ok {
			return "", ErrInvalidMCPProfileRequest
		}
		if _, duplicate := seen[member]; duplicate {
			return "", ErrInvalidMCPProfileRequest
		}
		seen[member] = struct{}{}
	}
	return profile.CanonicalScope, nil
}

func validMCPRedirectURI(value string) bool {
	redirect, err := url.Parse(value)
	if err != nil || !redirect.IsAbs() || redirect.User != nil || redirect.Fragment != "" {
		return false
	}
	if redirect.Scheme == "https" {
		return redirect.Host != ""
	}
	if redirect.Scheme != "http" {
		return false
	}
	ip := net.ParseIP(redirect.Hostname())
	return ip != nil && ip.IsLoopback()
}
