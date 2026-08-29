// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/require"
)

func newFinalizedMCPRegistration(t *testing.T, userID int64, scope, redirectURI string) (*auth_model.OAuth2Application, *auth_model.OAuth2Grant) {
	t.Helper()
	app, err := auth_model.CreateMCPClientRegistration(t.Context(), "MCP integration harness", "", []string{redirectURI}, auth_model.MCPRedirectClassLoopback, time.Now().Add(time.Minute), 1000)
	require.NoError(t, err)
	app, grant, _, err := auth_model.ApproveMCPClientRegistration(t.Context(), app.ID, userID, scope, "", redirectURI, "mcp-integration-challenge", "S256", setting.MCPResource(), time.Now())
	require.NoError(t, err)
	return app, grant
}
