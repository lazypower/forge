// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

//go:build !sqlite_mattn

package db

import (
	"errors"

	"modernc.org/sqlite"
)

func sqliteDriverWorkTransactionErrorCode(err error) (int, bool) {
	var driverErr *sqlite.Error
	if !errors.As(err, &driverErr) {
		return 0, false
	}
	return driverErr.Code(), true
}
