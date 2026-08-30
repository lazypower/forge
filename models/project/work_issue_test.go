// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package project_test

import (
	"testing"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	project_model "gitea.dev/models/project"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetWorkProjectIssuesOrdersByIssueNumber(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())
	issues := []*issues_model.Issue{
		{RepoID: 1, Index: 90_002, PosterID: 2, Title: "later", IsPull: true},
		{RepoID: 1, Index: 90_001, PosterID: 2, Title: "earlier"},
		{RepoID: 2, Index: 90_000, PosterID: 2, Title: "foreign"},
	}
	assert.NoError(t, db.Insert(t.Context(), issues))
	assert.NoError(t, db.Insert(t.Context(), &project_model.ProjectIssue{ProjectID: 1, IssueID: issues[0].ID, ProjectColumnID: 1}))
	assert.NoError(t, db.Insert(t.Context(), &project_model.ProjectIssue{ProjectID: 1, IssueID: issues[1].ID, ProjectColumnID: 1}))
	assert.NoError(t, db.Insert(t.Context(), &project_model.ProjectIssue{ProjectID: 1, IssueID: issues[2].ID, ProjectColumnID: 1}))

	entries, err := project_model.GetWorkProjectIssues(t.Context(), 1, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(entries), 2)
	assert.NotContains(t, entries, project_model.WorkProjectIssue{ProjectID: 1, IssueID: issues[2].ID, Index: issues[2].Index})
	assert.Equal(t, []int64{90_001, 90_002}, []int64{entries[len(entries)-2].Index, entries[len(entries)-1].Index})
	assert.False(t, entries[len(entries)-2].IsPull)
	assert.True(t, entries[len(entries)-1].IsPull)

	memberships, err := project_model.GetWorkIssueProjectIDs(t.Context(), 1, []int64{issues[0].ID, issues[1].ID})
	assert.NoError(t, err)
	assert.Equal(t, []int64{1}, memberships[issues[0].ID])
	assert.Equal(t, []int64{1}, memberships[issues[1].ID])
}
