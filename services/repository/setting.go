// Copyright 2023 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repository

import (
	"context"
	"slices"

	"gitea.dev/models/db"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/modules/log"
	actions_service "gitea.dev/services/actions"
)

// UpdateRepositoryUnits updates a repository's units
func UpdateRepositoryUnits(ctx context.Context, repo *repo_model.Repository, units []repo_model.RepoUnit, deleteUnitTypes []unit.Type) (err error) {
	normalizedDeletes := slices.Clone(deleteUnitTypes)
	replacements := make(map[unit.Type]repo_model.RepoUnit, len(units))
	for _, repoUnit := range units {
		normalizedDeletes = append(normalizedDeletes, repoUnit.Type)
		replacements[repoUnit.Type] = repoUnit
	}
	threatensWorkPlan := false
	if slices.Contains(normalizedDeletes, unit.TypeProjects) {
		_, retained := replacements[unit.TypeProjects]
		threatensWorkPlan = !retained
	}
	if slices.Contains(normalizedDeletes, unit.TypeIssues) {
		issuesUnit, retained := replacements[unit.TypeIssues]
		issuesConfig, valid := issuesUnit.Config.(*repo_model.IssuesConfig)
		threatensWorkPlan = threatensWorkPlan || !retained || !valid || issuesConfig == nil || !issuesConfig.EnableDependencies
	}

	run := db.WithTx
	if threatensWorkPlan {
		run = db.WithWorkTx
	}
	return run(ctx, func(ctx context.Context) error {
		activePlan, err := project_model.HasActiveWorkPlan(ctx, repo.ID)
		if err != nil {
			return err
		}
		if activePlan {
			if slices.Contains(normalizedDeletes, unit.TypeProjects) {
				if _, retained := replacements[unit.TypeProjects]; !retained {
					return project_model.ErrActiveWorkPlan
				}
			}
			if slices.Contains(normalizedDeletes, unit.TypeIssues) {
				issuesUnit, retained := replacements[unit.TypeIssues]
				issuesConfig, valid := issuesUnit.Config.(*repo_model.IssuesConfig)
				if !retained || !valid || issuesConfig == nil || !issuesConfig.EnableDependencies {
					return project_model.ErrActiveWorkPlan
				}
			}
		}

		if slices.Contains(normalizedDeletes, unit.TypeActions) {
			if err := actions_service.CleanRepoScheduleTasks(ctx, repo); err != nil {
				log.Error("CleanRepoScheduleTasks: %v", err)
			}
		}

		for _, u := range units {
			if u.Type == unit.TypeActions {
				if err := actions_service.DetectAndHandleSchedules(ctx, repo); err != nil {
					log.Error("DetectAndHandleSchedules: %v", err)
				}
				break
			}
		}

		if _, err = db.GetEngine(ctx).Where("repo_id = ?", repo.ID).In("type", normalizedDeletes).Delete(new(repo_model.RepoUnit)); err != nil {
			return err
		}

		if len(units) > 0 {
			if err = db.Insert(ctx, units); err != nil {
				return err
			}
		}

		return nil
	})
}
