// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"context"
	"testing"

	mcpwork_model "gitea.dev/models/mcpwork"
	"gitea.dev/models/migrations/migrationtest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type freshAttributedReceipt mcpwork_model.Receipt

func (*freshAttributedReceipt) TableName() string { return "fresh_attributed_receipt" }

func Test_AddMCPWorkClientAttribution(t *testing.T) {
	x, cleanup := migrationtest.PrepareTestEnv(t, 0)
	defer cleanup()
	if x == nil || t.Failed() {
		return
	}
	require.NoError(t, AddMCPWorkReceiptSchema(x))
	require.NoError(t, AddMCPWorkClientAttribution(x))
	require.NoError(t, x.Sync(new(freshAttributedReceipt)))
	tables := migrationtest.LoadTableSchemasMap(t, x)
	assertMCPWorkTableMatchesFresh(t, tables, "mcp_work_receipt", "fresh_attributed_receipt")
	assert.Nil(t, tables["mcp_work_receipt"].GetColumn("actor_trust"))
	indexes, err := x.Dialect().GetIndexes(x.DB(), context.Background(), "mcp_work_receipt")
	require.NoError(t, err)
	assert.True(t, hasIndexWithColumns(indexes, []string{"principal_id", "audience_digest", "key_digest"}, true))
	for _, columns := range [][]string{{"operation_uuid"}, {"principal_id"}, {"application_id"}, {"grant_id"}, {"committed_unix"}, {"tombstoned_unix"}} {
		assert.True(t, hasIndexWithColumns(indexes, columns, false))
	}
	count, err := x.Count(new(mcpwork_model.Receipt))
	require.NoError(t, err)
	assert.Zero(t, count)
	receipt := &mcpwork_model.Receipt{
		OperationUUID: "11111111-1111-4111-8111-111111111111", PrincipalID: 1,
		AudienceDigest: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		KeyDigest:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		RequestDigest:  "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		Tool:           "work_plan.begin", SchemaVersion: "1", ApplicationID: 2, GrantID: 3,
		CredentialID: "22222222-2222-4222-8222-222222222222", Scope: "read:repository write:issue write:repository",
		Origin: "mcp", Outcome: mcpwork_model.OutcomeApplied, Profile: "work-planning",
		RegisteredClientLabel: "Example Client", RegisteredInstallationLabel: "Example Installation",
		Harness: "Example Harness", Model: "Example Model", AttributionSource: "client-reported",
	}
	_, err = x.Insert(receipt)
	require.NoError(t, err)
	duplicate := *receipt
	duplicate.ID = 0
	_, err = x.Insert(&duplicate)
	require.Error(t, err)
	stored := new(mcpwork_model.Receipt)
	has, err := x.ID(receipt.ID).Get(stored)
	require.NoError(t, err)
	require.True(t, has)
	assert.Equal(t, receipt.RegisteredClientLabel, stored.RegisteredClientLabel)
	assert.Equal(t, receipt.Model, stored.Model)
	assert.Equal(t, receipt.AttributionSource, stored.AttributionSource)
}
