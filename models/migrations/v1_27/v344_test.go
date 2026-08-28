// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package v1_27

import (
	"testing"

	"gitea.dev/models/db"
	"gitea.dev/models/migrations/migrationtest"
	project_model "gitea.dev/models/project"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"xorm.io/xorm/schemas"
)

type projectBeforePlanningState struct {
	ID    int64  `xorm:"pk autoincr"`
	Title string `xorm:"INDEX NOT NULL"`
}

func (projectBeforePlanningState) TableName() string {
	return "project"
}

type projectAfterPlanningState struct {
	ID            int64                       `xorm:"pk autoincr"`
	Title         string                      `xorm:"INDEX NOT NULL"`
	PlanningState project_model.PlanningState `xorm:"INDEX NOT NULL DEFAULT 0"`
}

func (projectAfterPlanningState) TableName() string {
	return "project"
}

type freshProjectPlanningStateSchema project_model.Project

func (freshProjectPlanningStateSchema) TableName() string {
	return "fresh_project_planning_state_schema"
}

func Test_AddPlanningStateToProject(t *testing.T) {
	x, deferable := migrationtest.PrepareTestEnv(t, 0, new(projectBeforePlanningState))
	defer deferable()
	if x == nil || t.Failed() {
		return
	}

	ordinaryProjects := []*projectBeforePlanningState{
		{Title: "First ordinary Project"},
		{Title: "Second ordinary Project"},
	}
	_, err := x.Insert(ordinaryProjects)
	require.NoError(t, err)

	require.NoError(t, AddPlanningStateToProject(x))

	var migratedProjects []*projectAfterPlanningState
	require.NoError(t, x.OrderBy("id").Find(&migratedProjects))
	require.Len(t, migratedProjects, len(ordinaryProjects))
	for i, project := range migratedProjects {
		assert.NotZero(t, project.ID)
		assert.Equal(t, ordinaryProjects[i].Title, project.Title)
		assert.Equal(t, project_model.PlanningStateDisabled, project.PlanningState)
	}

	assertFreshAndUpgradedPlanningStateSchemasAgree(t, x)

	_, err = x.ID(migratedProjects[0].ID).Cols("planning_state").Update(&projectAfterPlanningState{PlanningState: 255})
	require.NoError(t, err)
	var unknown projectAfterPlanningState
	has, err := x.ID(migratedProjects[0].ID).Get(&unknown)
	require.NoError(t, err)
	require.True(t, has)
	assert.False(t, project_model.IsPlanningStateValid(unknown.PlanningState))
}

func assertFreshAndUpgradedPlanningStateSchemasAgree(t *testing.T, x db.EngineMigration) {
	t.Helper()
	require.NoError(t, x.Sync(new(freshProjectPlanningStateSchema)))
	upgradedSchemas, err := x.DBMetas()
	require.NoError(t, err)

	var upgradedSchema *schemas.Table
	var freshSchema *schemas.Table
	for _, table := range upgradedSchemas {
		switch table.Name {
		case "project":
			upgradedSchema = table
		case "fresh_project_planning_state_schema":
			freshSchema = table
		}
	}
	require.NotNil(t, upgradedSchema)
	require.NotNil(t, freshSchema)

	freshColumn := freshSchema.GetColumn("planning_state")
	upgradedColumn := upgradedSchema.GetColumn("planning_state")
	require.NotNil(t, freshColumn)
	require.NotNil(t, upgradedColumn)
	assert.Equal(t, freshColumn.SQLType.Name, upgradedColumn.SQLType.Name)
	assert.Equal(t, freshColumn.Length, upgradedColumn.Length)
	assert.Equal(t, freshColumn.Nullable, upgradedColumn.Nullable)
	assert.Equal(t, freshColumn.Default, upgradedColumn.Default)
	assert.True(t, hasColumnIndex(freshSchema, "planning_state"))
	assert.True(t, hasColumnIndex(upgradedSchema, "planning_state"))
}

func hasColumnIndex(table *schemas.Table, column string) bool {
	for _, index := range table.Indexes {
		if len(index.Cols) == 1 && index.Cols[0] == column {
			return true
		}
	}
	return false
}
