// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var dependencyTestIndex atomic.Int64

func enableDependencyTests(t *testing.T, repoID int64) {
	t.Helper()
	unit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: repoID, Type: unit.TypeIssues})
	unit.IssuesConfig().EnableDependencies = true
	require.NoError(t, repo_model.UpdateRepoUnitConfig(t.Context(), unit))
}

func newDependencyTestIssues(t *testing.T, repoID int64, pulls ...bool) issues_model.IssueList {
	t.Helper()
	issues := make(issues_model.IssueList, 0, len(pulls))
	for i, isPull := range pulls {
		index := dependencyTestIndex.Add(1) + 10_000
		issues = append(issues, &issues_model.Issue{
			RepoID:   repoID,
			Index:    index,
			PosterID: 1,
			Title:    fmt.Sprintf("dependency test %d", i),
			IsPull:   isPull,
		})
	}
	require.NoError(t, issues_model.InsertIssues(t.Context(), issues...))
	return issues
}

func dependencyCommentCount(t *testing.T, issueIDs []int64, commentType issues_model.CommentType) int {
	t.Helper()
	args := make([]any, 0, len(issueIDs)+1)
	args = append(args, commentType)
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(issueIDs)), ",")
	for _, issueID := range issueIDs {
		args = append(args, issueID)
	}
	return unittest.GetCount(t, &issues_model.Comment{}, unittest.Cond("type = ? AND issue_id IN ("+placeholders+")", args...))
}

func dependencyCycleRejected(err error) bool {
	if errors.Is(err, ErrInvalidDependency) {
		return true
	}
	var conflict *db.WorkTransactionConflict
	return errors.As(err, &conflict)
}

func TestEnsureDependencyValidatesWorkEndpoints(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	enableDependencyTests(t, 1)
	enableDependencyTests(t, 3)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issues := newDependencyTestIssues(t, 1, false, true)
	external := newDependencyTestIssues(t, 3, false)[0]

	_, err := EnsureDependency(t.Context(), doer, issues[0], issues[0], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrSelfDependency)
	require.ErrorIs(t, err, ErrInvalidDependency)

	_, err = EnsureDependency(t.Context(), doer, issues[0], issues[1], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrDependencyEndpoint)
	_, err = EnsureDependency(t.Context(), doer, issues[1], issues[0], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrDependencyEndpoint)

	_, err = EnsureDependency(t.Context(), doer, issues[0], external, DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrCrossRepositoryDependency)

	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: issues[0].ID})
}

func TestEnsureDependencyPreservesOrdinaryCompatibility(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	enableDependencyTests(t, 1)
	enableDependencyTests(t, 3)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issue := newDependencyTestIssues(t, 1, false)[0]
	pull := newDependencyTestIssues(t, 1, true)[0]
	external := newDependencyTestIssues(t, 3, false)[0]

	changed, err := EnsureDependency(t.Context(), doer, issue, pull, DependencyPresent, IssueDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)

	allowCrossRepository := setting.Service.AllowCrossRepositoryDependencies
	setting.Service.AllowCrossRepositoryDependencies = true
	t.Cleanup(func() {
		setting.Service.AllowCrossRepositoryDependencies = allowCrossRepository
	})
	changed, err = EnsureDependency(t.Context(), doer, issue, external, DependencyPresent, IssueDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)

	setting.Service.AllowCrossRepositoryDependencies = false
	changed, err = EnsureDependency(t.Context(), doer, issue, external, DependencyPresent, IssueDependencyScope)
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = EnsureDependency(t.Context(), doer, issue, external, DependencyAbsent, IssueDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)
}

func TestEnsureDependencyChecksCurrentPermissions(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	enableDependencyTests(t, 1)
	enableDependencyTests(t, 3)
	issues := newDependencyTestIssues(t, 1, false, false)
	hidden := newDependencyTestIssues(t, 3, false)[0]
	reader := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 4})

	_, err := EnsureDependency(t.Context(), reader, issues[0], issues[1], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrDependencyNotPermitted)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: issues[0].ID, DependencyID: issues[1].ID})

	writer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
	_, err = EnsureDependency(t.Context(), writer, issues[0], hidden, DependencyPresent, IssueDependencyScope)
	require.ErrorIs(t, err, ErrDependencyUnavailable)

	unit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: 1, Type: unit.TypeIssues})
	unit.IssuesConfig().EnableDependencies = false
	require.NoError(t, repo_model.UpdateRepoUnitConfig(t.Context(), unit))
	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	_, err = EnsureDependency(t.Context(), admin, issues[0], issues[1], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrDependencyNotPermitted)
}

func TestEnsureDependencyPresenceConverges(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	enableDependencyTests(t, 1)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issues := newDependencyTestIssues(t, 1, false, false)
	issueIDs := []int64{issues[0].ID, issues[1].ID}

	changed, err := EnsureDependency(t.Context(), doer, issues[0], issues[1], DependencyPresent, WorkDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = EnsureDependency(t.Context(), doer, issues[0], issues[1], DependencyPresent, WorkDependencyScope)
	require.NoError(t, err)
	assert.False(t, changed)
	unittest.AssertCount(t, &issues_model.IssueDependency{IssueID: issues[0].ID, DependencyID: issues[1].ID}, 1)
	assert.Equal(t, 2, dependencyCommentCount(t, issueIDs, issues_model.CommentTypeAddDependency))

	changed, err = EnsureDependency(t.Context(), doer, issues[0], issues[1], DependencyAbsent, WorkDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = EnsureDependency(t.Context(), doer, issues[0], issues[1], DependencyAbsent, WorkDependencyScope)
	require.NoError(t, err)
	assert.False(t, changed)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: issues[0].ID, DependencyID: issues[1].ID})
	assert.Equal(t, 2, dependencyCommentCount(t, issueIDs, issues_model.CommentTypeRemoveDependency))
}

func TestEnsureDependencyRejectsReciprocalAndTransitiveCycles(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	enableDependencyTests(t, 1)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issues := newDependencyTestIssues(t, 1, false, false, false, false)

	changed, err := EnsureDependency(t.Context(), doer, issues[0], issues[1], DependencyPresent, WorkDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)
	_, err = EnsureDependency(t.Context(), doer, issues[1], issues[0], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrInvalidDependency)

	changed, err = EnsureDependency(t.Context(), doer, issues[1], issues[2], DependencyPresent, WorkDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)
	changed, err = EnsureDependency(t.Context(), doer, issues[2], issues[3], DependencyPresent, WorkDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)
	_, err = EnsureDependency(t.Context(), doer, issues[3], issues[0], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrInvalidDependency)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: issues[3].ID, DependencyID: issues[0].ID})
}

func TestEnsureDependencyHiddenAndBoundFailuresAreEquivalent(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	enableDependencyTests(t, 1)
	admin := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
	visible := newDependencyTestIssues(t, 1, false, false, false)
	hidden := newDependencyTestIssues(t, 3, false)[0]

	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), admin, visible[1], hidden))
	_, hiddenIntermediate := EnsureDependency(t.Context(), doer, visible[0], visible[1], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, hiddenIntermediate, ErrInvalidDependency)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: visible[0].ID, DependencyID: visible[1].ID})

	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), admin, hidden, visible[0]))
	_, hiddenCycle := EnsureDependency(t.Context(), doer, visible[0], visible[1], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, hiddenCycle, ErrInvalidDependency)

	require.NoError(t, issues_model.RemoveIssueDependency(t.Context(), admin, visible[1], hidden, issues_model.DependencyTypeBlockedBy))
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), admin, visible[1], visible[2]))
	maxGraphNodes := setting.Work.MaxGraphNodes
	setting.Work.MaxGraphNodes = 1
	t.Cleanup(func() {
		setting.Work.MaxGraphNodes = maxGraphNodes
	})
	_, overBound := EnsureDependency(t.Context(), doer, visible[0], visible[1], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, overBound, ErrInvalidDependency)

	assert.Equal(t, ErrInvalidDependency.Error(), hiddenIntermediate.Error())
	assert.Equal(t, hiddenIntermediate.Error(), hiddenCycle.Error())
	assert.Equal(t, hiddenIntermediate.Error(), overBound.Error())
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: visible[0].ID, DependencyID: visible[1].ID})
}

func TestEnsureDependencyGraphBoundCountsVisitedNodes(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	enableDependencyTests(t, 1)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issues := newDependencyTestIssues(t, 1, false, false, false)
	maxGraphNodes := setting.Work.MaxGraphNodes
	setting.Work.MaxGraphNodes = 1
	t.Cleanup(func() {
		setting.Work.MaxGraphNodes = maxGraphNodes
	})

	changed, err := EnsureDependency(t.Context(), doer, issues[0], issues[1], DependencyPresent, WorkDependencyScope)
	require.NoError(t, err)
	assert.True(t, changed)
	_, err = EnsureDependency(t.Context(), doer, issues[2], issues[0], DependencyPresent, WorkDependencyScope)
	require.ErrorIs(t, err, ErrInvalidDependency)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: issues[2].ID, DependencyID: issues[0].ID})
}

func TestValidateDependencyDAGUsesSharedGraphAuthority(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issues := newDependencyTestIssues(t, 1, false, false, false)
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), doer, issues[0], issues[1]))
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), doer, issues[1], issues[2]))
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), doer, issues[2], issues[0]))

	err := ValidateDependencyDAG(t.Context(), doer, issues_model.IssueList{issues[0]}, WorkDependencyScope)
	require.ErrorIs(t, err, ErrInvalidDependency)
}

func TestValidateDependencyDAGAppliesWorkEndpointRules(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issues := newDependencyTestIssues(t, 1, false, true)
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), doer, issues[0], issues[1]))

	require.NoError(t, ValidateDependencyDAG(t.Context(), doer, issues_model.IssueList{issues[0]}, IssueDependencyScope))
	err := ValidateDependencyDAG(t.Context(), doer, issues_model.IssueList{issues[0]}, WorkDependencyScope)
	require.ErrorIs(t, err, ErrDependencyEndpoint)
}

func TestEnsureDependencyConcurrentEdgesCannotCloseCycle(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	enableDependencyTests(t, 1)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issues := newDependencyTestIssues(t, 1, false, false, false, false)
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), doer, issues[1], issues[2]))
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), doer, issues[3], issues[0]))

	start := make(chan struct{})
	results := make(chan error, 2)
	var ready sync.WaitGroup
	ready.Add(2)
	add := func(blocked, prerequisite *issues_model.Issue) {
		ready.Done()
		<-start
		_, err := EnsureDependency(t.Context(), doer, blocked, prerequisite, DependencyPresent, WorkDependencyScope)
		results <- err
	}
	go add(issues[0], issues[1])
	go add(issues[2], issues[3])
	ready.Wait()
	close(start)

	first, second := <-results, <-results
	assert.True(t, (first == nil && dependencyCycleRejected(second)) || (second == nil && dependencyCycleRejected(first)), "results: %v, %v", first, second)
	assert.Equal(t, 3, unittest.GetCount(t, &issues_model.IssueDependency{}))
	require.NoError(t, ValidateDependencyDAG(t.Context(), doer, issues_model.IssueList{issues[0], issues[2]}, WorkDependencyScope))
}
