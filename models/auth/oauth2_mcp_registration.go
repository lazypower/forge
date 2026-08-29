// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"context"
	"errors"
	"net"
	"net/url"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"xorm.io/builder"
)

// MCPRegistrationState is the explicit lifecycle of a constrained MCP client.
type MCPRegistrationState string

const (
	MCPRegistrationStateProvisional MCPRegistrationState = "provisional"
	MCPRegistrationStateFinalized   MCPRegistrationState = "finalized"
	mcpRegistrationStateChanging    MCPRegistrationState = "changing"
)

// MCPRedirectClass records the single validated redirect class of an MCP client.
type MCPRedirectClass string

const (
	MCPRedirectClassHTTPS    MCPRedirectClass = "https"
	MCPRedirectClassLoopback MCPRedirectClass = "loopback"
)

var (
	ErrMCPRegistrationCapacity       = errors.New("MCP registration capacity unavailable")
	ErrMCPRegistrationExpired        = errors.New("MCP registration expired")
	ErrMCPRegistrationNotProvisional = errors.New("MCP registration is not provisional")
	ErrMCPRegistrationWrongPrincipal = errors.New("MCP registration belongs to another principal")
	ErrMCPRegistrationHasGrant       = errors.New("MCP registration has an active grant")
)

// OAuth2MCPRegistrationAdmission is the database authority for provisional storage capacity.
type OAuth2MCPRegistrationAdmission struct {
	ID          int64 `xorm:"pk"`
	Outstanding int
}

func init() {
	db.RegisterModel(new(OAuth2MCPRegistrationAdmission))
}

func (OAuth2MCPRegistrationAdmission) TableName() string {
	return "oauth2_mcp_registration_admission"
}

func ensureMCPRegistrationAdmission(ctx context.Context) error {
	const admissionID = 1
	has, err := db.GetEngine(ctx).ID(admissionID).Exist(new(OAuth2MCPRegistrationAdmission))
	if err != nil || has {
		return err
	}
	_, err = db.GetEngine(ctx).Insert(&OAuth2MCPRegistrationAdmission{ID: admissionID})
	return err
}

// IsMCPClientRegistration reports whether the application belongs to the constrained MCP lifecycle.
func (app *OAuth2Application) IsMCPClientRegistration() bool {
	return app != nil && (app.MCPRegistrationState == MCPRegistrationStateProvisional || app.MCPRegistrationState == MCPRegistrationStateFinalized)
}

// IsMCPRegistrationExpired reports whether the provisional lifetime has elapsed.
func (app *OAuth2Application) IsMCPRegistrationExpired(now time.Time) bool {
	return app != nil && app.MCPRegistrationState == MCPRegistrationStateProvisional &&
		(app.MCPExpiresUnix.IsZero() || app.MCPExpiresUnix <= timeutil.TimeStamp(now.Unix()))
}

// ContainsMCPRedirectURI performs exact matching except for a native loopback's runtime port.
func (app *OAuth2Application) ContainsMCPRedirectURI(redirectURI string) bool {
	if app == nil || !app.IsMCPClientRegistration() {
		return false
	}
	if slices.Contains(app.RedirectURIs, redirectURI) {
		return true
	}
	if app.MCPRedirectClass != MCPRedirectClassLoopback {
		return false
	}
	normalized, ok := normalizeMCPLoopbackRuntimePort(redirectURI)
	if !ok {
		return false
	}
	for _, registeredURI := range app.RedirectURIs {
		if registeredURI == normalized {
			return true
		}
		registeredNormalized, ok := normalizeMCPLoopbackRuntimePort(registeredURI)
		if ok && registeredNormalized == normalized {
			return true
		}
	}
	return false
}

func normalizeMCPLoopbackRuntimePort(value string) (string, bool) {
	redirect, err := url.Parse(value)
	if err != nil || redirect.Scheme != "http" || redirect.Port() == "" || redirect.User != nil || redirect.Fragment != "" {
		return "", false
	}
	ip := net.ParseIP(redirect.Hostname())
	if ip == nil || !ip.IsLoopback() {
		return "", false
	}
	host := redirect.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	redirect.Host = host
	return redirect.String(), true
}

// CreateMCPClientRegistration persists one authority-free provisional public client.
func CreateMCPClientRegistration(ctx context.Context, name, installationLabel string, redirectURIs []string, redirectClass MCPRedirectClass, expires time.Time, maxOutstanding int) (*OAuth2Application, error) {
	return db.WithTx2(ctx, func(ctx context.Context) (*OAuth2Application, error) {
		if err := ensureMCPRegistrationAdmission(ctx); err != nil {
			return nil, err
		}
		admission := new(OAuth2MCPRegistrationAdmission)
		affected, err := db.GetEngine(ctx).ID(1).
			Where("outstanding < ?", maxOutstanding).
			Incr("outstanding").
			Update(admission)
		if err != nil {
			return nil, err
		}
		if affected == 0 {
			return nil, ErrMCPRegistrationCapacity
		}
		app := &OAuth2Application{
			Name:                 name,
			ClientID:             "mcp_" + base32Lower.EncodeToString(util.CryptoRandomBytes(32)),
			RedirectURIs:         append([]string(nil), redirectURIs...),
			ConfidentialClient:   false,
			MCPRegistrationState: MCPRegistrationStateProvisional,
			MCPInstallationLabel: installationLabel,
			MCPRedirectClass:     redirectClass,
			MCPExpiresUnix:       timeutil.TimeStamp(expires.Unix()),
		}
		if err := db.Insert(ctx, app); err != nil {
			return nil, err
		}
		return app, nil
	})
}

// ApproveMCPClientRegistration atomically binds a provisional client, creates its first grant, and issues a code.
func ApproveMCPClientRegistration(ctx context.Context, applicationID, userID int64, scope, nonce, redirectURI, codeChallenge, codeChallengeMethod, resource string, now time.Time) (*OAuth2Application, *OAuth2Grant, *OAuth2AuthorizationCode, error) {
	var app *OAuth2Application
	var grant *OAuth2Grant
	var code *OAuth2AuthorizationCode
	var outcome error
	err := db.WithTx(ctx, func(ctx context.Context) error {
		var err error
		app, err = GetOAuth2ApplicationByID(ctx, applicationID)
		if err != nil {
			return err
		}
		if !app.IsMCPClientRegistration() {
			return ErrMCPRegistrationNotProvisional
		}
		switch app.MCPRegistrationState {
		case MCPRegistrationStateProvisional:
			if app.IsMCPRegistrationExpired(now) {
				if err := deleteProvisionalMCPRegistration(ctx, app.ID); err != nil {
					return err
				}
				outcome = ErrMCPRegistrationExpired
				return nil
			}
			update := &OAuth2Application{
				MCPRegistrationState: mcpRegistrationStateChanging,
				MCPBoundUserID:       userID,
				MCPExpiresUnix:       0,
			}
			affected, err := db.GetEngine(ctx).Where(builder.Eq{
				"id":                     app.ID,
				"mcp_registration_state": MCPRegistrationStateProvisional,
			}).And("mcp_expires_unix > ?", now.Unix()).Cols("mcp_registration_state", "mcp_bound_user_id", "mcp_expires_unix").Update(update)
			if err != nil {
				return err
			}
			if affected != 1 {
				return ErrMCPRegistrationNotProvisional
			}
			if err := releaseMCPRegistrationAdmission(ctx, 1); err != nil {
				return err
			}
			app.MCPRegistrationState = mcpRegistrationStateChanging
			app.MCPBoundUserID = userID
			app.MCPExpiresUnix = 0
		case MCPRegistrationStateFinalized:
			if app.MCPBoundUserID != userID {
				return ErrMCPRegistrationWrongPrincipal
			}
			update := &OAuth2Application{MCPRegistrationState: mcpRegistrationStateChanging}
			affected, err := db.GetEngine(ctx).Where(builder.Eq{
				"id":                     app.ID,
				"mcp_registration_state": MCPRegistrationStateFinalized,
				"mcp_bound_user_id":      userID,
			}).Cols("mcp_registration_state").Update(update)
			if err != nil {
				return err
			}
			if affected != 1 {
				return ErrMCPRegistrationNotProvisional
			}
			app.MCPRegistrationState = mcpRegistrationStateChanging
		}
		grant, err = app.GetGrantByUserID(ctx, userID)
		if err != nil {
			return err
		}
		if grant == nil {
			grant, err = app.CreateGrant(ctx, userID, scope)
			if err != nil {
				return err
			}
		} else if grant.Scope != scope {
			return errors.New("MCP registration has a grant with different scope")
		}
		if nonce != "" {
			if err := grant.SetNonce(ctx, nonce); err != nil {
				return err
			}
		}
		code, err = grant.GenerateNewAuthorizationCode(ctx, redirectURI, codeChallenge, codeChallengeMethod, resource)
		if err != nil {
			return err
		}
		affected, err := db.GetEngine(ctx).Where(builder.Eq{
			"id":                     app.ID,
			"mcp_registration_state": mcpRegistrationStateChanging,
			"mcp_bound_user_id":      userID,
		}).Cols("mcp_registration_state").Update(&OAuth2Application{MCPRegistrationState: MCPRegistrationStateFinalized})
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrMCPRegistrationNotProvisional
		}
		app.MCPRegistrationState = MCPRegistrationStateFinalized
		return nil
	})
	if err != nil {
		return nil, nil, nil, err
	}
	if outcome != nil {
		return nil, nil, nil, outcome
	}
	return app, grant, code, nil
}

// DeleteProvisionalMCPRegistration removes an unapproved client by client ID.
func DeleteProvisionalMCPRegistration(ctx context.Context, clientID string) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		app, err := GetOAuth2ApplicationByClientID(ctx, clientID)
		if err != nil {
			return err
		}
		if app.MCPRegistrationState != MCPRegistrationStateProvisional {
			return ErrMCPRegistrationNotProvisional
		}
		return deleteProvisionalMCPRegistration(ctx, app.ID)
	})
}

func deleteProvisionalMCPRegistration(ctx context.Context, applicationID int64) error {
	deleted, err := db.GetEngine(ctx).Where(builder.Eq{
		"id":                     applicationID,
		"mcp_registration_state": MCPRegistrationStateProvisional,
	}).Delete(new(OAuth2Application))
	if err != nil {
		return err
	}
	if deleted == 0 {
		return ErrMCPRegistrationNotProvisional
	}
	return releaseMCPRegistrationAdmission(ctx, int(deleted))
}

func releaseMCPRegistrationAdmission(ctx context.Context, count int) error {
	if count <= 0 {
		return nil
	}
	affected, err := db.GetEngine(ctx).ID(1).Where("outstanding >= ?", count).Decr("outstanding", count).Update(new(OAuth2MCPRegistrationAdmission))
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("MCP registration admission counter is inconsistent")
	}
	return nil
}

// CleanupExpiredMCPClientRegistrations deletes at most limit expired provisional clients.
func CleanupExpiredMCPClientRegistrations(ctx context.Context, now time.Time, limit int) (int, error) {
	deleted := 0
	err := db.WithTx(ctx, func(ctx context.Context) error {
		var registrations []*OAuth2Application
		if err := db.GetEngine(ctx).
			Where("mcp_registration_state = ? AND mcp_expires_unix <= ?", MCPRegistrationStateProvisional, now.Unix()).
			OrderBy("id").Limit(limit).Find(&registrations); err != nil {
			return err
		}
		for _, app := range registrations {
			if err := deleteProvisionalMCPRegistration(ctx, app.ID); err != nil {
				return err
			}
			deleted++
		}
		return nil
	})
	return deleted, err
}

// ListUngrantFinalizedMCPRegistrations returns inert registrations bound to one principal.
func ListUngrantFinalizedMCPRegistrations(ctx context.Context, userID int64) ([]*OAuth2Application, error) {
	var apps []*OAuth2Application
	err := db.GetEngine(ctx).Table("oauth2_application").
		Join("LEFT", "oauth2_grant", "oauth2_grant.application_id = oauth2_application.id").
		Where("oauth2_application.mcp_registration_state = ? AND oauth2_application.mcp_bound_user_id = ? AND oauth2_grant.id IS NULL", MCPRegistrationStateFinalized, userID).
		OrderBy("oauth2_application.id").Find(&apps)
	return apps, err
}

// DeleteFinalizedMCPRegistration deletes only an inert registration owned by the principal.
func DeleteFinalizedMCPRegistration(ctx context.Context, applicationID, userID int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		app, err := GetOAuth2ApplicationByID(ctx, applicationID)
		if err != nil {
			return err
		}
		if app.MCPRegistrationState != MCPRegistrationStateFinalized || app.MCPBoundUserID != userID {
			return ErrMCPRegistrationWrongPrincipal
		}
		affected, err := db.GetEngine(ctx).Where(builder.Eq{
			"id":                     applicationID,
			"mcp_registration_state": MCPRegistrationStateFinalized,
			"mcp_bound_user_id":      userID,
		}).Cols("mcp_registration_state").Update(&OAuth2Application{MCPRegistrationState: mcpRegistrationStateChanging})
		if err != nil {
			return err
		}
		if affected != 1 {
			return ErrMCPRegistrationWrongPrincipal
		}
		grant, err := app.GetGrantByUserID(ctx, userID)
		if err != nil {
			return err
		}
		if grant != nil {
			return ErrMCPRegistrationHasGrant
		}
		deleted, err := db.GetEngine(ctx).Where(builder.Eq{
			"id":                     applicationID,
			"mcp_registration_state": mcpRegistrationStateChanging,
			"mcp_bound_user_id":      userID,
		}).Delete(new(OAuth2Application))
		if err != nil {
			return err
		}
		if deleted == 0 {
			return ErrOAuthApplicationNotFound{ID: applicationID}
		}
		return nil
	})
}
