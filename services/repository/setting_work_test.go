// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"testing"

	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/require"
)

func TestUpdateRepositoryUnitsProtectsActiveWorkPlanAuthorities(t *testing.T) {
	tests := []struct {
		name    string
		units   []repo_model.RepoUnit
		deletes []unit.Type
		wantErr bool
	}{
		{name: "remove projects", deletes: []unit.Type{unit.TypeProjects}, wantErr: true},
		{name: "remove issues", deletes: []unit.Type{unit.TypeIssues}, wantErr: true},
		{name: "disable dependencies", units: []repo_model.RepoUnit{{Type: unit.TypeIssues, Config: &repo_model.IssuesConfig{EnableDependencies: false}}}, wantErr: true},
		{name: "invalid issues config", units: []repo_model.RepoUnit{{Type: unit.TypeIssues, Config: &repo_model.UnitConfig{}}}, wantErr: true},
		{name: "retain projects", units: []repo_model.RepoUnit{{Type: unit.TypeProjects, Config: &repo_model.ProjectsConfig{ProjectsMode: repo_model.ProjectsModeAll}}}},
		{name: "retain dependencies", units: []repo_model.RepoUnit{{Type: unit.TypeIssues, Config: &repo_model.IssuesConfig{EnableDependencies: true}}}},
		{name: "unrelated unit", deletes: []unit.Type{unit.TypeWiki}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.NoError(t, unittest.PrepareTestDatabase())
			repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
			plan := &project_model.Project{
				Type: project_model.TypeRepository, Title: "Active plan", RepoID: repo.ID, CreatorID: 2,
				PlanningState: project_model.PlanningStateActive,
			}
			require.NoError(t, project_model.NewProject(t.Context(), plan))
			for index := range test.units {
				test.units[index].RepoID = repo.ID
			}
			err := UpdateRepositoryUnits(t.Context(), repo, test.units, test.deletes)
			if test.wantErr {
				require.ErrorIs(t, err, project_model.ErrActiveWorkPlan)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
