// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"time"
)

var errToolCapacityUnavailable = errors.New("MCP tool capacity unavailable")

// toolExecutor owns the endpoint-wide execution budget shared by every tool.
type toolExecutor struct {
	capacity chan struct{}
	timeout  time.Duration
}

func newToolExecutor(maxInFlight int, timeout time.Duration) *toolExecutor {
	return &toolExecutor{capacity: make(chan struct{}, maxInFlight), timeout: timeout}
}

func (e *toolExecutor) begin(parent context.Context) (context.Context, func(), error) {
	if err := parent.Err(); err != nil {
		return nil, nil, err
	}
	select {
	case e.capacity <- struct{}{}:
	case <-parent.Done():
		return nil, nil, parent.Err()
	default:
		return nil, nil, errToolCapacityUnavailable
	}
	executionCtx, cancel := context.WithTimeout(parent, e.timeout)
	return executionCtx, func() {
		cancel()
		<-e.capacity
	}, nil
}

func executionFailureCode(parentCtx, executionCtx context.Context) string {
	if errors.Is(parentCtx.Err(), context.Canceled) {
		return "cancelled"
	}
	if executionCtx != nil && errors.Is(executionCtx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}
	return ""
}
