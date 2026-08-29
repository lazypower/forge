// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"context"

	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	access_model "gitea.dev/models/perm/access"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	pull_service "gitea.dev/services/pull"
)

type nativeSource interface {
	repository(context.Context, string, string) (*repo_model.Repository, error)
	permission(context.Context, *repo_model.Repository, *user_model.User) (access_model.Permission, error)
	issue(context.Context, int64, int64) (*issues_model.Issue, error)
	issues(context.Context, []int64) (issues_model.IssueList, error)
	project(context.Context, int64, int64) (*project_model.Project, error)
	projects(context.Context, []int64) (map[int64]*project_model.Project, error)
	projectIssues(context.Context, int64, int) ([]project_model.WorkProjectIssue, error)
	issueProjectIDs(context.Context, int64, []int64) (map[int64][]int64, error)
	dependencyIDs(context.Context, []int64) (map[int64][]int64, error)
	dependentIDs(context.Context, []int64) (map[int64][]int64, error)
	closingPulls(context.Context, []int64) (map[int64][]issues_model.WorkClosingPullReference, error)
	pulls(context.Context, []int64) (issues_model.PullRequestList, error)
	repositories(context.Context, []int64) (map[int64]*repo_model.Repository, error)
	statuses(context.Context, []git_model.RepoSHA) (map[git_model.RepoSHA][]*git_model.CommitStatus, error)
	revisions(context.Context, issues_model.PullRequestList) (map[int64]pull_service.WorkRevision, error)
}

type forgeSource struct{}

func (forgeSource) repository(ctx context.Context, owner, name string) (*repo_model.Repository, error) {
	return repo_model.GetRepositoryByOwnerAndName(ctx, owner, name)
}

func (forgeSource) permission(ctx context.Context, repo *repo_model.Repository, doer *user_model.User) (access_model.Permission, error) {
	return access_model.GetDoerRepoPermission(ctx, repo, doer)
}

func (forgeSource) issue(ctx context.Context, repoID, index int64) (*issues_model.Issue, error) {
	return issues_model.GetIssueByIndex(ctx, repoID, index)
}

func (forgeSource) issues(ctx context.Context, ids []int64) (issues_model.IssueList, error) {
	return issues_model.GetIssuesByIDs(ctx, ids)
}

func (forgeSource) project(ctx context.Context, repoID, projectID int64) (*project_model.Project, error) {
	return project_model.GetProjectForRepoByID(ctx, repoID, projectID)
}

func (forgeSource) projects(ctx context.Context, ids []int64) (map[int64]*project_model.Project, error) {
	return project_model.GetProjectsMapByIDs(ctx, ids)
}

func (forgeSource) projectIssues(ctx context.Context, projectID int64, limit int) ([]project_model.WorkProjectIssue, error) {
	return project_model.GetWorkProjectIssues(ctx, projectID, limit)
}

func (forgeSource) issueProjectIDs(ctx context.Context, repoID int64, ids []int64) (map[int64][]int64, error) {
	return project_model.GetWorkIssueProjectIDs(ctx, repoID, ids)
}

func (forgeSource) dependencyIDs(ctx context.Context, ids []int64) (map[int64][]int64, error) {
	return issues_model.GetIssueDependencyIDs(ctx, ids)
}

func (forgeSource) dependentIDs(ctx context.Context, ids []int64) (map[int64][]int64, error) {
	return issues_model.GetIssueDependentIDs(ctx, ids)
}

func (forgeSource) closingPulls(ctx context.Context, ids []int64) (map[int64][]issues_model.WorkClosingPullReference, error) {
	return issues_model.GetWorkClosingPullReferences(ctx, ids)
}

func (forgeSource) pulls(ctx context.Context, ids []int64) (issues_model.PullRequestList, error) {
	return issues_model.GetPullRequestByIssueIDs(ctx, ids)
}

func (forgeSource) repositories(ctx context.Context, ids []int64) (map[int64]*repo_model.Repository, error) {
	return repo_model.GetRepositoriesMapByIDs(ctx, ids)
}

func (forgeSource) statuses(ctx context.Context, pairs []git_model.RepoSHA) (map[git_model.RepoSHA][]*git_model.CommitStatus, error) {
	return git_model.GetLatestCommitStatusesByRepoAndSHA(ctx, pairs)
}

func (forgeSource) revisions(ctx context.Context, pulls issues_model.PullRequestList) (map[int64]pull_service.WorkRevision, error) {
	return pull_service.ResolveWorkRevisions(ctx, pulls)
}
