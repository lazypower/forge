// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

type oauth2GrantCredentialRotation struct {
	CredentialRotatedUnix timeutil.TimeStamp
}

func (*oauth2GrantCredentialRotation) TableName() string { return "oauth2_grant" }

// AddOAuth2GrantCredentialRotation adds a timestamp without inventing past credential activity.
func AddOAuth2GrantCredentialRotation(x db.EngineMigration) error {
	return x.Sync(new(oauth2GrantCredentialRotation))
}
