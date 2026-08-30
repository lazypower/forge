// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"fmt"
	"strings"

	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/container"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/util"
)

// WorkRevision is the immutable revision used by a Work delivery summary.
type WorkRevision struct {
	Revision string
}

// ResolveWorkRevisions freezes delivery revisions while opening each base
// repository only once. Merged pulls use their recorded merged revision;
// other pulls use the internal pull head maintained by the base repository.
func ResolveWorkRevisions(ctx context.Context, pulls issues_model.PullRequestList) (map[int64]WorkRevision, error) {
	revisions := make(map[int64]WorkRevision, len(pulls))
	repoIDs := container.Set[int64]{}
	for _, pr := range pulls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if pr.HasMerged {
			if pr.MergedCommitID == "" {
				return nil, fmt.Errorf("merged pull %d has no revision", pr.ID)
			}
			revisions[pr.ID] = WorkRevision{Revision: pr.MergedCommitID}
			continue
		}
		repoIDs.Add(pr.BaseRepoID)
	}
	repositories, err := repo_model.GetRepositoriesMapByIDs(ctx, repoIDs.Values())
	if err != nil {
		return nil, fmt.Errorf("load pull base repositories: %w", err)
	}
	refsByRepo := make(map[int64]map[string]string, len(repositories))
	for repoID, repo := range repositories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		gitRepo, err := gitrepo.OpenRepository(ctx, repo)
		if err != nil {
			return nil, fmt.Errorf("open pull base repository: %w", err)
		}
		refs, refsErr := gitRepo.GetRefsFiltered(git.PullPrefix)
		gitRepo.Close()
		if refsErr != nil {
			return nil, fmt.Errorf("list internal pull revisions: %w", refsErr)
		}
		byName := make(map[string]string, len(refs))
		for _, ref := range refs {
			if strings.HasSuffix(ref.Name, "/head") {
				byName[ref.Name] = ref.Object.String()
			}
		}
		refsByRepo[repoID] = byName
	}
	for _, pr := range pulls {
		if pr.HasMerged {
			continue
		}
		revision := refsByRepo[pr.BaseRepoID][pr.GetGitHeadRefName()]
		if revision == "" {
			return nil, fmt.Errorf("resolve internal pull revision: %w", util.ErrNotExist)
		}
		revisions[pr.ID] = WorkRevision{Revision: revision}
	}
	return revisions, nil
}
