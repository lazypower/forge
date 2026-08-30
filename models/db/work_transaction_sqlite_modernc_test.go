// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !sqlite_mattn

package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLiteWorkTransactionErrorIdentity(t *testing.T) {
	driver, connection, err := makeSQLiteConnStr(SQLiteConnStrOptions{
		FilePath:    filepath.Join(t.TempDir(), "work-transaction.db"),
		BusyTimeout: 1,
	})
	require.NoError(t, err)
	database, err := sql.Open(driver, connection)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, database.Close())
	})
	database.SetMaxOpenConns(2)

	first, err := database.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = first.Rollback()
	})
	_, err = database.BeginTx(t.Context(), &sql.TxOptions{Isolation: sql.LevelSerializable})
	require.Error(t, err)
	assert.True(t, isRetryableWorkTransactionError(setting.DatabaseTypeSQLite3, fmt.Errorf("wrapped: %w", err)))
}
