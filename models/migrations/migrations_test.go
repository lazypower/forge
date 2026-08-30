// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package migrations

import (
	"testing"

	"gitea.dev/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrations(t *testing.T) {
	defer test.MockVariableValue(&preparedMigrations)()
	preparedMigrations = []*migration{
		{idNumber: 70},
		{idNumber: 71},
	}
	assert.EqualValues(t, 72, calcDBVersion(preparedMigrations))
	assert.EqualValues(t, 72, ExpectedDBVersion())

	assert.EqualValues(t, 71, migrationIDNumberToDBVersion(70))

	assert.Equal(t, []*migration{{idNumber: 70}, {idNumber: 71}}, getPendingMigrations(70, preparedMigrations))
	assert.Equal(t, []*migration{{idNumber: 71}}, getPendingMigrations(71, preparedMigrations))
	assert.Equal(t, []*migration{}, getPendingMigrations(72, preparedMigrations))
}

func TestMCPWorkReceiptMigrationOrdering(t *testing.T) {
	defer test.MockVariableValue(&preparedMigrations)()
	preparedMigrations = nil
	migrations := prepareMigrationTasks()
	require.GreaterOrEqual(t, len(migrations), 5)
	assert.EqualValues(t, 344, migrations[len(migrations)-5].idNumber)
	assert.EqualValues(t, 345, migrations[len(migrations)-4].idNumber)
	assert.EqualValues(t, 346, migrations[len(migrations)-3].idNumber)
	assert.EqualValues(t, 347, migrations[len(migrations)-2].idNumber)
	assert.EqualValues(t, 348, migrations[len(migrations)-1].idNumber)
	assert.EqualValues(t, 349, ExpectedDBVersion())
}
