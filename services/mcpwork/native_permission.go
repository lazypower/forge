// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcpwork

import (
	"context"
	"errors"

	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	access_model "gitea.dev/models/perm/access"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/util"
)

// CurrentReferencePermission resolves each stable reference against native
// state and the viewer's current repository permissions. Missing or mismatched
// references are denied without disclosing which native check failed.
func CurrentReferencePermission(viewer *user_model.User) ReferencePermission {
	return func(ctx context.Context, reference ArtifactReference) (bool, error) {
		repository, err := repo_model.GetRepositoryByID(ctx, reference.RepositoryID)
		if errors.Is(err, util.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		permission, err := access_model.GetDoerRepoPermission(ctx, repository, viewer)
		if err != nil {
			return false, err
		}

		switch reference.Kind {
		case mcpwork_model.ArtifactKindProject:
			return canReadProjectReference(ctx, repository, permission, reference)
		case mcpwork_model.ArtifactKindIssue:
			return canReadIssueReference(ctx, permission, reference)
		default:
			return false, nil
		}
	}
}

func canReadProjectReference(ctx context.Context, repository *repo_model.Repository, permission access_model.Permission, reference ArtifactReference) (bool, error) {
	if unit.TypeProjects.UnitGlobalDisabled() {
		return false, nil
	}
	project, err := project_model.GetProjectForRepoByID(ctx, repository.ID, reference.ArtifactID)
	if errors.Is(err, util.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !project.IsRepositoryProject() {
		return false, nil
	}
	projectsUnit, err := repository.GetUnit(ctx, unit.TypeProjects)
	if errors.Is(err, util.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return projectsUnit.ProjectsConfig().IsProjectsAllowed(repo_model.ProjectsModeRepo) && permission.CanRead(unit.TypeProjects), nil
}

func canReadIssueReference(ctx context.Context, permission access_model.Permission, reference ArtifactReference) (bool, error) {
	issue, err := issues_model.GetIssueByRepoID(ctx, reference.RepositoryID, reference.ArtifactID)
	if errors.Is(err, util.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if issue.Index != reference.ArtifactNumber {
		return false, nil
	}
	return permission.CanReadIssuesOrPulls(issue.IsPull), nil
}
