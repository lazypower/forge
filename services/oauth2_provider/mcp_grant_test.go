// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package oauth2_provider

import (
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mcpSigningFailure struct {
	JWTSigningKey
	calls, failOn int
}

func (key *mcpSigningFailure) SignKey() any {
	key.calls++
	if key.calls == key.failOn {
		return struct{}{}
	}
	return key.JWTSigningKey.SignKey()
}

func prepareMCPGrantService(t *testing.T) (*auth_model.OAuth2Application, *auth_model.OAuth2Grant, JWTSigningKey) {
	t.Helper()
	require.NoError(t, unittest.PrepareTestDatabase())
	t.Cleanup(test_module.MockVariableValue(&setting.AppURL, "https://forge.example/"))
	t.Cleanup(test_module.MockVariableValue(&setting.MCP.Enabled, true))
	t.Cleanup(test_module.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth))
	t.Cleanup(test_module.MockVariableValue(&setting.MCP.WorkMutationEnabled, true))
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Harness", "Installation", []string{"http://127.0.0.1/callback"}, auth_model.MCPRedirectClassLoopback, time.Now().Add(30*time.Minute), 10)
	require.NoError(t, err)
	app, grant, _, err := auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, 1, MCPReadScope, "", "http://127.0.0.1:49152/callback", "challenge", "S256", setting.MCPResource(), time.Now())
	require.NoError(t, err)
	return app, grant, newTestSigningKey(t, "01234567890123456789012345678901")
}

func TestMCPGrantCredentialRotationTime(t *testing.T) {
	app, grant, key := prepareMCPGrantService(t)
	assert.Zero(t, grant.CredentialRotatedUnix)
	_, tokenErr := NewMCPAccessTokenResponse(t.Context(), app, grant, key, key)
	require.Nil(t, tokenErr)
	assert.WithinDuration(t, time.Now(), grant.CredentialRotatedUnix.AsTime(), 2*time.Second)
	// A known earlier issuance distinguishes real rotation from unrelated grant updates without sleeping.
	grant.CredentialRotatedUnix = timeutil.TimeStamp(1000)
	_, err := db.GetEngine(t.Context()).ID(grant.ID).Cols("credential_rotated_unix").Update(grant)
	require.NoError(t, err)
	require.NoError(t, grant.SetNonce(t.Context(), "later consent"))
	before := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: grant.ID})
	assert.Equal(t, timeutil.TimeStamp(1000), before.CredentialRotatedUnix)
	for _, failOn := range []int{1, 2} {
		brokenKey := &mcpSigningFailure{JWTSigningKey: key, failOn: failOn}
		response, tokenErr := NewMCPAccessTokenResponse(t.Context(), app, grant, brokenKey, key)
		require.NotNil(t, tokenErr)
		assert.Nil(t, response)
		assert.Equal(t, before, unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: grant.ID}))
		assert.Equal(t, before.Counter, grant.Counter)
		assert.Equal(t, before.CredentialRotatedUnix, grant.CredentialRotatedUnix)
	}
	_, tokenErr = NewMCPAccessTokenResponse(t.Context(), app, grant, key, key)
	require.Nil(t, tokenErr)
	after := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: grant.ID})
	assert.Greater(t, after.CredentialRotatedUnix, before.CredentialRotatedUnix)
	assert.Equal(t, before.Counter+1, after.Counter)
}

type mcpPausedSigningKey struct {
	JWTSigningKey
	started, resume chan struct{}
	calls           int
}

func (key *mcpPausedSigningKey) SignKey() any {
	key.calls++
	if key.calls == 2 {
		close(key.started)
		<-key.resume
	}
	return key.JWTSigningKey.SignKey()
}

func TestMCPGrantIssuanceRace(t *testing.T) {
	for _, revoke := range []bool{false, true} {
		name := "replace"
		if revoke {
			name = "revoke"
		}
		t.Run(name, func(t *testing.T) {
			app, grant, key := prepareMCPGrantService(t)
			old, tokenErr := NewMCPAccessTokenResponse(t.Context(), app, grant, key, key)
			require.Nil(t, tokenErr)
			paused := &mcpPausedSigningKey{JWTSigningKey: key, started: make(chan struct{}), resume: make(chan struct{})}
			result := make(chan *AccessTokenError, 1)
			go func() {
				_, tokenErr := NewMCPAccessTokenResponse(t.Context(), app, grant, paused, key)
				result <- tokenErr
			}()
			select {
			case <-paused.started:
			case <-time.After(5 * time.Second):
				close(paused.resume)
				t.Fatal("issuance did not reach refresh signing")
			}
			var err error
			if revoke {
				err = auth_model.RevokeOAuth2Grant(t.Context(), grant.ID, grant.UserID)
			} else {
				_, _, _, err = auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, grant.UserID, MCPWorkWriteScope, "", "http://127.0.0.1:49152/callback", "challenge", "S256", setting.MCPResource(), time.Now())
			}
			close(paused.resume)
			require.NoError(t, err)
			tokenErr = <-result
			require.NotNil(t, tokenErr)
			assert.Equal(t, AccessTokenErrorCode(AccessTokenErrorCodeInvalidGrant), tokenErr.ErrorCode)
			_, err = VerifyAccessToken(t.Context(), old.AccessToken, setting.MCPResource(), key)
			assert.ErrorIs(t, err, ErrInvalidAccessToken)
		})
	}
}
