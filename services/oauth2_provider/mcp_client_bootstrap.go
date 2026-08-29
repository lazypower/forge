// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/setting"
)

const (
	maxMCPRegistrationLabelCharacters = 128
	maxMCPRedirectURIBytes            = 2048
)

var ErrInvalidMCPClientMetadata = errors.New("invalid MCP client metadata")

// MCPClientRegistrationRequest is the complete closed bootstrap request profile.
type MCPClientRegistrationRequest struct {
	RedirectURIs            []string `json:"redirect_uris"`
	TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method,omitempty"`
	GrantTypes              []string `json:"grant_types,omitempty"`
	ResponseTypes           []string `json:"response_types,omitempty"`
	ClientName              string   `json:"client_name,omitempty"`
	ApplicationType         string   `json:"application_type,omitempty"`
	InstallationName        string   `json:"installation_name,omitempty"`
}

// MCPClientRegistrationResponse returns public metadata only and never issues a secret.
type MCPClientRegistrationResponse struct {
	MCPClientRegistrationRequest
	ClientID         string `json:"client_id"`
	ClientIDIssuedAt int64  `json:"client_id_issued_at"`
}

// CreateMCPClientBootstrap validates and persists an authority-free provisional registration.
func CreateMCPClientBootstrap(ctx context.Context, request MCPClientRegistrationRequest, now time.Time) (*MCPClientRegistrationResponse, error) {
	redirectClass, err := validateMCPClientRegistrationRequest(&request)
	if err != nil {
		return nil, err
	}
	if _, err := auth_model.CleanupExpiredMCPClientRegistrations(ctx, now, setting.MCP.ClientBootstrapCleanupBatchSize); err != nil {
		return nil, err
	}
	app, err := auth_model.CreateMCPClientRegistration(ctx, request.ClientName, request.InstallationName, request.RedirectURIs, redirectClass, now.Add(setting.MCP.ClientBootstrapProvisionalLifetime), setting.MCP.ClientBootstrapMaxOutstanding)
	if err != nil {
		return nil, err
	}
	request.TokenEndpointAuthMethod = "none"
	request.GrantTypes = []string{"authorization_code", "refresh_token"}
	request.ResponseTypes = []string{"code"}
	if request.ApplicationType == "" {
		if redirectClass == auth_model.MCPRedirectClassLoopback {
			request.ApplicationType = "native"
		} else {
			request.ApplicationType = "web"
		}
	}
	return &MCPClientRegistrationResponse{
		MCPClientRegistrationRequest: request,
		ClientID:                     app.ClientID,
		ClientIDIssuedAt:             now.Unix(),
	}, nil
}

func validateMCPClientRegistrationRequest(request *MCPClientRegistrationRequest) (auth_model.MCPRedirectClass, error) {
	if request == nil || !validMCPRegistrationLabel(request.ClientName, true) || !validMCPRegistrationLabel(request.InstallationName, false) {
		return "", ErrInvalidMCPClientMetadata
	}
	if request.TokenEndpointAuthMethod != "" && request.TokenEndpointAuthMethod != "none" {
		return "", ErrInvalidMCPClientMetadata
	}
	if len(request.GrantTypes) > 0 && !sameStringSet(request.GrantTypes, []string{"authorization_code", "refresh_token"}) {
		return "", ErrInvalidMCPClientMetadata
	}
	if len(request.ResponseTypes) > 0 && !slices.Equal(request.ResponseTypes, []string{"code"}) {
		return "", ErrInvalidMCPClientMetadata
	}
	if len(request.RedirectURIs) == 0 || len(request.RedirectURIs) > setting.MCP.ClientBootstrapMaxRedirectURIs {
		return "", ErrInvalidMCPClientMetadata
	}
	seen := make(map[string]struct{}, len(request.RedirectURIs))
	var redirectClass auth_model.MCPRedirectClass
	for _, value := range request.RedirectURIs {
		class, comparison, err := validateMCPRegisteredRedirectURI(value)
		if err != nil || (redirectClass != "" && redirectClass != class) {
			return "", ErrInvalidMCPClientMetadata
		}
		if _, duplicate := seen[comparison]; duplicate {
			return "", ErrInvalidMCPClientMetadata
		}
		seen[comparison] = struct{}{}
		redirectClass = class
	}
	wantApplicationType := "web"
	if redirectClass == auth_model.MCPRedirectClassLoopback {
		wantApplicationType = "native"
	}
	if request.ApplicationType != "" && request.ApplicationType != wantApplicationType {
		return "", ErrInvalidMCPClientMetadata
	}
	return redirectClass, nil
}

func validMCPRegistrationLabel(value string, required bool) bool {
	if value == "" {
		return !required
	}
	if !utf8.ValidString(value) || strings.TrimSpace(value) != value || utf8.RuneCountInString(value) > maxMCPRegistrationLabelCharacters {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	return true
}

func sameStringSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	seen := make(map[string]struct{}, len(got))
	for _, value := range got {
		if _, duplicate := seen[value]; duplicate || !slices.Contains(want, value) {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validateMCPRegisteredRedirectURI(value string) (auth_model.MCPRedirectClass, string, error) {
	if value == "" || len(value) > maxMCPRedirectURIBytes || strings.TrimSpace(value) != value {
		return "", "", ErrInvalidMCPClientMetadata
	}
	redirect, err := url.Parse(value)
	if err != nil || !redirect.IsAbs() || redirect.Opaque != "" || redirect.User != nil || redirect.Fragment != "" || redirect.Host == "" || redirect.Hostname() == "" {
		return "", "", ErrInvalidMCPClientMetadata
	}
	switch redirect.Scheme {
	case "https":
		return auth_model.MCPRedirectClassHTTPS, value, nil
	case "http":
		ip := net.ParseIP(redirect.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return "", "", ErrInvalidMCPClientMetadata
		}
		host := redirect.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		redirect.Host = host
		return auth_model.MCPRedirectClassLoopback, redirect.String(), nil
	default:
		return "", "", ErrInvalidMCPClientMetadata
	}
}

// MCPConsentCallbackContext returns non-label callback context for the consent page.
func MCPConsentCallbackContext(app *auth_model.OAuth2Application, redirectURI string) (string, error) {
	if app == nil || !app.ContainsMCPRedirectURI(redirectURI) {
		return "", ErrInvalidMCPClientMetadata
	}
	if app.MCPRedirectClass == auth_model.MCPRedirectClassLoopback {
		return "Local application (loopback)", nil
	}
	redirect, err := url.Parse(redirectURI)
	if err != nil || redirect.Scheme != "https" || redirect.Host == "" {
		return "", ErrInvalidMCPClientMetadata
	}
	return redirect.Scheme + "://" + redirect.Host, nil
}
