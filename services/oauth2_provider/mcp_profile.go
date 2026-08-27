// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"errors"
	"net"
	"net/url"
	"strconv"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/setting"
)

// ErrInvalidMCPProfileRequest is returned when a request violates the fixed MCP OAuth profile.
var ErrInvalidMCPProfileRequest = errors.New("invalid MCP OAuth profile request")

// ValidateMCPAccessTokenClient validates the grant's registered client profile.
func ValidateMCPAccessTokenClient(app *auth_model.OAuth2Application) error {
	if !auth_model.IsMCPBuiltinOAuth2Application(app) {
		return ErrInvalidMCPProfileRequest
	}
	return validateMCPClient(app)
}

// ValidateMCPAuthorizationRequest protects the fixed client and resource boundary.
func ValidateMCPAuthorizationRequest(app *auth_model.OAuth2Application, resource, scope, codeChallengeMethod, codeChallenge, redirectURI string) error {
	if !auth_model.IsMCPBuiltinOAuth2Application(app) {
		if resource != "" {
			return ErrInvalidMCPProfileRequest
		}
		return nil
	}
	if err := validateMCPClient(app); err != nil {
		return err
	}
	if resource != setting.MCPResource() || scope != string(auth_model.AccessTokenScopeReadRepository) || codeChallengeMethod != "S256" || codeChallenge == "" {
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
	if err := validateMCPClient(app); err != nil {
		return err
	}
	if grant == nil || grant.Scope != string(auth_model.AccessTokenScopeReadRepository) || requestedResource != setting.MCPResource() || codeResource != setting.MCPResource() {
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
	if err := validateMCPClient(app); err != nil {
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

func validateMCPClient(app *auth_model.OAuth2Application) error {
	if !setting.MCP.Enabled || setting.MCP.Authentication != setting.MCPAuthenticationProfileOAuth || app == nil || app.ConfidentialClient {
		return ErrInvalidMCPProfileRequest
	}
	builtin := auth_model.BuiltinApplications()[auth_model.MCPBuiltinOAuth2ApplicationClientID]
	if builtin == nil || len(app.RedirectURIs) != len(builtin.RedirectURIs) {
		return ErrInvalidMCPProfileRequest
	}
	for i := range builtin.RedirectURIs {
		if app.RedirectURIs[i] != builtin.RedirectURIs[i] {
			return ErrInvalidMCPProfileRequest
		}
	}
	return nil
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
