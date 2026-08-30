// Copyright 2021 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues

import (
	"context"
	"errors"

	"gitea.dev/models/db"
	project_model "gitea.dev/models/project"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
)

// ErrActivePlanMembership prevents legacy whole-set callers from silently
// removing or adding cards in an active Work plan. Semantic Work mutations use
// EnsureIssueProjectInWorkTx after validating the complete plan.
var ErrActivePlanMembership = errors.New("active work plan membership requires the Work service")

// LoadProjects loads all projects the issue is assigned to
func (issue *Issue) LoadProjects(ctx context.Context) (err error) {
	if !issue.isProjectsLoaded {
		err = db.GetEngine(ctx).Table("project").
			Join("INNER", "project_issue", "project.id=project_issue.project_id").
			Where("project_issue.issue_id = ?", issue.ID).
			OrderBy("project.id ASC").
			Find(&issue.Projects)
		if err == nil {
			issue.isProjectsLoaded = true
		}
	}
	return err
}

func (issue *Issue) projectIDs(ctx context.Context) (projectIDs []int64, _ error) {
	err := db.GetEngine(ctx).Table("project_issue").Where("issue_id = ?", issue.ID).Cols("project_id").Find(&projectIDs)
	return projectIDs, err
}

// ProjectColumnMap returns a map of project ID to column ID for this issue.
func (issue *Issue) ProjectColumnMap(ctx context.Context) (map[int64]int64, error) {
	var projIssues []project_model.ProjectIssue
	if err := db.GetEngine(ctx).Where("issue_id=?", issue.ID).Find(&projIssues); err != nil {
		return nil, err
	}

	result := make(map[int64]int64, len(projIssues))
	for _, projIssue := range projIssues {
		result[projIssue.ProjectID] = projIssue.ProjectColumnID
	}
	return result, nil
}

func LoadProjectIssueColumnMap(ctx context.Context, projectID, defaultColumnID int64) (map[int64]int64, error) {
	issues := make([]project_model.ProjectIssue, 0)
	if err := db.GetEngine(ctx).Where("project_id=?", projectID).Find(&issues); err != nil {
		return nil, err
	}
	result := make(map[int64]int64, len(issues))
	for _, issue := range issues {
		if issue.ProjectColumnID == 0 {
			issue.ProjectColumnID = defaultColumnID
		}
		result[issue.IssueID] = issue.ProjectColumnID
	}
	return result, nil
}

// IssueAssignOrRemoveProject updates the projects associated with an issue.
// It adds projects that are in newProjectIDs but not currently assigned,
// and removes projects that are currently assigned but not in newProjectIDs.
// If newProjectIDs is empty, all projects are removed from the issue.
// When adding an issue to a project, it is placed in the project's default column.
func IssueAssignOrRemoveProject(ctx context.Context, issue *Issue, doer *user_model.User, newProjectIDs []int64) error {
	return db.WithTx(ctx, func(ctx context.Context) error {
		if err := issue.LoadRepo(ctx); err != nil {
			return err
		}

		oldProjectIDs, err := issue.projectIDs(ctx)
		if err != nil {
			return err
		}

		projectsToAdd, projectsToRemove := util.DiffSlice(oldProjectIDs, newProjectIDs)
		changedProjectIDs := append(append([]int64{}, projectsToAdd...), projectsToRemove...)
		if len(changedProjectIDs) > 0 {
			if err := project_model.StabilizePlanningStates(ctx, changedProjectIDs); err != nil {
				return err
			}
			projects, err := project_model.GetProjectsMapByIDs(ctx, changedProjectIDs)
			if err != nil {
				return err
			}
			for _, project := range projects {
				if project.PlanningState == project_model.PlanningStateActive {
					return ErrActivePlanMembership
				}
			}
		}
		issue.isProjectsLoaded = false
		issue.Projects = nil

		if len(projectsToRemove) > 0 {
			if _, err := db.GetEngine(ctx).Where("issue_id=?", issue.ID).In("project_id", projectsToRemove).Delete(&project_model.ProjectIssue{}); err != nil {
				return err
			}
			for _, projectID := range projectsToRemove {
				if _, err := CreateComment(ctx, &CreateCommentOptions{
					Type:         CommentTypeProject,
					Doer:         doer,
					Repo:         issue.Repo,
					Issue:        issue,
					OldProjectID: projectID,
					ProjectID:    0,
				}); err != nil {
					return err
				}
			}
		}

		if len(projectsToAdd) > 0 {
			projectMap, err := project_model.GetProjectsMapByIDs(ctx, projectsToAdd)
			if err != nil {
				return err
			}

			for _, projectID := range projectsToAdd {
				newProject, ok := projectMap[projectID]
				if !ok {
					return util.NewNotExistErrorf("project %d not found", projectID)
				}
				if !newProject.CanBeAccessedByOwnerRepo(issue.Repo.OwnerID, issue.Repo) {
					return util.NewPermissionDeniedErrorf("issue %d can't be accessed by project %d", issue.ID, newProject.ID)
				}

				defaultColumn, err := newProject.MustDefaultColumn(ctx)
				if err != nil {
					return err
				}

				newSorting, err := project_model.GetColumnIssueNextSorting(ctx, projectID, defaultColumn.ID)
				if err != nil {
					return err
				}

				err = db.Insert(ctx, &project_model.ProjectIssue{
					IssueID:         issue.ID,
					ProjectID:       projectID,
					ProjectColumnID: defaultColumn.ID,
					Sorting:         newSorting,
				})
				if err != nil {
					return err
				}

				if _, err := CreateComment(ctx, &CreateCommentOptions{
					Type:         CommentTypeProject,
					Doer:         doer,
					Repo:         issue.Repo,
					Issue:        issue,
					OldProjectID: 0,
					ProjectID:    projectID,
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// EnsureIssueProjectInWorkTx makes one Project membership present or absent
// without replacing unrelated memberships. The caller owns the Work
// transaction and the complete-plan validation.
func EnsureIssueProjectInWorkTx(ctx context.Context, issue *Issue, doer *user_model.User, project *project_model.Project, present bool) (bool, *Comment, error) {
	if issue == nil || project == nil {
		return false, nil, util.ErrInvalidArgument
	}
	if err := issue.LoadRepo(ctx); err != nil {
		return false, nil, err
	}
	if !project.CanBeAccessedByOwnerRepo(issue.Repo.OwnerID, issue.Repo) {
		return false, nil, util.NewPermissionDeniedErrorf("issue %d can't be accessed by project %d", issue.ID, project.ID)
	}

	relation := &project_model.ProjectIssue{ProjectID: project.ID, IssueID: issue.ID}
	exists, err := db.GetEngine(ctx).Where("project_id = ? AND issue_id = ?", project.ID, issue.ID).Exist(relation)
	if err != nil {
		return false, nil, err
	}
	if present == exists {
		return false, nil, nil
	}
	issue.isProjectsLoaded = false
	issue.Projects = nil
	if !present {
		if _, err := db.GetEngine(ctx).Where("project_id = ? AND issue_id = ?", project.ID, issue.ID).Delete(new(project_model.ProjectIssue)); err != nil {
			return false, nil, err
		}
		comment, err := CreateComment(ctx, &CreateCommentOptions{
			Type: CommentTypeProject, Doer: doer, Repo: issue.Repo, Issue: issue, OldProjectID: project.ID,
		})
		return err == nil, comment, err
	}

	defaultColumn, err := project.MustDefaultColumn(ctx)
	if err != nil {
		return false, nil, err
	}
	sorting, err := project_model.GetColumnIssueNextSorting(ctx, project.ID, defaultColumn.ID)
	if err != nil {
		return false, nil, err
	}
	if err := db.Insert(ctx, &project_model.ProjectIssue{
		IssueID: issue.ID, ProjectID: project.ID, ProjectColumnID: defaultColumn.ID, Sorting: sorting,
	}); err != nil {
		return false, nil, err
	}
	comment, err := CreateComment(ctx, &CreateCommentOptions{
		Type: CommentTypeProject, Doer: doer, Repo: issue.Repo, Issue: issue, ProjectID: project.ID,
	})
	return err == nil, comment, err
}
