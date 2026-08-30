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

type oauth2GrantBeforeRotation struct {
	ID int64 `xorm:"pk autoincr"`
}

func (*oauth2GrantBeforeRotation) TableName() string { return "oauth2_grant" }

type freshOAuth2GrantRotation auth_model.OAuth2Grant

func (*freshOAuth2GrantRotation) TableName() string { return "fresh_oauth2_grant" }

func Test_AddOAuth2GrantCredentialRotation(t *testing.T) {
	x, cleanup := migrationtest.PrepareTestEnv(t, 0, new(oauth2GrantBeforeRotation))
	defer cleanup()
	if x == nil || t.Failed() {
		return
	}
	_, err := x.Insert(&oauth2GrantBeforeRotation{ID: 1})
	require.NoError(t, err)
	require.NoError(t, AddOAuth2GrantCredentialRotation(x))
	require.NoError(t, x.Sync(new(freshOAuth2GrantRotation)))
	tables := migrationtest.LoadTableSchemasMap(t, x)
	got := tables["oauth2_grant"].GetColumn("credential_rotated_unix")
	want := tables["fresh_oauth2_grant"].GetColumn("credential_rotated_unix")
	require.NotNil(t, got)
	require.NotNil(t, want)
	assert.Equal(t, want.SQLType, got.SQLType)
	assert.Equal(t, want.Nullable, got.Nullable)
	assert.Equal(t, want.Default, got.Default)
	var grant struct{ CredentialRotatedUnix int64 }
	has, err := x.Table("oauth2_grant").Where("id = ?", 1).Get(&grant)
	require.NoError(t, err)
	require.True(t, has)
	assert.Zero(t, grant.CredentialRotatedUnix, "the schema must not invent past rotation activity")
}
