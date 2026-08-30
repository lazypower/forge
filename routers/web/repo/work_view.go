// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"errors"
	"slices"
	"strconv"
	"time"

	issues_model "gitea.dev/models/issues"
	project_model "gitea.dev/models/project"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/log"
	"gitea.dev/modules/optional"
	"gitea.dev/modules/references"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	issue_service "gitea.dev/services/issue"
	mcpwork_service "gitea.dev/services/mcpwork"
	project_service "gitea.dev/services/projects"
	work_service "gitea.dev/services/work"
)

type humanWorkContext struct {
	Ref             string
	ProjectRef      string
	ProjectID       int64
	ProjectLabel    string
	ProjectURL      string
	DerivedState    string
	IntegrityStatus string
	Selected        bool
}

type humanWorkItem struct {
	Item            work_service.WorkItem
	SelectedContext *work_service.PlanContext
	Contexts        []humanWorkContext
}

type humanWorkPlan struct {
	Plan          work_service.WorkPlan
	PlanningState string
	Provenance    []humanWorkProvenance
}

type humanPullWorkItem struct {
	Item     work_service.WorkItem
	Contexts []humanWorkContext
}

type humanWorkProvenance struct {
	PrincipalName               string
	CommittedAt                 time.Time
	RegisteredClientLabel       string
	RegisteredInstallationLabel string
	ClientAttribution           mcpwork_service.ClientAttribution
}

type humanWorkProvenancePresenter interface {
	issue_service.MCPWorkTimelinePresenter
	project_service.MCPWorkProjectPresenter
}

// Presentation methods are read-only and do not use the mutation secret held
// by the receipt service.
var humanWorkProvenanceReceipts humanWorkProvenancePresenter = new(mcpwork_service.Service)

func prepareProjectWorkView(ctx *context.Context, project *project_model.Project) {
	if !project.IsPlanningEnabled() || !project.HasValidPlanningState() {
		return
	}
	inspection, err := work_service.NewReadService().InspectPlan(ctx, ctx.Doer, work_service.PlanRequest{
		Owner: ctx.Repo.Repository.OwnerName, Repository: ctx.Repo.Repository.Name, ProjectID: project.ID,
	})
	if err != nil {
		var failure *work_service.ReadFailure
		if !errors.As(err, &failure) || failure.Kind != work_service.ReadUnavailable {
			log.Error("Compose repository Work plan %d: %v", project.ID, err)
		}
		return
	}
	planningState := humanPlanningState(inspection.WorkPlan.PlanningState)
	if planningState == "" {
		log.Error("Compose repository Work plan %d with invalid planning state", project.ID)
		return
	}
	ctx.Data["HumanWorkPlan"] = &humanWorkPlan{
		Plan: inspection.WorkPlan, PlanningState: planningState,
		Provenance: presentProjectWorkProvenance(ctx, project),
	}
}

func prepareIssueWorkView(ctx *context.Context, issue *issues_model.Issue) {
	if issue.IsPull {
		preparePullWorkView(ctx, issue)
		return
	}

	request := work_service.ItemRequest{
		Owner: ctx.Repo.Repository.OwnerName, Repository: ctx.Repo.Repository.Name,
		IssueNumber: issue.Index, SelectedProjectID: ctx.FormInt64("work_plan"),
	}
	reader := work_service.NewReadService()
	inspection, err := reader.InspectItem(ctx, ctx.Doer, request)
	if err != nil && request.SelectedProjectID > 0 {
		var failure *work_service.ReadFailure
		if errors.As(err, &failure) && failure.Kind == work_service.ReadUnavailable {
			request.SelectedProjectID = 0
			inspection, err = reader.InspectItem(ctx, ctx.Doer, request)
		}
	}
	if err != nil {
		var failure *work_service.ReadFailure
		if !errors.As(err, &failure) || failure.Kind != work_service.ReadUnavailable {
			log.Error("Compose Work item %d: %v", issue.ID, err)
		}
		return
	}
	humanItem := makeHumanWorkItem(inspection)
	if len(humanItem.Contexts) > 0 || humanItem.SelectedContext != nil || len(humanItem.Item.DeliverySummaries) > 0 {
		ctx.Data["HumanWorkItem"] = humanItem
	}
	prepareIssueWorkProvenance(ctx, issue)
}

func preparePullWorkView(ctx *context.Context, issue *issues_model.Issue) {
	if issue.PullRequest == nil {
		return
	}
	referencesToResolve, err := issue.PullRequest.ResolveCrossReferences(ctx)
	if err != nil {
		log.Error("Resolve pull request Work references %d: %v", issue.ID, err)
		return
	}
	targetIDs := make([]int64, 0, len(referencesToResolve))
	for _, reference := range referencesToResolve {
		if reference.RefAction == references.XRefActionCloses {
			targetIDs = append(targetIDs, reference.IssueID)
		}
	}
	if len(targetIDs) == 0 {
		return
	}
	targets, err := issues_model.Issues(ctx, &issues_model.IssuesOptions{
		IssueIDs: targetIDs, IsPull: optional.Some(false), Doer: ctx.Doer, AllPublic: ctx.Doer == nil,
	})
	if err != nil {
		log.Error("Load pull request Work targets %d: %v", issue.ID, err)
		return
	}
	if _, err := targets.LoadRepositories(ctx); err != nil {
		log.Error("Load pull request Work target repositories %d: %v", issue.ID, err)
		return
	}

	reader := work_service.NewReadService()
	workItems := make([]humanPullWorkItem, 0, min(len(targets), setting.Work.MaxProjectionItems))
	for _, target := range targets {
		if len(workItems) >= setting.Work.MaxProjectionItems {
			break
		}
		if target.IsPull || target.Repo == nil {
			continue
		}
		inspection, err := reader.InspectItem(ctx, ctx.Doer, work_service.ItemRequest{
			Owner: target.Repo.OwnerName, Repository: target.Repo.Name, IssueNumber: target.Index,
		})
		if err != nil {
			var failure *work_service.ReadFailure
			if !errors.As(err, &failure) || failure.Kind != work_service.ReadUnavailable {
				log.Error("Compose pull request Work target %d: %v", target.ID, err)
			}
			continue
		}
		if !slices.ContainsFunc(inspection.WorkItem.DeliverySummaries, func(delivery work_service.Delivery) bool {
			return delivery.Repository.Owner == ctx.Repo.Repository.OwnerName &&
				delivery.Repository.Name == ctx.Repo.Repository.Name &&
				delivery.Ref == "pull/"+strconv.FormatInt(issue.Index, 10)
		}) {
			continue
		}
		workItems = append(workItems, humanPullWorkItem{
			Item: inspection.WorkItem, Contexts: humanWorkContexts(inspection.WorkItem, nil),
		})
	}
	if len(workItems) > 0 {
		ctx.Data["HumanPullWorkItems"] = workItems
	}
}

func humanPlanningState(value string) string {
	// Accept the semantic contract and the compact native encoding, then fail closed.
	switch value {
	case "draft", string(rune(project_model.PlanningStateDraft)):
		return "draft"
	case "active", string(rune(project_model.PlanningStateActive)):
		return "active"
	default:
		return ""
	}
}

func makeHumanWorkItem(inspection *work_service.ItemInspection) *humanWorkItem {
	return &humanWorkItem{
		Item: inspection.WorkItem, SelectedContext: inspection.SelectedContext,
		Contexts: humanWorkContexts(inspection.WorkItem, inspection.SelectedContext),
	}
}

func humanWorkContexts(item work_service.WorkItem, selected *work_service.PlanContext) []humanWorkContext {
	memberships := make(map[string]work_service.Reference, len(item.ProjectMemberships))
	for _, membership := range item.ProjectMemberships {
		if membership.Availability == "available" {
			memberships[membership.Ref] = membership
		}
	}
	contexts := make([]humanWorkContext, 0, len(item.ContextSummaries))
	for _, summary := range item.ContextSummaries {
		membership := memberships[summary.WorkPlan]
		context := humanWorkContext{
			Ref: summary.Ref, ProjectRef: summary.WorkPlan, ProjectID: workProjectIDFromRef(summary.WorkPlan), ProjectLabel: membership.Label,
			ProjectURL: membership.URL, DerivedState: summary.DerivedState,
			IntegrityStatus: summary.IntegrityStatus,
		}
		if selected != nil && selected.Ref == summary.Ref {
			context.Selected = true
		}
		contexts = append(contexts, context)
	}
	return contexts
}

func presentProjectWorkProvenance(ctx *context.Context, project *project_model.Project) []humanWorkProvenance {
	presentations, err := project_service.PresentMCPWorkProject(ctx, humanWorkProvenanceReceipts, ctx.Doer, project)
	if err != nil {
		log.Error("Present repository Work plan provenance %d: %v", project.ID, err)
		return nil
	}
	return humanWorkProvenanceForPresentations(ctx, presentations)
}

func prepareIssueWorkProvenance(ctx *context.Context, issue *issues_model.Issue) {
	byComment := make(map[int64][]humanWorkProvenance)
	for _, comment := range issue.Comments {
		if !isWorkMutationEvent(comment.Type) {
			continue
		}
		presentations, err := issue_service.PresentMCPWorkTimelineEvent(ctx, humanWorkProvenanceReceipts, ctx.Doer, issue, comment)
		if err != nil {
			log.Error("Present Work timeline provenance %d: %v", comment.ID, err)
			continue
		}
		if provenance := humanWorkProvenanceForPresentations(ctx, presentations); len(provenance) > 0 {
			byComment[comment.ID] = provenance
		}
	}
	if len(byComment) > 0 {
		ctx.Data["HumanWorkProvenanceByComment"] = byComment
	}
}

func humanWorkProvenanceForPresentations(ctx *context.Context, presentations []*mcpwork_service.Presentation) []humanWorkProvenance {
	principalIDs := make([]int64, 0, len(presentations))
	for _, presentation := range presentations {
		if presentation.Available && presentation.Origin == "mcp" {
			principalIDs = append(principalIDs, presentation.PrincipalID)
		}
	}
	principals, err := user_model.GetUsersMapByIDs(ctx, principalIDs)
	if err != nil {
		log.Error("Load MCP Work principals: %v", err)
		return nil
	}
	provenance := make([]humanWorkProvenance, 0, len(presentations))
	for _, presentation := range presentations {
		principal := principals[presentation.PrincipalID]
		if !presentation.Available || presentation.Origin != "mcp" || principal == nil {
			continue
		}
		provenance = append(provenance, humanWorkProvenance{
			PrincipalName: principal.Name, CommittedAt: presentation.CommittedAt,
			RegisteredClientLabel: presentation.RegisteredClientLabel, RegisteredInstallationLabel: presentation.RegisteredInstallationLabel,
			ClientAttribution: presentation.ClientAttribution,
		})
	}
	return provenance
}

func isWorkMutationEvent(commentType issues_model.CommentType) bool {
	return slices.Contains([]issues_model.CommentType{
		issues_model.CommentTypeReopen,
		issues_model.CommentTypeClose,
		issues_model.CommentTypeChangeTitle,
		issues_model.CommentTypeAddDependency,
		issues_model.CommentTypeRemoveDependency,
		issues_model.CommentTypeProject,
	}, commentType)
}

func workProjectIDFromRef(ref string) int64 {
	const prefix = "project/"
	if len(ref) <= len(prefix) || ref[:len(prefix)] != prefix {
		return 0
	}
	id, err := strconv.ParseInt(ref[len(prefix):], 10, 64)
	if err != nil {
		return 0
	}
	return id
}
