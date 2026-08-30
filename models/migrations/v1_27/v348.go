// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import "gitea.dev/models/db"

type mcpWorkClientAttribution struct {
	Receipt                     mcpWorkReceipt `xorm:"extends"`
	Profile                     string         `xorm:"VARCHAR(32) NOT NULL"`
	RegisteredClientLabel       string         `xorm:"VARCHAR(128) NOT NULL"`
	RegisteredInstallationLabel string         `xorm:"VARCHAR(128) NOT NULL"`
	Harness                     string         `xorm:"VARCHAR(128) NOT NULL"`
	HarnessVersion              string         `xorm:"VARCHAR(64) NOT NULL"`
	Model                       string         `xorm:"VARCHAR(128) NOT NULL"`
	AttributionSource           string         `xorm:"VARCHAR(32) NOT NULL"`
}

func (*mcpWorkClientAttribution) TableName() string { return "mcp_work_receipt" }

// AddMCPWorkClientAttribution extends the empty pre-release receipt schema only.
func AddMCPWorkClientAttribution(x db.EngineMigration) error {
	return x.Sync(new(mcpWorkClientAttribution))
}
