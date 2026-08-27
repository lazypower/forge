// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"

	"xorm.io/xorm"
)

type oauth2AuthorizationCodeResource struct {
	Resource string `xorm:"TEXT"`
}

func (oauth2AuthorizationCodeResource) TableName() string {
	return "oauth2_authorization_code"
}

// AddResourceToOAuth2AuthorizationCode persists the RFC 8707 resource until one-use exchange.
func AddResourceToOAuth2AuthorizationCode(x db.EngineMigration) error {
	_, err := x.SyncWithOptions(xorm.SyncOptions{
		IgnoreConstrains: true,
		IgnoreIndices:    true,
	}, new(oauth2AuthorizationCodeResource))
	return err
}
