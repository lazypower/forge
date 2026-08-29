// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type workTransactionInvariantEdge struct {
	ID           int64  `xorm:"pk autoincr"`
	Blocked      string `xorm:"VARCHAR(64) NOT NULL"`
	Prerequisite string `xorm:"VARCHAR(64) NOT NULL"`
}

func (*workTransactionInvariantEdge) TableName() string {
	return "test_work_transaction_invariant_edge"
}

func TestWorkTransactionConcurrentInvariant(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	x := unittest.GetXORMEngine()
	_, _ = x.Exec("DROP TABLE IF EXISTS test_work_transaction_invariant_edge")
	require.NoError(t, x.Sync(new(workTransactionInvariantEdge)))
	t.Cleanup(func() {
		_, _ = x.Exec("DROP TABLE IF EXISTS test_work_transaction_invariant_edge")
	})

	errCycle := errors.New("cycle")
	addEdge := func(ctx context.Context, blocked, prerequisite string) error {
		reverseCount, err := db.GetEngine(ctx).Where("blocked = ? AND prerequisite = ?", prerequisite, blocked).Count(new(workTransactionInvariantEdge))
		if err != nil {
			return err
		}
		if reverseCount != 0 {
			return errCycle
		}
		_, err = db.GetEngine(ctx).Insert(&workTransactionInvariantEdge{Blocked: blocked, Prerequisite: prerequisite})
		return err
	}

	results := make(chan error, 2)
	if setting.Database.Type.IsSQLite3() {
		firstEntered := make(chan struct{})
		releaseFirst := make(chan struct{})
		secondEntered := make(chan struct{})
		go func() {
			results <- db.WithWorkTx(ctx, func(ctx context.Context) error {
				close(firstEntered)
				<-releaseFirst
				return addEdge(ctx, "issue-a", "issue-b")
			})
		}()
		<-firstEntered
		go func() {
			results <- db.WithWorkTx(ctx, func(ctx context.Context) error {
				close(secondEntered)
				return addEdge(ctx, "issue-b", "issue-a")
			})
		}()
		select {
		case <-secondEntered:
			t.Fatal("second SQLite callback entered before the first transaction completed")
		case <-time.After(20 * time.Millisecond):
		}
		close(releaseFirst)
	} else {
		checked := make(chan struct{}, 2)
		release := make(chan struct{})
		start := func(blocked, prerequisite string) {
			go func() {
				results <- db.WithWorkTx(ctx, func(ctx context.Context) error {
					reverseCount, err := db.GetEngine(ctx).Where("blocked = ? AND prerequisite = ?", prerequisite, blocked).Count(new(workTransactionInvariantEdge))
					if err != nil {
						return err
					}
					if reverseCount != 0 {
						return errCycle
					}
					checked <- struct{}{}
					<-release
					_, err = db.GetEngine(ctx).Insert(&workTransactionInvariantEdge{Blocked: blocked, Prerequisite: prerequisite})
					return err
				})
			}()
		}
		start("issue-a", "issue-b")
		start("issue-b", "issue-a")
		select {
		case <-checked:
		case <-time.After(5 * time.Second):
			t.Fatal("first serializable transaction did not reach invariant validation")
		}
		select {
		case <-checked:
		case <-time.After(5 * time.Second):
			t.Fatal("second serializable transaction did not reach invariant validation")
		}
		close(release)
	}

	firstResult := <-results
	secondResult := <-results
	assert.True(t, (firstResult == nil && errors.Is(secondResult, errCycle)) || (secondResult == nil && errors.Is(firstResult, errCycle)), "results: %v, %v", firstResult, secondResult)
	count, err := x.Count(new(workTransactionInvariantEdge))
	require.NoError(t, err)
	assert.EqualValues(t, 1, count)
}
