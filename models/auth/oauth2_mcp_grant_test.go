// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth_test

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm"
	"xorm.io/xorm/contexts"
)

const mcpWorkScope = "read:repository write:issue write:repository"

type mcpGrantSQLKey struct{}

type mcpGrantSQLProbe struct {
	verb, table     string
	err             error
	started, resume chan struct{}
}

type mcpGrantSQLHook struct{}

func (mcpGrantSQLHook) BeforeProcess(hook *contexts.ContextHook) (context.Context, error) {
	probe, _ := hook.Ctx.Value(mcpGrantSQLKey{}).(*mcpGrantSQLProbe)
	if probe == nil || !strings.HasPrefix(hook.SQL, probe.verb) || !strings.Contains(hook.SQL, probe.table) {
		return hook.Ctx, nil
	}
	if probe.started != nil {
		close(probe.started)
		select {
		case <-probe.resume:
		case <-hook.Ctx.Done():
			return hook.Ctx, hook.Ctx.Err()
		}
	}
	return hook.Ctx, probe.err
}

func (mcpGrantSQLHook) AfterProcess(*contexts.ContextHook) error { return nil }

var installMCPGrantSQLHook sync.Once

func mcpGrantProbeContext(t *testing.T, probe *mcpGrantSQLProbe) context.Context {
	t.Helper()
	installMCPGrantSQLHook.Do(func() { unittest.GetXORMEngine().AddHook(mcpGrantSQLHook{}) })
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)
	return context.WithValue(ctx, mcpGrantSQLKey{}, probe)
}

func approveMCPGrant(ctx context.Context, appID int64, scope string) (*auth_model.OAuth2Application, *auth_model.OAuth2Grant, *auth_model.OAuth2AuthorizationCode, error) {
	return auth_model.ApproveMCPClientRegistration(ctx, appID, 1, scope, "consent nonce", "http://127.0.0.1:49152/callback", "challenge", "S256", "https://forge.example/mcp", time.Now())
}

func createMCPGrant(t *testing.T, scope string) (*auth_model.OAuth2Application, *auth_model.OAuth2Grant, *auth_model.OAuth2AuthorizationCode) {
	t.Helper()
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "Same harness", "Same installation label", []string{"http://127.0.0.1/callback"}, auth_model.MCPRedirectClassLoopback, time.Now().Add(30*time.Minute), 10)
	require.NoError(t, err)
	app, grant, code, err := approveMCPGrant(t.Context(), app.ID, scope)
	require.NoError(t, err)
	return app, grant, code
}

func TestMCPGrantReplacementRollback(t *testing.T) {
	for _, oldScope := range []string{"read:repository", mcpWorkScope} {
		for _, table := range []string{"oauth2_grant", "oauth2_authorization_code"} {
			t.Run(oldScope+"/"+table, func(t *testing.T) {
				require.NoError(t, unittest.PrepareTestDatabase())
				app, old, code := createMCPGrant(t, oldScope)
				require.NoError(t, old.RotateMCPCredentials(t.Context()))
				before := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: old.ID})
				newScope := mcpWorkScope
				if oldScope == mcpWorkScope {
					newScope = "read:repository"
				}
				failure := errors.New("injected replacement insert failure")
				ctx := mcpGrantProbeContext(t, &mcpGrantSQLProbe{verb: "INSERT", table: table, err: failure})
				err := db.WithTx(ctx, func(ctx context.Context) error {
					// Ordinary transactions use the engine's default SQL context; bind this probe explicitly.
					db.GetEngine(ctx).(*xorm.Session).Context(ctx)
					_, _, _, err := approveMCPGrant(ctx, app.ID, newScope)
					return err
				})
				require.ErrorIs(t, err, failure)
				after := unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: old.ID})
				assert.Equal(t, before, after)
				assert.Equal(t, 1, unittest.GetCount(t, &auth_model.OAuth2Grant{ApplicationID: app.ID}))
				unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2AuthorizationCode{ID: code.ID, GrantID: old.ID})
				unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Application{ID: app.ID, MCPRegistrationState: auth_model.MCPRegistrationStateFinalized})
				require.NoError(t, after.RotateMCPCredentials(t.Context()))
			})
		}
	}
}

func TestMCPGrantReplacementRejectsStaleRotation(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	app, old, oldCode := createMCPGrant(t, "read:repository")
	otherApp, other, otherCode := createMCPGrant(t, "read:repository")
	probe := &mcpGrantSQLProbe{verb: "UPDATE", table: "oauth2_grant", started: make(chan struct{}), resume: make(chan struct{})}
	ctx := mcpGrantProbeContext(t, probe)
	result := make(chan error, 1)
	go func() { result <- old.RotateMCPCredentials(ctx) }()
	select {
	case <-probe.started:
	case <-ctx.Done():
		t.Fatal("rotation did not reach the grant update")
	}
	_, replacement, _, err := approveMCPGrant(t.Context(), app.ID, mcpWorkScope)
	close(probe.resume)
	require.NoError(t, err)
	require.ErrorIs(t, <-result, auth_model.ErrOAuth2GrantStaleCounter)
	assert.NotEqual(t, old.ID, replacement.ID)
	assert.Zero(t, replacement.CredentialRotatedUnix)
	assert.Equal(t, 1, unittest.GetCount(t, &auth_model.OAuth2Grant{ApplicationID: app.ID}))
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ID: old.ID})
	unittest.AssertNotExistsBean(t, &auth_model.OAuth2AuthorizationCode{ID: oldCode.ID})
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: other.ID, ApplicationID: otherApp.ID})
	unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2AuthorizationCode{ID: otherCode.ID})
	require.NoError(t, other.RotateMCPCredentials(t.Context()))
}

func TestMCPGrantRevokeReconnectRace(t *testing.T) {
	for _, scope := range []string{"read:repository", mcpWorkScope} {
		t.Run(scope, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			app, old, _ := createMCPGrant(t, "read:repository")
			start := make(chan struct{})
			revoked := make(chan error, 1)
			go func() {
				<-start
				revoked <- auth_model.RevokeOAuth2Grant(t.Context(), old.ID, 1)
			}()
			close(start)
			_, _, _, err := approveMCPGrant(t.Context(), app.ID, scope)
			require.NoError(t, err)
			require.NoError(t, <-revoked)
			unittest.AssertNotExistsBean(t, &auth_model.OAuth2Grant{ID: old.ID})
			unittest.AssertNotExistsBean(t, &auth_model.OAuth2AuthorizationCode{GrantID: old.ID})
			_, current, _, err := approveMCPGrant(t.Context(), app.ID, scope)
			require.NoError(t, err)
			assert.NotEqual(t, old.ID, current.ID)
			assert.Equal(t, 1, unittest.GetCount(t, &auth_model.OAuth2Grant{ApplicationID: app.ID}))
			assert.Equal(t, scope, current.Scope)
			assert.ErrorIs(t, auth_model.DeleteFinalizedMCPRegistration(t.Context(), app.ID, 1), auth_model.ErrMCPRegistrationHasGrant)
		})
	}
}
