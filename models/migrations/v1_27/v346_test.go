// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type oauth2ApplicationBeforeMCPRegistration struct {
	ID int64 `xorm:"pk autoincr"`
}

func (*oauth2ApplicationBeforeMCPRegistration) TableName() string { return "oauth2_application" }

type freshOAuth2ApplicationMCPRegistration auth_model.OAuth2Application

func (*freshOAuth2ApplicationMCPRegistration) TableName() string {
	return "fresh_oauth2_application"
}

type freshOAuth2MCPRegistrationAdmission auth_model.OAuth2MCPRegistrationAdmission

func (*freshOAuth2MCPRegistrationAdmission) TableName() string {
	return "fresh_oauth2_mcp_registration_admission"
}

func Test_AddMCPClientRegistrationSchema(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0, new(oauth2ApplicationBeforeMCPRegistration))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}
	require.NoError(t, AddMCPClientRegistrationSchema(x))
	require.NoError(t, x.Sync(new(freshOAuth2ApplicationMCPRegistration), new(freshOAuth2MCPRegistrationAdmission)))
	tables := migrationtest.LoadTableSchemasMap(t, x)
	upgraded := tables["oauth2_application"]
	fresh := tables["fresh_oauth2_application"]
	for _, columnName := range []string{"mcp_registration_state", "mcp_installation_label", "mcp_redirect_class", "mcp_bound_user_id", "mcp_expires_unix"} {
		got := upgraded.GetColumn(columnName)
		want := fresh.GetColumn(columnName)
		require.NotNil(t, got, columnName)
		require.NotNil(t, want, columnName)
		assert.Equal(t, want.SQLType.Name, got.SQLType.Name, columnName)
		assert.Equal(t, want.Length, got.Length, columnName)
		assert.Equal(t, want.Nullable, got.Nullable, columnName)
		assert.Equal(t, want.Default, got.Default, columnName)
	}
	assertMCPWorkTableMatchesFresh(t, tables, "oauth2_mcp_registration_admission", "fresh_oauth2_mcp_registration_admission")
	count, err := x.Count(new(oauth2ApplicationBeforeMCPRegistration))
	require.NoError(t, err)
	assert.Zero(t, count, "the schema migration must not create or translate any client")
	admission := new(oauth2MCPRegistrationAdmission)
	has, err := x.ID(1).Get(admission)
	require.NoError(t, err)
	require.True(t, has)
	assert.Zero(t, admission.Outstanding)
}
