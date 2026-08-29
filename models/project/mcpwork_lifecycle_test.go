// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package project

import (
	"context"
	"errors"
	"testing"

	"gitea.dev/models/unittest"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/require"
)

func TestDeleteProjectMCPReceiptRetirementFailureRollsBack(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	project := unittest.AssertExistsAndLoadBean(t, &Project{ID: 1, RepoID: 1})
	injected := errors.New("receipt retirement unavailable")
	defer test.MockVariableValue(&retireProjectMCPWorkReceipts, func(context.Context, int64, int64) error {
		return injected
	})()

	err := DeleteProjectByID(t.Context(), project.ID)
	require.ErrorIs(t, err, injected)
	unittest.AssertExistsAndLoadBean(t, &Project{ID: project.ID, RepoID: project.RepoID})
}

func TestDeleteProjectsByRepoMCPReceiptRetirementFailureRollsBack(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	project := unittest.AssertExistsAndLoadBean(t, &Project{ID: 1, RepoID: 1})
	injected := errors.New("receipt retirement unavailable")
	defer test.MockVariableValue(&retireRepoProjectMCPWorkReceipts, func(context.Context, int64) error {
		return injected
	})()

	err := DeleteProjectByRepoID(t.Context(), project.RepoID)
	require.ErrorIs(t, err, injected)
	unittest.AssertExistsAndLoadBean(t, &Project{ID: project.ID, RepoID: project.RepoID})
}
