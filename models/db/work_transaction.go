// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package db

import (
	"context"
	"database/sql"
	"errors"
	"math/rand/v2"
	"time"

	"gitea.dev/modules/setting"

	"github.com/go-sql-driver/mysql"
	"github.com/lib/pq"
	"github.com/lib/pq/pqerror"
)

const (
	workTransactionMaxAttempts = 3
	workTransactionRetryBase   = 10 * time.Millisecond
	workTransactionRetryBudget = 30 * time.Millisecond
)

// WorkTransactionConflict means concurrent work could not be serialized within
// the bounded retry budget. The complete operation is safe to retry.
type WorkTransactionConflict struct{}

func (*WorkTransactionConflict) Error() string {
	return "work transaction conflict"
}

// Retryable reports that callers may safely retry the complete work operation.
func (*WorkTransactionConflict) Retryable() bool {
	return true
}

// WithWorkTx executes a complete work operation in a serializable transaction.
// The callback may run three times, receives a fresh transaction each time, and
// must keep every effect inside that transaction.
func WithWorkTx(ctx context.Context, f func(context.Context) error) error {
	return retryWorkTransaction(ctx, setting.Database.Type, runWorkTransactionAttempt, waitForWorkTransactionRetry, randomWorkTransactionJitter, f)
}

type (
	workTransactionAttempt func(context.Context, func(context.Context) error) error
	workTransactionWait    func(context.Context, time.Duration) error
	workTransactionJitter  func(time.Duration) time.Duration
)

func retryWorkTransaction(ctx context.Context, dbType setting.DatabaseType, attempt workTransactionAttempt, wait workTransactionWait, jitter workTransactionJitter, f func(context.Context) error) error {
	waited := time.Duration(0)
	for attemptIndex := range workTransactionMaxAttempts {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := attempt(ctx, f)
		if err == nil {
			return nil
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if !isRetryableWorkTransactionError(dbType, err) {
			return err
		}
		if attemptIndex == workTransactionMaxAttempts-1 {
			return &WorkTransactionConflict{}
		}

		remaining := workTransactionRetryBudget - waited
		ceiling := min(workTransactionRetryBase<<attemptIndex, remaining)
		delay := min(max(jitter(ceiling), 0), ceiling)
		if err := wait(ctx, delay); err != nil {
			return err
		}
		waited += delay
	}
	panic("unreachable")
}

func runWorkTransactionAttempt(ctx context.Context, f func(context.Context) error) error {
	sess := xormEngine.NewSession()
	defer sess.Close()

	if err := sess.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable}); err != nil {
		return err
	}
	if err := f(withContextEngine(ctx, sess)); err != nil {
		if rollbackErr := sess.Rollback(); rollbackErr != nil && ctx.Err() == nil {
			return rollbackErr
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = sess.Rollback()
		return err
	}
	if err := sess.Commit(); err != nil {
		_ = sess.Rollback()
		return err
	}
	return nil
}

func waitForWorkTransactionRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func randomWorkTransactionJitter(ceiling time.Duration) time.Duration {
	if ceiling <= 0 {
		return 0
	}
	return time.Duration(rand.Int64N(int64(ceiling) + 1))
}

func isRetryableWorkTransactionError(dbType setting.DatabaseType, err error) bool {
	switch {
	case dbType.IsSQLite3():
		code, ok := sqliteDriverWorkTransactionErrorCode(err)
		if !ok {
			return false
		}
		return isRetryableSQLiteWorkTransactionCode(code)
	case dbType.IsMySQL():
		var driverErr *mysql.MySQLError
		if errors.As(err, &driverErr) {
			switch driverErr.Number {
			case 1205, 1213: // lock wait timeout, deadlock
				return true
			}
		}
	case dbType.IsPostgreSQL():
		var driverErr *pq.Error
		if errors.As(err, &driverErr) {
			return driverErr.Code == pqerror.TRSerializationFailure || driverErr.Code == pqerror.TRDeadlockDetected
		}
	case dbType.IsMSSQL():
		var driverErr interface{ SQLErrorNumber() int32 }
		if errors.As(err, &driverErr) {
			return driverErr.SQLErrorNumber() == 1205
		}
	}
	return false
}

func isRetryableSQLiteWorkTransactionCode(code int) bool {
	switch code & 0xff {
	case 5, 6: // SQLITE_BUSY, SQLITE_LOCKED
		return true
	default:
		return false
	}
}
