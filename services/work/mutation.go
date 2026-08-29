// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	mcpwork_model "gitea.dev/models/mcpwork"
	access_model "gitea.dev/models/perm/access"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	issue_service "gitea.dev/services/issue"
	mcpwork_service "gitea.dev/services/mcpwork"
)

// rollbackRejection is a deterministic domain rejection discovered after
// tentative writes. The mutation savepoint rolls back native facts, timeline
// rows, and provenance links while WP7 retains and finalizes its rejected
// receipt.
type rollbackRejection struct{ problemCode string }

func (rejection *rollbackRejection) Error() string {
	return "Work mutation rejected: " + rejection.problemCode
}

// MutationService owns ADR 0004 semantic Work writes. It calls WP2 for every
// dependency decision and WP3 for every complete plan/token decision.
type MutationService struct {
	reader *ReadService
}

// NewMutationService returns the production Work mutation authority.
func NewMutationService() *MutationService {
	return &MutationService{reader: NewReadService()}
}

// BeginPlan runs the shared human-facing operation without an MCP receipt.
func (service *MutationService) BeginPlan(ctx context.Context, doer *user_model.User, request BeginPlanRequest) (MutationCommit, error) {
	return runDirectMutation(ctx, func(txCtx context.Context) (MutationCommit, error) {
		return service.BeginPlanInWorkTx(txCtx, doer, request, mcpwork_service.Operation{})
	})
}

// ReviseItem runs the shared human-facing Issue operation without an MCP receipt.
func (service *MutationService) ReviseItem(ctx context.Context, doer *user_model.User, request ItemRevisionRequest) (MutationCommit, error) {
	return runDirectMutation(ctx, func(txCtx context.Context) (MutationCommit, error) {
		return service.ReviseItemInWorkTx(txCtx, doer, request, mcpwork_service.Operation{})
	})
}

// RevisePlan runs the shared human-facing plan operation without an MCP receipt.
func (service *MutationService) RevisePlan(ctx context.Context, doer *user_model.User, request PlanRevisionRequest) (MutationCommit, error) {
	return runDirectMutation(ctx, func(txCtx context.Context) (MutationCommit, error) {
		return service.RevisePlanInWorkTx(txCtx, doer, request, mcpwork_service.Operation{})
	})
}

func runDirectMutation(ctx context.Context, mutate func(context.Context) (MutationCommit, error)) (MutationCommit, error) {
	var commit MutationCommit
	err := db.WithWorkTx(ctx, func(txCtx context.Context) error {
		commit = MutationCommit{}
		var err error
		commit, err = mutate(txCtx)
		return err
	})
	if err != nil {
		return MutationCommit{}, err
	}
	if commit.Completion.Outcome == mcpwork_model.OutcomeApplied {
		commit.RunPostCommit(ctx)
	}
	return commit, nil
}

// BeginPlanInWorkTx creates or opts in one draft plan inside WP7's frozen
// mutation callback.
func (service *MutationService) BeginPlanInWorkTx(ctx context.Context, doer *user_model.User, request BeginPlanRequest, _ mcpwork_service.Operation) (MutationCommit, error) {
	if doer == nil || request.RepositoryID <= 0 || request.ExistingProjectID < 0 ||
		(request.ExistingProjectID == 0 && (!validTitle(request.Title) || !validMarkdown(request.Markdown))) ||
		(request.ExistingProjectID > 0 && (request.Title != "" || request.Markdown != "")) {
		return rejected("invalid_input"), nil
	}
	if err := repo_model.StabilizeWorkPlanning(ctx, request.RepositoryID); err != nil {
		return MutationCommit{}, err
	}
	repo, _, rejection, err := mutationRepository(ctx, doer, request.RepositoryID, true, false)
	if err != nil || rejection != "" {
		return rejected(rejection), err
	}

	var project *project_model.Project
	if request.ExistingProjectID == 0 {
		project = &project_model.Project{
			RepoID: repo.ID, Type: project_model.TypeRepository, CreatorID: doer.ID,
			Title: strings.TrimSpace(request.Title), Description: request.Markdown, PlanningState: project_model.PlanningStateDraft,
		}
		if err := project_model.NewProject(ctx, project); err != nil {
			return MutationCommit{}, err
		}
	} else {
		project, err = project_model.GetProjectForRepoByID(ctx, repo.ID, request.ExistingProjectID)
		if err != nil {
			return rejected("unavailable"), nil
		}
		if project.PlanningState != project_model.PlanningStateDisabled || !project.HasValidPlanningState() {
			return rejected("invalid_plan"), nil
		}
		updated, err := db.GetEngine(ctx).Table(new(project_model.Project)).ID(project.ID).
			Where("planning_state = ?", project_model.PlanningStateDisabled).
			Update(map[string]any{"planning_state": project_model.PlanningStateDraft})
		if err != nil {
			return MutationCommit{}, err
		}
		if updated != 1 {
			return rejected("conflict"), nil
		}
		project.PlanningState = project_model.PlanningStateDraft
	}

	return applied(true, []mcpwork_service.ArtifactReference{projectArtifact(project)}, nil, nil, nil), nil
}

// ReviseItemInWorkTx conditionally revises one Issue and applies desired state
// inside WP7's transaction callback.
func (service *MutationService) ReviseItemInWorkTx(ctx context.Context, doer *user_model.User, request ItemRevisionRequest, _ mcpwork_service.Operation) (MutationCommit, error) {
	return runMutationSavepoint(ctx, func() (MutationCommit, error) {
		return service.reviseItemInWorkTx(ctx, doer, request)
	})
}

func (service *MutationService) reviseItemInWorkTx(ctx context.Context, doer *user_model.User, request ItemRevisionRequest) (MutationCommit, error) {
	if doer == nil || request.RepositoryID <= 0 || request.IssueNumber <= 0 ||
		(request.Title == nil && request.Markdown == nil && request.DesiredState == nil) ||
		(request.Title != nil && (!validExpectedTitle(request.Title.Expected) || !validTitle(request.Title.Desired))) ||
		(request.Markdown != nil && (request.Markdown.ExpectedContentVersion < 0 || !validMarkdown(request.Markdown.Desired))) ||
		(request.DesiredState != nil && *request.DesiredState != ItemStateOpen && *request.DesiredState != ItemStateClosed) {
		return rejected("invalid_input"), nil
	}
	repo, permission, rejection, err := mutationRepository(ctx, doer, request.RepositoryID, false, true)
	if err != nil || rejection != "" {
		return rejected(rejection), err
	}
	issue, err := issues_model.GetIssueByIndex(ctx, repo.ID, request.IssueNumber)
	if err != nil || issue.IsPull {
		return rejected("unavailable"), nil
	}
	issue.Repo = repo
	canEditContent := issue.PosterID == doer.ID || permission.CanWrite(unit.TypeIssues)
	if (request.Title != nil || request.Markdown != nil) && !canEditContent {
		return rejected("not_permitted"), nil
	}
	if request.DesiredState != nil && !permission.CanWrite(unit.TypeIssues) {
		return rejected("not_permitted"), nil
	}

	changed := false
	effects := make([]issue_service.PostCommitEffect, 0, 3)
	events := make([]mcpwork_service.EventReference, 0, 2)
	if request.Title != nil || request.Markdown != nil {
		revision := issues_model.ConditionalIssueRevision{}
		if request.Title != nil {
			revision.ExpectedTitle, revision.DesiredTitle = &request.Title.Expected, &request.Title.Desired
		}
		if request.Markdown != nil {
			revision.ExpectedContentVersion, revision.DesiredContent = &request.Markdown.ExpectedContentVersion, &request.Markdown.Desired
		}
		result, contentEffects, err := issue_service.ReviseWorkIssueInTx(ctx, issue, doer, revision)
		if errors.Is(err, issues_model.ErrIssueAlreadyChanged) {
			return rejected("conflict"), nil
		}
		if err != nil {
			return MutationCommit{}, err
		}
		issue = result.Issue
		changed = result.TitleChanged || result.BodyChanged
		effects = append(effects, contentEffects...)
		if result.TitleComment != nil {
			events = append(events, issueEvent(issue, result.TitleComment))
		}
	}
	if request.DesiredState != nil {
		comment, stateChanged, effect, err := issue_service.SetWorkIssueStateInTx(ctx, issue, doer, *request.DesiredState == ItemStateClosed)
		if issues_model.IsErrDependenciesLeft(err) {
			return rejectOrRollback(changed, "invalid_dependency")
		}
		if err != nil {
			return MutationCommit{}, err
		}
		changed = changed || stateChanged
		if effect != nil {
			effects = append(effects, *effect)
		}
		if comment != nil {
			events = append(events, issueEvent(issue, comment))
		}
	}
	return applied(changed, []mcpwork_service.ArtifactReference{issueArtifact(issue, "")}, events, effects, nil), nil
}

// RevisePlanInWorkTx applies the closed bounded plan revision in WP7's frozen
// transaction callback. Every validation failure returns one durable rejection;
// infrastructure and cancellation errors return no final completion.
func (service *MutationService) RevisePlanInWorkTx(ctx context.Context, doer *user_model.User, request PlanRevisionRequest, _ mcpwork_service.Operation) (MutationCommit, error) {
	return runMutationSavepoint(ctx, func() (MutationCommit, error) {
		return service.revisePlanInWorkTx(ctx, doer, request)
	})
}

func (service *MutationService) revisePlanInWorkTx(ctx context.Context, doer *user_model.User, request PlanRevisionRequest) (MutationCommit, error) {
	validated, problem := validatePlanRevision(request)
	if problem != "" || doer == nil {
		return rejected("invalid_input"), nil
	}
	repo, permission, rejection, err := mutationRepository(ctx, doer, request.RepositoryID, true, true)
	if err != nil || rejection != "" {
		return rejected(rejection), err
	}
	project, err := project_model.GetProjectForRepoByID(ctx, repo.ID, request.ProjectID)
	if err != nil || !project.IsPlanningEnabled() || !project.HasValidPlanningState() {
		return rejected("unavailable"), nil
	}
	if err := project_model.StabilizePlanningStates(ctx, []int64{project.ID}); err != nil {
		return MutationCommit{}, err
	}
	if !permission.CanWrite(unit.TypeProjects) || !permission.CanWrite(unit.TypeIssues) {
		return rejected("not_permitted"), nil
	}

	if validated.lifecycle != nil || validated.deleteDraft {
		inspection, err := service.reader.InspectPlan(ctx, doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: project.ID})
		if err != nil {
			return MutationCommit{}, err
		}
		if validatePlanTokenAgainstCurrent(service.reader.secret, request.ExpectedPlanToken, inspection.WorkPlan.PlanToken) != nil {
			return rejected("conflict"), nil
		}
		if validated.lifecycle != nil && validated.lifecycle.ExpectedState != planningStateName(project.PlanningState) {
			return rejected("conflict"), nil
		}
	}
	if validated.deleteDraft {
		if project.PlanningState != project_model.PlanningStateDraft {
			return rejected("invalid_plan"), nil
		}
		if err := project_model.DeleteProjectByID(ctx, project.ID); err != nil {
			return MutationCommit{}, err
		}
		return applied(true, nil, nil, nil, nil), nil
	}

	resolved := make(map[string]*issues_model.Issue, len(validated.creates))
	artifacts := []mcpwork_service.ArtifactReference{projectArtifact(project)}
	events := make([]mcpwork_service.EventReference, 0)
	effects := make([]issue_service.PostCommitEffect, 0)
	created := make(map[string]string, len(validated.creates))
	changed := false
	for _, change := range validated.creates {
		issue := &issues_model.Issue{
			RepoID: repo.ID, Repo: repo, PosterID: doer.ID, Poster: doer,
			Title: strings.TrimSpace(change.Title), Content: change.Markdown,
		}
		effect, err := issue_service.NewWorkIssueInTx(ctx, repo, issue)
		if err != nil {
			return MutationCommit{}, err
		}
		membershipChanged, comment, err := issues_model.EnsureIssueProjectInWorkTx(ctx, issue, doer, project, true)
		if err != nil {
			return MutationCommit{}, err
		}
		resolved[change.LocalReference] = issue
		created[change.LocalReference] = fmt.Sprintf("issue/%d", issue.Index)
		artifacts = append(artifacts, issueArtifact(issue, change.LocalReference))
		effects = append(effects, effect)
		changed = true
		if membershipChanged && comment != nil {
			events = append(events, issueEvent(issue, comment))
		}
	}
	resolve := func(selector ItemSelector) (*issues_model.Issue, string) {
		if selector.LocalReference != "" {
			issue := resolved[selector.LocalReference]
			if issue == nil {
				return nil, "invalid_input"
			}
			return issue, ""
		}
		issue, err := issues_model.GetIssueByIndex(ctx, repo.ID, selector.IssueNumber)
		if err != nil || issue.IsPull {
			return nil, "unavailable"
		}
		issue.Repo = repo
		return issue, ""
	}

	for _, change := range validated.memberships {
		issue, problem := resolve(change.WorkItem)
		if problem != "" {
			return rejectOrRollback(changed, problem)
		}
		if project.PlanningState == project_model.PlanningStateActive && change.Presence == PresenceAbsent {
			return rejectOrRollback(changed, "invalid_plan")
		}
		membershipChanged, comment, err := issues_model.EnsureIssueProjectInWorkTx(ctx, issue, doer, project, change.Presence == PresencePresent)
		if err != nil {
			return MutationCommit{}, err
		}
		changed = changed || membershipChanged
		artifacts = appendArtifact(artifacts, issueArtifact(issue, ""))
		if membershipChanged && comment != nil {
			events = append(events, issueEvent(issue, comment))
		}
	}
	for _, change := range validated.dependencies {
		blocked, problem := resolve(change.Blocked)
		if problem != "" {
			return rejectOrRollback(changed, problem)
		}
		prerequisite, problem := resolve(change.Prerequisite)
		if problem != "" {
			return rejectOrRollback(changed, problem)
		}
		presence := issue_service.DependencyPresent
		if change.Presence == PresenceAbsent {
			presence = issue_service.DependencyAbsent
		}
		dependencyChanged, comments, err := issue_service.EnsureDependencyWithEventsInWorkTx(ctx, doer, blocked, prerequisite, presence, issue_service.WorkDependencyScope)
		if errors.Is(err, issue_service.ErrInvalidDependency) || errors.Is(err, issue_service.ErrDependencyUnavailable) {
			return rejectOrRollback(changed, "invalid_dependency")
		}
		if errors.Is(err, issue_service.ErrDependencyNotPermitted) {
			return rejectOrRollback(changed, "not_permitted")
		}
		if err != nil {
			return MutationCommit{}, err
		}
		changed = changed || dependencyChanged
		artifacts = appendArtifact(artifacts, issueArtifact(blocked, ""))
		artifacts = appendArtifact(artifacts, issueArtifact(prerequisite, ""))
		if dependencyChanged {
			for _, comment := range comments {
				owner := blocked
				if comment.IssueID == prerequisite.ID {
					owner = prerequisite
				}
				events = append(events, issueEvent(owner, comment))
			}
		}
	}

	desiredActive := project.PlanningState == project_model.PlanningStateActive
	if validated.lifecycle != nil {
		desiredActive = validated.lifecycle.DesiredState == PlanningStateActive
	}
	if desiredActive {
		if problem, err := service.validateCompletePlan(ctx, doer, repo, project); err != nil {
			return MutationCommit{}, err
		} else if problem != "" {
			return rejectOrRollback(changed, problem)
		}
	}
	if validated.lifecycle != nil && validated.lifecycle.DesiredState != validated.lifecycle.ExpectedState {
		desired := project_model.PlanningStateDraft
		if validated.lifecycle.DesiredState == PlanningStateActive {
			desired = project_model.PlanningStateActive
		}
		updated, err := db.GetEngine(ctx).Table(new(project_model.Project)).ID(project.ID).
			Where("planning_state = ?", project.PlanningState).Update(map[string]any{"planning_state": desired})
		if err != nil {
			return MutationCommit{}, err
		}
		if updated != 1 {
			return rejectOrRollback(changed, "conflict")
		}
		project.PlanningState = desired
		changed = true
	}
	return applied(changed, artifacts, events, effects, created), nil
}

func (service *MutationService) validateCompletePlan(ctx context.Context, doer *user_model.User, repo *repo_model.Repository, project *project_model.Project) (string, error) {
	inspection, err := service.reader.InspectPlan(ctx, doer, PlanRequest{Owner: repo.OwnerName, Repository: repo.Name, ProjectID: project.ID})
	if err != nil {
		return "", err
	}
	if inspection.WorkPlan.Integrity.Status != "valid" {
		return "invalid_plan", nil
	}
	members, err := project_model.GetWorkProjectIssues(ctx, project.ID, setting.Work.MaxPlanItems+1)
	if err != nil {
		return "", err
	}
	if len(members) > setting.Work.MaxPlanItems {
		return "limit_exceeded", nil
	}
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		if !member.IsPull {
			ids = append(ids, member.IssueID)
		}
	}
	issues, err := issues_model.GetIssuesByIDs(ctx, ids)
	if err != nil {
		return "", err
	}
	if len(issues) != len(ids) {
		return "invalid_plan", nil
	}
	if err := issue_service.ValidateDependencyDAGInWorkTx(ctx, doer, issues, issue_service.WorkDependencyScope); err != nil {
		if errors.Is(err, issue_service.ErrInvalidDependency) {
			return "invalid_dependency", nil
		}
		return "", err
	}
	return "", nil
}

type validatedPlanRevision struct {
	creates      []PlanChange
	memberships  []PlanChange
	dependencies []PlanChange
	lifecycle    *PlanChange
	deleteDraft  bool
}

func validatePlanRevision(request PlanRevisionRequest) (validatedPlanRevision, string) {
	result := validatedPlanRevision{}
	if request.RepositoryID <= 0 || request.ProjectID <= 0 || len(request.Changes) == 0 || len(request.Changes) > setting.Work.MaxPlanRevisionChanges {
		return result, "invalid_input"
	}
	locals := make(map[string]struct{})
	targets := make(map[string]struct{})
	for _, change := range request.Changes {
		var target string
		switch change.Kind {
		case PlanChangeCreateMember:
			if !validLocalReference(change.LocalReference) || !validTitle(change.Title) || !validMarkdown(change.Markdown) {
				return result, "invalid_input"
			}
			if _, duplicate := locals[change.LocalReference]; duplicate {
				return result, "invalid_input"
			}
			locals[change.LocalReference] = struct{}{}
			target = "member:local/" + change.LocalReference
			result.creates = append(result.creates, change)
		case PlanChangeEnsureMember:
			if !validSelector(change.WorkItem, locals) || !validPresence(change.Presence) {
				return result, "invalid_input"
			}
			target = "member:" + selectorKey(change.WorkItem)
			result.memberships = append(result.memberships, change)
		case PlanChangeEnsureDependency:
			if !validSelector(change.Blocked, locals) || !validSelector(change.Prerequisite, locals) || !validPresence(change.Presence) {
				return result, "invalid_input"
			}
			target = "edge:" + selectorKey(change.Blocked) + ":" + selectorKey(change.Prerequisite)
			result.dependencies = append(result.dependencies, change)
		case PlanChangeSetPlanningState:
			if result.lifecycle != nil || !validPlanningState(change.ExpectedState) || !validPlanningState(change.DesiredState) {
				return result, "invalid_input"
			}
			lifecycleChange := change
			result.lifecycle = &lifecycleChange
			target = "lifecycle"
		case PlanChangeDeleteDraft:
			result.deleteDraft = true
			target = "delete"
		default:
			return result, "invalid_input"
		}
		if _, duplicate := targets[target]; duplicate {
			return result, "invalid_input"
		}
		targets[target] = struct{}{}
	}
	if len(result.creates) > setting.Work.MaxPlanRevisionCreatedItems || (result.deleteDraft && len(request.Changes) != 1) ||
		((result.lifecycle != nil || result.deleteDraft) && request.ExpectedPlanToken == "") {
		return validatedPlanRevision{}, "invalid_input"
	}
	return result, ""
}

func mutationRepository(ctx context.Context, doer *user_model.User, repositoryID int64, projects, issues bool) (*repo_model.Repository, access_model.Permission, string, error) {
	repo, err := repo_model.GetRepositoryByID(ctx, repositoryID)
	if err != nil {
		return nil, access_model.Permission{}, "unavailable", nil
	}
	if repo.IsArchived {
		return nil, access_model.Permission{}, "not_permitted", nil
	}
	permission, err := access_model.GetDoerRepoPermission(ctx, repo, doer)
	if err != nil {
		return nil, access_model.Permission{}, "", err
	}
	if projects && (!repo.UnitEnabled(ctx, unit.TypeProjects) || !permission.CanWrite(unit.TypeProjects)) {
		return nil, permission, "not_permitted", nil
	}
	if issues && (!repo.UnitEnabled(ctx, unit.TypeIssues) || !permission.CanRead(unit.TypeIssues)) {
		return nil, permission, "not_permitted", nil
	}
	return repo, permission, "", nil
}

func rejected(problem string) MutationCommit {
	if problem == "" {
		problem = "mutation_failed"
	}
	return MutationCommit{Completion: mcpwork_service.Completion{Outcome: mcpwork_model.OutcomeRejected, ProblemCode: problem}}
}

func rejectOrRollback(changed bool, problem string) (MutationCommit, error) {
	if changed {
		return MutationCommit{}, &rollbackRejection{problemCode: problem}
	}
	return rejected(problem), nil
}

func applied(changed bool, artifacts []mcpwork_service.ArtifactReference, events []mcpwork_service.EventReference, effects []issue_service.PostCommitEffect, created map[string]string) MutationCommit {
	outcome := mcpwork_model.OutcomeUnchanged
	if changed {
		outcome = mcpwork_model.OutcomeApplied
	}
	return MutationCommit{
		Completion:        mcpwork_service.Completion{Outcome: outcome, Artifacts: artifacts, Events: events},
		CreatedReferences: created, Effects: effects,
	}
}

func projectArtifact(project *project_model.Project) mcpwork_service.ArtifactReference {
	return mcpwork_service.ArtifactReference{RepositoryID: project.RepoID, Kind: mcpwork_model.ArtifactKindProject, ArtifactID: project.ID}
}

func issueArtifact(issue *issues_model.Issue, local string) mcpwork_service.ArtifactReference {
	return mcpwork_service.ArtifactReference{
		RepositoryID: issue.RepoID, Kind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID, ArtifactNumber: issue.Index, LocalReference: local,
	}
}

func issueEvent(issue *issues_model.Issue, comment *issues_model.Comment) mcpwork_service.EventReference {
	return mcpwork_service.EventReference{
		RepositoryID: issue.RepoID, Kind: mcpwork_model.EventKindIssueComment, EventID: comment.ID,
		ArtifactKind: mcpwork_model.ArtifactKindIssue, ArtifactID: issue.ID,
	}
}

func appendArtifact(artifacts []mcpwork_service.ArtifactReference, artifact mcpwork_service.ArtifactReference) []mcpwork_service.ArtifactReference {
	for _, existing := range artifacts {
		if existing.RepositoryID == artifact.RepositoryID && existing.Kind == artifact.Kind && existing.ArtifactID == artifact.ArtifactID {
			return artifacts
		}
	}
	return append(artifacts, artifact)
}

func validTitle(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= setting.Work.MaxTitleCharacters
}

func validExpectedTitle(value string) bool {
	return utf8.ValidString(value) && utf8.RuneCountInString(value) <= setting.Work.MaxTitleCharacters
}

func validMarkdown(value string) bool {
	return utf8.ValidString(value) && int64(len(value)) <= setting.Work.MaxMarkdownBytes
}

func validPresence(value Presence) bool { return value == PresencePresent || value == PresenceAbsent }

func validPlanningState(value PlanningState) bool {
	return value == PlanningStateDraft || value == PlanningStateActive
}

func validLocalReference(value string) bool {
	if len(value) == 0 || len(value) > 64 || !isASCIIAlpha(rune(value[0])) {
		return false
	}
	for index, char := range value {
		if index == 0 && !isASCIIAlpha(char) || index > 0 && !isASCIIAlpha(char) && (char < '0' || char > '9') && char != '_' && char != '-' {
			return false
		}
	}
	return true
}

func isASCIIAlpha(char rune) bool { return char >= 'A' && char <= 'Z' || char >= 'a' && char <= 'z' }

func planningStateName(state project_model.PlanningState) PlanningState {
	if state == project_model.PlanningStateActive {
		return PlanningStateActive
	}
	return PlanningStateDraft
}

func validSelector(selector ItemSelector, locals map[string]struct{}) bool {
	if selector.LocalReference != "" {
		_, known := locals[selector.LocalReference]
		return known && selector.IssueNumber == 0
	}
	return selector.IssueNumber > 0
}

func selectorKey(selector ItemSelector) string {
	if selector.LocalReference != "" {
		return "local/" + selector.LocalReference
	}
	return fmt.Sprintf("issue/%d", selector.IssueNumber)
}
