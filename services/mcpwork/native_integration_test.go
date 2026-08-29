// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcpwork_test

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	issue_service "gitea.dev/services/issue"
	mcpwork_service "gitea.dev/services/mcpwork"
	project_service "gitea.dev/services/projects"
	repo_service "gitea.dev/services/repository"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	nativeReceiptSecret = "0123456789abcdef0123456789abcdef"
	nativeReceiptKey    = "native-receipt-key-000000000000001"
)

func TestCurrentReferencePermissionUsesNativeState(t *testing.T) {
	unittest.PrepareTestEnv(t)
	permitted := mcpwork_service.CurrentReferencePermission(nil)

	allowed, err := permitted(t.Context(), mcpwork_service.ArtifactReference{
		RepositoryID: 1, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: 1, ArtifactNumber: 1,
	})
	require.NoError(t, err)
	assert.True(t, allowed)
	allowed, err = permitted(t.Context(), mcpwork_service.ArtifactReference{
		RepositoryID: 1, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: 1,
	})
	require.NoError(t, err)
	assert.True(t, allowed)

	for _, reference := range []mcpwork_service.ArtifactReference{
		{RepositoryID: 1, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: 1, ArtifactNumber: 999},
		{RepositoryID: 4, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: 3},
		{RepositoryID: 1, Kind: mcpwork_model.ArtifactKind("unknown"), ArtifactID: 1},
		{RepositoryID: unittest.NonexistentID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: 1, ArtifactNumber: 1},
	} {
		allowed, err = permitted(t.Context(), reference)
		require.NoError(t, err)
		assert.False(t, allowed)
	}
}

func TestNativeIssueTimelineAndProjectPresentation(t *testing.T) {
	unittest.PrepareTestEnv(t)
	receipts := newNativeReceiptService(t)
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1})
	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 2, IssueID: issue.ID})
	project := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: 1, RepoID: issue.RepoID})

	request := nativeReceiptRequest(nativeReceiptKey, "visible")
	_, err := receipts.Execute(t.Context(), request, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		return mcpwork_service.Completion{
			Outcome: mcpwork_model.OutcomeApplied,
			Artifacts: []mcpwork_service.ArtifactReference{
				{RepositoryID: issue.RepoID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID, ArtifactNumber: issue.Index},
				{RepositoryID: project.RepoID, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: project.ID},
			},
			Events: []mcpwork_service.EventReference{{
				RepositoryID: issue.RepoID, Kind: mcpwork_model.EventKindIssueComment, EventID: comment.ID,
				ArtifactKind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID,
			}},
		}, nil
	})
	require.NoError(t, err)

	timeline, err := issue_service.PresentMCPWorkTimelineEvent(t.Context(), receipts, nil, issue, comment)
	require.NoError(t, err)
	require.Len(t, timeline, 1)
	assert.True(t, timeline[0].Available)
	assert.Len(t, timeline[0].Artifacts, 2)
	projects, err := project_service.PresentMCPWorkProject(t.Context(), receipts, nil, project)
	require.NoError(t, err)
	require.Len(t, projects, 1)
	assert.True(t, projects[0].Available)

	repository := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: issue.RepoID})
	repository.IsPrivate = true
	_, err = db.GetEngine(t.Context()).ID(repository.ID).Cols("is_private").Update(repository)
	require.NoError(t, err)
	timeline, err = issue_service.PresentMCPWorkTimelineEvent(t.Context(), receipts, nil, issue, comment)
	require.NoError(t, err)
	assert.Empty(t, timeline)
	projects, err = project_service.PresentMCPWorkProject(t.Context(), receipts, nil, project)
	require.NoError(t, err)
	assert.Empty(t, projects)

	wrongOwner := *comment
	wrongOwner.IssueID++
	_, err = issue_service.PresentMCPWorkTimelineEvent(t.Context(), receipts, nil, issue, &wrongOwner)
	require.ErrorIs(t, err, mcpwork_service.ErrInvalidRequest)
	ownerProject := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: 4, RepoID: 0})
	_, err = project_service.PresentMCPWorkProject(t.Context(), receipts, nil, ownerProject)
	require.ErrorIs(t, err, mcpwork_service.ErrInvalidRequest)
}

func TestNativeIssueAndProjectDeletionRetiresLastReference(t *testing.T) {
	unittest.PrepareTestEnv(t)
	receipts := newNativeReceiptService(t)
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1, RepoID: 1})
	comment := unittest.AssertExistsAndLoadBean(t, &issues_model.Comment{ID: 2, IssueID: issue.ID})
	project := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: 1, RepoID: issue.RepoID})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	request := nativeReceiptRequest(nativeReceiptKey, "delete-last-reference")

	result, err := receipts.Execute(t.Context(), request, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		return mcpwork_service.Completion{
			Outcome: mcpwork_model.OutcomeApplied,
			Artifacts: []mcpwork_service.ArtifactReference{
				{RepositoryID: issue.RepoID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID, ArtifactNumber: issue.Index},
				{RepositoryID: project.RepoID, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: project.ID},
			},
			Events: []mcpwork_service.EventReference{{
				RepositoryID: issue.RepoID, Kind: mcpwork_model.EventKindIssueComment, EventID: comment.ID,
				ArtifactKind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID,
			}},
		}, nil
	})
	require.NoError(t, err)

	require.NoError(t, issue_service.DeleteIssue(t.Context(), doer, issue))
	stored, links, _, err := mcpwork_model.GetReceiptByUUID(t.Context(), result.OperationUUID)
	require.NoError(t, err)
	assert.Zero(t, stored.TombstonedUnix)
	require.Len(t, links, 1)
	assert.Equal(t, mcpwork_model.ArtifactKindProject, links[0].Kind)
	assert.Zero(t, countNativeModel(t, new(mcpwork_model.EventLink)))

	require.NoError(t, project_model.DeleteProjectByID(t.Context(), project.ID))
	tombstone := loadNativeTombstone(t, request.Authority.PrincipalID)
	assertNativeTombstone(t, tombstone)
	assert.Zero(t, countNativeModel(t, new(mcpwork_model.ArtifactLink)))
	assert.Zero(t, countNativeModel(t, new(mcpwork_model.EventLink)))

	restarted := newNativeReceiptService(t)
	var callbacks atomic.Int64
	_, err = restarted.Execute(t.Context(), request, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		callbacks.Add(1)
		return mcpwork_service.Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
	})
	require.ErrorIs(t, err, mcpwork_service.ErrReceiptTombstoned)
	changed := nativeReceiptRequest(nativeReceiptKey, "changed-target")
	_, err = restarted.Execute(t.Context(), changed, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		callbacks.Add(1)
		return mcpwork_service.Completion{Outcome: mcpwork_model.OutcomeApplied}, nil
	})
	require.ErrorIs(t, err, mcpwork_service.ErrIdempotencyConflict)
	assert.Zero(t, callbacks.Load())
	assert.NotContains(t, err.Error(), issue.Title)
	assert.NotContains(t, err.Error(), project.Title)
}

func TestRetirementRetainsDetailWhileNativeArtifactExists(t *testing.T) {
	unittest.PrepareTestEnv(t)
	receipts := newNativeReceiptService(t)
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 1, RepoID: 1})
	request := nativeReceiptRequest(nativeReceiptKey, "retain-live-artifact")

	result, err := receipts.Execute(t.Context(), request, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		return mcpwork_service.Completion{
			Outcome: mcpwork_model.OutcomeApplied,
			Artifacts: []mcpwork_service.ArtifactReference{{
				RepositoryID: issue.RepoID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID, ArtifactNumber: issue.Index,
			}},
		}, nil
	})
	require.NoError(t, err)

	require.NoError(t, mcpwork_model.RetireIssue(t.Context(), issue.RepoID, issue.ID))
	stored, links, _, err := mcpwork_model.GetReceiptByUUID(t.Context(), result.OperationUUID)
	require.NoError(t, err)
	assert.Zero(t, stored.TombstonedUnix)
	assert.NotEmpty(t, stored.Tool)
	assert.Len(t, links, 1)
}

func TestNativeBulkProjectDeletionRetiresProjectReceipts(t *testing.T) {
	unittest.PrepareTestEnv(t)
	receipts := newNativeReceiptService(t)
	project := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: 1, RepoID: 1})
	request := nativeReceiptRequest(nativeReceiptKey, "delete-repository-projects")

	_, err := receipts.Execute(t.Context(), request, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		return mcpwork_service.Completion{
			Outcome: mcpwork_model.OutcomeApplied,
			Artifacts: []mcpwork_service.ArtifactReference{{
				RepositoryID: project.RepoID, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: project.ID,
			}},
		}, nil
	})
	require.NoError(t, err)

	require.NoError(t, project_model.DeleteProjectByRepoID(t.Context(), project.RepoID))
	assertNativeTombstone(t, loadNativeTombstone(t, request.Authority.PrincipalID))
	assert.Zero(t, countNativeModel(t, new(mcpwork_model.ArtifactLink)))
}

func TestNativeRepositoryDeletionRetiresReceipts(t *testing.T) {
	unittest.PrepareTestEnv(t)
	receipts := newNativeReceiptService(t)
	issue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: 5, RepoID: 1})
	project := unittest.AssertExistsAndLoadBean(t, &project_model.Project{ID: 1, RepoID: issue.RepoID})
	request := nativeReceiptRequest(nativeReceiptKey, "delete-repository")
	_, err := receipts.Execute(t.Context(), request, func(context.Context, mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		return mcpwork_service.Completion{
			Outcome: mcpwork_model.OutcomeApplied,
			Artifacts: []mcpwork_service.ArtifactReference{
				{RepositoryID: issue.RepoID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID, ArtifactNumber: issue.Index},
				{RepositoryID: project.RepoID, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: project.ID},
			},
		}, nil
	})
	require.NoError(t, err)

	require.NoError(t, repo_service.DeleteRepositoryDirectly(t.Context(), issue.RepoID))
	assertNativeTombstone(t, loadNativeTombstone(t, request.Authority.PrincipalID))
	assert.Zero(t, countNativeModel(t, new(mcpwork_model.ArtifactLink)))
}

func newNativeReceiptService(t *testing.T) *mcpwork_service.Service {
	t.Helper()
	receipts, err := mcpwork_service.NewService([]byte(nativeReceiptSecret))
	require.NoError(t, err)
	return receipts
}

func nativeReceiptRequest(key, marker string) mcpwork_service.Request {
	return mcpwork_service.Request{
		Tool: "work_plan.begin", SchemaVersion: "1", IdempotencyKey: key,
		ExpandedInput:     fmt.Appendf(nil, `{"idempotencyKey":%q,"marker":%q}`, key, marker),
		ClientAttribution: mcpwork_service.ClientAttribution{Harness: "Example Harness", HarnessVersion: "1.0", Model: "Example Model", Source: "client-reported"},
		Authority: mcpwork_service.Authority{
			Profile: "work-planning", RegisteredClientLabel: "Example Client", RegisteredInstallationLabel: "Example Installation",
			PrincipalID: 901, OAuthApplicationID: 902, OAuthGrantID: 903,
			CredentialJTI: "99999999-9999-4999-8999-999999999999",
			Audience:      "https://forge.example/mcp",
			Scope:         "read:repository write:issue write:repository",
		},
	}
}

func loadNativeTombstone(t *testing.T, principalID int64) *mcpwork_model.Receipt {
	t.Helper()
	receipt := new(mcpwork_model.Receipt)
	has, err := db.GetEngine(t.Context()).Where("principal_id = ?", principalID).Get(receipt)
	require.NoError(t, err)
	require.True(t, has)
	return receipt
}

func assertNativeTombstone(t *testing.T, receipt *mcpwork_model.Receipt) {
	t.Helper()
	assert.Positive(t, receipt.TombstonedUnix)
	assert.Positive(t, receipt.PrincipalID)
	assert.Len(t, receipt.AudienceDigest, 64)
	assert.Len(t, receipt.KeyDigest, 64)
	assert.Len(t, receipt.RequestDigest, 64)
	assert.Empty(t, receipt.OperationUUID)
	assert.Empty(t, receipt.Tool)
	assert.Empty(t, receipt.SchemaVersion)
	assert.Zero(t, receipt.ApplicationID)
	assert.Zero(t, receipt.GrantID)
	assert.Empty(t, receipt.CredentialID)
	assert.Empty(t, receipt.Scope)
	assert.Empty(t, receipt.AttributionSource)
	assert.Empty(t, receipt.Profile)
	assert.Empty(t, receipt.RegisteredClientLabel)
	assert.Empty(t, receipt.RegisteredInstallationLabel)
	assert.Empty(t, receipt.Harness)
	assert.Empty(t, receipt.HarnessVersion)
	assert.Empty(t, receipt.Model)
	assert.Empty(t, receipt.Origin)
	assert.Empty(t, receipt.Outcome)
	assert.Empty(t, receipt.ProblemCode)
	assert.Zero(t, receipt.CreatedUnix)
	assert.Zero(t, receipt.CommittedUnix)
}

func countNativeModel(t *testing.T, bean any) int64 {
	t.Helper()
	count, err := db.GetEngine(t.Context()).Count(bean)
	require.NoError(t, err)
	return count
}
