// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/migrations/migrationtest"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/require"
)

type oauth2AuthorizationCodeBeforeResource struct {
	ID                  int64 `xorm:"pk autoincr"`
	GrantID             int64
	Code                string `xorm:"INDEX unique"`
	CodeChallenge       string
	CodeChallengeMethod string
	RedirectURI         string
	ValidUntil          timeutil.TimeStamp `xorm:"index"`
}

func (oauth2AuthorizationCodeBeforeResource) TableName() string {
	return "oauth2_authorization_code"
}

type oauth2AuthorizationCodeAfterResource struct {
	ID                  int64 `xorm:"pk autoincr"`
	GrantID             int64
	Code                string `xorm:"INDEX unique"`
	CodeChallenge       string
	CodeChallengeMethod string
	RedirectURI         string
	Resource            string             `xorm:"TEXT"`
	ValidUntil          timeutil.TimeStamp `xorm:"index"`
}

func (oauth2AuthorizationCodeAfterResource) TableName() string {
	return "oauth2_authorization_code"
}

func Test_AddResourceToOAuth2AuthorizationCode(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0, new(oauth2AuthorizationCodeBeforeResource))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}

	before := &oauth2AuthorizationCodeBeforeResource{
		GrantID:             42,
		Code:                "pre-migration-code",
		CodeChallenge:       "pre-migration-challenge",
		CodeChallengeMethod: "S256",
		RedirectURI:         "http://127.0.0.1:49152",
		ValidUntil:          timeutil.TimeStamp(1_800_000_000),
	}
	_, err := x.Insert(before)
	require.NoError(t, err)
	require.NotZero(t, before.ID)

	require.NoError(t, AddResourceToOAuth2AuthorizationCode(x))

	var migrated oauth2AuthorizationCodeAfterResource
	has, err := x.ID(before.ID).Get(&migrated)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, before.ID, migrated.ID)
	require.Equal(t, before.GrantID, migrated.GrantID)
	require.Equal(t, before.Code, migrated.Code)
	require.Equal(t, before.CodeChallenge, migrated.CodeChallenge)
	require.Equal(t, before.CodeChallengeMethod, migrated.CodeChallengeMethod)
	require.Equal(t, before.RedirectURI, migrated.RedirectURI)
	require.Equal(t, before.ValidUntil, migrated.ValidUntil)
	require.Empty(t, migrated.Resource)

	after := &oauth2AuthorizationCodeAfterResource{
		GrantID:             43,
		Code:                "post-migration-code",
		CodeChallenge:       "post-migration-challenge",
		CodeChallengeMethod: "S256",
		RedirectURI:         "https://127.0.0.1:49153",
		Resource:            "https://forge.example/mcp",
		ValidUntil:          timeutil.TimeStamp(1_800_000_001),
	}
	_, err = x.Insert(after)
	require.NoError(t, err)
	require.NotZero(t, after.ID)

	var roundTripped oauth2AuthorizationCodeAfterResource
	has, err = x.ID(after.ID).Get(&roundTripped)
	require.NoError(t, err)
	require.True(t, has)
	require.Equal(t, after.Resource, roundTripped.Resource)
}
