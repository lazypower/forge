// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build sqlite_mattn

package db

import (
	"fmt"
	"testing"

	"gitea.dev/modules/setting"

	"github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
)

func TestSQLiteWorkTransactionErrorIdentity(t *testing.T) {
	err := sqlite3.Error{Code: sqlite3.ErrBusy, ExtendedCode: sqlite3.ErrBusyRecovery}
	assert.True(t, isRetryableWorkTransactionError(setting.DatabaseTypeSQLite3, fmt.Errorf("wrapped: %w", err)))
}
