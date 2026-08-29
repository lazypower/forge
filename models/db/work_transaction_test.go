// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package db_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"

	"github.com/go-sql-driver/mysql"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkTransactionRetryRollsBackEveryAttempt(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.Exec(t.Context(), "DROP TABLE IF EXISTS work_transaction_effect")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "CREATE TABLE work_transaction_effect (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS work_transaction_effect")
	})

	engines := make([]db.Engine, 0, 3)
	databaseType := setting.Database.Type
	setting.Database.Type = "mysql"
	t.Cleanup(func() {
		setting.Database.Type = databaseType
	})
	err = db.WithWorkTx(t.Context(), func(ctx context.Context) error {
		engines = append(engines, db.GetEngine(ctx))
		if _, err := db.Exec(ctx, "INSERT INTO work_transaction_effect (id) VALUES (?)", len(engines)); err != nil {
			return err
		}
		return &mysql.MySQLError{Number: 1213, Message: "private mysql text"}
	})

	var conflict *db.WorkTransactionConflict
	require.ErrorAs(t, err, &conflict)
	require.Len(t, engines, 3)
	assert.NotSame(t, engines[0], engines[1])
	assert.NotSame(t, engines[1], engines[2])
	var count int64
	has, queryErr := db.GetEngine(t.Context()).SQL("SELECT COUNT(*) FROM work_transaction_effect").Get(&count)
	require.NoError(t, queryErr)
	require.True(t, has)
	assert.Zero(t, count)
}

func TestWorkTransactionRejectsOrdinaryTransactionParent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.Exec(t.Context(), "DROP TABLE IF EXISTS work_transaction_nested_effect")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "CREATE TABLE work_transaction_nested_effect (id INTEGER PRIMARY KEY)")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS work_transaction_nested_effect")
	})

	innerCalled := false
	errOuterRollback := errors.New("outer rollback")
	err = db.WithTx(t.Context(), func(ctx context.Context) error {
		_, err := db.Exec(ctx, "INSERT INTO work_transaction_nested_effect (id) VALUES (?)", 1)
		require.NoError(t, err)

		err = db.WithWorkTx(ctx, func(context.Context) error {
			innerCalled = true
			return nil
		})
		require.ErrorIs(t, err, db.ErrWorkTransactionNested)
		return errOuterRollback
	})

	require.ErrorIs(t, err, errOuterRollback)
	assert.False(t, innerCalled)
	var count int64
	has, queryErr := db.GetEngine(t.Context()).SQL("SELECT COUNT(*) FROM work_transaction_nested_effect").Get(&count)
	require.NoError(t, queryErr)
	require.True(t, has)
	assert.Zero(t, count)
}

func TestWorkTransactionRejectsWorkTransactionParent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	innerCalled := false

	err := db.WithWorkTx(t.Context(), func(ctx context.Context) error {
		err := db.WithWorkTx(ctx, func(context.Context) error {
			innerCalled = true
			return nil
		})
		require.ErrorIs(t, err, db.ErrWorkTransactionNested)
		return nil
	})

	require.NoError(t, err)
	assert.False(t, innerCalled)
}

func TestOrdinaryTransactionJoinsWorkTransaction(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	var workEngine, ordinaryEngine db.Engine

	err := db.WithWorkTx(t.Context(), func(ctx context.Context) error {
		workEngine = db.GetEngine(ctx)
		return db.WithTx(ctx, func(ctx context.Context) error {
			ordinaryEngine = db.GetEngine(ctx)
			return nil
		})
	})

	require.NoError(t, err)
	assert.Same(t, workEngine, ordinaryEngine)
}

func TestWorkTransactionCancellationReachesDatabase(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	ctx, cancel := context.WithCancel(context.WithValue(t.Context(), db.ContextKeyTestFixtures, true))
	started := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		result <- db.WithWorkTx(ctx, func(ctx context.Context) error {
			close(started)
			_, err := db.GetEngine(ctx).QueryInterface(`WITH RECURSIVE sequence(value) AS (
				VALUES(0) UNION ALL SELECT value + 1 FROM sequence WHERE value < 100000000
			) SELECT sum(value) FROM sequence`)
			return err
		})
	}()

	<-started
	cancel()
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("database work did not observe cancellation")
	}
}

func TestWorkTransactionSerializesConcurrentInvariant(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.Exec(t.Context(), "DROP TABLE IF EXISTS work_transaction_edge")
	require.NoError(t, err)
	_, err = db.Exec(t.Context(), "CREATE TABLE work_transaction_edge (blocked TEXT NOT NULL, prerequisite TEXT NOT NULL, UNIQUE(blocked, prerequisite))")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(context.Background(), "DROP TABLE IF EXISTS work_transaction_edge")
	})

	firstChecked := make(chan struct{})
	releaseFirst := make(chan struct{})
	secondEntered := make(chan struct{})
	results := make(chan error, 2)
	errCycle := errors.New("cycle")
	addEdge := func(ctx context.Context, blocked, prerequisite string) error {
		var reverseCount int64
		has, err := db.GetEngine(ctx).SQL("SELECT COUNT(*) FROM work_transaction_edge WHERE blocked = ? AND prerequisite = ?", prerequisite, blocked).Get(&reverseCount)
		if err != nil {
			return err
		}
		if !has {
			return errors.New("invariant query returned no row")
		}
		if reverseCount != 0 {
			return errCycle
		}
		_, err = db.Exec(ctx, "INSERT INTO work_transaction_edge (blocked, prerequisite) VALUES (?, ?)", blocked, prerequisite)
		return err
	}

	go func() {
		results <- db.WithWorkTx(t.Context(), func(ctx context.Context) error {
			close(firstChecked)
			<-releaseFirst
			return addEdge(ctx, "issue-a", "issue-b")
		})
	}()
	<-firstChecked
	go func() {
		results <- db.WithWorkTx(t.Context(), func(ctx context.Context) error {
			close(secondEntered)
			return addEdge(ctx, "issue-b", "issue-a")
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("second serializable callback entered before the first transaction completed")
	case <-time.After(20 * time.Millisecond):
	}
	close(releaseFirst)

	firstResult := <-results
	secondResult := <-results
	assert.True(t, (firstResult == nil && errors.Is(secondResult, errCycle)) || (secondResult == nil && errors.Is(firstResult, errCycle)), "results: %v, %v", firstResult, secondResult)
	var count int64
	has, err := db.GetEngine(t.Context()).SQL("SELECT COUNT(*) FROM work_transaction_edge").Get(&count)
	require.NoError(t, err)
	require.True(t, has)
	assert.EqualValues(t, 1, count)
}
