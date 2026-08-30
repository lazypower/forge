// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"errors"
	"testing"

	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestDeleteRepositoryMCPReceiptRetirementFailureRollsBack(t *testing.T) {
	unittest.PrepareTestEnv(t)
	repository := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	injected := errors.New("receipt retirement unavailable")
	defer test.MockVariableValue(&retireRepositoryMCPWorkReceipts, func(context.Context, int64) error {
		return injected
	})()

	err := DeleteRepositoryDirectly(t.Context(), repository.ID)
	require.ErrorIs(t, err, injected)
	unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repository.ID})
}
