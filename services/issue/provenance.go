// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"context"

	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	user_model "gitea.dev/models/user"
	mcpwork_service "gitea.dev/services/mcpwork"
)

// MCPWorkTimelinePresenter is the narrow receipt presentation dependency used
// by native Issue timeline composition.
type MCPWorkTimelinePresenter interface {
	PresentEvent(context.Context, mcpwork_service.EventReference, mcpwork_service.ArtifactReference, mcpwork_service.ReferencePermission) ([]*mcpwork_service.Presentation, error)
}

// PresentMCPWorkTimelineEvent returns permission-filtered MCP provenance for
// one native Issue comment without rendering or changing timeline state.
func PresentMCPWorkTimelineEvent(ctx context.Context, presenter MCPWorkTimelinePresenter, viewer *user_model.User, issue *issues_model.Issue, comment *issues_model.Comment) ([]*mcpwork_service.Presentation, error) {
	if presenter == nil || issue == nil || comment == nil || issue.ID <= 0 || issue.RepoID <= 0 || issue.Index <= 0 || comment.ID <= 0 || comment.IssueID != issue.ID {
		return nil, mcpwork_service.ErrInvalidRequest
	}
	owner := mcpwork_service.ArtifactReference{
		RepositoryID: issue.RepoID, Kind: mcpwork_model.ArtifactKindIssue,
		ArtifactID: issue.ID, ArtifactNumber: issue.Index,
	}
	event := mcpwork_service.EventReference{
		RepositoryID: issue.RepoID, Kind: mcpwork_model.EventKindIssueComment, EventID: comment.ID,
		ArtifactKind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID,
	}
	return presenter.PresentEvent(ctx, event, owner, mcpwork_service.CurrentReferencePermission(viewer))
}
