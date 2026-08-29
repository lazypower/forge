// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build sqlite_mattn

package db

import (
	"errors"

	"github.com/mattn/go-sqlite3"
)

func sqliteDriverWorkTransactionErrorCode(err error) (int, bool) {
	var driverErr sqlite3.Error
	if !errors.As(err, &driverErr) {
		return 0, false
	}
	return int(driverErr.Code), true
}
