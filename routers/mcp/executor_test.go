// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolExecutorNonBlockingSharedCapacity(t *testing.T) {
	executor := newToolExecutor(1, time.Second)
	_, release, err := executor.begin(t.Context())
	require.NoError(t, err)

	_, _, err = executor.begin(t.Context())
	assert.ErrorIs(t, err, errToolCapacityUnavailable)
	release()

	_, recovered, err := executor.begin(t.Context())
	require.NoError(t, err)
	recovered()
}

func TestToolExecutorCancellationAndTimeout(t *testing.T) {
	t.Run("cancelled before admission", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, _, err := newToolExecutor(1, time.Second).begin(ctx)
		assert.ErrorIs(t, err, context.Canceled)
	})

	t.Run("execution deadline", func(t *testing.T) {
		executionCtx, release, err := newToolExecutor(1, time.Millisecond).begin(t.Context())
		require.NoError(t, err)
		defer release()
		<-executionCtx.Done()
		assert.Equal(t, "timeout", executionFailureCode(t.Context(), executionCtx))
	})
}
