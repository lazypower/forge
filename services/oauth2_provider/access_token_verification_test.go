// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"context"
	"errors"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testOAuthResource = "https://forge.example/mcp"

func TestVerifyAccessToken(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "")()
	now := time.Now().UTC().Truncate(time.Second)
	signingKey := newTestSigningKey(t, "01234567890123456789012345678901")
	otherKey := newTestSigningKey(t, "abcdefghijklmnopqrstuvwxyz123456")
	activePrincipal := &user_model.User{ID: 42, IsActive: true}
	grant := &auth_model.OAuth2Grant{ID: 7, UserID: activePrincipal.ID, Scope: "read:user read:repository read:user"}
	findGrant := func(context.Context, int64) (*auth_model.OAuth2Grant, error) { return grant, nil }
	findPrincipal := func(context.Context, int64) (*user_model.User, error) { return activePrincipal, nil }

	legacyClaims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	}
	resourceClaims := legacyClaims
	resourceClaims.Issuer = TokenIssuer()
	resourceClaims.Subject = "42"
	resourceClaims.Audience = jwt.ClaimStrings{testOAuthResource}

	tests := []struct {
		name             string
		token            string
		expectedResource string
		accepted         bool
		resource         string
	}{
		{
			name:     "legacy audience-less access token",
			token:    signTestToken(t, signingKey, KindAccessToken, legacyClaims),
			accepted: true,
		},
		{
			name:             "resource-bound access token",
			token:            signTestToken(t, signingKey, KindAccessToken, resourceClaims),
			expectedResource: testOAuthResource,
			accepted:         true,
			resource:         testOAuthResource,
		},
		{
			name:             "resource mismatch",
			token:            signTestToken(t, signingKey, KindAccessToken, resourceClaims),
			expectedResource: "https://forge.example/api",
		},
		{
			name:  "missing expected resource",
			token: signTestToken(t, signingKey, KindAccessToken, resourceClaims),
		},
		{
			name:             "unbound token at resource",
			token:            signTestToken(t, signingKey, KindAccessToken, legacyClaims),
			expectedResource: testOAuthResource,
		},
		{
			name:  "malformed",
			token: "not-a-jwt",
		},
		{
			name: "expired",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(now.Add(-2 * time.Hour)),
				ExpiresAt: jwt.NewNumericDate(now.Add(-time.Hour)),
			}),
		},
		{
			name: "future issued-at",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(now.Add(time.Minute)),
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			}),
		},
		{
			name: "missing expiry",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				IssuedAt: jwt.NewNumericDate(now.Add(-time.Minute)),
			}),
		},
		{
			name: "missing issued-at",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
			}),
		},
		{
			name: "wrong issuer",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				Issuer:    "https://other.example",
				IssuedAt:  legacyClaims.IssuedAt,
				ExpiresAt: legacyClaims.ExpiresAt,
			}),
		},
		{
			name:  "wrong signature",
			token: signTestToken(t, otherKey, KindAccessToken, legacyClaims),
		},
		{
			name:  "refresh token",
			token: signTestToken(t, signingKey, KindRefreshToken, legacyClaims),
		},
		{
			name:  "unsupported token kind",
			token: signTestToken(t, signingKey, TokenKind(99), legacyClaims),
		},
		{
			name: "missing token kind",
			token: signTestClaims(t, signingKey, jwt.MapClaims{
				"gnt": 7,
				"iat": now.Add(-time.Minute).Unix(),
				"exp": now.Add(time.Hour).Unix(),
			}),
		},
		{
			name: "wrong resource subject",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				Issuer:    TokenIssuer(),
				Subject:   "7",
				Audience:  jwt.ClaimStrings{testOAuthResource},
				IssuedAt:  legacyClaims.IssuedAt,
				ExpiresAt: legacyClaims.ExpiresAt,
			}),
			expectedResource: testOAuthResource,
		},
		{
			name: "missing resource subject",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				Issuer:    TokenIssuer(),
				Audience:  jwt.ClaimStrings{testOAuthResource},
				IssuedAt:  legacyClaims.IssuedAt,
				ExpiresAt: legacyClaims.ExpiresAt,
			}),
			expectedResource: testOAuthResource,
		},
		{
			name: "missing resource issuer",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				Subject:   "42",
				Audience:  jwt.ClaimStrings{testOAuthResource},
				IssuedAt:  legacyClaims.IssuedAt,
				ExpiresAt: legacyClaims.ExpiresAt,
			}),
			expectedResource: testOAuthResource,
		},
		{
			name: "wrong resource issuer",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				Issuer:    "https://other.example",
				Subject:   "42",
				Audience:  jwt.ClaimStrings{testOAuthResource},
				IssuedAt:  legacyClaims.IssuedAt,
				ExpiresAt: legacyClaims.ExpiresAt,
			}),
			expectedResource: testOAuthResource,
		},
		{
			name: "invalid expected resource",
			token: signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
				Issuer:    TokenIssuer(),
				Subject:   "42",
				Audience:  jwt.ClaimStrings{"relative-resource"},
				IssuedAt:  legacyClaims.IssuedAt,
				ExpiresAt: legacyClaims.ExpiresAt,
			}),
			expectedResource: "relative-resource",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			verified, err := verifyAccessToken(t.Context(), test.token, test.expectedResource, signingKey, findGrant, findPrincipal, now)
			if !test.accepted {
				assert.Nil(t, verified)
				assert.ErrorIs(t, err, ErrInvalidAccessToken)
				assert.EqualError(t, err, "invalid OAuth access token")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, verified)
			assert.Same(t, activePrincipal, verified.Principal)
			assert.Equal(t, auth_model.AccessTokenScope("read:repository,read:user"), verified.Scope)
			assert.Equal(t, now.Add(time.Hour).Unix(), verified.ExpiresAt.Unix())
			assert.Equal(t, test.resource, verified.Resource)
		})
	}
}

func TestVerifyAccessTokenRejectsInvalidAudienceRepresentations(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "")()
	now := time.Now().UTC().Truncate(time.Second)
	signingKey := newTestSigningKey(t, "01234567890123456789012345678901")
	grant := &auth_model.OAuth2Grant{ID: 7, UserID: 42, Scope: "read:repository"}
	principal := &user_model.User{ID: 42, IsActive: true}
	findGrant := func(context.Context, int64) (*auth_model.OAuth2Grant, error) { return grant, nil }
	findPrincipal := func(context.Context, int64) (*user_model.User, error) { return principal, nil }

	for name, audience := range map[string]any{
		"null":             nil,
		"empty string":     "",
		"empty array":      []string{},
		"blank string":     " ",
		"duplicate":        []string{testOAuthResource, testOAuthResource},
		"multiple":         []string{testOAuthResource, "https://forge.example/api"},
		"case variant":     "https://Forge.example/mcp",
		"trailing slash":   testOAuthResource + "/",
		"relative":         "mcp",
		"fragment":         testOAuthResource + "#fragment",
		"non-string":       42,
		"non-string array": []any{testOAuthResource, 42},
	} {
		t.Run(name, func(t *testing.T) {
			token := signTestClaims(t, signingKey, jwt.MapClaims{
				"gnt": 7,
				"tt":  KindAccessToken,
				"iss": TokenIssuer(),
				"sub": "42",
				"aud": audience,
				"iat": now.Add(-time.Minute).Unix(),
				"exp": now.Add(time.Hour).Unix(),
			})
			verified, err := verifyAccessToken(t.Context(), token, testOAuthResource, signingKey, findGrant, findPrincipal, now)
			assert.Nil(t, verified)
			assert.ErrorIs(t, err, ErrInvalidAccessToken)
		})
	}
}

func TestVerifyAccessTokenPrincipalStateAndScopeFallback(t *testing.T) {
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "")()
	now := time.Now().UTC().Truncate(time.Second)
	signingKey := newTestSigningKey(t, "01234567890123456789012345678901")
	legacyToken := signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	})
	resourceToken := signTestToken(t, signingKey, KindAccessToken, jwt.RegisteredClaims{
		Issuer:    TokenIssuer(),
		Subject:   "42",
		Audience:  jwt.ClaimStrings{testOAuthResource},
		IssuedAt:  jwt.NewNumericDate(now.Add(-time.Minute)),
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
	})

	tests := []struct {
		name      string
		grant     *auth_model.OAuth2Grant
		grantErr  error
		principal *user_model.User
		userErr   error
		resource  bool
		accepted  bool
	}{
		{name: "empty scope retains full-access fallback", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42}, principal: &user_model.User{ID: 42, IsActive: true}, accepted: true},
		{name: "OIDC-only scope retains full-access fallback", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42, Scope: "openid profile email groups"}, principal: &user_model.User{ID: 42, IsActive: true}, accepted: true},
		{name: "invalid scope retains full-access fallback", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42, Scope: "invalid"}, principal: &user_model.User{ID: 42, IsActive: true}, accepted: true},
		{name: "legacy inactive principal remains available to existing gate", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42}, principal: &user_model.User{ID: 42}, accepted: true},
		{name: "legacy prohibited principal remains available to existing gate", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42}, principal: &user_model.User{ID: 42, IsActive: true, ProhibitLogin: true}, accepted: true},
		{name: "missing grant"},
		{name: "grant lookup failure", grantErr: errors.New("database unavailable")},
		{name: "missing principal", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42}},
		{name: "principal lookup failure", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42}, userErr: errors.New("database unavailable")},
		{name: "resource-bound inactive principal", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42}, principal: &user_model.User{ID: 42}, resource: true},
		{name: "resource-bound prohibited principal", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42}, principal: &user_model.User{ID: 42, IsActive: true, ProhibitLogin: true}, resource: true},
		{name: "principal changed", grant: &auth_model.OAuth2Grant{ID: 7, UserID: 42}, principal: &user_model.User{ID: 43, IsActive: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			findGrant := func(context.Context, int64) (*auth_model.OAuth2Grant, error) { return test.grant, test.grantErr }
			findPrincipal := func(context.Context, int64) (*user_model.User, error) { return test.principal, test.userErr }
			token, expectedResource := legacyToken, ""
			if test.resource {
				token, expectedResource = resourceToken, testOAuthResource
			}
			verified, err := verifyAccessToken(t.Context(), token, expectedResource, signingKey, findGrant, findPrincipal, now)
			if !test.accepted {
				assert.Nil(t, verified)
				assert.ErrorIs(t, err, ErrInvalidAccessToken)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, auth_model.AccessTokenScopeAll, verified.Scope)
		})
	}
}

func newTestSigningKey(t *testing.T, secret string) JWTSigningKey {
	t.Helper()
	key, err := CreateJWTSigningKey("HS256", []byte(secret))
	require.NoError(t, err)
	return key
}

func signTestToken(t *testing.T, signingKey JWTSigningKey, kind TokenKind, claims jwt.RegisteredClaims) string {
	t.Helper()
	return signTestClaims(t, signingKey, &Token{GrantID: 7, Kind: kind, RegisteredClaims: claims})
}

func signTestClaims(t *testing.T, signingKey JWTSigningKey, claims jwt.Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(signingKey.SigningMethod(), claims)
	signingKey.PreProcessToken(token)
	signed, err := token.SignedString(signingKey.SignKey())
	require.NoError(t, err)
	return signed
}
