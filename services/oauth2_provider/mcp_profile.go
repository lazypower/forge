// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"errors"
	"slices"
	"strconv"
	"strings"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/setting"
)

const (
	// MCPReadScope is the canonical scope for the MCP Read profile.
	MCPReadScope = "read:repository"
	// MCPWorkWriteScope is the canonical scope for the MCP Work Planning profile.
	MCPWorkWriteScope = "read:repository write:issue write:repository"
)

var ErrInvalidMCPProfileRequest = errors.New("invalid MCP OAuth profile request")

// MCPProfile is the verified server-defined OAuth profile and its exact scope snapshot.
type MCPProfile struct {
	Name           auth_model.MCPProfile
	CanonicalScope string
	Scopes         []string
}

// MCPProfileForAccessToken validates a finalized registration and its grant-owned scope.
func MCPProfileForAccessToken(app *auth_model.OAuth2Application, grant *auth_model.OAuth2Grant) (MCPProfile, error) {
	if app == nil || grant == nil || grant.ApplicationID != app.ID ||
		app.MCPRegistrationState != auth_model.MCPRegistrationStateFinalized ||
		app.MCPBoundUserID != grant.UserID {
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
	return mcpProfileForScope(app, grant.Scope)
}

// CanonicalMCPAuthorizationScope canonicalizes one exact MCP profile scope.
// Non-MCP applications retain their existing scope behavior.
func CanonicalMCPAuthorizationScope(app *auth_model.OAuth2Application, scope string) (string, error) {
	if app == nil || !app.IsMCPClientRegistration() {
		return scope, nil
	}
	profile, err := mcpProfileForScope(app, scope)
	if err != nil {
		return "", err
	}
	return profile.CanonicalScope, nil
}

// ValidateMCPAuthorizationRequest protects the constrained client and resource boundary.
func ValidateMCPAuthorizationRequest(app *auth_model.OAuth2Application, resource, scope, codeChallengeMethod, codeChallenge, redirectURI string) error {
	if app == nil || !app.IsMCPClientRegistration() {
		if resource != "" {
			return ErrInvalidMCPProfileRequest
		}
		return nil
	}
	_, err := mcpProfileForScope(app, scope)
	if err != nil || resource != setting.MCPResource() || codeChallengeMethod != "S256" || codeChallenge == "" {
		return ErrInvalidMCPProfileRequest
	}
	if !app.ContainsMCPRedirectURI(redirectURI) {
		return ErrInvalidMCPProfileRequest
	}
	return nil
}

// ValidateMCPAuthorizationCodeExchange validates the immutable code resource at token exchange.
func ValidateMCPAuthorizationCodeExchange(app *auth_model.OAuth2Application, grant *auth_model.OAuth2Grant, requestedResource, codeResource string) error {
	if app == nil || !app.IsMCPClientRegistration() {
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
	if app == nil || !app.IsMCPClientRegistration() {
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

func mcpProfileForScope(app *auth_model.OAuth2Application, scope string) (MCPProfile, error) {
	if app == nil || !app.IsMCPClientRegistration() || !setting.MCP.Enabled ||
		setting.MCP.Authentication != setting.MCPAuthenticationProfileOAuth || app.ConfidentialClient ||
		len(app.RedirectURIs) == 0 ||
		(app.MCPRedirectClass != auth_model.MCPRedirectClassHTTPS && app.MCPRedirectClass != auth_model.MCPRedirectClassLoopback) {
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
	profile, err := MCPProfileForScope(scope)
	if err != nil || (profile.Name == auth_model.MCPProfileWorkPlanning && !setting.MCP.WorkMutationEnabled) {
		return MCPProfile{}, ErrInvalidMCPProfileRequest
	}
	return profile, nil
}

// MCPProfileForScope derives consent facts even when MCP is disabled. It does not authorize use.
func MCPProfileForScope(scope string) (MCPProfile, error) {
	profiles := []MCPProfile{{
		Name:           auth_model.MCPProfileRead,
		CanonicalScope: MCPReadScope,
		Scopes:         []string{string(auth_model.AccessTokenScopeReadRepository)},
	}, {
		Name:           auth_model.MCPProfileWorkPlanning,
		CanonicalScope: MCPWorkWriteScope,
		Scopes: []string{
			string(auth_model.AccessTokenScopeReadRepository),
			string(auth_model.AccessTokenScopeWriteIssue),
			string(auth_model.AccessTokenScopeWriteRepository),
		},
	}}
	for _, profile := range profiles {
		canonical, err := canonicalMCPProfileScope(profile, scope)
		if err == nil && canonical == profile.CanonicalScope {
			return profile, nil
		}
	}
	return MCPProfile{}, ErrInvalidMCPProfileRequest
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
