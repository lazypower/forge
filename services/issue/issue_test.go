// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"context"
	"errors"
	"testing"

	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/test"
	mcpwork_service "gitea.dev/services/mcpwork"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRefEndNamesAndURLs(t *testing.T) {
	issues := []*issues_model.Issue{
		{ID: 1, Ref: "refs/heads/branch1"},
		{ID: 2, Ref: "refs/tags/tag1"},
		{ID: 3, Ref: "c0ffee"},
	}
	repoLink := "/foo/bar"

	endNames, urls := GetRefEndNamesAndURLs(issues, repoLink)
	assert.Equal(t, map[int64]string{1: "branch1", 2: "tag1", 3: "c0ffee"}, endNames)
	assert.Equal(t, map[int64]string{
		1: repoLink + "/src/branch/branch1",
		2: repoLink + "/src/tag/tag1",
		3: repoLink + "/src/commit/c0ffee",
	}, urls)
}

func TestIssue_DeleteIssue(t *testing.T) {
	assert.NoError(t, unittest.PrepareTestDatabase())

	issueIDs, err := issues_model.GetIssueIDsByRepoID(t.Context(), 1)
	assert.NoError(t, err)
	assert.Len(t, issueIDs, 5)

	issue := &issues_model.Issue{
		RepoID: 1,
		ID:     issueIDs[2],
	}

	_, err = deleteIssue(t.Context(), issue)
	assert.NoError(t, err)
	issueIDs, err = issues_model.GetIssueIDsByRepoID(t.Context(), 1)
	assert.NoError(t, err)
	assert.Len(t, issueIDs, 4)

	// check attachment removal
	attachments, err := repo_model.GetAttachmentsByIssueID(t.Context(), 4)
	assert.NoError(t, err)
	issue, err = issues_model.GetIssueByID(t.Context(), 4)
	assert.NoError(t, err)
	_, err = deleteIssue(t.Context(), issue)
	assert.NoError(t, err)
	assert.Len(t, attachments, 2)
	for i := range attachments {
		attachment, err := repo_model.GetAttachmentByUUID(t.Context(), attachments[i].UUID)
		assert.Error(t, err)
		assert.True(t, repo_model.IsErrAttachmentNotExist(err))
		assert.Nil(t, attachment)
	}

	// check issue dependencies
	user, err := user_model.GetUserByID(t.Context(), 1)
	assert.NoError(t, err)
	issue1, err := issues_model.GetIssueByID(t.Context(), 1)
	assert.NoError(t, err)
	issue2, err := issues_model.GetIssueByID(t.Context(), 2)
	assert.NoError(t, err)
	err = issues_model.CreateIssueDependency(t.Context(), user, issue1, issue2)
	assert.NoError(t, err)
	left, err := issues_model.IssueNoDependenciesLeft(t.Context(), issue1)
	assert.NoError(t, err)
	assert.False(t, left)

	_, err = deleteIssue(t.Context(), issue2)
	assert.NoError(t, err)
	left, err = issues_model.IssueNoDependenciesLeft(t.Context(), issue1)
	assert.NoError(t, err)
	assert.True(t, left)
}

func TestDeleteIssueMCPReceiptRetirementFailureRollsBack(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 5})
	receipts, err := mcpwork_service.NewService([]byte("0123456789abcdef0123456789abcdef"))
	require.NoError(t, err)
	requestKey := "rollback-receipt-key-000000000001"
	result, err := receipts.Execute(t.Context(), mcpwork_service.Request{
		Tool: "work_plan.begin", SchemaVersion: "1", IdempotencyKey: requestKey,
		ExpandedInput: []byte(`{"idempotencyKey":"rollback-receipt-key-000000000001"}`),
		Authority: mcpwork_service.Authority{
			PrincipalID: 801, OAuthApplicationID: 802, OAuthGrantID: 803,
			CredentialJTI: "88888888-8888-4888-8888-888888888888", Audience: "https://forge.example/mcp",
			Scope: "read:repository write:issue write:repository",
		},
	}, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		return mcpwork_service.Completion{
			Outcome: mcpwork_model.OutcomeApplied,
			Artifacts: []mcpwork_service.ArtifactReference{{
				RepositoryID: issue.RepoID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID, ArtifactNumber: issue.Index,
			}},
		}, nil
	})
	require.NoError(t, err)
	injected := errors.New("receipt retirement unavailable")
	defer test.MockVariableValue(&retireIssueMCPWorkReceipts, func(context.Context, int64, int64) error {
		return injected
	})()

	_, err = deleteIssue(t.Context(), issue)
	require.ErrorIs(t, err, injected)
	unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: issue.ID, RepoID: issue.RepoID})
	receipt, links, _, err := mcpwork_model.GetReceiptByUUID(t.Context(), result.OperationUUID)
	require.NoError(t, err)
	assert.Zero(t, receipt.TombstonedUnix)
	assert.NotEmpty(t, receipt.Tool)
	assert.Len(t, links, 1)
}
