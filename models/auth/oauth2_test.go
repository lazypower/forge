// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth_test

import (
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

func TestOAuth2AuthorizationCode(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())

	t.Run("GenerateSetsValidUntil", func(t *testing.T) {
		grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1})
		expectedValidUntil := timeutil.TimeStamp(time.Now().Unix() + 600)
		code, err := grant.GenerateNewAuthorizationCode(t.Context(), "http://127.0.0.1/", "", "", "https://forge.example/mcp")
		require.NoError(t, err)
		assert.Equal(t, expectedValidUntil, code.ValidUntil)
		assert.False(t, code.IsExpired())
		assert.Positive(t, code.ID)

		code2, err := auth_model.GetOAuth2AuthorizationByCode(t.Context(), code.Code)
		require.NoError(t, err)
		assert.Equal(t, code.Code, code2.Code)
		assert.Equal(t, "https://forge.example/mcp", code2.Resource)
		assert.Equal(t, grant.Scope, code2.Grant.Scope)
		_, grantHasResource := reflect.TypeFor[auth_model.OAuth2Grant]().FieldByName("Resource")
		assert.False(t, grantHasResource)

		assert.NoError(t, code.Invalidate(t.Context()))

		code, err = auth_model.GetOAuth2AuthorizationByCode(t.Context(), "does not exist")
		require.NoError(t, err)
		require.Nil(t, code)
	})

	t.Run("Expired", func(t *testing.T) {
		defer timeutil.MockSet(time.Unix(2, 0).UTC())()

		code := &auth_model.OAuth2AuthorizationCode{ValidUntil: timeutil.TimeStamp(1)}
		assert.True(t, code.IsExpired())
	})

	t.Run("Invalidate", func(t *testing.T) {
		grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1})
		code, err := grant.GenerateNewAuthorizationCode(t.Context(), "http://127.0.0.1/", "", "", "")
		require.NoError(t, err)
		require.NotNil(t, code)
		require.NoError(t, code.Invalidate(t.Context()))
		unittest.AssertNotExistsBean(t, &auth_model.OAuth2AuthorizationCode{Code: code.Code})
		assert.ErrorIs(t, code.Invalidate(t.Context()), auth_model.ErrOAuth2AuthorizationCodeInvalidated)
	})
}

func TestMCPClientRegistrationLifecycle(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	now := time.Now().UTC().Truncate(time.Second)
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Planning harness", "laptop", []string{"http://127.0.0.1/callback"}, auth_model.MCPRedirectClassLoopback, now.Add(30*time.Minute), 2)
	require.NoError(t, err)
	assert.True(t, app.IsMCPClientRegistration())
	assert.Equal(t, auth_model.MCPRegistrationStateProvisional, app.MCPRegistrationState)
	assert.True(t, strings.HasPrefix(app.ClientID, "mcp_"))
	assert.GreaterOrEqual(t, len(app.ClientID), 50)
	assert.Empty(t, app.ClientSecret)
	_, err = app.GenerateClientSecret(t.Context())
	assert.ErrorContains(t, err, "cannot have a client secret")
	assert.True(t, app.ContainsMCPRedirectURI("http://127.0.0.1:49152/callback"))
	assert.False(t, app.ContainsMCPRedirectURI("http://127.0.0.1:49152/Callback"))
	grant, err := app.GetGrantByUserID(t.Context(), 1)
	require.NoError(t, err)
	assert.Nil(t, grant)

	finalized, grant, code, err := auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, 1, "read:repository", "", "http://127.0.0.1:49152/callback", "challenge", "S256", "https://forge.example/mcp", now)
	require.NoError(t, err)
	assert.Equal(t, auth_model.MCPRegistrationStateFinalized, finalized.MCPRegistrationState)
	assert.Equal(t, int64(1), finalized.MCPBoundUserID)
	require.NotNil(t, grant)
	require.NotNil(t, code)
	_, _, _, err = auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, 2, "read:repository", "", "http://127.0.0.1:49152/callback", "challenge", "S256", "https://forge.example/mcp", now)
	assert.ErrorIs(t, err, auth_model.ErrMCPRegistrationWrongPrincipal)

	_, err = auth_model.UpdateOAuth2Application(t.Context(), auth_model.UpdateOAuth2ApplicationOptions{ID: app.ID, UserID: 0, Name: "changed"})
	assert.ErrorContains(t, err, "metadata is immutable")
	assert.ErrorIs(t, auth_model.DeleteFinalizedMCPRegistration(t.Context(), app.ID, 1), auth_model.ErrMCPRegistrationHasGrant)
	require.NoError(t, auth_model.RevokeOAuth2Grant(t.Context(), grant.ID, 1))
	reloadedCode, err := auth_model.GetOAuth2AuthorizationByCode(t.Context(), code.Code)
	require.NoError(t, err)
	assert.Nil(t, reloadedCode)
	registrations, err := auth_model.ListUngrantFinalizedMCPRegistrations(t.Context(), 1)
	require.NoError(t, err)
	require.Len(t, registrations, 1)
	assert.ErrorIs(t, auth_model.DeleteFinalizedMCPRegistration(t.Context(), app.ID, 2), auth_model.ErrMCPRegistrationWrongPrincipal)
	require.NoError(t, auth_model.DeleteFinalizedMCPRegistration(t.Context(), app.ID, 1))
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: app.ID})
}

func TestMCPSharedClientsAbsentFromFreshInitialization(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	for _, clientID := range []string{
		"f16c9e54-1f8b-4a9c-9b62-70d8d46f0e31",
		"92e7ae67-8fae-4d6f-a122-0e2f8b82ef1a",
	} {
		_, configured := auth_model.BuiltinApplications()[clientID]
		assert.False(t, configured)
		_, err := auth_model.GetOAuth2ApplicationByClientID(t.Context(), clientID)
		assert.True(t, auth_model.IsErrOauthClientIDInvalid(err))
	}
}

func TestMCPClientRegistrationCapacityExpiryAndCleanup(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	now := time.Now().UTC().Truncate(time.Second)
	for range 2 {
		_, err := auth_model.CreateMCPClientRegistration(t.Context(), "Harness", "", []string{"https://client.example/callback"}, auth_model.MCPRedirectClassHTTPS, now.Add(-time.Minute), 2)
		require.NoError(t, err)
	}
	_, err := auth_model.CreateMCPClientRegistration(t.Context(), "Harness", "", []string{"https://client.example/callback"}, auth_model.MCPRedirectClassHTTPS, now.Add(time.Minute), 2)
	assert.ErrorIs(t, err, auth_model.ErrMCPRegistrationCapacity)
	deleted, err := auth_model.CleanupExpiredMCPClientRegistrations(t.Context(), now, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	_, err = auth_model.CreateMCPClientRegistration(t.Context(), "Harness", "", []string{"https://client.example/callback"}, auth_model.MCPRedirectClassHTTPS, now.Add(time.Minute), 2)
	require.NoError(t, err)
	deleted, err = auth_model.CleanupExpiredMCPClientRegistrations(t.Context(), now, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, deleted)
	deleted, err = auth_model.CleanupExpiredMCPClientRegistrations(t.Context(), now, 10)
	require.NoError(t, err)
	assert.Zero(t, deleted)
}

func TestMCPClientRegistrationExpiryDuringConsentCreatesNoGrant(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	now := time.Now().UTC().Truncate(time.Second)
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Harness", "", []string{"http://127.0.0.1/callback"}, auth_model.MCPRedirectClassLoopback, now.Add(-time.Second), 10)
	require.NoError(t, err)
	_, _, _, err = auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, 1, "read:repository", "", "http://127.0.0.1:49152/callback", "challenge", "S256", "https://forge.example/mcp", now)
	assert.ErrorIs(t, err, auth_model.ErrMCPRegistrationExpired)
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: app.ID})
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ApplicationID: app.ID})
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2AuthorizationCode{})
}

func TestMCPClientRegistrationExactRedirectMatching(t *testing.T) {
	httpsApp := &auth_model.OAuth2Application{
		MCPRegistrationState: auth_model.MCPRegistrationStateFinalized,
		MCPRedirectClass:     auth_model.MCPRedirectClassHTTPS,
		RedirectURIs:         []string{"https://client.example/Callback?channel=A"},
	}
	assert.True(t, httpsApp.ContainsMCPRedirectURI("https://client.example/Callback?channel=A"))
	for _, changed := range []string{
		"https://client.example/callback?channel=A",
		"https://client.example/Callback/?channel=A",
		"https://client.example/Callback?channel=a",
		"https://CLIENT.example/Callback?channel=A",
		"https://client.example/%43allback?channel=A",
		"https://client.example/path/../Callback?channel=A",
	} {
		assert.False(t, httpsApp.ContainsMCPRedirectURI(changed), changed)
	}

	loopback := &auth_model.OAuth2Application{
		MCPRegistrationState: auth_model.MCPRegistrationStateFinalized,
		MCPRedirectClass:     auth_model.MCPRedirectClassLoopback,
		RedirectURIs:         []string{"http://127.0.0.1:49151/Callback?channel=A"},
	}
	assert.True(t, loopback.ContainsMCPRedirectURI("http://127.0.0.1:49152/Callback?channel=A"))
	for _, changed := range []string{
		"http://127.0.0.1:49152/callback?channel=A",
		"http://127.0.0.1:49152/Callback/?channel=A",
		"http://127.0.0.1:49152/Callback?channel=a",
		"http://127.0.0.2:49152/Callback?channel=A",
	} {
		assert.False(t, loopback.ContainsMCPRedirectURI(changed), changed)
	}

	localhost := &auth_model.OAuth2Application{
		MCPRegistrationState: auth_model.MCPRegistrationStateFinalized,
		MCPRedirectClass:     auth_model.MCPRedirectClassLoopback,
		RedirectURIs:         []string{"http://localhost:49151/Callback?channel=A"},
	}
	assert.True(t, localhost.ContainsMCPRedirectURI("http://localhost:49152/Callback?channel=A"))
	for _, changed := range []string{
		"http://127.0.0.1:49152/Callback?channel=A",
		"http://[::1]:49152/Callback?channel=A",
		"http://LOCALHOST:49152/Callback?channel=A",
		"http://localhost:49152/callback?channel=A",
		"http://localhost:49152/Callback?channel=a",
	} {
		assert.False(t, localhost.ContainsMCPRedirectURI(changed), changed)
	}
}

func TestMCPClientRegistrationConcurrentFirstApproval(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	now := time.Now().UTC().Truncate(time.Second)
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Harness", "", []string{"http://127.0.0.1/callback"}, auth_model.MCPRedirectClassLoopback, now.Add(30*time.Minute), 10)
	require.NoError(t, err)
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for _, userID := range []int64{1, 2} {
		wait.Go(func() {
			<-start
			_, _, _, err := auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, userID, "read:repository", "", "http://127.0.0.1:49152/callback", "challenge", "S256", "https://forge.example/mcp", now)
			results <- err
		})
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
			continue
		}
		assert.True(t, errors.Is(err, auth_model.ErrMCPRegistrationNotProvisional) || errors.Is(err, auth_model.ErrMCPRegistrationWrongPrincipal), err)
	}
	assert.Equal(t, 1, succeeded)
	assert.Equal(t, 1, unittest.GetCount(t, &auth_model.OAuth2Grant{ApplicationID: app.ID}))
}

func TestMCPClientRegistrationReconnectDeleteRace(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	now := time.Now().UTC().Truncate(time.Second)
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Harness", "", []string{"http://127.0.0.1/callback"}, auth_model.MCPRedirectClassLoopback, now.Add(30*time.Minute), 10)
	require.NoError(t, err)
	app, grant, _, err := auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, 1, "read:repository", "", "http://127.0.0.1:49152/callback", "challenge", "S256", "https://forge.example/mcp", now)
	require.NoError(t, err)
	require.NoError(t, auth_model.RevokeOAuth2Grant(t.Context(), grant.ID, 1))

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Go(func() {
		<-start
		_, _, _, err := auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, 1, "read:repository", "", "http://127.0.0.1:49152/callback", "challenge", "S256", "https://forge.example/mcp", now)
		results <- err
	})
	wait.Go(func() {
		<-start
		results <- auth_model.DeleteFinalizedMCPRegistration(t.Context(), app.ID, 1)
	})
	close(start)
	wait.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	assert.Equal(t, 1, succeeded)
	appCount := unittest.GetCount(t, &auth_model.OAuth2Application{ID: app.ID})
	grantCount := unittest.GetCount(t, &auth_model.OAuth2Grant{ApplicationID: app.ID})
	assert.True(t, (appCount == 1 && grantCount == 1) || (appCount == 0 && grantCount == 0))
}

func TestOAuth2Application_GenerateClientSecret(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1})
	secret, err := app.GenerateClientSecret(t.Context())
	assert.NoError(t, err)
	assert.NotEmpty(t, secret)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1, ClientSecret: app.ClientSecret})
}

func BenchmarkOAuth2Application_GenerateClientSecret(b *testing.B) {
	assert.NoError(b, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(b, &auth_model.OAuth2Application{ID: 1})
	for b.Loop() {
		_, _ = app.GenerateClientSecret(b.Context())
	}
}

func TestOAuth2Application_ContainsRedirectURI(t *testing.T) {
	app := &auth_model.OAuth2Application{
		RedirectURIs: []string{"a", "b", "c"},
	}
	assert.True(t, app.ContainsRedirectURI("a"))
	assert.True(t, app.ContainsRedirectURI("b"))
	assert.True(t, app.ContainsRedirectURI("c"))
	assert.False(t, app.ContainsRedirectURI("d"))
}

func TestOAuth2Application_ContainsRedirectURI_WithPort(t *testing.T) {
	app := &auth_model.OAuth2Application{
		RedirectURIs:       []string{"http://127.0.0.1/", "http://::1/", "http://192.168.0.1/", "http://intranet/", "https://127.0.0.1/"},
		ConfidentialClient: false,
	}

	// http loopback uris should ignore port
	// https://datatracker.ietf.org/doc/html/rfc8252#section-7.3
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1:3456/"))
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1/"))
	assert.True(t, app.ContainsRedirectURI("http://[::1]:3456/"))

	// not http
	assert.False(t, app.ContainsRedirectURI("https://127.0.0.1:3456/"))
	// not loopback
	assert.False(t, app.ContainsRedirectURI("http://192.168.0.1:9954/"))
	assert.False(t, app.ContainsRedirectURI("http://intranet:3456/"))
	// unparseable
	assert.False(t, app.ContainsRedirectURI(":"))
}

func TestOAuth2Application_ContainsRedirect_Slash(t *testing.T) {
	app := &auth_model.OAuth2Application{RedirectURIs: []string{"http://127.0.0.1"}}
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1"))
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1/"))
	assert.False(t, app.ContainsRedirectURI("http://127.0.0.1/other"))

	app = &auth_model.OAuth2Application{RedirectURIs: []string{"http://127.0.0.1/"}}
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1"))
	assert.True(t, app.ContainsRedirectURI("http://127.0.0.1/"))
	assert.False(t, app.ContainsRedirectURI("http://127.0.0.1/other"))
}

func TestOAuth2Application_ContainsRedirectURI_ASCIIOnlyNormalization(t *testing.T) {
	testCases := []struct {
		name        string
		registered  []string
		redirectURI string
		allowed     bool
	}{
		{
			name:        "exact-match",
			registered:  []string{"https://signin.example.test/callback"},
			redirectURI: "https://signin.example.test/callback",
			allowed:     true,
		},
		{
			name:        "ascii-case-insensitive",
			registered:  []string{"https://signin.example.test/callback"},
			redirectURI: "https://signIN.example.test/callback",
			allowed:     true,
		},
		{
			name:        "non-ascii-not-folded",
			registered:  []string{"https://signin.example.test/callback"},
			redirectURI: "https://signİn.example.test/callback",
			allowed:     false,
		},
		{
			name:        "loopback-strips-port",
			registered:  []string{"http://127.0.0.1/callback"},
			redirectURI: "http://127.0.0.1:12345/callback",
			allowed:     true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			app := &auth_model.OAuth2Application{RedirectURIs: tc.registered}
			assert.Equal(t, tc.allowed, app.ContainsRedirectURI(tc.redirectURI))
		})
	}
}

func TestOAuth2Application_ValidateClientSecret(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1})
	secret, err := app.GenerateClientSecret(t.Context())
	assert.NoError(t, err)
	assert.True(t, app.ValidateClientSecret([]byte(secret)))
	assert.False(t, app.ValidateClientSecret([]byte("fewijfowejgfiowjeoifew")))
}

func TestGetOAuth2ApplicationByClientID(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	app, err := auth_model.GetOAuth2ApplicationByClientID(t.Context(), "da7da3ba-9a13-4167-856f-3899de0b0138")
	assert.NoError(t, err)
	assert.Equal(t, "da7da3ba-9a13-4167-856f-3899de0b0138", app.ClientID)

	app, err = auth_model.GetOAuth2ApplicationByClientID(t.Context(), "invalid client id")
	assert.Error(t, err)
	assert.Nil(t, app)
}

func TestCreateOAuth2Application(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	app, err := auth_model.CreateOAuth2Application(t.Context(), auth_model.CreateOAuth2ApplicationOptions{Name: "newapp", UserID: 1})
	assert.NoError(t, err)
	assert.Equal(t, "newapp", app.Name)
	assert.Len(t, app.ClientID, 36)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{Name: "newapp"})
}

func TestOAuth2Application_TableName(t *testing.T) {
	assert.Equal(t, "oauth2_application", new(auth_model.OAuth2Application).TableName())
}

func TestOAuth2Application_GetGrantByUserID(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1})
	grant, err := app.GetGrantByUserID(t.Context(), 1)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), grant.UserID)

	grant, err = app.GetGrantByUserID(t.Context(), 34923458)
	assert.NoError(t, err)
	assert.Nil(t, grant)
}

func TestOAuth2Application_CreateGrant(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	app := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: 1})
	grant, err := app.CreateGrant(t.Context(), 2, "")
	assert.NoError(t, err)
	assert.NotNil(t, grant)
	assert.Equal(t, int64(2), grant.UserID)
	assert.Equal(t, int64(1), grant.ApplicationID)
	assert.Empty(t, grant.Scope)
}

//////////////////// Grant

func TestGetOAuth2GrantByID(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	grant, err := auth_model.GetOAuth2GrantByID(t.Context(), 1)
	assert.NoError(t, err)
	assert.Equal(t, int64(1), grant.ID)

	grant, err = auth_model.GetOAuth2GrantByID(t.Context(), 34923458)
	assert.NoError(t, err)
	assert.Nil(t, grant)
}

func TestOAuth2Grant_IncreaseCounter(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1, Counter: 1})
	assert.NoError(t, grant.IncreaseCounter(t.Context()))
	assert.Equal(t, int64(2), grant.Counter)
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1, Counter: 2})
}

func TestOAuth2Grant_IncreaseCounterRejectsStaleCounter(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1, Counter: 1})
	stale := *grant

	assert.NoError(t, grant.IncreaseCounter(t.Context()))
	err := stale.IncreaseCounter(t.Context())
	assert.ErrorIs(t, err, auth_model.ErrOAuth2GrantStaleCounter)
}

func TestOAuth2Grant_ScopeContains(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1, Scope: "openid profile"})
	assert.True(t, grant.ScopeContains("openid"))
	assert.True(t, grant.ScopeContains("profile"))
	assert.False(t, grant.ScopeContains("profil"))
	assert.False(t, grant.ScopeContains("profile2"))
}

func TestOAuth2Grant_GenerateNewAuthorizationCode(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: 1})
	code, err := grant.GenerateNewAuthorizationCode(t.Context(), "https://example2.com/callback", "CjvyTLSdR47G5zYenDA-eDWW4lRrO8yvjcWwbD_deOg", "S256", "https://forge.example/mcp")
	assert.NoError(t, err)
	assert.NotNil(t, code)
	assert.Greater(t, len(code.Code), 32) // secret length > 32
	assert.Equal(t, "https://forge.example/mcp", code.Resource)
}

func TestOAuth2Grant_TableName(t *testing.T) {
	assert.Equal(t, "oauth2_grant", new(auth_model.OAuth2Grant).TableName())
}

func TestGetOAuth2GrantsByUserID(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	result, err := auth_model.GetOAuth2GrantsByUserID(t.Context(), 1)
	assert.NoError(t, err)
	assert.Len(t, result, 1)
	assert.Equal(t, int64(1), result[0].ID)
	assert.Equal(t, result[0].ApplicationID, result[0].Application.ID)

	result, err = auth_model.GetOAuth2GrantsByUserID(t.Context(), 34134)
	assert.NoError(t, err)
	assert.Empty(t, result)
}

func TestRevokeOAuth2Grant(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	assert.NoError(t, auth_model.RevokeOAuth2Grant(t.Context(), 1, 1))
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ID: 1, UserID: 1})
}

//////////////////// Authorization Code

func TestOAuth2AuthorizationCode_ValidateCodeChallenge(t *testing.T) {
	s256Verifier := "s256-verifier"
	s256Challenge := oauth2.S256ChallengeFromVerifier(s256Verifier)
	missingVerifierChallenge := oauth2.S256ChallengeFromVerifier("verifier-not-supplied")

	testCases := []struct {
		name      string
		method    string
		challenge string
		verifier  string
		valid     bool
	}{
		{"plain-success", "plain", "plain-secret", "plain-secret", true},
		{"plain-failure", "plain", "plain-secret", "ierwgjoergjio", false},
		{"s256-success", "S256", s256Challenge, s256Verifier, true},
		{"s256-failure", "S256", s256Challenge, "wiogjerogorewngoenrgoiuenorg", false},
		{"unsupported-method", "monkey", "foiwgjioriogeiogjerger", "foiwgjioriogeiogjerger", false},
		{"no-pkce-configured", "", "", "", true},
		{"s256-missing-verifier", "S256", missingVerifierChallenge, "", false},
		{"plain-missing-verifier", "plain", "plain-secret", "", false},
		{"missing-method-with-challenge", "", "foierjiogerogerg", "", false},
		{"missing-method-rejects-even-matching-verifier", "", "foierjiogerogerg", "foierjiogerogerg", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			code := &auth_model.OAuth2AuthorizationCode{
				CodeChallengeMethod: tc.method,
				CodeChallenge:       tc.challenge,
			}
			assert.Equal(t, tc.valid, code.ValidateCodeChallenge(tc.verifier))
		})
	}
}

func TestOAuth2AuthorizationCode_GenerateRedirectURI(t *testing.T) {
	code := &auth_model.OAuth2AuthorizationCode{
		RedirectURI: "https://example.com/callback",
		Code:        "thecode",
	}

	redirect, err := code.GenerateRedirectURI("thestate")
	assert.NoError(t, err)
	assert.Equal(t, "https://example.com/callback?code=thecode&state=thestate", redirect.String())

	redirect, err = code.GenerateRedirectURI("")
	assert.NoError(t, err)
	assert.Equal(t, "https://example.com/callback?code=thecode", redirect.String())
}

func TestOAuth2AuthorizationCode_TableName(t *testing.T) {
	assert.Equal(t, "oauth2_authorization_code", new(auth_model.OAuth2AuthorizationCode).TableName())
}
