// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package project

import (
	"testing"

	"gitea.dev/models/db"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/timeutil"
	"gitea.dev/modules/util"

	"github.com/stretchr/testify/assert"
)

func TestIsProjectTypeValid(t *testing.T) {
	const UnknownType Type = 15

	cases := []struct {
		typ   Type
		valid bool
	}{
		{TypeIndividual, true},
		{TypeRepository, true},
		{TypeOrganization, true},
		{UnknownType, false},
	}

	for _, v := range cases {
		assert.Equal(t, v.valid, IsTypeValid(v.typ))
	}
}

func TestIsPlanningStateValid(t *testing.T) {
	const unknownState PlanningState = 255

	tests := []struct {
		state   PlanningState
		valid   bool
		enabled bool
	}{
		{state: PlanningStateDisabled, valid: true},
		{state: PlanningStateDraft, valid: true, enabled: true},
		{state: PlanningStateActive, valid: true, enabled: true},
		{state: unknownState},
	}

	for _, tt := range tests {
		project := Project{PlanningState: tt.state}
		assert.Equal(t, tt.valid, project.HasValidPlanningState())
		assert.Equal(t, tt.enabled, project.IsPlanningEnabled())
	}
}

func TestGetProjects(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	projects, err := db.Find[Project](t.Context(), SearchOptions{RepoID: 1})
	assert.NoError(t, err)

	// 1 value for this repo exists in the fixtures
	assert.Len(t, projects, 1)

	projects, err = db.Find[Project](t.Context(), SearchOptions{RepoID: 3})
	assert.NoError(t, err)

	// 1 value for this repo exists in the fixtures
	assert.Len(t, projects, 1)
}

func TestProject(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	project := &Project{
		Type:         TypeRepository,
		TemplateType: TemplateTypeBasicKanban,
		CardType:     CardTypeTextOnly,
		Title:        "New Project",
		RepoID:       1,
		CreatedUnix:  timeutil.TimeStampNow(),
		CreatorID:    2,
	}

	assert.NoError(t, NewProject(t.Context(), project))
	assert.Equal(t, PlanningStateDisabled, project.PlanningState)

	projectFromDB, err := GetProjectByID(t.Context(), project.ID)
	assert.NoError(t, err)
	assert.Equal(t, PlanningStateDisabled, projectFromDB.PlanningState)

	// Update project
	project.Title = "Updated title"
	assert.NoError(t, UpdateProject(t.Context(), project))

	projectFromDB, err = GetProjectByID(t.Context(), project.ID)
	assert.NoError(t, err)

	assert.Equal(t, project.Title, projectFromDB.Title)

	assert.NoError(t, ChangeProjectStatus(t.Context(), project, true))

	// Retrieve from DB afresh to check if it is truly closed
	projectFromDB, err = GetProjectByID(t.Context(), project.ID)
	assert.NoError(t, err)

	assert.True(t, projectFromDB.IsClosed)
}

func TestNewProjectRejectsUnknownPlanningState(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	project := &Project{
		Type:          TypeRepository,
		Title:         "Unknown planning state",
		RepoID:        1,
		CreatorID:     2,
		PlanningState: 255,
	}
	err := NewProject(t.Context(), project)
	assert.ErrorIs(t, err, util.ErrInvalidArgument)
	assert.EqualError(t, err, "project planning state is not valid")
	assert.Zero(t, project.ID)
}

func TestProjectsSort(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	tests := []struct {
		sortType string
		wants    []int64
	}{
		{
			sortType: "default",
			wants:    []int64{1, 3, 2, 6, 5, 4},
		},
		{
			sortType: "oldest",
			wants:    []int64{4, 5, 6, 2, 3, 1},
		},
		{
			sortType: "recentupdate",
			wants:    []int64{1, 3, 2, 6, 5, 4},
		},
		{
			sortType: "leastupdate",
			wants:    []int64{4, 5, 6, 2, 3, 1},
		},
	}

	for _, tt := range tests {
		projects, count, err := db.FindAndCount[Project](t.Context(), SearchOptions{
			OrderBy: GetSearchOrderByBySortType(tt.sortType),
		})
		assert.NoError(t, err)
		assert.Equal(t, int64(6), count)
		if assert.Len(t, projects, 6) {
			for i := range projects {
				assert.Equal(t, tt.wants[i], projects[i].ID)
			}
		}
	}
}
