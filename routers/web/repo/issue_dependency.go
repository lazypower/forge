// Copyright 2018 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"errors"
	"net/http"

	issues_model "gitea.dev/models/issues"
	"gitea.dev/services/context"
	issue_service "gitea.dev/services/issue"
)

// AddDependency adds new dependencies
func AddDependency(ctx *context.Context) {
	issueIndex := ctx.PathParamInt64("index")
	issue, err := issues_model.GetIssueByIndex(ctx, ctx.Repo.Repository.ID, issueIndex)
	if err != nil {
		ctx.ServerError("GetIssueByIndex", err)
		return
	}

	// Check if the Repo is allowed to have dependencies
	if !ctx.Repo.CanCreateIssueDependencies(ctx, ctx.Doer, issue.IsPull) {
		ctx.HTTPError(http.StatusForbidden, "CanCreateIssueDependencies")
		return
	}

	depID := ctx.FormInt64("newDependency")

	if err = issue.LoadRepo(ctx); err != nil {
		ctx.ServerError("LoadRepo", err)
		return
	}

	// Redirect
	defer func() {
		if !ctx.Written() {
			ctx.Redirect(issue.Link())
		}
	}()

	// Dependency
	dep, err := issues_model.GetIssueByID(ctx, depID)
	if err != nil {
		ctx.Flash.Error(ctx.Tr("repo.issues.dependency.add_error_dep_issue_not_exist"))
		return
	}

	_, err = issue_service.EnsureDependency(ctx, ctx.Doer, issue, dep, issue_service.DependencyPresent, issue_service.IssueDependencyScope)
	if err != nil {
		switch {
		case errors.Is(err, issue_service.ErrSelfDependency):
			ctx.Flash.Error(ctx.Tr("repo.issues.dependency.add_error_same_issue"))
		case errors.Is(err, issue_service.ErrCrossRepositoryDependency):
			ctx.Flash.Error(ctx.Tr("repo.issues.dependency.add_error_dep_not_same_repo"))
		case errors.Is(err, issue_service.ErrInvalidDependency):
			ctx.Flash.Error(ctx.Tr("repo.issues.dependency.add_error_cannot_create_circular"))
		case errors.Is(err, issue_service.ErrDependencyNotPermitted):
			ctx.HTTPError(http.StatusForbidden, "EnsureDependency")
		case errors.Is(err, issue_service.ErrDependencyUnavailable):
			return
		default:
			ctx.ServerError("EnsureDependency", err)
		}
		return
	}
}

// RemoveDependency removes the dependency
func RemoveDependency(ctx *context.Context) {
	issueIndex := ctx.PathParamInt64("index")
	issue, err := issues_model.GetIssueByIndex(ctx, ctx.Repo.Repository.ID, issueIndex)
	if err != nil {
		ctx.ServerError("GetIssueByIndex", err)
		return
	}

	depID := ctx.FormInt64("removeDependencyID")

	if err = issue.LoadRepo(ctx); err != nil {
		ctx.ServerError("LoadRepo", err)
		return
	}

	// Dependency Type
	depTypeStr := ctx.Req.PostFormValue("dependencyType")

	var depType issues_model.DependencyType

	switch depTypeStr {
	case "blockedBy":
		depType = issues_model.DependencyTypeBlockedBy
	case "blocking":
		depType = issues_model.DependencyTypeBlocking
	default:
		ctx.HTTPError(http.StatusBadRequest, "GetDependencyType")
		return
	}

	// Dependency
	dep, err := issues_model.GetIssueByID(ctx, depID)
	if err != nil {
		ctx.ServerError("GetIssueByID", err)
		return
	}

	blocked, prerequisite := issue, dep
	if depType == issues_model.DependencyTypeBlocking {
		blocked, prerequisite = dep, issue
	}
	if _, err = issue_service.EnsureDependency(ctx, ctx.Doer, blocked, prerequisite, issue_service.DependencyAbsent, issue_service.IssueDependencyScope); err != nil {
		if errors.Is(err, issue_service.ErrInvalidDependency) || errors.Is(err, issue_service.ErrDependencyNotPermitted) || errors.Is(err, issue_service.ErrDependencyUnavailable) {
			ctx.Redirect(issue.Link())
			return
		}
		ctx.ServerError("EnsureDependency", err)
		return
	}

	// Redirect
	ctx.Redirect(issue.Link())
}
