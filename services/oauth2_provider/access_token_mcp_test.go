// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"strconv"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewMCPAccessTokenResponse(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test_module.MockVariableValue(&setting.AppURL, "https://forge.example/")()
	defer test_module.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test_module.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "https://forge.example")()
	defer test_module.MockVariableValue(&setting.OAuth2.InvalidateRefreshTokens, false)()
	defer test_module.MockVariableValue(&setting.OAuth2.AccessTokenExpirationTime, int64(300))()
	key := newTestSigningKey(t, "01234567890123456789012345678901")
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1})
	grant.Scope = "read:repository"
	beforeCounter := grant.Counter
	resource := setting.MCPResource()

	response, tokenErr := NewMCPAccessTokenResponse(t.Context(), grant, key, key)
	require.Nil(t, tokenErr)
	require.NotNil(t, response)
	assert.Equal(t, int64(300), response.ExpiresIn)
	assert.Equal(t, beforeCounter+1, grant.Counter)

	accessToken, err := ParseToken(response.AccessToken, key)
	require.NoError(t, err)
	assert.Equal(t, KindAccessToken, accessToken.Kind)
	assert.Equal(t, TokenIssuer(), accessToken.Issuer)
	assert.Equal(t, strconv.FormatInt(grant.UserID, 10), accessToken.Subject)
	assert.Equal(t, []string{resource}, []string(accessToken.Audience))
	assert.WithinDuration(t, time.Now().Add(5*time.Minute), accessToken.ExpiresAt.Time, 5*time.Second)

	refreshToken, err := ParseToken(response.RefreshToken, key)
	require.NoError(t, err)
	assert.Equal(t, TokenKind(KindRefreshToken), refreshToken.Kind)
	assert.Equal(t, grant.Counter, refreshToken.Counter)
	assert.Equal(t, TokenIssuer(), refreshToken.Issuer)
	assert.Equal(t, strconv.FormatInt(grant.UserID, 10), refreshToken.Subject)
	assert.Equal(t, []string{resource}, []string(refreshToken.Audience))

	staleGrant := *grant
	currentGrant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: grant.ID})
	currentGrant.Scope = "read:repository"
	_, tokenErr = NewMCPAccessTokenResponse(t.Context(), currentGrant, key, key)
	require.Nil(t, tokenErr)
	_, tokenErr = NewMCPAccessTokenResponse(t.Context(), &staleGrant, key, key)
	require.NotNil(t, tokenErr)
	assert.Equal(t, AccessTokenErrorCode(AccessTokenErrorCodeInvalidGrant), tokenErr.ErrorCode)
}

func TestLegacyAccessTokenResponseRemainsAudienceLessWithoutRotation(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test_module.MockVariableValue(&setting.OAuth2.InvalidateRefreshTokens, false)()
	key := newTestSigningKey(t, "01234567890123456789012345678901")
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1})
	beforeCounter := grant.Counter

	response, tokenErr := NewAccessTokenResponse(t.Context(), grant, key, key)
	require.Nil(t, tokenErr)
	accessToken, err := ParseToken(response.AccessToken, key)
	require.NoError(t, err)
	refreshToken, err := ParseToken(response.RefreshToken, key)
	require.NoError(t, err)
	assert.Empty(t, accessToken.Issuer)
	assert.Empty(t, accessToken.Subject)
	assert.Empty(t, accessToken.Audience)
	assert.Empty(t, refreshToken.Audience)
	assert.Equal(t, beforeCounter, grant.Counter)
}
