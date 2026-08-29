// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"testing"

	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/setting"
	pull_service "gitea.dev/services/pull"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInspectItemUnplannedAndMultiPlanState(t *testing.T) {
	source := newFakeSource()
	item := source.addIssue(1, 7, false, false)
	disabled := source.addProject(10, project_model.PlanningStateDisabled)
	draft := source.addProject(11, project_model.PlanningStateDraft)
	active := source.addProject(12, project_model.PlanningStateActive)
	source.memberships[item.ID] = []int64{disabled.ID, draft.ID, active.ID}
	service := &ReadService{source: source, secret: "test-secret"}

	inspection, err := service.InspectItem(t.Context(), nil, ItemRequest{
		Owner: "owner", Repository: "repo", IssueNumber: item.Index, PageKind: "contexts",
	})
	require.NoError(t, err)
	assert.Equal(t, "planned", inspection.WorkItem.Classification)
	require.Len(t, inspection.WorkItem.ProjectMemberships, 3)
	require.Len(t, inspection.WorkItem.ContextSummaries, 2)
	assert.Equal(t, "planned", inspection.WorkItem.ContextSummaries[0].DerivedState)
	assert.Equal(t, "ready", inspection.WorkItem.ContextSummaries[1].DerivedState)
	assert.Equal(t, "none", inspection.Page.SnapshotConsistency)
	assert.True(t, inspection.Page.ReinspectBeforeAction)

	source.memberships[item.ID] = nil
	inspection, err = service.InspectItem(t.Context(), nil, ItemRequest{
		Owner: "owner", Repository: "repo", IssueNumber: item.Index,
	})
	require.NoError(t, err)
	assert.Equal(t, "unplanned", inspection.WorkItem.Classification)
	assert.Empty(t, inspection.WorkItem.ContextSummaries)
}

func TestInspectPlanExcludesPullCards(t *testing.T) {
	source := newFakeSource()
	project := source.addProject(20, project_model.PlanningStateActive)
	issue := source.addIssue(2, 2, false, false)
	pull := source.addIssue(3, 3, true, false)
	source.members[project.ID] = []project_model.WorkProjectIssue{
		{ProjectID: project.ID, IssueID: issue.ID, Index: issue.Index},
		{ProjectID: project.ID, IssueID: pull.ID, Index: pull.Index, IsPull: true},
	}
	service := &ReadService{source: source, secret: "test-secret"}

	inspection, err := service.InspectPlan(t.Context(), nil, PlanRequest{
		Owner: "owner", Repository: "repo", ProjectID: project.ID,
	})
	require.NoError(t, err)
	require.Len(t, inspection.WorkPlan.ItemSummaries, 1)
	assert.Equal(t, "ready", inspection.WorkPlan.ItemSummaries[0].DerivedState)
	require.Len(t, inspection.WorkPlan.ExcludedMembers, 1)
	assert.Equal(t, "pull/3", inspection.WorkPlan.ExcludedMembers[0].Ref)
	assert.Equal(t, "excluded", inspection.WorkPlan.ExcludedMembers[0].State)
}

func TestInspectPlanHiddenPrerequisiteFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name            string
		planningState   project_model.PlanningState
		integrityStatus string
	}{
		{name: "active", planningState: project_model.PlanningStateActive, integrityStatus: "concern"},
		{name: "draft", planningState: project_model.PlanningStateDraft, integrityStatus: "incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := newFakeSource()
			project := source.addProject(30, test.planningState)
			issue := source.addIssue(4, 4, false, false)
			hidden := source.addExternalIssue(40, 5, false)
			source.hiddenRepos[hidden.RepoID] = true
			source.dependencies[issue.ID] = []int64{hidden.ID}
			source.members[project.ID] = []project_model.WorkProjectIssue{{ProjectID: project.ID, IssueID: issue.ID, Index: issue.Index}}
			service := &ReadService{source: source, secret: "test-secret"}

			inspection, err := service.InspectPlan(t.Context(), nil, PlanRequest{
				Owner: "owner", Repository: "repo", ProjectID: project.ID,
			})
			require.NoError(t, err)
			assert.Equal(t, test.integrityStatus, inspection.WorkPlan.Integrity.Status)
			assert.Empty(t, inspection.WorkPlan.ReadyFrontier)
			require.Len(t, inspection.WorkPlan.EdgeSummaries, 1)
			assert.Equal(t, Reference{Availability: "undisclosed"}, inspection.WorkPlan.EdgeSummaries[0].Prerequisite)
		})
	}
}

func TestInspectPlanGraphBoundState(t *testing.T) {
	oldBound := setting.Work.MaxGraphNodes
	setting.Work.MaxGraphNodes = 1
	t.Cleanup(func() { setting.Work.MaxGraphNodes = oldBound })
	for _, test := range []struct {
		state     project_model.PlanningState
		integrity string
	}{
		{project_model.PlanningStateActive, "concern"},
		{project_model.PlanningStateDraft, "incomplete"},
	} {
		source := newFakeSource()
		project := source.addProject(41, test.state)
		issue := source.addIssue(6, 6, false, false)
		dependency := source.addIssue(7, 7, false, true)
		source.dependencies[issue.ID] = []int64{dependency.ID}
		source.members[project.ID] = []project_model.WorkProjectIssue{{ProjectID: project.ID, IssueID: issue.ID, Index: issue.Index}}
		service := &ReadService{source: source, secret: "test-secret"}

		inspection, err := service.InspectPlan(t.Context(), nil, PlanRequest{Owner: "owner", Repository: "repo", ProjectID: project.ID})
		require.NoError(t, err)
		assert.Equal(t, test.integrity, inspection.WorkPlan.Integrity.Status)
		assert.Empty(t, inspection.WorkPlan.ReadyFrontier)
	}
}

func TestInspectPlanPaginationBinding(t *testing.T) {
	source := newFakeSource()
	firstProject := source.addProject(50, project_model.PlanningStateActive)
	secondProject := source.addProject(51, project_model.PlanningStateActive)
	for i := int64(1); i <= 3; i++ {
		issue := source.addIssue(100+i, i, false, false)
		entry := project_model.WorkProjectIssue{ProjectID: firstProject.ID, IssueID: issue.ID, Index: issue.Index}
		source.members[firstProject.ID] = append(source.members[firstProject.ID], entry)
		entry.ProjectID = secondProject.ID
		source.members[secondProject.ID] = append(source.members[secondProject.ID], entry)
	}
	service := &ReadService{source: source, secret: "test-secret"}
	request := PlanRequest{Owner: "owner", Repository: "repo", ProjectID: firstProject.ID, Limit: 2}

	first, err := service.InspectPlan(t.Context(), nil, request)
	require.NoError(t, err)
	require.Len(t, first.Page.Items, 2)
	require.NotEmpty(t, first.Page.NextCursor)
	request.Cursor = first.Page.NextCursor
	second, err := service.InspectPlan(t.Context(), nil, request)
	require.NoError(t, err)
	require.Len(t, second.Page.Items, 1)
	assert.Empty(t, second.Page.NextCursor)

	request.ProjectID = secondProject.ID
	_, err = service.InspectPlan(t.Context(), nil, request)
	assertReadFailure(t, err, ReadInvalidCursor)
	request.ProjectID = firstProject.ID
	request.PageKind = "ready_frontier"
	_, err = service.InspectPlan(t.Context(), nil, request)
	assertReadFailure(t, err, ReadInvalidCursor)
	request.PageKind = "items"
	request.Cursor += "tampered"
	_, err = service.InspectPlan(t.Context(), nil, request)
	assertReadFailure(t, err, ReadInvalidCursor)
}

func TestInspectItemReferencePaginationUsesRepositoryTieBreak(t *testing.T) {
	source := newFakeSource()
	item := source.addIssue(150, 10, false, false)
	local := source.addIssue(151, 5, false, true)
	external := source.addExternalIssue(152, 5, true)
	source.dependencies[item.ID] = []int64{local.ID, external.ID}
	service := &ReadService{source: source, secret: "test-secret"}
	request := ItemRequest{Owner: "owner", Repository: "repo", IssueNumber: item.Index, PageKind: "prerequisites", Limit: 1}

	first, err := service.InspectItem(t.Context(), nil, request)
	require.NoError(t, err)
	require.Len(t, first.Page.Items, 1)
	require.NotEmpty(t, first.Page.NextCursor)
	request.Cursor = first.Page.NextCursor
	second, err := service.InspectItem(t.Context(), nil, request)
	require.NoError(t, err)
	require.Len(t, second.Page.Items, 1)
	assert.Empty(t, second.Page.NextCursor)
}

func TestInspectItemDeliveryPaginationUsesRepositoryTieBreak(t *testing.T) {
	source := newFakeSource()
	item := source.addIssue(170, 10, false, false)
	localIssue := source.addIssue(171, 5, true, false)
	externalIssue := source.addExternalIssue(172, 5, false)
	externalIssue.IsPull = true
	local := &issues_model.PullRequest{ID: 180, IssueID: localIssue.ID, Index: localIssue.Index, BaseRepoID: 1, HeadRepoID: 1}
	external := &issues_model.PullRequest{ID: 181, IssueID: externalIssue.ID, Index: externalIssue.Index, BaseRepoID: 2, HeadRepoID: 2}
	source.pullList = issues_model.PullRequestList{local, external}
	source.closing[item.ID] = []issues_model.WorkClosingPullReference{
		{IssueID: item.ID, PullRepoID: 1, PullIssueID: localIssue.ID},
		{IssueID: item.ID, PullRepoID: 2, PullIssueID: externalIssue.ID},
	}
	source.revisionByPull[local.ID] = pull_service.WorkRevision{Revision: fmt.Sprintf("%040d", 1)}
	source.revisionByPull[external.ID] = pull_service.WorkRevision{Revision: fmt.Sprintf("%040d", 2)}
	service := &ReadService{source: source, secret: "test-secret"}
	request := ItemRequest{Owner: "owner", Repository: "repo", IssueNumber: item.Index, PageKind: "deliveries", Limit: 1}

	first, err := service.InspectItem(t.Context(), nil, request)
	require.NoError(t, err)
	require.Len(t, first.Page.Items, 1)
	require.NotEmpty(t, first.Page.NextCursor)
	request.Cursor = first.Page.NextCursor
	second, err := service.InspectItem(t.Context(), nil, request)
	require.NoError(t, err)
	require.Len(t, second.Page.Items, 1)
	assert.Empty(t, second.Page.NextCursor)
}

func TestInspectItemBoundsProjectionCollections(t *testing.T) {
	oldBound := setting.Work.MaxProjectionItems
	setting.Work.MaxProjectionItems = 1
	t.Cleanup(func() { setting.Work.MaxProjectionItems = oldBound })
	source := newFakeSource()
	item := source.addIssue(160, 10, false, false)
	first := source.addIssue(161, 11, false, true)
	second := source.addIssue(162, 12, false, true)
	source.dependencies[item.ID] = []int64{first.ID, second.ID}
	service := &ReadService{source: source, secret: "test-secret"}

	inspection, err := service.InspectItem(t.Context(), nil, ItemRequest{Owner: "owner", Repository: "repo", IssueNumber: item.Index, PageKind: "prerequisites"})
	require.NoError(t, err)
	assert.Len(t, inspection.WorkItem.PrerequisiteSummaries, 1)
	assert.Len(t, inspection.Page.Items, 2, "pagination retains the complete bounded source collection")
}

func TestInspectItemDeliveryUsesBaseEvidence(t *testing.T) {
	source := newFakeSource()
	issue := source.addIssue(200, 20, false, false)
	pullIssue := source.addIssue(201, 21, true, false)
	pull := &issues_model.PullRequest{ID: 80, IssueID: pullIssue.ID, Index: pullIssue.Index, BaseRepoID: 1, HeadRepoID: 2}
	source.pullList = issues_model.PullRequestList{pull}
	source.closing[issue.ID] = []issues_model.WorkClosingPullReference{{IssueID: issue.ID, PullRepoID: 1, PullIssueID: pullIssue.ID}}
	source.revisionByPull[pull.ID] = pull_service.WorkRevision{Revision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	service := &ReadService{source: source, secret: "test-secret"}

	inspection, err := service.InspectItem(t.Context(), nil, ItemRequest{Owner: "owner", Repository: "repo", IssueNumber: issue.Index, PageKind: "deliveries"})
	require.NoError(t, err)
	require.Len(t, inspection.WorkItem.DeliverySummaries, 1)
	assert.Equal(t, "unverified", inspection.WorkItem.DeliverySummaries[0].CheckState)

	pair := git_model.RepoSHA{RepoID: 1, SHA: source.revisionByPull[pull.ID].Revision}
	source.statusByPair[pair] = []*git_model.CommitStatus{{RepoID: 1, SHA: pair.SHA, State: commitstatus.CommitStatusFailure}}
	inspection, err = service.InspectItem(t.Context(), nil, ItemRequest{Owner: "owner", Repository: "repo", IssueNumber: issue.Index})
	require.NoError(t, err)
	assert.Equal(t, "failure", inspection.WorkItem.DeliverySummaries[0].CheckState)
	assert.Equal(t, []git_model.RepoSHA{pair}, source.lastStatusPairs)
}

func TestInspectPlanBatchesBySetNotItem(t *testing.T) {
	queryCalls := func(size int64) map[string]int {
		source := newFakeSource()
		project := source.addProject(60, project_model.PlanningStateActive)
		for i := int64(1); i <= size; i++ {
			issue := source.addIssue(300+i, i, false, false)
			source.members[project.ID] = append(source.members[project.ID], project_model.WorkProjectIssue{ProjectID: project.ID, IssueID: issue.ID, Index: issue.Index})
			pullIssue := source.addIssue(10_000+i, 1_000+i, true, false)
			pull := &issues_model.PullRequest{ID: 20_000 + i, IssueID: pullIssue.ID, Index: pullIssue.Index, BaseRepoID: 1, HeadRepoID: 1}
			source.pullList = append(source.pullList, pull)
			source.closing[issue.ID] = []issues_model.WorkClosingPullReference{{IssueID: issue.ID, PullRepoID: 1, PullIssueID: pullIssue.ID}}
			source.revisionByPull[pull.ID] = pull_service.WorkRevision{Revision: fmt.Sprintf("%040d", i)}
		}
		service := &ReadService{source: source, secret: "test-secret"}
		_, err := service.InspectPlan(t.Context(), nil, PlanRequest{Owner: "owner", Repository: "repo", ProjectID: project.ID})
		require.NoError(t, err)
		return source.callSnapshot()
	}
	small := queryCalls(2)
	large := queryCalls(50)
	for _, operation := range []string{"projectIssues", "issues", "dependencyIDs", "closingPulls", "pulls", "statuses", "revisions"} {
		assert.Equal(t, small[operation], large[operation], operation)
	}
	assert.Equal(t, 1, large["projectIssues"])
	assert.Equal(t, 1, large["dependencyIDs"])
	for _, operation := range []string{"closingPulls", "pulls", "statuses", "revisions"} {
		assert.Equal(t, 1, large[operation], operation)
	}
}

func TestInspectPlanCancellationInterruptsDependencyRead(t *testing.T) {
	source := newFakeSource()
	project := source.addProject(70, project_model.PlanningStateActive)
	issue := source.addIssue(400, 1, false, false)
	source.members[project.ID] = []project_model.WorkProjectIssue{{ProjectID: project.ID, IssueID: issue.ID, Index: issue.Index}}
	source.blockDependencies = true
	dependencyStarted := make(chan struct{})
	source.dependencyStarted = dependencyStarted
	service := &ReadService{source: source, secret: "test-secret"}
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		_, err := service.InspectPlan(ctx, nil, PlanRequest{Owner: "owner", Repository: "repo", ProjectID: project.ID})
		result <- err
	}()
	<-dependencyStarted
	cancel()
	err := <-result
	assert.ErrorIs(t, err, context.Canceled)
}

func assertReadFailure(t *testing.T, err error, kind ReadFailureKind) {
	t.Helper()
	var failure *ReadFailure
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, kind, failure.Kind)
}

type fakeSource struct {
	mu                sync.Mutex
	repos             map[int64]*repo_model.Repository
	issuesByID        map[int64]*issues_model.Issue
	issuesByIndex     map[int64]*issues_model.Issue
	projectsByID      map[int64]*project_model.Project
	members           map[int64][]project_model.WorkProjectIssue
	memberships       map[int64][]int64
	dependencies      map[int64][]int64
	dependents        map[int64][]int64
	closing           map[int64][]issues_model.WorkClosingPullReference
	pullList          issues_model.PullRequestList
	revisionByPull    map[int64]pull_service.WorkRevision
	statusByPair      map[git_model.RepoSHA][]*git_model.CommitStatus
	hiddenRepos       map[int64]bool
	calls             map[string]int
	lastStatusPairs   []git_model.RepoSHA
	blockDependencies bool
	dependencyStarted chan struct{}
}

func newFakeSource() *fakeSource {
	repo := fakeRepo(1, "owner", "repo")
	return &fakeSource{
		repos: map[int64]*repo_model.Repository{repo.ID: repo}, issuesByID: map[int64]*issues_model.Issue{}, issuesByIndex: map[int64]*issues_model.Issue{},
		projectsByID: map[int64]*project_model.Project{}, members: map[int64][]project_model.WorkProjectIssue{}, memberships: map[int64][]int64{},
		dependencies: map[int64][]int64{}, dependents: map[int64][]int64{}, closing: map[int64][]issues_model.WorkClosingPullReference{},
		revisionByPull: map[int64]pull_service.WorkRevision{}, statusByPair: map[git_model.RepoSHA][]*git_model.CommitStatus{},
		hiddenRepos: map[int64]bool{}, calls: map[string]int{},
	}
}

func fakeRepo(id int64, owner, name string) *repo_model.Repository {
	repo := &repo_model.Repository{ID: id, OwnerName: owner, Name: name}
	repo.Units = []*repo_model.RepoUnit{
		{RepoID: id, Type: unit.TypeIssues}, {RepoID: id, Type: unit.TypePullRequests}, {RepoID: id, Type: unit.TypeProjects},
	}
	return repo
}

func (source *fakeSource) addIssue(id, index int64, isPull, closed bool) *issues_model.Issue {
	issue := &issues_model.Issue{ID: id, RepoID: 1, Repo: source.repos[1], Index: index, Title: fmt.Sprintf("Issue %d", index), Content: "body", IsPull: isPull, IsClosed: closed}
	source.issuesByID[id] = issue
	source.issuesByIndex[index] = issue
	return issue
}

func (source *fakeSource) addExternalIssue(id, index int64, closed bool) *issues_model.Issue {
	repoID := int64(2)
	if source.repos[repoID] == nil {
		source.repos[repoID] = fakeRepo(repoID, "external", "repo")
	}
	issue := &issues_model.Issue{ID: id, RepoID: repoID, Repo: source.repos[repoID], Index: index, Title: "External", IsClosed: closed}
	source.issuesByID[id] = issue
	return issue
}

func (source *fakeSource) addProject(id int64, state project_model.PlanningState) *project_model.Project {
	project := &project_model.Project{ID: id, RepoID: 1, Type: project_model.TypeRepository, Title: fmt.Sprintf("Project %d", id), PlanningState: state}
	source.projectsByID[id] = project
	return project
}

func (source *fakeSource) count(operation string) {
	source.mu.Lock()
	defer source.mu.Unlock()
	source.calls[operation]++
}

func (source *fakeSource) callSnapshot() map[string]int {
	source.mu.Lock()
	defer source.mu.Unlock()
	result := make(map[string]int, len(source.calls))
	maps.Copy(result, source.calls)
	return result
}

func (source *fakeSource) repository(_ context.Context, owner, name string) (*repo_model.Repository, error) {
	source.count("repository")
	if owner == "owner" && name == "repo" {
		return source.repos[1], nil
	}
	return nil, repo_model.ErrRepoNotExist{}
}

func (source *fakeSource) permission(_ context.Context, repo *repo_model.Repository, _ *user_model.User) (access_model.Permission, error) {
	source.count("permission")
	mode := perm.AccessModeOwner
	if source.hiddenRepos[repo.ID] {
		mode = perm.AccessModeNone
	}
	permission := access_model.Permission{AccessMode: mode}
	permission.SetUnitsWithDefaultAccessMode(repo.Units, mode)
	return permission, nil
}

func (source *fakeSource) issue(_ context.Context, repoID, index int64) (*issues_model.Issue, error) {
	source.count("issue")
	issue := source.issuesByIndex[index]
	if issue == nil || issue.RepoID != repoID {
		return nil, issues_model.ErrIssueNotExist{}
	}
	return issue, nil
}

func (source *fakeSource) issues(_ context.Context, ids []int64) (issues_model.IssueList, error) {
	source.count("issues")
	result := make(issues_model.IssueList, 0, len(ids))
	for _, id := range ids {
		if issue := source.issuesByID[id]; issue != nil {
			result = append(result, issue)
		}
	}
	return result, nil
}

func (source *fakeSource) project(_ context.Context, repoID, projectID int64) (*project_model.Project, error) {
	source.count("project")
	project := source.projectsByID[projectID]
	if project == nil || project.RepoID != repoID {
		return nil, project_model.ErrProjectNotExist{}
	}
	return project, nil
}

func (source *fakeSource) projects(_ context.Context, ids []int64) (map[int64]*project_model.Project, error) {
	source.count("projects")
	result := make(map[int64]*project_model.Project, len(ids))
	for _, id := range ids {
		if project := source.projectsByID[id]; project != nil {
			result[id] = project
		}
	}
	return result, nil
}

func (source *fakeSource) projectIssues(_ context.Context, projectID int64, limit int) ([]project_model.WorkProjectIssue, error) {
	source.count("projectIssues")
	return append([]project_model.WorkProjectIssue(nil), source.members[projectID][:min(limit, len(source.members[projectID]))]...), nil
}

func (source *fakeSource) issueProjectIDs(_ context.Context, _ int64, ids []int64) (map[int64][]int64, error) {
	source.count("issueProjectIDs")
	result := make(map[int64][]int64, len(ids))
	for _, id := range ids {
		result[id] = append([]int64(nil), source.memberships[id]...)
	}
	return result, nil
}

func (source *fakeSource) dependencyIDs(ctx context.Context, ids []int64) (map[int64][]int64, error) {
	source.count("dependencyIDs")
	if source.blockDependencies {
		if source.dependencyStarted != nil {
			close(source.dependencyStarted)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	result := make(map[int64][]int64, len(ids))
	for _, id := range ids {
		result[id] = append([]int64(nil), source.dependencies[id]...)
	}
	return result, nil
}

func (source *fakeSource) dependentIDs(_ context.Context, ids []int64) (map[int64][]int64, error) {
	source.count("dependentIDs")
	result := make(map[int64][]int64, len(ids))
	for _, id := range ids {
		result[id] = append([]int64(nil), source.dependents[id]...)
	}
	return result, nil
}

func (source *fakeSource) closingPulls(_ context.Context, ids []int64) (map[int64][]issues_model.WorkClosingPullReference, error) {
	source.count("closingPulls")
	result := make(map[int64][]issues_model.WorkClosingPullReference, len(ids))
	for _, id := range ids {
		result[id] = append([]issues_model.WorkClosingPullReference(nil), source.closing[id]...)
	}
	return result, nil
}

func (source *fakeSource) pulls(_ context.Context, ids []int64) (issues_model.PullRequestList, error) {
	source.count("pulls")
	if len(ids) == 0 {
		return issues_model.PullRequestList{}, nil
	}
	return append(issues_model.PullRequestList(nil), source.pullList...), nil
}

func (source *fakeSource) repositories(_ context.Context, ids []int64) (map[int64]*repo_model.Repository, error) {
	source.count("repositories")
	result := make(map[int64]*repo_model.Repository, len(ids))
	for _, id := range ids {
		if repo := source.repos[id]; repo != nil {
			result[id] = repo
		}
	}
	return result, nil
}

func (source *fakeSource) statuses(_ context.Context, pairs []git_model.RepoSHA) (map[git_model.RepoSHA][]*git_model.CommitStatus, error) {
	source.count("statuses")
	source.lastStatusPairs = append([]git_model.RepoSHA(nil), pairs...)
	return source.statusByPair, nil
}

func (source *fakeSource) revisions(ctx context.Context, pulls issues_model.PullRequestList) (map[int64]pull_service.WorkRevision, error) {
	source.count("revisions")
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return source.revisionByPull, nil
}

func TestReadFailureUnwrap(t *testing.T) {
	err := readFailure(ReadInvalidInput, ErrInvalidInput)
	assert.ErrorIs(t, err, ErrInvalidInput)
}
