// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"

	auth_model "gitea.dev/models/auth"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
)

// ErrInvalidAccessToken is returned for every rejected OAuth access token.
var ErrInvalidAccessToken = errors.New("invalid OAuth access token")

// VerifiedAccessToken is an OAuth access token bound to its current Forge principal and grant.
type VerifiedAccessToken struct {
	Principal *user_model.User
	Grant     *auth_model.OAuth2Grant
	Scope     auth_model.AccessTokenScope
	ExpiresAt time.Time
	Resource  string
}

type (
	oauthGrantLookup func(context.Context, int64) (*auth_model.OAuth2Grant, error)
	principalLookup  func(context.Context, int64) (*user_model.User, error)
)

// VerifyAccessToken validates a signed Forge OAuth access token for the expected resource.
// An empty expected resource accepts only legacy audience-less access tokens.
func VerifyAccessToken(ctx context.Context, tokenValue, expectedResource string, signingKey JWTSigningKey) (*VerifiedAccessToken, error) {
	return verifyAccessToken(ctx, tokenValue, expectedResource, signingKey, auth_model.GetOAuth2GrantByID, user_model.GetUserByID, time.Now())
}

func verifyAccessToken(ctx context.Context, tokenValue, expectedResource string, signingKey JWTSigningKey, findGrant oauthGrantLookup, findPrincipal principalLookup, now time.Time) (*VerifiedAccessToken, error) {
	token, err := ParseToken(tokenValue, signingKey)
	if err != nil {
		return nil, ErrInvalidAccessToken
	}
	claims, err := signedTokenClaims(tokenValue)
	if err != nil || !claimHasValue(claims, "tt") {
		return nil, ErrInvalidAccessToken
	}
	if token.Kind != KindAccessToken || token.GrantID <= 0 || token.ExpiresAt == nil || token.IssuedAt == nil {
		return nil, ErrInvalidAccessToken
	}
	if !token.ExpiresAt.After(now) || token.IssuedAt.After(now) {
		return nil, ErrInvalidAccessToken
	}

	_, audiencePresent := claims["aud"]
	resource, ok := tokenResource(token.Audience, audiencePresent)
	if !ok || resource != expectedResource {
		return nil, ErrInvalidAccessToken
	}
	if expectedResource != "" && !validResource(expectedResource) {
		return nil, ErrInvalidAccessToken
	}
	issuer := TokenIssuer()
	if token.Issuer != "" && token.Issuer != issuer {
		return nil, ErrInvalidAccessToken
	}
	if resource != "" && (issuer == "" || token.Issuer != issuer) {
		return nil, ErrInvalidAccessToken
	}

	grant, err := findGrant(ctx, token.GrantID)
	if err != nil || grant == nil || grant.UserID <= 0 {
		return nil, ErrInvalidAccessToken
	}
	principal, err := findPrincipal(ctx, grant.UserID)
	if err != nil || !currentOAuthPrincipal(principal, grant.UserID) {
		return nil, ErrInvalidAccessToken
	}
	// Legacy consumers retain their existing principal-state response behavior.
	if resource != "" && (!principal.IsActive || principal.ProhibitLogin) {
		return nil, ErrInvalidAccessToken
	}
	if token.Subject != "" && token.Subject != strconv.FormatInt(principal.ID, 10) {
		return nil, ErrInvalidAccessToken
	}
	if resource != "" && token.Subject == "" {
		return nil, ErrInvalidAccessToken
	}

	return &VerifiedAccessToken{
		Principal: principal,
		Grant:     grant,
		Scope:     GrantAdditionalScopes(grant.Scope),
		ExpiresAt: token.ExpiresAt.Time,
		Resource:  resource,
	}, nil
}

func signedTokenClaims(tokenValue string) (map[string]any, error) {
	parts := strings.Split(tokenValue, ".")
	if len(parts) != 3 {
		return nil, ErrInvalidAccessToken
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrInvalidAccessToken
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, ErrInvalidAccessToken
	}
	return claims, nil
}

func claimHasValue(claims map[string]any, name string) bool {
	value, ok := claims[name]
	return ok && value != nil
}

func tokenResource(audience []string, present bool) (string, bool) {
	if !present {
		return "", true
	}
	switch len(audience) {
	case 1:
		if !validResource(audience[0]) {
			return "", false
		}
		return audience[0], true
	default:
		return "", false
	}
}

func validResource(resource string) bool {
	if resource == "" || strings.TrimSpace(resource) != resource {
		return false
	}
	parsed, err := url.Parse(resource)
	return err == nil && parsed.IsAbs() && parsed.Fragment == ""
}

func currentOAuthPrincipal(principal *user_model.User, expectedID int64) bool {
	return principal != nil && principal.ID == expectedID
}
