// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPClientRegistrationConcurrentCleanup(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	now := time.Now().UTC().Truncate(time.Second)
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Cleanup harness", "", []string{"https://client.example/callback"}, auth_model.MCPRedirectClassHTTPS, now.Add(-time.Minute), 1)
	require.NoError(t, err)
	start := make(chan struct{})
	var wait sync.WaitGroup
	var deleted [2]int
	var failures [2]error
	for i := range 2 {
		wait.Go(func() {
			<-start
			deleted[i], failures[i] = auth_model.CleanupExpiredMCPClientRegistrations(t.Context(), now, 1)
		})
	}
	close(start)
	wait.Wait()
	for _, err := range failures {
		assert.True(t, err == nil || errors.Is(err, auth_model.ErrMCPRegistrationNotProvisional), "%v", err)
	}
	assert.Equal(t, 1, deleted[0]+deleted[1])
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: app.ID})
	assert.Zero(t, unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2MCPRegistrationAdmission{ID: 1}).Outstanding)
	deletedAgain, err := auth_model.CleanupExpiredMCPClientRegistrations(t.Context(), now, 1)
	require.NoError(t, err)
	assert.Zero(t, deletedAgain)
}

func TestMCPClientRegistrationCleanupApprovalRace(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	require.NoError(t, auth_model.Init(t.Context()))
	now := time.Now().UTC().Truncate(time.Second)
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Cleanup consent harness", "", []string{"https://client.example/callback"}, auth_model.MCPRedirectClassHTTPS, now.Add(time.Minute), 1)
	require.NoError(t, err)
	start := make(chan struct{})
	var wait sync.WaitGroup
	var approvalErr, cleanupErr error
	var deleted int
	wait.Go(func() {
		<-start
		// Consent started before expiry; cleanup observes the later expiry boundary.
		_, _, _, approvalErr = auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, 1, "read:repository", "", "https://client.example/callback", "challenge", "S256", "https://forge.example/mcp", now)
	})
	wait.Go(func() {
		<-start
		deleted, cleanupErr = auth_model.CleanupExpiredMCPClientRegistrations(t.Context(), now.Add(2*time.Minute), 1)
	})
	close(start)
	wait.Wait()
	assert.True(t, cleanupErr == nil || errors.Is(cleanupErr, auth_model.ErrMCPRegistrationNotProvisional), "%v", cleanupErr)
	if approvalErr == nil {
		assert.Zero(t, deleted)
		unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: app.ID, MCPRegistrationState: auth_model.MCPRegistrationStateFinalized, MCPBoundUserID: 1})
		grant := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ApplicationID: app.ID, UserID: 1})
		assert.Equal(t, 1, unittest.GetCount(t, &auth_model.OAuth2AuthorizationCode{GrantID: grant.ID}))
	} else {
		assert.True(t, auth_model.IsErrOAuthApplicationNotFound(approvalErr) || errors.Is(approvalErr, auth_model.ErrMCPRegistrationNotProvisional), "%v", approvalErr)
		assert.Equal(t, 1, deleted)
		unittest.AssertNotExistsBean(t, &auth_model.OAuth2Application{ID: app.ID})
		unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ApplicationID: app.ID})
		unittest.AssertNotExistsBean(t, &auth_model.OAuth2AuthorizationCode{})
	}
	assert.Zero(t, unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2MCPRegistrationAdmission{ID: 1}).Outstanding)
	deletedAgain, err := auth_model.CleanupExpiredMCPClientRegistrations(t.Context(), now.Add(2*time.Minute), 1)
	require.NoError(t, err)
	assert.Zero(t, deletedAgain)
}
