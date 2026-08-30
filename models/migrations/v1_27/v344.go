// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"gitea.dev/models/db"

	"xorm.io/xorm"
)

type projectPlanningState struct {
	PlanningState uint8 `xorm:"INDEX NOT NULL DEFAULT 0"`
}

func (projectPlanningState) TableName() string {
	return "project"
}

// AddPlanningStateToProject leaves every existing Project as an ordinary board.
func AddPlanningStateToProject(x db.EngineMigration) error {
	_, err := x.SyncWithOptions(xorm.SyncOptions{
		IgnoreDropIndices: true,
		IgnoreConstrains:  true,
	}, new(projectPlanningState))
	return err
}
