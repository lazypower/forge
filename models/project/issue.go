// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package project

import (
	"context"
	"errors"

	"gitea.dev/models/db"
	"gitea.dev/modules/util"
)

// ProjectIssue saves relation from issue to a project
type ProjectIssue struct { //revive:disable-line:exported
	ID        int64 `xorm:"pk autoincr"`
	IssueID   int64 `xorm:"INDEX"`
	ProjectID int64 `xorm:"INDEX"`

	// ProjectColumnID should not be zero since 1.22. If it's zero, the issue will not be displayed on UI and it might result in errors.
	ProjectColumnID int64 `xorm:"'project_board_id' INDEX"`

	// the sorting order on the column
	Sorting int64 `xorm:"NOT NULL DEFAULT 0"`
}

// WorkProjectIssue is the Issue identity needed to compose one Project page.
type WorkProjectIssue struct {
	ProjectID int64
	IssueID   int64
	Index     int64
	IsPull    bool
}

func init() {
	db.RegisterModel(new(ProjectIssue))
}

func deleteProjectIssuesByProjectID(ctx context.Context, projectID int64) error {
	_, err := db.GetEngine(ctx).Where("project_id=?", projectID).Delete(&ProjectIssue{})
	return err
}

// GetColumnIssueNextSorting returns the sorting value to append an issue at the end of the column.
func GetColumnIssueNextSorting(ctx context.Context, projectID, columnID int64) (int64, error) {
	res := struct {
		MaxSorting int64
		IssueCount int64
	}{}
	if _, err := db.GetEngine(ctx).Select("max(sorting) AS max_sorting, count(*) AS issue_count").
		Table("project_issue").
		Where("project_id=?", projectID).
		And("project_board_id=?", columnID).
		Get(&res); err != nil {
		return 0, err
	}
	return util.Iif(res.IssueCount > 0, res.MaxSorting+1, 0), nil
}

func moveIssuesToAnotherColumn(ctx context.Context, oldColumn, newColumn *Column) error {
	if oldColumn.ProjectID != newColumn.ProjectID {
		return errors.New("columns have to be in the same project")
	}

	if oldColumn.ID == newColumn.ID {
		return nil
	}

	movedIssues, err := oldColumn.GetIssues(ctx)
	if err != nil {
		return err
	}
	if len(movedIssues) == 0 {
		return nil
	}

	nextSorting, err := GetColumnIssueNextSorting(ctx, newColumn.ProjectID, newColumn.ID)
	if err != nil {
		return err
	}
	return db.WithTx(ctx, func(ctx context.Context) error {
		for i, issue := range movedIssues {
			issue.ProjectColumnID = newColumn.ID
			issue.Sorting = nextSorting + int64(i)
			if _, err := db.GetEngine(ctx).ID(issue.ID).Cols("project_board_id", "sorting").Update(issue); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteAllProjectIssueByIssueIDsAndProjectIDs delete all project's issues by issue's and project's ids
func DeleteAllProjectIssueByIssueIDsAndProjectIDs(ctx context.Context, issueIDs, projectIDs []int64) error {
	_, err := db.GetEngine(ctx).In("project_id", projectIDs).In("issue_id", issueIDs).Delete(&ProjectIssue{})
	return err
}

// GetWorkProjectIssues returns Project members in deterministic Issue-number order.
// The caller supplies one extra result when it needs to determine whether a page continues.
func GetWorkProjectIssues(ctx context.Context, projectID int64, limit int) ([]WorkProjectIssue, error) {
	if projectID <= 0 || limit <= 0 {
		return nil, errors.New("invalid Work Project membership query")
	}
	entries := make([]WorkProjectIssue, 0, limit)
	err := db.GetEngine(ctx).
		Table("project_issue").
		Select("project_issue.project_id, issue.id AS issue_id, issue.`index`, issue.is_pull").
		Join("INNER", "issue", "issue.id = project_issue.issue_id").
		Join("INNER", "project", "project.id = project_issue.project_id").
		Where("project_issue.project_id = ?", projectID).
		And("issue.repo_id = project.repo_id").
		OrderBy("issue.`index` ASC").
		Limit(limit).
		Find(&entries)
	return entries, err
}

// GetWorkIssueProjectIDs returns repository Project memberships for a batch of Issues.
func GetWorkIssueProjectIDs(ctx context.Context, repoID int64, issueIDs []int64) (map[int64][]int64, error) {
	projectIDs := make(map[int64][]int64, len(issueIDs))
	if len(issueIDs) == 0 {
		return projectIDs, nil
	}
	type membership struct {
		IssueID   int64
		ProjectID int64
	}
	for len(issueIDs) > 0 {
		batchSize := min(len(issueIDs), db.DefaultMaxInSize)
		memberships := make([]membership, 0, batchSize)
		err := db.GetEngine(ctx).
			Table("project_issue").
			Select("project_issue.issue_id, project_issue.project_id").
			Join("INNER", "project", "project.id = project_issue.project_id").
			Where("project.repo_id = ?", repoID).
			In("project_issue.issue_id", issueIDs[:batchSize]).
			OrderBy("project_issue.issue_id ASC, project_issue.project_id ASC").
			Find(&memberships)
		if err != nil {
			return nil, err
		}
		for _, membership := range memberships {
			ids := projectIDs[membership.IssueID]
			if len(ids) == 0 || ids[len(ids)-1] != membership.ProjectID {
				projectIDs[membership.IssueID] = append(ids, membership.ProjectID)
			}
		}
		issueIDs = issueIDs[batchSize:]
	}
	return projectIDs, nil
}
