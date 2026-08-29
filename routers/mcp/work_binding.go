// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"strconv"
	"strings"

	user_model "gitea.dev/models/user"
	work_service "gitea.dev/services/work"
)

var errWorkProjectionMissing = errors.New("services/work returned no projection")

// boundWorkReadService keeps the MCP projection a mechanical adapter over the
// authoritative Work reader.
type boundWorkReadService struct {
	reader work_service.Reader
}

func newBoundWorkReadService(reader work_service.Reader) WorkReadService {
	return &boundWorkReadService{reader: reader}
}

func (service *boundWorkReadService) InspectWorkItem(ctx context.Context, doer *user_model.User, request WorkItemInspectRequest) (*WorkItemInspection, error) {
	issueNumber, ok := parseWorkReference(request.WorkItem, "issue/")
	if !ok {
		return nil, &WorkReadFailure{Kind: WorkReadInvalidInput}
	}
	selectedProjectID := int64(0)
	if request.SelectedPlan != "" {
		selectedProjectID, ok = parseWorkReference(request.SelectedPlan, "project/")
		if !ok {
			return nil, &WorkReadFailure{Kind: WorkReadInvalidInput}
		}
	}
	page := WorkPageRequest{Limit: defaultWorkPageItems}
	if request.Page != nil {
		page = *request.Page
	}
	inspection, err := service.reader.InspectItem(ctx, doer, work_service.ItemRequest{
		Owner: request.Repository.Owner, Repository: request.Repository.Name, IssueNumber: issueNumber,
		SelectedProjectID: selectedProjectID, PageKind: request.PageKind, Limit: page.Limit, Cursor: page.Cursor,
	})
	if err != nil {
		return nil, mapWorkReadFailure(err)
	}
	return mapWorkItemInspection(inspection)
}

func (service *boundWorkReadService) InspectWorkPlan(ctx context.Context, doer *user_model.User, request WorkPlanInspectRequest) (*WorkPlanInspection, error) {
	projectID, ok := parseWorkReference(request.WorkPlan, "project/")
	if !ok {
		return nil, &WorkReadFailure{Kind: WorkReadInvalidInput}
	}
	page := WorkPageRequest{Limit: defaultWorkPageItems}
	if request.Page != nil {
		page = *request.Page
	}
	inspection, err := service.reader.InspectPlan(ctx, doer, work_service.PlanRequest{
		Owner: request.Repository.Owner, Repository: request.Repository.Name, ProjectID: projectID,
		PageKind: request.PageKind, Limit: page.Limit, Cursor: page.Cursor,
	})
	if err != nil {
		return nil, mapWorkReadFailure(err)
	}
	return mapWorkPlanInspection(inspection)
}

func parseWorkReference(value, prefix string) (int64, bool) {
	numberText, ok := strings.CutPrefix(value, prefix)
	if !ok || numberText == "" || numberText[0] == '0' {
		return 0, false
	}
	number, err := strconv.ParseInt(numberText, 10, 64)
	return number, err == nil && number > 0 && value == prefix+strconv.FormatInt(number, 10)
}

func mapWorkReadFailure(err error) error {
	var failure *work_service.ReadFailure
	if !errors.As(err, &failure) {
		return err
	}
	kind := WorkReadFailureKind(failure.Kind)
	switch failure.Kind {
	case work_service.ReadUnavailable, work_service.ReadInvalidInput, work_service.ReadInvalidCursor,
		work_service.ReadInvalidDependency, work_service.ReadLimitExceeded:
		return &WorkReadFailure{Kind: kind, Cause: failure}
	default:
		return err
	}
}

func mapWorkItemInspection(source *work_service.ItemInspection) (*WorkItemInspection, error) {
	if source == nil {
		return nil, errWorkProjectionMissing
	}
	page, err := mapWorkPage(source.Page)
	if err != nil {
		return nil, err
	}
	return &WorkItemInspection{
		Repository:      mapWorkRepository(source.Repository),
		WorkItem:        mapWorkItem(source.WorkItem),
		SelectedContext: mapWorkContextPointer(source.SelectedContext),
		Page:            page,
	}, nil
}

func mapWorkPlanInspection(source *work_service.PlanInspection) (*WorkPlanInspection, error) {
	if source == nil {
		return nil, errWorkProjectionMissing
	}
	page, err := mapWorkPage(source.Page)
	if err != nil {
		return nil, err
	}
	return &WorkPlanInspection{
		Repository: mapWorkRepository(source.Repository),
		WorkPlan:   mapWorkPlan(source.WorkPlan),
		Page:       page,
	}, nil
}

func mapWorkRepository(source work_service.Repository) WorkRepositoryResult {
	return WorkRepositoryResult{Owner: source.Owner, Name: source.Name, URL: source.URL}
}

func mapWorkReference(source work_service.Reference) WorkReferenceSummary {
	result := WorkReferenceSummary{Availability: source.Availability, Ref: source.Ref, URL: source.URL, Label: source.Label, State: source.State}
	if source.Repository != nil {
		repository := mapWorkRepository(*source.Repository)
		result.Repository = &repository
	}
	return result
}

func mapWorkReferences(source []work_service.Reference) []WorkReferenceSummary {
	result := make([]WorkReferenceSummary, len(source))
	for index := range source {
		result[index] = mapWorkReference(source[index])
	}
	return result
}

func mapWorkDelivery(source work_service.Delivery) WorkDeliverySummary {
	return WorkDeliverySummary{
		Repository: mapWorkRepository(source.Repository), Ref: source.Ref, URL: source.URL,
		State: source.State, Revision: source.Revision, CheckState: source.CheckState,
	}
}

func mapWorkDeliveries(source []work_service.Delivery) []WorkDeliverySummary {
	result := make([]WorkDeliverySummary, len(source))
	for index := range source {
		result[index] = mapWorkDelivery(source[index])
	}
	return result
}

func mapWorkContextSummary(source work_service.ContextSummary) WorkContextSummary {
	return WorkContextSummary{
		Ref: source.Ref, WorkPlan: source.WorkPlan, DerivedState: source.DerivedState, IntegrityStatus: source.IntegrityStatus,
	}
}

func mapWorkContextSummaries(source []work_service.ContextSummary) []WorkContextSummary {
	result := make([]WorkContextSummary, len(source))
	for index := range source {
		result[index] = mapWorkContextSummary(source[index])
	}
	return result
}

func mapWorkIntegrity(source work_service.Integrity) WorkIntegrity {
	concerns := make([]WorkIntegrityConcern, len(source.Concerns))
	for index := range source.Concerns {
		concerns[index] = WorkIntegrityConcern{Code: source.Concerns[index].Code, Message: source.Concerns[index].Message}
	}
	return WorkIntegrity{Status: source.Status, Concerns: concerns}
}

func mapWorkItem(source work_service.WorkItem) WorkItemResult {
	return WorkItemResult{
		Ref: source.Ref, URL: source.URL, Title: source.Title, Markdown: source.Markdown,
		ContentVersion: source.ContentVersion, State: source.State, Classification: source.Classification,
		ContextSummaries: mapWorkContextSummaries(source.ContextSummaries), ProjectMemberships: mapWorkReferences(source.ProjectMemberships),
		PrerequisiteSummaries: mapWorkReferences(source.PrerequisiteSummaries), DependentSummaries: mapWorkReferences(source.DependentSummaries),
		DeliverySummaries: mapWorkDeliveries(source.DeliverySummaries),
	}
}

func mapWorkContext(source work_service.PlanContext) WorkPlanContextResult {
	return WorkPlanContextResult{
		Ref: source.Ref, WorkPlan: source.WorkPlan, WorkItem: source.WorkItem, DerivedState: source.DerivedState,
		Integrity: mapWorkIntegrity(source.Integrity), PrerequisiteSummaries: mapWorkReferences(source.PrerequisiteSummaries),
		DeliverySummaries: mapWorkDeliveries(source.DeliverySummaries),
	}
}

func mapWorkContextPointer(source *work_service.PlanContext) *WorkPlanContextResult {
	if source == nil {
		return nil
	}
	result := mapWorkContext(*source)
	return &result
}

func mapWorkEdges(source []work_service.Edge) []WorkEdgeSummary {
	result := make([]WorkEdgeSummary, len(source))
	for index := range source {
		result[index] = WorkEdgeSummary{Blocked: mapWorkReference(source[index].Blocked), Prerequisite: mapWorkReference(source[index].Prerequisite)}
	}
	return result
}

func mapWorkPlan(source work_service.WorkPlan) WorkPlanResult {
	return WorkPlanResult{
		Ref: source.Ref, URL: source.URL, Title: source.Title, Markdown: source.Markdown,
		PlanningState: source.PlanningState, ProjectState: source.ProjectState, Integrity: mapWorkIntegrity(source.Integrity),
		ItemSummaries: mapWorkContextSummaries(source.ItemSummaries), EdgeSummaries: mapWorkEdges(source.EdgeSummaries),
		ReadyFrontier: mapWorkContextSummaries(source.ReadyFrontier), ExcludedMembers: mapWorkReferences(source.ExcludedMembers),
		PlanToken: source.PlanToken,
	}
}

func mapWorkPage(source work_service.Page) (WorkPageResult, error) {
	items := make([]WorkPageEntry, 0, len(source.Items))
	for _, item := range source.Items {
		switch value := item.(type) {
		case work_service.Reference:
			items = append(items, mapWorkReference(value))
		case work_service.ContextSummary:
			items = append(items, mapWorkContextSummary(value))
		case work_service.Delivery:
			items = append(items, mapWorkDelivery(value))
		case work_service.Edge:
			items = append(items, WorkEdgeSummary{Blocked: mapWorkReference(value.Blocked), Prerequisite: mapWorkReference(value.Prerequisite)})
		default:
			return WorkPageResult{}, errors.New("services/work returned an unsupported page entry")
		}
	}
	return WorkPageResult{
		Kind: source.Kind, Items: items, NextCursor: source.NextCursor,
		SnapshotConsistency: source.SnapshotConsistency, ReinspectBeforeAction: source.ReinspectBeforeAction,
	}, nil
}
