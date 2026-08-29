// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gitea.dev/modules/setting"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mssqlNumberError struct {
	number int32
	text   string
}

func (err mssqlNumberError) Error() string         { return err.text }
func (err mssqlNumberError) SQLErrorNumber() int32 { return err.number }

func TestRetryableWorkTransactionErrors(t *testing.T) {
	tests := []struct {
		name   string
		dbType setting.DatabaseType
		err    error
	}{
		{name: "mysql lock wait", dbType: "mysql", err: &mysql.MySQLError{Number: 1205, Message: "private mysql text"}},
		{name: "mysql deadlock", dbType: "mysql", err: &mysql.MySQLError{Number: 1213, Message: "private mysql text"}},
		{name: "postgres serialization", dbType: "postgres", err: &pq.Error{Code: "40001", Message: "private postgres text"}},
		{name: "postgres deadlock", dbType: "postgres", err: &pq.Error{Code: "40P01", Message: "private postgres text"}},
		{name: "mssql deadlock", dbType: "mssql", err: mssqlNumberError{number: 1205, text: "private mssql text"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.True(t, isRetryableWorkTransactionError(test.dbType, fmt.Errorf("wrapped: %w", test.err)))
		})
	}
}

func TestRetryableSQLiteWorkTransactionCodes(t *testing.T) {
	assert.True(t, isRetryableSQLiteWorkTransactionCode(5))
	assert.True(t, isRetryableSQLiteWorkTransactionCode(6|1<<8))
	assert.False(t, isRetryableSQLiteWorkTransactionCode(19))
}

func TestNonRetryableWorkTransactionErrors(t *testing.T) {
	tests := []struct {
		name   string
		dbType setting.DatabaseType
		err    error
	}{
		{name: "mysql duplicate", dbType: "mysql", err: &mysql.MySQLError{Number: 1062, Message: "duplicate"}},
		{name: "postgres constraint", dbType: "postgres", err: &pq.Error{Code: "23505", Message: "duplicate"}},
		{name: "mssql constraint", dbType: "mssql", err: mssqlNumberError{number: 2627, text: "duplicate"}},
		{name: "wrong backend", dbType: "postgres", err: &mysql.MySQLError{Number: 1213, Message: "deadlock"}},
		{name: "untyped", dbType: "postgres", err: errors.New("driver-looking text 40001")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.False(t, isRetryableWorkTransactionError(test.dbType, test.err))
		})
	}
}

func TestWorkTransactionRetryBudget(t *testing.T) {
	attempts := 0
	delays := make([]time.Duration, 0, workTransactionMaxAttempts-1)
	err := retryWorkTransaction(t.Context(), "mysql",
		func(context.Context, func(context.Context) error) error {
			attempts++
			return &mysql.MySQLError{Number: 1213, Message: "private driver text"}
		},
		func(_ context.Context, delay time.Duration) error {
			delays = append(delays, delay)
			return nil
		},
		func(ceiling time.Duration) time.Duration { return ceiling },
		func(context.Context) error { return nil },
	)

	var conflict *WorkTransactionConflict
	require.ErrorAs(t, err, &conflict)
	assert.True(t, conflict.Retryable())
	assert.NotContains(t, err.Error(), "private driver text")
	assert.NoError(t, errors.Unwrap(err))
	assert.Equal(t, workTransactionMaxAttempts, attempts)
	assert.Equal(t, []time.Duration{workTransactionRetryBase, 2 * workTransactionRetryBase}, delays)
	assert.Equal(t, workTransactionRetryBudget, delays[0]+delays[1])
}

func TestWorkTransactionCancellationPreventsRetry(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	attempts := 0
	waits := 0
	err := retryWorkTransaction(ctx, "mysql",
		func(context.Context, func(context.Context) error) error {
			attempts++
			cancel()
			return &mysql.MySQLError{Number: 1213, Message: "locked"}
		},
		func(context.Context, time.Duration) error {
			waits++
			return nil
		},
		func(time.Duration) time.Duration { return 0 },
		func(context.Context) error { return nil },
	)

	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, 1, attempts)
	assert.Zero(t, waits)
}
