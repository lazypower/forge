// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	access_model "gitea.dev/models/perm/access"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
)

// DependencyPresence is the desired state of one dependency edge.
type DependencyPresence string

const (
	// DependencyPresent ensures that the blocked Issue depends on the prerequisite.
	DependencyPresent DependencyPresence = "present"
	// DependencyAbsent ensures that the blocked Issue does not depend on the prerequisite.
	DependencyAbsent DependencyPresence = "absent"
)

// DependencyScope selects the existing Issue or stricter Work endpoint rules.
type DependencyScope string

const (
	// IssueDependencyScope preserves configured ordinary Issue dependency behavior.
	IssueDependencyScope DependencyScope = "issue"
	// WorkDependencyScope requires non-pull endpoints in one repository.
	WorkDependencyScope DependencyScope = "work"
)

var (
	// ErrInvalidDependency is deliberately shared by cyclic, hidden-path, and
	// graph-bound failures so callers cannot use mutation as an existence oracle.
	ErrInvalidDependency = errors.New("invalid dependency")
	// ErrSelfDependency identifies a disclosed self-edge without weakening the
	// non-disclosing graph failure above.
	ErrSelfDependency = fmt.Errorf("%w: issue cannot depend on itself", ErrInvalidDependency)
	// ErrDependencyEndpoint identifies a disclosed pull request endpoint.
	ErrDependencyEndpoint = fmt.Errorf("%w: dependency endpoints must be issues", ErrInvalidDependency)
	// ErrCrossRepositoryDependency identifies a new cross-repository edge.
	ErrCrossRepositoryDependency = fmt.Errorf("%w: dependency endpoints must be in the same repository", ErrInvalidDependency)
	// ErrDependencyNotPermitted reports that the blocked Issue cannot currently be changed.
	ErrDependencyNotPermitted = errors.New("dependency change not permitted")
	// ErrDependencyUnavailable reports a missing or unreadable named prerequisite.
	ErrDependencyUnavailable = errors.New("dependency unavailable")
	// ErrDependencyPresence reports an unsupported desired state.
	ErrDependencyPresence = errors.New("invalid dependency presence")
	// ErrDependencyScope reports an unsupported dependency domain.
	ErrDependencyScope = errors.New("invalid dependency scope")
)

// EnsureDependency makes one dependency edge present or absent. It is the
// permission, graph-integrity, and transaction authority for every interface.
func EnsureDependency(ctx context.Context, doer *user_model.User, blocked, prerequisite *issues_model.Issue, presence DependencyPresence, scope DependencyScope) (bool, error) {
	if presence != DependencyPresent && presence != DependencyAbsent {
		return false, ErrDependencyPresence
	}
	if scope != IssueDependencyScope && scope != WorkDependencyScope {
		return false, ErrDependencyScope
	}
	if blocked == nil || prerequisite == nil {
		return false, ErrInvalidDependency
	}

	changed := false
	err := db.WithWorkTx(ctx, func(ctx context.Context) error {
		changed = false
		var err error
		changed, err = EnsureDependencyInWorkTx(ctx, doer, blocked, prerequisite, presence, scope)
		return err
	})
	if err != nil {
		return false, err
	}
	return changed, nil
}

// EnsureDependencyInWorkTx applies the same authority inside a caller-owned
// db.WithWorkTx callback so a larger Work revision can remain atomic.
func EnsureDependencyInWorkTx(ctx context.Context, doer *user_model.User, blocked, prerequisite *issues_model.Issue, presence DependencyPresence, scope DependencyScope) (bool, error) {
	changed, _, err := EnsureDependencyWithEventsInWorkTx(ctx, doer, blocked, prerequisite, presence, scope)
	return changed, err
}

// EnsureDependencyWithEventsInWorkTx applies WP2's authority and returns only
// the native timeline rows created by this change for provenance linking.
func EnsureDependencyWithEventsInWorkTx(ctx context.Context, doer *user_model.User, blocked, prerequisite *issues_model.Issue, presence DependencyPresence, scope DependencyScope) (bool, []*issues_model.Comment, error) {
	if presence != DependencyPresent && presence != DependencyAbsent {
		return false, nil, ErrDependencyPresence
	}
	if scope != IssueDependencyScope && scope != WorkDependencyScope {
		return false, nil, ErrDependencyScope
	}
	if blocked == nil || prerequisite == nil {
		return false, nil, ErrInvalidDependency
	}
	return ensureDependency(ctx, doer, blocked.ID, prerequisite.ID, presence, scope)
}

func ensureDependency(ctx context.Context, doer *user_model.User, blockedID, prerequisiteID int64, presence DependencyPresence, scope DependencyScope) (bool, []*issues_model.Comment, error) {
	blocked, err := issues_model.GetIssueByID(ctx, blockedID)
	if err != nil {
		return false, nil, ErrDependencyNotPermitted
	}
	if err := blocked.LoadRepo(ctx); err != nil {
		return false, nil, err
	}
	blockedPermission, err := access_model.GetDoerRepoPermission(ctx, blocked.Repo, doer)
	if err != nil {
		return false, nil, err
	}
	if blocked.Repo.IsArchived || !blocked.Repo.IsDependenciesEnabled(ctx) || !blockedPermission.CanWriteIssuesOrPulls(blocked.IsPull) {
		return false, nil, ErrDependencyNotPermitted
	}

	prerequisite, err := issues_model.GetIssueByID(ctx, prerequisiteID)
	if err != nil {
		return false, nil, ErrDependencyUnavailable
	}
	if err := prerequisite.LoadRepo(ctx); err != nil {
		return false, nil, err
	}
	prerequisitePermission, err := access_model.GetDoerRepoPermission(ctx, prerequisite.Repo, doer)
	if err != nil {
		return false, nil, err
	}
	if !prerequisitePermission.CanReadIssuesOrPulls(prerequisite.IsPull) {
		return false, nil, ErrDependencyUnavailable
	}

	exists, err := issues_model.IssueDependencyExists(ctx, blocked.ID, prerequisite.ID)
	if err != nil {
		return false, nil, err
	}
	if presence == DependencyAbsent {
		if !exists {
			return false, nil, nil
		}
		comments, err := issues_model.RemoveIssueDependencyWithComments(ctx, doer, blocked, prerequisite, issues_model.DependencyTypeBlockedBy)
		if err != nil {
			if issues_model.IsErrDependencyNotExists(err) {
				return false, nil, nil
			}
			return false, nil, err
		}
		return true, comments, nil
	}
	if exists {
		return false, nil, nil
	}

	if blocked.ID == prerequisite.ID {
		return false, nil, ErrSelfDependency
	}
	if scope == WorkDependencyScope {
		if blocked.IsPull || prerequisite.IsPull {
			return false, nil, ErrDependencyEndpoint
		}
		if blocked.RepoID != prerequisite.RepoID {
			return false, nil, ErrCrossRepositoryDependency
		}
	} else if blocked.RepoID != prerequisite.RepoID && !setting.Service.AllowCrossRepositoryDependencies {
		return false, nil, ErrCrossRepositoryDependency
	}

	graph, err := loadDependencyGraph(ctx, doer, issues_model.IssueList{prerequisite}, scope, setting.Work.MaxGraphNodes)
	if err != nil {
		return false, nil, err
	}
	if _, found := graph.nodes[blocked.ID]; found || graph.cyclic() {
		return false, nil, ErrInvalidDependency
	}
	comments, err := issues_model.CreateIssueDependencyWithComments(ctx, doer, blocked, prerequisite)
	if err != nil {
		if issues_model.IsErrDependencyExists(err) {
			return false, nil, nil
		}
		return false, nil, err
	}
	return true, comments, nil
}

// ValidateDependencyDAG validates the complete bounded prerequisite closure.
func ValidateDependencyDAG(ctx context.Context, doer *user_model.User, roots issues_model.IssueList, scope DependencyScope) error {
	if scope != IssueDependencyScope && scope != WorkDependencyScope {
		return ErrDependencyScope
	}
	return db.WithWorkTx(ctx, func(ctx context.Context) error {
		return ValidateDependencyDAGInWorkTx(ctx, doer, roots, scope)
	})
}

// ValidateDependencyDAGInWorkTx validates a graph inside a caller-owned
// db.WithWorkTx callback for atomic plan validation.
func ValidateDependencyDAGInWorkTx(ctx context.Context, doer *user_model.User, roots issues_model.IssueList, scope DependencyScope) error {
	if scope != IssueDependencyScope && scope != WorkDependencyScope {
		return ErrDependencyScope
	}
	rootIDs := make([]int64, 0, len(roots))
	seenRoots := make(map[int64]struct{}, len(roots))
	for _, root := range roots {
		if root == nil {
			return ErrInvalidDependency
		}
		if _, seen := seenRoots[root.ID]; seen {
			continue
		}
		seenRoots[root.ID] = struct{}{}
		rootIDs = append(rootIDs, root.ID)
	}
	var err error
	roots, err = getDependencyIssues(ctx, rootIDs)
	if err != nil {
		return err
	}
	if len(roots) != len(rootIDs) {
		return ErrInvalidDependency
	}
	graph, err := loadDependencyGraph(ctx, doer, roots, scope, setting.Work.MaxGraphNodes)
	if err != nil {
		return err
	}
	if graph.cyclic() {
		return ErrInvalidDependency
	}
	return nil
}

type dependencyGraph struct {
	nodes map[int64]struct{}
	edges map[int64][]int64
}

func loadDependencyGraph(ctx context.Context, doer *user_model.User, roots issues_model.IssueList, scope DependencyScope, maxNodes int) (*dependencyGraph, error) {
	frontier := roots
	graph := &dependencyGraph{
		nodes: make(map[int64]struct{}, len(roots)),
		edges: make(map[int64][]int64),
	}
	for _, root := range roots {
		if root == nil {
			return nil, ErrInvalidDependency
		}
		graph.nodes[root.ID] = struct{}{}
	}
	permissions := make(map[int64]access_model.Permission)

	for len(frontier) > 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(graph.nodes) > maxNodes {
			return nil, ErrInvalidDependency
		}
		if _, err := frontier.LoadRepositories(ctx); err != nil {
			return nil, err
		}

		frontierIDs := make([]int64, 0, len(frontier))
		for _, issue := range frontier {
			permission, ok := permissions[issue.RepoID]
			if !ok {
				var err error
				permission, err = access_model.GetDoerRepoPermission(ctx, issue.Repo, doer)
				if err != nil {
					return nil, err
				}
				permissions[issue.RepoID] = permission
			}
			if !permission.CanReadIssuesOrPulls(issue.IsPull) {
				return nil, ErrInvalidDependency
			}
			if scope == WorkDependencyScope && issue.IsPull {
				return nil, ErrDependencyEndpoint
			}
			frontierIDs = append(frontierIDs, issue.ID)
		}

		dependencyIDs, err := issues_model.GetIssueDependencyIDs(ctx, frontierIDs)
		if err != nil {
			return nil, err
		}
		nextIDs := make([]int64, 0)
		for _, issueID := range frontierIDs {
			graph.edges[issueID] = dependencyIDs[issueID]
			for _, dependencyID := range dependencyIDs[issueID] {
				if _, ok := graph.nodes[dependencyID]; ok {
					continue
				}
				graph.nodes[dependencyID] = struct{}{}
				nextIDs = append(nextIDs, dependencyID)
			}
		}
		if len(graph.nodes) > maxNodes {
			return nil, ErrInvalidDependency
		}
		frontier, err = getDependencyIssues(ctx, nextIDs)
		if err != nil {
			return nil, err
		}
		if len(frontier) != len(nextIDs) {
			return nil, ErrInvalidDependency
		}
	}
	return graph, nil
}

func getDependencyIssues(ctx context.Context, issueIDs []int64) (issues_model.IssueList, error) {
	issues := make(issues_model.IssueList, 0, len(issueIDs))
	for len(issueIDs) > 0 {
		batchSize := min(len(issueIDs), db.DefaultMaxInSize)
		batch, err := issues_model.GetIssuesByIDs(ctx, issueIDs[:batchSize])
		if err != nil {
			return nil, err
		}
		issues = append(issues, batch...)
		issueIDs = issueIDs[batchSize:]
	}
	return issues, nil
}

func (graph *dependencyGraph) cyclic() bool {
	const (
		unseen uint8 = iota
		visiting
		visited
	)
	states := make(map[int64]uint8, len(graph.nodes))
	var visit func(int64) bool
	visit = func(issueID int64) bool {
		switch states[issueID] {
		case visiting:
			return true
		case visited:
			return false
		}
		states[issueID] = visiting
		if slices.ContainsFunc(graph.edges[issueID], visit) {
			return true
		}
		states[issueID] = visited
		return false
	}
	for issueID := range graph.nodes {
		if states[issueID] == unseen && visit(issueID) {
			return true
		}
	}
	return false
}
