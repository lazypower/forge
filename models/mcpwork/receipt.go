// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package mcpwork persists the narrow safety and provenance records for MCP
// Work mutations. It does not persist Work projections or request content.
package mcpwork

import (
	"context"

	"gitea.dev/models/db"
	"gitea.dev/modules/timeutil"
)

// ErrReceiptNotExist means no durable receipt has the requested locator.
var ErrReceiptNotExist = db.ErrNotExist{Resource: "MCP Work receipt"}

// Outcome is the final durable result of an MCP Work mutation.
type Outcome string

const (
	OutcomePending   Outcome = "pending"
	OutcomeApplied   Outcome = "applied"
	OutcomeUnchanged Outcome = "unchanged"
	OutcomeRejected  Outcome = "rejected"
)

// ArtifactKind identifies the bounded native artifact types a Work mutation
// may affect.
type ArtifactKind string

const (
	ArtifactKindProject ArtifactKind = "project"
	ArtifactKindIssue   ArtifactKind = "issue"
)

// EventKind identifies native event rows that may carry MCP provenance.
type EventKind string

const (
	EventKindIssueComment EventKind = "issue_comment"
)

// Receipt is one final MCP Work mutation outcome. Digests are keyed, fixed-size
// locators; request content and raw credentials are intentionally absent.
type Receipt struct {
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
	Outcome        Outcome            `xorm:"VARCHAR(16) NOT NULL"`
	ProblemCode    string             `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`
	CreatedUnix    timeutil.TimeStamp `xorm:"created"`
	CommittedUnix  timeutil.TimeStamp `xorm:"INDEX"`
	TombstonedUnix timeutil.TimeStamp `xorm:"INDEX"`
}

func (*Receipt) TableName() string {
	return "mcp_work_receipt"
}

// ArtifactLink relates a receipt to one stable native Project or Issue.
type ArtifactLink struct {
	ID             int64        `xorm:"pk autoincr"`
	ReceiptID      int64        `xorm:"INDEX UNIQUE(mcp_work_artifact) NOT NULL"`
	RepositoryID   int64        `xorm:"INDEX UNIQUE(mcp_work_artifact) NOT NULL"`
	Kind           ArtifactKind `xorm:"VARCHAR(16) UNIQUE(mcp_work_artifact) NOT NULL"`
	ArtifactID     int64        `xorm:"UNIQUE(mcp_work_artifact) NOT NULL"`
	ArtifactNumber int64        `xorm:"NOT NULL DEFAULT 0"`
	LocalReference string       `xorm:"VARCHAR(64) NOT NULL DEFAULT ''"`
}

func (*ArtifactLink) TableName() string {
	return "mcp_work_artifact_link"
}

// EventLink relates a receipt to one native timeline event without copying the
// event or its content.
type EventLink struct {
	ID           int64        `xorm:"pk autoincr"`
	ReceiptID    int64        `xorm:"INDEX UNIQUE(mcp_work_event) NOT NULL"`
	RepositoryID int64        `xorm:"INDEX UNIQUE(mcp_work_event) NOT NULL"`
	Kind         EventKind    `xorm:"VARCHAR(32) UNIQUE(mcp_work_event) NOT NULL"`
	EventID      int64        `xorm:"UNIQUE(mcp_work_event) NOT NULL"`
	ArtifactKind ArtifactKind `xorm:"VARCHAR(16) NOT NULL"`
	ArtifactID   int64        `xorm:"NOT NULL"`
}

func (*EventLink) TableName() string {
	return "mcp_work_event_link"
}

func init() {
	db.RegisterModel(new(Receipt))
	db.RegisterModel(new(ArtifactLink))
	db.RegisterModel(new(EventLink))
}

// FindByKey returns a detailed receipt or compact tombstone for one
// already-digested locator.
func FindByKey(ctx context.Context, principalID int64, audienceDigest, keyDigest string) (*Receipt, error) {
	receipt := new(Receipt)
	has, err := db.GetEngine(ctx).
		Where("principal_id = ? AND audience_digest = ? AND key_digest = ?", principalID, audienceDigest, keyDigest).
		Get(receipt)
	if err != nil {
		return nil, err
	}
	if has {
		return receipt, nil
	}
	return nil, ErrReceiptNotExist
}

// GetReceiptByUUID returns one receipt and its stable links.
func GetReceiptByUUID(ctx context.Context, operationUUID string) (*Receipt, []*ArtifactLink, []*EventLink, error) {
	receipt := new(Receipt)
	has, err := db.GetEngine(ctx).Where("operation_uuid = ?", operationUUID).Get(receipt)
	if err != nil {
		return nil, nil, nil, err
	}
	if !has {
		return nil, nil, nil, ErrReceiptNotExist
	}
	artifacts, events, err := GetReceiptLinks(ctx, receipt.ID)
	return receipt, artifacts, events, err
}

// GetReceiptLinks returns the stable links for one internal receipt ID.
func GetReceiptLinks(ctx context.Context, receiptID int64) ([]*ArtifactLink, []*EventLink, error) {
	artifacts := make([]*ArtifactLink, 0)
	if err := db.GetEngine(ctx).Where("receipt_id = ?", receiptID).OrderBy("id").Find(&artifacts); err != nil {
		return nil, nil, err
	}
	events := make([]*EventLink, 0)
	if err := db.GetEngine(ctx).Where("receipt_id = ?", receiptID).OrderBy("id").Find(&events); err != nil {
		return nil, nil, err
	}
	return artifacts, events, nil
}

// ListOperationUUIDsByArtifact returns the latest bounded provenance locators
// for one native artifact. Callers must authorize the artifact first.
func ListOperationUUIDsByArtifact(ctx context.Context, repositoryID int64, kind ArtifactKind, artifactID int64, limit int) ([]string, error) {
	links := make([]*ArtifactLink, 0, limit)
	if err := db.GetEngine(ctx).
		Where("repository_id = ? AND kind = ? AND artifact_id = ?", repositoryID, kind, artifactID).
		Desc("id").Limit(limit).Find(&links); err != nil {
		return nil, err
	}
	return operationUUIDsForLinks(ctx, links, nil)
}

// ListOperationUUIDsByEvent returns the latest bounded provenance locators for
// one native event. Callers must authorize its owning artifact first.
func ListOperationUUIDsByEvent(ctx context.Context, repositoryID int64, kind EventKind, eventID int64, limit int) ([]string, error) {
	links := make([]*EventLink, 0, limit)
	if err := db.GetEngine(ctx).
		Where("repository_id = ? AND kind = ? AND event_id = ?", repositoryID, kind, eventID).
		Desc("id").Limit(limit).Find(&links); err != nil {
		return nil, err
	}
	return operationUUIDsForLinks(ctx, nil, links)
}

func operationUUIDsForLinks(ctx context.Context, artifacts []*ArtifactLink, events []*EventLink) ([]string, error) {
	receiptIDs := make([]int64, 0, len(artifacts)+len(events))
	for _, link := range artifacts {
		receiptIDs = append(receiptIDs, link.ReceiptID)
	}
	for _, link := range events {
		receiptIDs = append(receiptIDs, link.ReceiptID)
	}
	if len(receiptIDs) == 0 {
		return []string{}, nil
	}
	receipts := make([]*Receipt, 0, len(receiptIDs))
	if err := db.GetEngine(ctx).In("id", receiptIDs).Find(&receipts); err != nil {
		return nil, err
	}
	byID := make(map[int64]string, len(receipts))
	for _, receipt := range receipts {
		if receipt.TombstonedUnix == 0 && receipt.OperationUUID != "" {
			byID[receipt.ID] = receipt.OperationUUID
		}
	}
	operationUUIDs := make([]string, 0, len(receiptIDs))
	for _, receiptID := range receiptIDs {
		if operationUUID := byID[receiptID]; operationUUID != "" {
			operationUUIDs = append(operationUUIDs, operationUUID)
		}
	}
	return operationUUIDs, nil
}

// RetireIssue removes one deleted Issue from receipt provenance. It must be
// called after the native Issue row is deleted in the same transaction. If the
// row still exists, receipt detail is retained.
func RetireIssue(ctx context.Context, repositoryID, issueID int64) error {
	if repositoryID <= 0 || issueID <= 0 {
		return nil
	}
	exists, err := db.GetEngine(ctx).Table("issue").Where("id = ? AND repo_id = ?", issueID, repositoryID).Exist()
	if err != nil || exists {
		return err
	}
	if _, err := db.GetEngine(ctx).
		Where("repository_id = ? AND artifact_kind = ? AND artifact_id = ?", repositoryID, ArtifactKindIssue, issueID).
		Delete(new(EventLink)); err != nil {
		return err
	}
	return retireArtifactLinks(ctx, "repository_id = ? AND kind = ? AND artifact_id = ?", repositoryID, ArtifactKindIssue, issueID)
}

// RetireProject removes one deleted repository Project from receipt
// provenance. It must be called after the native Project row is deleted in the
// same transaction. If the row still exists, receipt detail is retained.
// Organization and user Projects are outside MCP Work.
func RetireProject(ctx context.Context, repositoryID, projectID int64) error {
	if repositoryID <= 0 || projectID <= 0 {
		return nil
	}
	exists, err := db.GetEngine(ctx).Table("project").Where("id = ? AND repo_id = ?", projectID, repositoryID).Exist()
	if err != nil || exists {
		return err
	}
	return retireArtifactLinks(ctx, "repository_id = ? AND kind = ? AND artifact_id = ?", repositoryID, ArtifactKindProject, projectID)
}

// RetireRepository removes every artifact and event link owned by a deleted
// repository. It must be called after the native Repository row is deleted in
// the same transaction. If the row still exists, receipt detail is retained.
func RetireRepository(ctx context.Context, repositoryID int64) error {
	if repositoryID <= 0 {
		return nil
	}
	exists, err := db.GetEngine(ctx).Table("repository").Where("id = ?", repositoryID).Exist()
	if err != nil || exists {
		return err
	}
	if _, err := db.GetEngine(ctx).Where("repository_id = ?", repositoryID).Delete(new(EventLink)); err != nil {
		return err
	}
	return retireArtifactLinks(ctx, "repository_id = ?", repositoryID)
}

// RetireRepositoryProjects removes repository Project links during the native
// bulk Project deletion path without retiring surviving Issue links. If any
// repository Project remains, receipt detail is retained.
func RetireRepositoryProjects(ctx context.Context, repositoryID int64) error {
	if repositoryID <= 0 {
		return nil
	}
	exists, err := db.GetEngine(ctx).Table("project").Where("repo_id = ?", repositoryID).Exist()
	if err != nil || exists {
		return err
	}
	return retireArtifactLinks(ctx, "repository_id = ? AND kind = ?", repositoryID, ArtifactKindProject)
}

func retireArtifactLinks(ctx context.Context, condition string, args ...any) error {
	receiptIDs := make([]int64, 0)
	if err := db.GetEngine(ctx).Table(new(ArtifactLink)).Where(condition, args...).Distinct("receipt_id").Find(&receiptIDs); err != nil {
		return err
	}
	if len(receiptIDs) == 0 {
		return nil
	}
	if _, err := db.GetEngine(ctx).Where(condition, args...).Delete(new(ArtifactLink)); err != nil {
		return err
	}
	for _, receiptID := range receiptIDs {
		remaining, err := db.GetEngine(ctx).Where("receipt_id = ?", receiptID).Count(new(ArtifactLink))
		if err != nil {
			return err
		}
		if remaining != 0 {
			continue
		}
		if err := minimizeReceipt(ctx, receiptID, timeutil.TimeStampNow()); err != nil {
			return err
		}
	}
	return nil
}

func minimizeReceipt(ctx context.Context, receiptID int64, tombstonedAt timeutil.TimeStamp) error {
	if _, err := db.GetEngine(ctx).Where("receipt_id = ?", receiptID).Delete(new(EventLink)); err != nil {
		return err
	}
	_, err := db.GetEngine(ctx).Table(new(Receipt)).ID(receiptID).Where("tombstoned_unix = 0").Update(map[string]any{
		"operation_uuid": "", "tool": "", "schema_version": "", "application_id": int64(0), "grant_id": int64(0),
		"credential_id": "", "scope": "", "actor_trust": "", "origin": "", "outcome": "", "problem_code": "",
		"created_unix": timeutil.TimeStamp(0), "committed_unix": timeutil.TimeStamp(0), "tombstoned_unix": tombstonedAt,
	})
	return err
}
