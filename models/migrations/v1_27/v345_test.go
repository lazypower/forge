// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"context"
	"testing"

	mcpwork_model "gitea.dev/models/mcpwork"
	"gitea.dev/models/migrations/migrationtest"
	project_model "gitea.dev/models/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm/schemas"
)

type freshMCPWorkReceipt mcpwork_model.Receipt

func (*freshMCPWorkReceipt) TableName() string { return "fresh_mcp_work_receipt" }

type freshMCPWorkArtifactLink mcpwork_model.ArtifactLink

func (*freshMCPWorkArtifactLink) TableName() string { return "fresh_mcp_work_artifact_link" }

type freshMCPWorkEventLink mcpwork_model.EventLink

func (*freshMCPWorkEventLink) TableName() string { return "fresh_mcp_work_event_link" }

func Test_AddMCPWorkReceiptSchema(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0)
	defer deferable()
	if x == nil || t.Failed() {
		return
	}

	require.NoError(t, AddMCPWorkReceiptSchema(x))
	require.NoError(t, x.Sync(new(freshMCPWorkReceipt), new(freshMCPWorkArtifactLink), new(freshMCPWorkEventLink)))
	tables := migrationtest.LoadTableSchemasMap(t, x)
	assertMCPWorkTableMatchesFresh(t, tables, "mcp_work_receipt", "fresh_mcp_work_receipt")
	assertMCPWorkTableMatchesFresh(t, tables, "mcp_work_artifact_link", "fresh_mcp_work_artifact_link")
	assertMCPWorkTableMatchesFresh(t, tables, "mcp_work_event_link", "fresh_mcp_work_event_link")

	receiptIndexes, err := x.Dialect().GetIndexes(x.DB(), context.Background(), "mcp_work_receipt")
	require.NoError(t, err)
	assert.True(t, hasIndexWithColumns(receiptIndexes, []string{"principal_id", "audience_digest", "key_digest"}, true))
	artifactIndexes, err := x.Dialect().GetIndexes(x.DB(), context.Background(), "mcp_work_artifact_link")
	require.NoError(t, err)
	assert.True(t, hasIndexWithColumns(artifactIndexes, []string{"receipt_id", "repository_id", "kind", "artifact_id"}, true))
	eventIndexes, err := x.Dialect().GetIndexes(x.DB(), context.Background(), "mcp_work_event_link")
	require.NoError(t, err)
	assert.True(t, hasIndexWithColumns(eventIndexes, []string{"receipt_id", "repository_id", "kind", "event_id"}, true))

	first := migrationReceiptFixture(1, "operation-a", "request-a")
	_, err = x.Insert(first)
	require.NoError(t, err)
	_, err = x.Insert(migrationReceiptFixture(1, "operation-b", "request-b"))
	require.Error(t, err)
	_, err = x.Insert(migrationReceiptFixture(2, "operation-c", "request-c"))
	require.NoError(t, err)

	receiptTable := tables["mcp_work_receipt"]
	for _, forbidden := range []string{
		"raw_key", "idempotency_key", "token", "raw_token", "request", "request_body", "markdown",
		"work_projection", "readiness", "client_actor", "actor_name", "audit_payload",
	} {
		assert.Nil(t, receiptTable.GetColumn(forbidden), "security-sensitive column %q must not exist", forbidden)
	}
}

func Test_PlanningStateThenMCPWorkReceiptSchema(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0, new(projectBeforePlanningState))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}

	ordinary := &projectBeforePlanningState{Title: "Existing ordinary Project"}
	_, err := x.Insert(ordinary)
	require.NoError(t, err)
	require.NoError(t, AddPlanningStateToProject(x))
	require.NoError(t, AddMCPWorkReceiptSchema(x))

	var upgraded projectAfterPlanningState
	has, err := x.ID(ordinary.ID).Get(&upgraded)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, project_model.PlanningStateDisabled, upgraded.PlanningState)

	tables := migrationtest.LoadTableSchemasMap(t, x)
	require.NotNil(t, tables["project"].GetColumn("planning_state"))
	for _, table := range []string{"mcp_work_receipt", "mcp_work_artifact_link", "mcp_work_event_link"} {
		require.Contains(t, tables, table)
	}
}

func migrationReceiptFixture(principalID int64, operationUUID, requestDigest string) *mcpWorkReceipt {
	return &mcpWorkReceipt{
		OperationUUID: operationUUID, PrincipalID: principalID,
		AudienceDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		KeyDigest:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RequestDigest:  requestDigest, Tool: "work_plan.begin", SchemaVersion: "1",
		ApplicationID: 10, GrantID: 11, CredentialID: "11111111-1111-4111-8111-111111111111",
		Scope: "read:repository write:issue write:repository", ActorTrust: "unverified", Origin: "mcp", Outcome: "applied",
	}
}

func assertMCPWorkTableMatchesFresh(t *testing.T, tables map[string]*schemas.Table, upgradedName, freshName string) {
	t.Helper()
	expected := tables[freshName]
	actual := tables[upgradedName]
	require.NotNil(t, expected)
	require.NotNil(t, actual)
	assert.Equal(t, expected.ColumnsSeq(), actual.ColumnsSeq())
	for _, columnName := range expected.ColumnsSeq() {
		expectedColumn := expected.GetColumn(columnName)
		actualColumn := actual.GetColumn(columnName)
		require.NotNil(t, actualColumn)
		assert.Equal(t, expectedColumn.SQLType.Name, actualColumn.SQLType.Name, columnName)
		assert.Equal(t, expectedColumn.Length, actualColumn.Length, columnName)
		assert.Equal(t, expectedColumn.Nullable, actualColumn.Nullable, columnName)
		assert.Equal(t, expectedColumn.Default, actualColumn.Default, columnName)
	}
}
