// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package project

import (
	"context"

	mcpwork_model "gitea.dev/models/mcpwork"
	project_model "gitea.dev/models/project"
	user_model "gitea.dev/models/user"
	mcpwork_service "gitea.dev/services/mcpwork"
)

// MCPWorkProjectPresenter is the narrow receipt presentation dependency used
// by native repository Project composition.
type MCPWorkProjectPresenter interface {
	PresentArtifact(context.Context, mcpwork_service.ArtifactReference, mcpwork_service.ReferencePermission) ([]*mcpwork_service.Presentation, error)
}

// PresentMCPWorkProject returns permission-filtered MCP provenance for one
// native repository Project without loading cards or rendering a view.
func PresentMCPWorkProject(ctx context.Context, presenter MCPWorkProjectPresenter, viewer *user_model.User, project *project_model.Project) ([]*mcpwork_service.Presentation, error) {
	if presenter == nil || project == nil || project.ID <= 0 || project.RepoID <= 0 || !project.IsRepositoryProject() {
		return nil, mcpwork_service.ErrInvalidRequest
	}
	return presenter.PresentArtifact(ctx, mcpwork_service.ArtifactReference{
		RepositoryID: project.RepoID, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: project.ID,
	}, mcpwork_service.CurrentReferencePermission(viewer))
}
