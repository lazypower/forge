// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"context"
	"errors"

	"gitea.dev/models/db"
	"gitea.dev/modules/setting"
)

const mutationSavepoint = "work_mutation"

// runMutationSavepoint lets WP7 retain and finalize its pending receipt when a
// deterministic rejection is discovered after tentative domain writes. Every
// other error still escapes and rolls back the complete outer transaction.
func runMutationSavepoint(ctx context.Context, mutate func() (MutationCommit, error)) (MutationCommit, error) {
	if err := beginMutationSavepoint(ctx); err != nil {
		return MutationCommit{}, err
	}
	commit, err := mutate()
	var rejection *rollbackRejection
	if errors.As(err, &rejection) {
		if rollbackErr := rollbackMutationSavepoint(ctx); rollbackErr != nil {
			return MutationCommit{}, rollbackErr
		}
		return rejected(rejection.problemCode), nil
	}
	if err != nil {
		return MutationCommit{}, err
	}
	if err := releaseMutationSavepoint(ctx); err != nil {
		return MutationCommit{}, err
	}
	return commit, nil
}

func beginMutationSavepoint(ctx context.Context) error {
	statement := "SAVEPOINT " + mutationSavepoint
	if setting.Database.Type.IsMSSQL() {
		statement = "SAVE TRANSACTION " + mutationSavepoint
	}
	_, err := db.GetEngine(ctx).Exec(statement)
	return err
}

func rollbackMutationSavepoint(ctx context.Context) error {
	statement := "ROLLBACK TO SAVEPOINT " + mutationSavepoint
	if setting.Database.Type.IsMSSQL() {
		statement = "ROLLBACK TRANSACTION " + mutationSavepoint
	}
	if _, err := db.GetEngine(ctx).Exec(statement); err != nil {
		return err
	}
	return releaseMutationSavepoint(ctx)
}

func releaseMutationSavepoint(ctx context.Context) error {
	if setting.Database.Type.IsMSSQL() {
		return nil
	}
	_, err := db.GetEngine(ctx).Exec("RELEASE SAVEPOINT " + mutationSavepoint)
	return err
}
