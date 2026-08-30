// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package convert

import (
	"testing"
	"time"

	project_model "gitea.dev/models/project"
	api "gitea.dev/modules/structs"
	"gitea.dev/modules/timeutil"

	"github.com/stretchr/testify/assert"
)

func TestToAPIProjectPlanningCompatibility(t *testing.T) {
	created := time.Date(2026, time.August, 28, 12, 0, 0, 0, time.UTC)
	updated := created.Add(time.Hour)
	project := &project_model.Project{
		ID:            42,
		Title:         "Ordinary Project",
		Description:   "An unchanged board",
		RepoID:        7,
		CreatorID:     9,
		PlanningState: project_model.PlanningStateDisabled,
		CreatedUnix:   timeutil.TimeStamp(created.Unix()),
		UpdatedUnix:   timeutil.TimeStamp(updated.Unix()),
	}

	assert.Equal(t, &api.Project{
		ID:          42,
		Title:       "Ordinary Project",
		Description: "An unchanged board",
		RepoID:      7,
		CreatorID:   9,
		Created:     project.CreatedUnix.AsTime(),
		Updated:     project.UpdatedUnix.AsTime(),
	}, ToAPIProject(project))
}
