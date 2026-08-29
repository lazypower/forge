// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sort"
	"strconv"

	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	access_model "gitea.dev/models/perm/access"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/container"
	"gitea.dev/modules/setting"
)

var _ Reader = (*ReadService)(nil)

// ReadService is the sole authority for ADR 0003 Work projections.
type ReadService struct {
	source nativeSource
	secret string
}

// NewReadService returns the production Work projection reader.
func NewReadService() *ReadService {
	return &ReadService{source: forgeSource{}, secret: setting.SecretKey}
}

type graph struct {
	nodes        map[int64]*issues_model.Issue
	edges        map[int64][]int64
	repositories map[int64]*repo_model.Repository
	hidden       map[int64]bool
	missing      map[int64]bool
	overBound    bool
	cyclic       bool
}

type composition struct {
	service     *ReadService
	ctx         context.Context
	doer        *user_model.User
	requestRepo *repo_model.Repository
	permissions map[int64]access_model.Permission
}

func (service *ReadService) InspectItem(ctx context.Context, doer *user_model.User, request ItemRequest) (*ItemInspection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.PageKind == "" {
		request.PageKind = "contexts"
	}
	if !slices.Contains([]string{"prerequisites", "dependents", "memberships", "contexts", "deliveries"}, request.PageKind) || request.IssueNumber <= 0 {
		return nil, readFailure(ReadInvalidInput, ErrInvalidInput)
	}
	limit, err := pageLimit(request.Limit)
	if err != nil {
		return nil, err
	}
	compose, permission, err := service.begin(ctx, doer, request.Owner, request.Repository)
	if err != nil {
		return nil, err
	}
	if !permission.CanRead(unit.TypeIssues) {
		return nil, readFailure(ReadUnavailable, ErrUnavailable)
	}
	issue, err := service.source.issue(ctx, compose.requestRepo.ID, request.IssueNumber)
	if err != nil || issue.IsPull {
		if err == nil || issues_model.IsErrIssueNotExist(err) {
			return nil, readFailure(ReadUnavailable, ErrUnavailable)
		}
		return nil, err
	}
	issue.Repo = compose.requestRepo

	dependencyGraph, err := compose.loadGraph(issues_model.IssueList{issue})
	if err != nil {
		return nil, err
	}
	dependentIDs, err := service.source.dependentIDs(ctx, []int64{issue.ID})
	if err != nil {
		return nil, err
	}
	dependents, dependentRepos, dependentHidden, err := compose.loadReadableIssues(dependentIDs[issue.ID])
	if err != nil {
		return nil, err
	}
	maps.Copy(dependencyGraph.repositories, dependentRepos)

	deliveries, deliveryKeys, err := compose.loadDeliveries([]int64{issue.ID})
	if err != nil {
		return nil, err
	}
	projectIDs, err := service.source.issueProjectIDs(ctx, compose.requestRepo.ID, []int64{issue.ID})
	if err != nil {
		return nil, err
	}
	projects, err := service.source.projects(ctx, projectIDs[issue.ID])
	if err != nil {
		return nil, err
	}

	repository := projectRepository(ctx, compose.requestRepo)
	item := WorkItem{
		Ref: "issue/" + strconv.FormatInt(issue.Index, 10), URL: issueHTMLURL(ctx, issue), Title: issue.Title,
		Markdown: issue.Content, ContentVersion: int64(issue.ContentVersion), State: issueState(issue), Classification: "unplanned",
		ContextSummaries: []ContextSummary{}, ProjectMemberships: []Reference{}, PrerequisiteSummaries: []Reference{},
		DependentSummaries: []Reference{}, DeliverySummaries: []Delivery{},
	}

	canReadProjects := permission.CanRead(unit.TypeProjects)
	projectKeys := make([]int64, 0, len(projects))
	for projectID := range projects {
		projectKeys = append(projectKeys, projectID)
	}
	slices.Sort(projectKeys)
	contexts := make([]PlanContext, 0, len(projectKeys))
	contextKeys := make([]int64, 0, len(projectKeys))
	membershipKeys := make([]int64, 0, len(projectKeys))
	for _, projectID := range projectKeys {
		project := projects[projectID]
		if project.RepoID != compose.requestRepo.ID {
			continue
		}
		if canReadProjects {
			item.ProjectMemberships = append(item.ProjectMemberships, projectReference(repository, project))
			membershipKeys = append(membershipKeys, project.ID)
		}
		if !canReadProjects || !project.IsPlanningEnabled() || !project.HasValidPlanningState() {
			continue
		}
		context := compose.planContext(project, issue, dependencyGraph, deliveries[issue.ID])
		contexts = append(contexts, context)
		contextKeys = append(contextKeys, project.ID)
		item.ContextSummaries = append(item.ContextSummaries, summarizeContext(context))
		item.Classification = "planned"
	}

	item.PrerequisiteSummaries = compose.immediatePrerequisites(issue.ID, dependencyGraph)
	dependentByID := mapIssues(dependents)
	for _, dependentID := range dependentIDs[issue.ID] {
		dependent := dependentByID[dependentID]
		if dependent == nil || dependentHidden[dependentID] {
			item.DependentSummaries = append(item.DependentSummaries, undisclosedReference())
			continue
		}
		item.DependentSummaries = append(item.DependentSummaries, issueReference(ctx, dependent, dependentRepos[dependent.RepoID]))
	}
	item.DeliverySummaries = deliveries[issue.ID]
	contextSummaries := item.ContextSummaries
	projectMemberships := item.ProjectMemberships
	prerequisiteSummaries := item.PrerequisiteSummaries
	dependentSummaries := item.DependentSummaries
	deliverySummaries := item.DeliverySummaries
	item.ContextSummaries = bounded(item.ContextSummaries)
	item.ProjectMemberships = bounded(item.ProjectMemberships)
	item.PrerequisiteSummaries = bounded(item.PrerequisiteSummaries)
	item.DependentSummaries = bounded(item.DependentSummaries)
	item.DeliverySummaries = bounded(item.DeliverySummaries)

	inspection := &ItemInspection{Repository: repository, WorkItem: item}
	if request.SelectedProjectID > 0 {
		if !canReadProjects {
			return nil, readFailure(ReadUnavailable, ErrUnavailable)
		}
		found := slices.IndexFunc(contexts, func(context PlanContext) bool {
			return context.WorkPlan == "project/"+strconv.FormatInt(request.SelectedProjectID, 10)
		})
		if found < 0 {
			return nil, readFailure(ReadUnavailable, ErrUnavailable)
		}
		inspection.SelectedContext = &contexts[found]
	}

	cursorBase := pageCursor{
		Version: workCursorVersion, RepositoryID: compose.requestRepo.ID, TopKind: "item", TopID: issue.Index,
		ProjectID: request.SelectedProjectID, PageKind: request.PageKind, Order: issueNumberOrder,
	}
	position, err := decodeCursor(service.secret, request.Cursor, cursorBase)
	if err != nil {
		return nil, readFailure(ReadInvalidCursor, err)
	}
	var values []any
	var keys, related []int64
	switch request.PageKind {
	case "prerequisites":
		values, keys, related = referencesPage(prerequisiteSummaries, dependencyGraph.edges[issue.ID], dependencyGraph.nodes)
	case "dependents":
		values, keys, related = referencesPage(dependentSummaries, dependentIDs[issue.ID], mapIssues(dependents))
	case "memberships":
		values, keys = referencesAny(projectMemberships), membershipKeys
	case "contexts":
		values, keys = contextsAny(contextSummaries), contextKeys
	case "deliveries":
		values, keys = deliveriesAny(deliverySummaries), deliveryKeys[issue.ID]
		related = ordinalTieBreak(keys)
	}
	inspection.Page, err = service.page(cursorBase, position, limit, values, keys, related)
	if err != nil {
		return nil, err
	}
	return inspection, nil
}

func (service *ReadService) InspectPlan(ctx context.Context, doer *user_model.User, request PlanRequest) (*PlanInspection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if request.PageKind == "" {
		request.PageKind = "items"
	}
	if !slices.Contains([]string{"items", "edges", "ready_frontier", "excluded_members"}, request.PageKind) || request.ProjectID <= 0 {
		return nil, readFailure(ReadInvalidInput, ErrInvalidInput)
	}
	limit, err := pageLimit(request.Limit)
	if err != nil {
		return nil, err
	}
	compose, permission, err := service.begin(ctx, doer, request.Owner, request.Repository)
	if err != nil {
		return nil, err
	}
	if !permission.CanRead(unit.TypeIssues) || !permission.CanRead(unit.TypeProjects) {
		return nil, readFailure(ReadUnavailable, ErrUnavailable)
	}
	project, err := service.source.project(ctx, compose.requestRepo.ID, request.ProjectID)
	if err != nil || !project.IsPlanningEnabled() || !project.HasValidPlanningState() {
		if err == nil || project_model.IsErrProjectNotExist(err) {
			return nil, readFailure(ReadUnavailable, ErrUnavailable)
		}
		return nil, err
	}

	members, err := service.source.projectIssues(ctx, project.ID, setting.Work.MaxPlanItems+1)
	if err != nil {
		return nil, err
	}
	memberBound := len(members) > setting.Work.MaxPlanItems
	if memberBound {
		members = members[:setting.Work.MaxPlanItems]
	}
	issueIDs := make([]int64, 0, len(members))
	issueNumbers := make(map[int64]int64, len(members))
	excludedIDs := make([]int64, 0)
	for _, member := range members {
		issueNumbers[member.IssueID] = member.Index
		if member.IsPull {
			excludedIDs = append(excludedIDs, member.IssueID)
		} else {
			issueIDs = append(issueIDs, member.IssueID)
		}
	}
	memberIssues, err := service.source.issues(ctx, append(slices.Clone(issueIDs), excludedIDs...))
	if err != nil {
		return nil, err
	}
	byID := mapIssues(memberIssues)
	for _, issue := range memberIssues {
		issue.Repo = compose.requestRepo
	}
	roots := make(issues_model.IssueList, 0, len(issueIDs))
	for _, id := range issueIDs {
		if issue := byID[id]; issue != nil {
			roots = append(roots, issue)
		}
	}
	dependencyGraph, err := compose.loadGraph(roots)
	if err != nil {
		return nil, err
	}
	for _, id := range issueIDs {
		if byID[id] == nil {
			dependencyGraph.missing[id] = true
		}
	}
	deliveries, _, err := compose.loadDeliveries(issueIDs)
	if err != nil {
		return nil, err
	}

	contexts := make([]PlanContext, 0, len(roots))
	contextNumbers := make([]int64, 0, len(roots))
	for _, issue := range roots {
		contexts = append(contexts, compose.planContext(project, issue, dependencyGraph, deliveries[issue.ID]))
		contextNumbers = append(contextNumbers, issue.Index)
	}
	sort.SliceStable(contexts, func(i, j int) bool { return contextIssueNumber(contexts[i]) < contextIssueNumber(contexts[j]) })
	slices.Sort(contextNumbers)

	integrity := aggregateIntegrity(project, dependencyGraph, memberBound)
	itemSummaries := make([]ContextSummary, 0, min(len(contexts), setting.Work.MaxProjectionItems))
	ready := make([]ContextSummary, 0)
	readyNumbers := make([]int64, 0)
	for _, context := range contexts {
		summary := summarizeContext(context)
		if len(itemSummaries) < setting.Work.MaxProjectionItems {
			itemSummaries = append(itemSummaries, summary)
		}
		if context.DerivedState == "ready" && integrity.Status == "valid" {
			ready = append(ready, summary)
			readyNumbers = append(readyNumbers, contextIssueNumber(context))
		}
	}
	if project.PlanningState == project_model.PlanningStateActive && integrity.Status != "valid" {
		ready = []ContextSummary{}
		readyNumbers = []int64{}
	}

	edges, edgeKeys, edgeRelated := compose.planEdges(issueIDs, issueNumbers, dependencyGraph)
	excluded, excludedNumbers := compose.excludedMembers(ctx, excludedIDs, issueNumbers, byID)
	plan := WorkPlan{
		Ref: "project/" + strconv.FormatInt(project.ID, 10), URL: projectHTMLURL(ctx, compose.requestRepo, project.ID), Title: project.Title,
		Markdown: project.Description, PlanningState: string(project.PlanningState), ProjectState: projectState(project), Integrity: integrity,
		ItemSummaries: itemSummaries, EdgeSummaries: bounded(edges), ReadyFrontier: bounded(ready), ExcludedMembers: bounded(excluded), PlanToken: "",
	}
	inspection := &PlanInspection{Repository: projectRepository(ctx, compose.requestRepo), WorkPlan: plan}
	cursorBase := pageCursor{
		Version: workCursorVersion, RepositoryID: compose.requestRepo.ID, TopKind: "plan", TopID: project.ID,
		ProjectID: project.ID, PageKind: request.PageKind, Order: issueNumberOrder,
	}
	position, err := decodeCursor(service.secret, request.Cursor, cursorBase)
	if err != nil {
		return nil, readFailure(ReadInvalidCursor, err)
	}
	var values []any
	var keys, related []int64
	switch request.PageKind {
	case "items":
		values, keys = contextsAny(summaries(contexts)), contextNumbers
	case "edges":
		values, keys, related = edgesAny(edges), edgeKeys, edgeRelated
	case "ready_frontier":
		values, keys = contextsAny(ready), readyNumbers
	case "excluded_members":
		values, keys = referencesAny(excluded), excludedNumbers
	}
	inspection.Page, err = service.page(cursorBase, position, limit, values, keys, related)
	if err != nil {
		return nil, err
	}
	return inspection, nil
}

func (service *ReadService) begin(ctx context.Context, doer *user_model.User, owner, name string) (*composition, access_model.Permission, error) {
	if owner == "" || name == "" {
		return nil, access_model.Permission{}, readFailure(ReadInvalidInput, ErrInvalidInput)
	}
	repo, err := service.source.repository(ctx, owner, name)
	if err != nil {
		if repo_model.IsErrRepoNotExist(err) {
			return nil, access_model.Permission{}, readFailure(ReadUnavailable, ErrUnavailable)
		}
		return nil, access_model.Permission{}, err
	}
	permission, err := service.source.permission(ctx, repo, doer)
	if err != nil {
		return nil, access_model.Permission{}, err
	}
	return &composition{
		service: service, ctx: ctx, doer: doer, requestRepo: repo,
		permissions: map[int64]access_model.Permission{repo.ID: permission},
	}, permission, nil
}

func pageLimit(limit int) (int, error) {
	if limit == 0 {
		limit = setting.Work.DefaultPageItems
	}
	if limit < 1 || limit > setting.Work.MaxPageItems {
		return 0, readFailure(ReadLimitExceeded, ErrLimitExceeded)
	}
	return limit, nil
}

func (service *ReadService) page(base pageCursor, position cursorPosition, limit int, values []any, keys, related []int64) (Page, error) {
	if len(values) != len(keys) || (len(related) > 0 && len(values) != len(related)) {
		return Page{}, errors.New("inconsistent Work page keys")
	}
	start := 0
	if position.Issue > 0 {
		start = sort.Search(len(keys), func(i int) bool {
			if keys[i] != position.Issue {
				return keys[i] > position.Issue
			}
			return len(related) > 0 && related[i] > position.Related
		})
	}
	end := min(start+limit, len(values))
	page := Page{
		Kind: base.PageKind, Items: append([]any{}, values[start:end]...),
		SnapshotConsistency: "none", ReinspectBeforeAction: true,
	}
	if end < len(values) && end > start {
		base.LastIssue = keys[end-1]
		if len(related) > 0 {
			base.LastRelated = related[end-1]
		}
		cursor, err := encodeCursor(service.secret, base)
		if err != nil {
			return Page{}, err
		}
		page.NextCursor = cursor
	}
	return page, nil
}

func (compose *composition) loadGraph(roots issues_model.IssueList) (*graph, error) {
	result := &graph{
		nodes: make(map[int64]*issues_model.Issue, len(roots)), edges: make(map[int64][]int64),
		repositories: map[int64]*repo_model.Repository{compose.requestRepo.ID: compose.requestRepo},
		hidden:       make(map[int64]bool), missing: make(map[int64]bool),
	}
	frontier := make([]int64, 0, len(roots))
	for _, root := range roots {
		if root == nil {
			result.missing[0] = true
			continue
		}
		result.nodes[root.ID] = root
		frontier = append(frontier, root.ID)
	}
	for len(frontier) > 0 {
		if err := compose.ctx.Err(); err != nil {
			return nil, err
		}
		dependencies, err := compose.service.source.dependencyIDs(compose.ctx, frontier)
		if err != nil {
			return nil, err
		}
		next := make([]int64, 0)
		for _, issueID := range frontier {
			result.edges[issueID] = dependencies[issueID]
			for _, dependencyID := range dependencies[issueID] {
				if _, seen := result.nodes[dependencyID]; seen || result.missing[dependencyID] {
					continue
				}
				if len(result.nodes)+len(result.missing)+len(next)+1 > setting.Work.MaxGraphNodes {
					result.overBound = true
					continue
				}
				next = append(next, dependencyID)
			}
		}
		if result.overBound {
			break
		}
		if len(next) == 0 {
			break
		}
		loaded, repositories, hidden, err := compose.loadReadableIssues(next)
		if err != nil {
			return nil, err
		}
		maps.Copy(result.repositories, repositories)
		loadedIDs := container.Set[int64]{}
		for _, issue := range loaded {
			result.nodes[issue.ID] = issue
			if hidden[issue.ID] {
				result.hidden[issue.ID] = true
			}
			loadedIDs.Add(issue.ID)
		}
		frontier = frontier[:0]
		for _, id := range next {
			if loadedIDs.Contains(id) {
				frontier = append(frontier, id)
			} else {
				result.missing[id] = true
			}
		}
	}
	result.cyclic = graphCyclic(result.edges)
	return result, nil
}

func (compose *composition) loadReadableIssues(ids []int64) (issues_model.IssueList, map[int64]*repo_model.Repository, map[int64]bool, error) {
	loaded := issues_model.IssueList{}
	repositories := make(map[int64]*repo_model.Repository)
	hidden := make(map[int64]bool)
	if len(ids) == 0 {
		return loaded, repositories, hidden, nil
	}
	issues, err := compose.service.source.issues(compose.ctx, ids)
	if err != nil {
		return nil, nil, nil, err
	}
	repoIDs := container.Set[int64]{}
	for _, issue := range issues {
		if issue.RepoID != compose.requestRepo.ID {
			repoIDs.Add(issue.RepoID)
		}
	}
	repositories[compose.requestRepo.ID] = compose.requestRepo
	if len(repoIDs) > 0 {
		external, err := compose.service.source.repositories(compose.ctx, repoIDs.Values())
		if err != nil {
			return nil, nil, nil, err
		}
		maps.Copy(repositories, external)
	}
	for _, issue := range issues {
		if err := compose.ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		repo := repositories[issue.RepoID]
		if repo == nil {
			hidden[issue.ID] = true
			loaded = append(loaded, issue)
			continue
		}
		issue.Repo = repo
		permission, err := compose.permission(repo)
		if err != nil {
			return nil, nil, nil, err
		}
		hidden[issue.ID] = !permission.CanReadIssuesOrPulls(issue.IsPull)
		loaded = append(loaded, issue)
	}
	return loaded, repositories, hidden, nil
}

func (compose *composition) permission(repo *repo_model.Repository) (access_model.Permission, error) {
	if permission, ok := compose.permissions[repo.ID]; ok {
		return permission, nil
	}
	permission, err := compose.service.source.permission(compose.ctx, repo, compose.doer)
	if err != nil {
		return access_model.Permission{}, err
	}
	compose.permissions[repo.ID] = permission
	return permission, nil
}

func (compose *composition) loadDeliveries(issueIDs []int64) (map[int64][]Delivery, map[int64][]int64, error) {
	result := make(map[int64][]Delivery, len(issueIDs))
	keys := make(map[int64][]int64, len(issueIDs))
	references, err := compose.service.source.closingPulls(compose.ctx, issueIDs)
	if err != nil {
		return nil, nil, err
	}
	pullIssueIDs := container.Set[int64]{}
	for _, issueReferences := range references {
		for _, reference := range issueReferences {
			pullIssueIDs.Add(reference.PullIssueID)
		}
	}
	if len(pullIssueIDs) == 0 {
		return result, keys, nil
	}
	pulls, err := compose.service.source.pulls(compose.ctx, pullIssueIDs.Values())
	if err != nil {
		return nil, nil, err
	}
	pullIssues, err := compose.service.source.issues(compose.ctx, pullIssueIDs.Values())
	if err != nil {
		return nil, nil, err
	}
	pullIssuesByID := mapIssues(pullIssues)
	baseRepoIDs := container.Set[int64]{}
	for _, pr := range pulls {
		baseRepoIDs.Add(pr.BaseRepoID)
	}
	baseRepos, err := compose.service.source.repositories(compose.ctx, baseRepoIDs.Values())
	if err != nil {
		return nil, nil, err
	}
	readablePulls := make(issues_model.PullRequestList, 0, len(pulls))
	for _, pr := range pulls {
		if err := compose.ctx.Err(); err != nil {
			return nil, nil, err
		}
		pr.Issue = pullIssuesByID[pr.IssueID]
		pr.BaseRepo = baseRepos[pr.BaseRepoID]
		if pr.Issue == nil || pr.BaseRepo == nil || !pr.Issue.IsPull {
			continue
		}
		pr.Issue.Repo = pr.BaseRepo
		permission, err := compose.permission(pr.BaseRepo)
		if err != nil {
			return nil, nil, err
		}
		if permission.CanRead(unit.TypePullRequests) {
			readablePulls = append(readablePulls, pr)
		}
	}
	revisions, err := compose.service.source.revisions(compose.ctx, readablePulls)
	if err != nil {
		return nil, nil, err
	}
	pairs := make([]git_model.RepoSHA, 0, len(readablePulls))
	for _, pr := range readablePulls {
		pairs = append(pairs, git_model.RepoSHA{RepoID: pr.BaseRepoID, SHA: revisions[pr.ID].Revision})
	}
	statuses, err := compose.service.source.statuses(compose.ctx, pairs)
	if err != nil {
		return nil, nil, err
	}
	byPullIssue := make(map[int64]*issues_model.PullRequest, len(readablePulls))
	for _, pr := range readablePulls {
		byPullIssue[pr.IssueID] = pr
	}
	for targetID, targetReferences := range references {
		seen := container.Set[int64]{}
		for _, reference := range targetReferences {
			pr := byPullIssue[reference.PullIssueID]
			if pr == nil || pr.BaseRepoID != reference.PullRepoID || !seen.Add(pr.ID) {
				continue
			}
			revision := revisions[pr.ID].Revision
			pair := git_model.RepoSHA{RepoID: pr.BaseRepoID, SHA: revision}
			result[targetID] = append(result[targetID], Delivery{
				Repository: projectRepository(compose.ctx, pr.BaseRepo), Ref: "pull/" + strconv.FormatInt(pr.Issue.Index, 10),
				URL: issueHTMLURL(compose.ctx, pr.Issue), State: pullState(pr), Revision: revision,
				CheckState: deliveryCheckState(pr, statuses[pair]),
			})
			keys[targetID] = append(keys[targetID], pr.Issue.Index)
		}
		order := make([]int, len(result[targetID]))
		for i := range order {
			order[i] = i
		}
		sort.SliceStable(order, func(i, j int) bool { return keys[targetID][order[i]] < keys[targetID][order[j]] })
		result[targetID] = permute(result[targetID], order)
		keys[targetID] = permute(keys[targetID], order)
	}
	return result, keys, nil
}

func (compose *composition) planContext(project *project_model.Project, issue *issues_model.Issue, dependencyGraph *graph, deliveries []Delivery) PlanContext {
	integrity := contextIntegrity(project, issue.ID, dependencyGraph)
	state := "planned"
	if issue.IsClosed {
		state = "complete"
	} else if !compose.requestRepo.IsArchived && !project.IsClosed && project.PlanningState == project_model.PlanningStateActive {
		if integrity.Status != "valid" || compose.hasOpenPrerequisite(issue.ID, dependencyGraph) {
			state = "blocked"
		} else {
			state = "ready"
		}
	}
	planRef := "project/" + strconv.FormatInt(project.ID, 10)
	itemRef := "issue/" + strconv.FormatInt(issue.Index, 10)
	return PlanContext{
		Ref: planRef + "/" + itemRef, WorkPlan: planRef, WorkItem: itemRef, DerivedState: state,
		Integrity: integrity, PrerequisiteSummaries: bounded(compose.immediatePrerequisites(issue.ID, dependencyGraph)), DeliverySummaries: bounded(deliveries),
	}
}

func (compose *composition) hasOpenPrerequisite(rootID int64, dependencyGraph *graph) bool {
	seen := container.Set[int64]{}
	frontier := slices.Clone(dependencyGraph.edges[rootID])
	for len(frontier) > 0 {
		id := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		if !seen.Add(id) {
			continue
		}
		issue := dependencyGraph.nodes[id]
		if issue == nil || dependencyGraph.hidden[id] || dependencyGraph.missing[id] || issue.IsPull || !issue.IsClosed {
			return true
		}
		frontier = append(frontier, dependencyGraph.edges[id]...)
	}
	return false
}

func (compose *composition) immediatePrerequisites(issueID int64, dependencyGraph *graph) []Reference {
	ids := dependencyGraph.edges[issueID]
	references := make([]Reference, 0, len(ids))
	for _, id := range ids {
		issue := dependencyGraph.nodes[id]
		if issue == nil || dependencyGraph.hidden[id] || dependencyGraph.missing[id] {
			references = append(references, undisclosedReference())
			continue
		}
		references = append(references, issueReference(compose.ctx, issue, dependencyGraph.repositories[issue.RepoID]))
	}
	return references
}

func contextIntegrity(project *project_model.Project, rootID int64, dependencyGraph *graph) Integrity {
	status := "valid"
	concerns := make([]IntegrityConcern, 0, 2)
	if dependencyGraph.overBound {
		status = integrityFailureStatus(project)
		concerns = append(concerns, IntegrityConcern{Code: "graph_bound", Message: "The prerequisite graph exceeds the configured bound."})
	}
	if graphReachableCyclic(rootID, dependencyGraph.edges) {
		status = integrityFailureStatus(project)
		concerns = append(concerns, IntegrityConcern{Code: "dependency_graph", Message: "The prerequisite graph is invalid."})
	}
	seen := container.Set[int64]{}
	frontier := slices.Clone(dependencyGraph.edges[rootID])
	hidden := false
	for len(frontier) > 0 {
		id := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		if !seen.Add(id) {
			continue
		}
		if dependencyGraph.hidden[id] || dependencyGraph.missing[id] || dependencyGraph.nodes[id] == nil || dependencyGraph.nodes[id].IsPull {
			hidden = true
		}
		frontier = append(frontier, dependencyGraph.edges[id]...)
	}
	if hidden {
		status = integrityFailureStatus(project)
		concerns = append(concerns, IntegrityConcern{Code: "unresolved_prerequisite", Message: "At least one prerequisite cannot be resolved."})
	}
	return Integrity{Status: status, Concerns: concerns}
}

func aggregateIntegrity(project *project_model.Project, dependencyGraph *graph, memberBound bool) Integrity {
	status := "valid"
	concerns := make([]IntegrityConcern, 0, 3)
	if memberBound {
		status = integrityFailureStatus(project)
		concerns = append(concerns, IntegrityConcern{Code: "plan_bound", Message: "The plan exceeds the configured item bound."})
	}
	if dependencyGraph.overBound {
		status = integrityFailureStatus(project)
		concerns = append(concerns, IntegrityConcern{Code: "graph_bound", Message: "The prerequisite graph exceeds the configured bound."})
	}
	if dependencyGraph.cyclic {
		status = integrityFailureStatus(project)
		concerns = append(concerns, IntegrityConcern{Code: "dependency_graph", Message: "The prerequisite graph is invalid."})
	}
	if len(dependencyGraph.hidden) > 0 || len(dependencyGraph.missing) > 0 {
		status = integrityFailureStatus(project)
		concerns = append(concerns, IntegrityConcern{Code: "unresolved_prerequisite", Message: "At least one prerequisite cannot be resolved."})
	}
	return Integrity{Status: status, Concerns: concerns}
}

func integrityFailureStatus(project *project_model.Project) string {
	if project.PlanningState == project_model.PlanningStateDraft {
		return "incomplete"
	}
	return "concern"
}

func (compose *composition) planEdges(memberIDs []int64, issueNumbers map[int64]int64, dependencyGraph *graph) ([]Edge, []int64, []int64) {
	edges := make([]Edge, 0)
	keys := make([]int64, 0)
	related := make([]int64, 0)
	memberSet := container.SetOf(memberIDs...)
	for _, blockedID := range memberIDs {
		blocked := dependencyGraph.nodes[blockedID]
		if blocked == nil {
			continue
		}
		for _, prerequisiteID := range dependencyGraph.edges[blockedID] {
			prerequisite := dependencyGraph.nodes[prerequisiteID]
			prerequisiteReference := undisclosedReference()
			prerequisiteNumber := int64(0)
			if prerequisite != nil && !dependencyGraph.hidden[prerequisiteID] && !dependencyGraph.missing[prerequisiteID] {
				prerequisiteReference = issueReference(compose.ctx, prerequisite, dependencyGraph.repositories[prerequisite.RepoID])
				prerequisiteNumber = prerequisite.Index
			}
			edges = append(edges, Edge{
				Blocked:      issueReference(compose.ctx, blocked, dependencyGraph.repositories[blocked.RepoID]),
				Prerequisite: prerequisiteReference,
			})
			keys = append(keys, issueNumbers[blockedID])
			if memberSet.Contains(prerequisiteID) {
				related = append(related, issueNumbers[prerequisiteID])
			} else {
				related = append(related, prerequisiteNumber)
			}
		}
	}
	order := make([]int, len(edges))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		left, right := order[i], order[j]
		if keys[left] != keys[right] {
			return keys[left] < keys[right]
		}
		return related[left] < related[right]
	})
	edges = permute(edges, order)
	keys = permute(keys, order)
	related = ordinalTieBreak(keys)
	return edges, keys, related
}

func (compose *composition) excludedMembers(ctx context.Context, ids []int64, issueNumbers map[int64]int64, byID map[int64]*issues_model.Issue) ([]Reference, []int64) {
	references := make([]Reference, 0, len(ids))
	keys := make([]int64, 0, len(ids))
	permission := compose.permissions[compose.requestRepo.ID]
	for _, id := range ids {
		issue := byID[id]
		if issue == nil || !permission.CanRead(unit.TypePullRequests) {
			references = append(references, undisclosedReference())
		} else {
			reference := issueReference(ctx, issue, compose.requestRepo)
			reference.State = "excluded"
			references = append(references, reference)
		}
		keys = append(keys, issueNumbers[id])
	}
	return references, keys
}

func graphCyclic(edges map[int64][]int64) bool {
	const (
		unseen uint8 = iota
		visiting
		visited
	)
	states := make(map[int64]uint8, len(edges))
	var visit func(int64) bool
	visit = func(id int64) bool {
		switch states[id] {
		case visiting:
			return true
		case visited:
			return false
		}
		states[id] = visiting
		if slices.ContainsFunc(edges[id], visit) {
			return true
		}
		states[id] = visited
		return false
	}
	for id := range edges {
		if states[id] == unseen && visit(id) {
			return true
		}
	}
	return false
}

func graphReachableCyclic(rootID int64, edges map[int64][]int64) bool {
	const (
		unseen uint8 = iota
		visiting
		visited
	)
	states := make(map[int64]uint8)
	var visit func(int64) bool
	visit = func(id int64) bool {
		switch states[id] {
		case visiting:
			return true
		case visited:
			return false
		}
		states[id] = visiting
		if slices.ContainsFunc(edges[id], visit) {
			return true
		}
		states[id] = visited
		return false
	}
	for _, dependencyID := range edges[rootID] {
		if states[dependencyID] == unseen && visit(dependencyID) {
			return true
		}
	}
	return false
}

func readFailure(kind ReadFailureKind, cause error) error {
	return &ReadFailure{Kind: kind, Cause: cause}
}

func projectRepository(ctx context.Context, repo *repo_model.Repository) Repository {
	return Repository{Owner: repo.OwnerName, Name: repo.Name, URL: repo.HTMLURL(ctx)}
}

func issueHTMLURL(ctx context.Context, issue *issues_model.Issue) string {
	path := "/issues/"
	if issue.IsPull {
		path = "/pulls/"
	}
	return issue.Repo.HTMLURL(ctx) + path + strconv.FormatInt(issue.Index, 10)
}

func projectHTMLURL(ctx context.Context, repo *repo_model.Repository, projectID int64) string {
	return repo.HTMLURL(ctx) + "/projects/" + strconv.FormatInt(projectID, 10)
}

func issueReference(ctx context.Context, issue *issues_model.Issue, repo *repo_model.Repository) Reference {
	if issue == nil || repo == nil {
		return undisclosedReference()
	}
	issue.Repo = repo
	refKind := "issue/"
	if issue.IsPull {
		refKind = "pull/"
	}
	return Reference{
		Availability: "available", Repository: new(projectRepository(ctx, repo)),
		Ref: refKind + strconv.FormatInt(issue.Index, 10), URL: issueHTMLURL(ctx, issue), Label: issue.Title, State: issueState(issue),
	}
}

func projectReference(repository Repository, project *project_model.Project) Reference {
	state := string(project.PlanningState)
	if !project.HasValidPlanningState() {
		return undisclosedReference()
	}
	return Reference{
		Availability: "available", Repository: new(repository), Ref: "project/" + strconv.FormatInt(project.ID, 10),
		URL: repository.URL + "/projects/" + strconv.FormatInt(project.ID, 10), Label: project.Title, State: state,
	}
}

func undisclosedReference() Reference { return Reference{Availability: "undisclosed"} }

func issueState(issue *issues_model.Issue) string {
	if issue.IsClosed {
		return "closed"
	}
	return "open"
}

func projectState(project *project_model.Project) string {
	if project.IsClosed {
		return "closed"
	}
	return "open"
}

func pullState(pr *issues_model.PullRequest) string {
	if pr.HasMerged {
		return "merged"
	}
	return issueState(pr.Issue)
}

func deliveryCheckState(pr *issues_model.PullRequest, statuses []*git_model.CommitStatus) string {
	if len(statuses) == 0 {
		if pr.HeadRepoID != pr.BaseRepoID {
			return "unverified"
		}
		return "none"
	}
	states := make(commitstatus.CommitStatusStates, 0, len(statuses))
	for _, status := range statuses {
		states = append(states, status.State)
	}
	switch states.Combine() {
	case commitstatus.CommitStatusSuccess:
		return "success"
	case commitstatus.CommitStatusPending:
		return "pending"
	default:
		return "failure"
	}
}

func summarizeContext(context PlanContext) ContextSummary {
	return ContextSummary{
		Ref: context.Ref, WorkPlan: context.WorkPlan, DerivedState: context.DerivedState, IntegrityStatus: context.Integrity.Status,
	}
}

func summaries(contexts []PlanContext) []ContextSummary {
	result := make([]ContextSummary, 0, len(contexts))
	for _, context := range contexts {
		result = append(result, summarizeContext(context))
	}
	return result
}

func contextIssueNumber(context PlanContext) int64 {
	value := context.WorkItem[len("issue/"):]
	number, _ := strconv.ParseInt(value, 10, 64)
	return number
}

func mapIssues(issues issues_model.IssueList) map[int64]*issues_model.Issue {
	result := make(map[int64]*issues_model.Issue, len(issues))
	for _, issue := range issues {
		result[issue.ID] = issue
	}
	return result
}

func referencesPage(references []Reference, ids []int64, issues map[int64]*issues_model.Issue) ([]any, []int64, []int64) {
	values := referencesAny(references)
	keys := make([]int64, 0, len(ids))
	for _, id := range ids {
		if issue := issues[id]; issue != nil {
			keys = append(keys, issue.Index)
		} else {
			keys = append(keys, int64(^uint64(0)>>1))
		}
	}
	order := make([]int, len(values))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return keys[order[i]] < keys[order[j]] })
	values = permute(values, order)
	keys = permute(keys, order)
	return values, keys, ordinalTieBreak(keys)
}

func ordinalTieBreak(keys []int64) []int64 {
	related := make([]int64, len(keys))
	var previous, ordinal int64
	for i, key := range keys {
		if i == 0 || key != previous {
			ordinal = 0
		}
		ordinal++
		related[i] = ordinal
		previous = key
	}
	return related
}

func referencesAny(values []Reference) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

func contextsAny(values []ContextSummary) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

func deliveriesAny(values []Delivery) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

func edgesAny(values []Edge) []any {
	result := make([]any, len(values))
	for i := range values {
		result[i] = values[i]
	}
	return result
}

func bounded[T any](values []T) []T {
	return values[:min(len(values), setting.Work.MaxProjectionItems)]
}

func permute[T any](values []T, order []int) []T {
	result := make([]T, 0, len(order))
	for _, index := range order {
		result = append(result, values[index])
	}
	return result
}
