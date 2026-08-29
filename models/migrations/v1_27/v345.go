// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

type mcpWorkReceipt struct {
	ID             int64              `xorm:"pk autoincr"`
	OperationUUID  string             `xorm:"CHAR(36) INDEX NOT NULL"`
	PrincipalID    int64              `xorm:"UNIQUE(mcp_work_key) INDEX NOT NULL"`
	AudienceDigest string             `xorm:"CHAR(64) UNIQUE(mcp_work_key) NOT NULL"`
	KeyDigest      string             `xorm:"CHAR(64) UNIQUE(mcp_work_key) NOT NULL"`
	RequestDigest  string             `xorm:"CHAR(64) NOT NULL"`
	Tool           string             `xorm:"VARCHAR(64) NOT NULL"`
	SchemaVersion  string             `xorm:"VARCHAR(16) NOT NULL"`
	ApplicationID  int64              `xorm:"INDEX NOT NULL"`
	GrantID        int64              `xorm:"INDEX NOT NULL"`
	CredentialID   string             `xorm:"CHAR(36) NOT NULL"`
	Scope          string             `xorm:"VARCHAR(255) NOT NULL"`
	ActorTrust     string             `xorm:"VARCHAR(16) NOT NULL"`
	Origin         string             `xorm:"VARCHAR(16) NOT NULL"`
	Outcome        string             `xorm:"VARCHAR(16) NOT NULL"`
	ProblemCode    string             `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`
	CreatedUnix    timeutil.TimeStamp `xorm:"created"`
	CommittedUnix  timeutil.TimeStamp `xorm:"INDEX"`
	TombstonedUnix timeutil.TimeStamp `xorm:"INDEX"`
}

func (*mcpWorkReceipt) TableName() string {
	return "mcp_work_receipt"
}

type mcpWorkArtifactLink struct {
	ID             int64  `xorm:"pk autoincr"`
	ReceiptID      int64  `xorm:"INDEX UNIQUE(mcp_work_artifact) NOT NULL"`
	RepositoryID   int64  `xorm:"INDEX UNIQUE(mcp_work_artifact) NOT NULL"`
	Kind           string `xorm:"VARCHAR(16) UNIQUE(mcp_work_artifact) NOT NULL"`
	ArtifactID     int64  `xorm:"UNIQUE(mcp_work_artifact) NOT NULL"`
	ArtifactNumber int64  `xorm:"NOT NULL DEFAULT 0"`
	LocalReference string `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`
}

func (*mcpWorkArtifactLink) TableName() string {
	return "mcp_work_artifact_link"
}

type mcpWorkEventLink struct {
	ID           int64  `xorm:"pk autoincr"`
	ReceiptID    int64  `xorm:"INDEX UNIQUE(mcp_work_event) NOT NULL"`
	RepositoryID int64  `xorm:"INDEX UNIQUE(mcp_work_event) NOT NULL"`
	Kind         string `xorm:"VARCHAR(32) UNIQUE(mcp_work_event) NOT NULL"`
	EventID      int64  `xorm:"UNIQUE(mcp_work_event) NOT NULL"`
	ArtifactKind string `xorm:"VARCHAR(16) NOT NULL"`
	ArtifactID   int64  `xorm:"NOT NULL"`
}

func (*mcpWorkEventLink) TableName() string {
	return "mcp_work_event_link"
}

// AddMCPWorkReceiptSchema adds only protocol receipts and stable provenance
// links; native Work facts remain in their existing tables.
func AddMCPWorkReceiptSchema(x db.EngineMigration) error {
	return x.Sync(new(mcpWorkReceipt), new(mcpWorkArtifactLink), new(mcpWorkEventLink))
}
