// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"fmt"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/modules/log"
)

// CancelRun cancels the supplied run's latest-attempt jobs and notifies its observers.
func CancelRun(ctx context.Context, run *actions_model.ActionRun, jobs []*actions_model.ActionRunJob) error {
	var updatedJobs []*actions_model.ActionRunJob

	if err := db.WithTx(ctx, func(ctx context.Context) error {
		cancelledJobs, err := actions_model.CancelJobs(ctx, jobs)
		if err != nil {
			return fmt.Errorf("cancel jobs: %w", err)
		}
		updatedJobs = append(updatedJobs, cancelledJobs...)
		if len(updatedJobs) > 0 {
			return nil // a job update already refreshed the run
		}
		return actions_model.SettleRunAfterCancel(ctx, run)
	}); err != nil {
		return err
	}

	CreateCommitStatusForRunJobs(ctx, run, jobs...)
	EmitJobsIfReadyByJobs(updatedJobs)
	NotifyWorkflowJobsStatusUpdate(ctx, updatedJobs...)
	// SettleRunAfterCancel finishes a run without updating any job, so compare the run itself.
	if reloaded, err := actions_model.GetRunByRepoAndID(ctx, run.RepoID, run.ID); err != nil {
		log.Error("GetRunByRepoAndID: %v", err)
	} else if len(updatedJobs) > 0 || reloaded.Status != run.Status {
		NotifyWorkflowRunStatusUpdate(ctx, reloaded)
	}

	return nil
}
