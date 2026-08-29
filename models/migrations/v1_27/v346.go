// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

type oauth2ApplicationMCPRegistration struct {
	MCPRegistrationState string             `xorm:"VARCHAR(16) NOT NULL DEFAULT '' INDEX"`
	MCPInstallationLabel string             `xorm:"VARCHAR(128)"`
	MCPRedirectClass     string             `xorm:"VARCHAR(16)"`
	MCPBoundUserID       int64              `xorm:"INDEX"`
	MCPExpiresUnix       timeutil.TimeStamp `xorm:"INDEX"`
}

func (*oauth2ApplicationMCPRegistration) TableName() string {
	return "oauth2_application"
}

type oauth2MCPRegistrationAdmission struct {
	ID          int64 `xorm:"pk"`
	Outstanding int
}

func (*oauth2MCPRegistrationAdmission) TableName() string {
	return "oauth2_mcp_registration_admission"
}

// AddMCPClientRegistrationSchema adds only the clean-slate constrained client lifecycle.
func AddMCPClientRegistrationSchema(x db.EngineMigration) error {
	if err := x.Sync(new(oauth2ApplicationMCPRegistration), new(oauth2MCPRegistrationAdmission)); err != nil {
		return err
	}
	has, err := x.ID(1).Exist(new(oauth2MCPRegistrationAdmission))
	if err != nil || has {
		return err
	}
	_, err = x.Insert(&oauth2MCPRegistrationAdmission{ID: 1})
	return err
}
