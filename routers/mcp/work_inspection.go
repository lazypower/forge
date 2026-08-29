// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"fmt"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workItemInspectToolName = "work_item.inspect"
	workPlanInspectToolName = "work_plan.inspect"

	workItemInspectionContent = "The structured work item inspection result is in structuredContent."
	workPlanInspectionContent = "The structured work plan inspection result is in structuredContent."
	defaultWorkPageItems      = 25
)

// WorkReadFailureKind identifies transport-neutral failures from a Work read adapter.
type WorkReadFailureKind string

const (
	WorkReadUnavailable   WorkReadFailureKind = "unavailable"
	WorkReadInvalidInput  WorkReadFailureKind = "invalid_input"
	WorkReadInvalidCursor WorkReadFailureKind = "invalid_cursor"
	WorkReadLimitExceeded WorkReadFailureKind = "limit_exceeded"
)

// WorkReadFailure carries a safe failure class from the future services/work binding.
type WorkReadFailure struct {
	Kind                   WorkReadFailureKind
	Retryable              bool
	RetryAfterMilliseconds int64
	Cause                  error
}

func (failure *WorkReadFailure) Error() string {
	return fmt.Sprintf("work read failed: %s", failure.Kind)
}

func (failure *WorkReadFailure) Unwrap() error { return failure.Cause }

// WorkReadService is the narrow fake-facing seam that Agent J will bind to services/work.
type WorkReadService interface {
	InspectWorkItem(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error)
	InspectWorkPlan(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error)
}

// WorkRepository identifies one repository in a Work tool request.
type WorkRepository struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
}

// WorkPageRequest selects one bounded result page.
type WorkPageRequest struct {
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// WorkItemInspectRequest is the service-facing item inspection request.
type WorkItemInspectRequest struct {
	Repository   WorkRepository   `json:"repository"`
	WorkItem     string           `json:"workItem"`
	SelectedPlan string           `json:"selectedPlan,omitempty"`
	PageKind     string           `json:"pageKind,omitempty"`
	Page         *WorkPageRequest `json:"page,omitempty"`
}

// WorkPlanInspectRequest is the service-facing plan inspection request.
type WorkPlanInspectRequest struct {
	Repository WorkRepository   `json:"repository"`
	WorkPlan   string           `json:"workPlan"`
	PageKind   string           `json:"pageKind,omitempty"`
	Page       *WorkPageRequest `json:"page,omitempty"`
}

// WorkRepositoryResult is the canonical repository locator returned by Forge.
type WorkRepositoryResult struct {
	Owner string `json:"owner"`
	Name  string `json:"name"`
	URL   string `json:"url"`
}

// WorkReferenceSummary is one permission-filtered Work reference.
type WorkReferenceSummary struct {
	Availability string                `json:"availability"`
	Repository   *WorkRepositoryResult `json:"repository,omitempty"`
	Ref          string                `json:"ref,omitempty"`
	URL          string                `json:"url,omitempty"`
	Label        string                `json:"label,omitempty"`
	State        string                `json:"state,omitempty"`
}

func (WorkReferenceSummary) workPageEntry() {}

// WorkIntegrityConcern describes one disclosed graph concern.
type WorkIntegrityConcern struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// WorkIntegrity reports the bounded integrity evaluation.
type WorkIntegrity struct {
	Status   string                 `json:"status"`
	Concerns []WorkIntegrityConcern `json:"concerns"`
}

// WorkDeliverySummary reports one qualifying delivery without embedding detail.
type WorkDeliverySummary struct {
	Repository WorkRepositoryResult `json:"repository"`
	Ref        string               `json:"ref"`
	URL        string               `json:"url"`
	State      string               `json:"state"`
	Revision   string               `json:"revision"`
	CheckState string               `json:"checkState"`
}

func (WorkDeliverySummary) workPageEntry() {}

// WorkContextSummary reports one plan-scoped derived state.
type WorkContextSummary struct {
	Ref             string `json:"ref"`
	WorkPlan        string `json:"workPlan"`
	DerivedState    string `json:"derivedState"`
	IntegrityStatus string `json:"integrityStatus"`
}

func (WorkContextSummary) workPageEntry() {}

// WorkEdgeSummary reports one directed dependency edge.
type WorkEdgeSummary struct {
	Blocked      WorkReferenceSummary `json:"blocked"`
	Prerequisite WorkReferenceSummary `json:"prerequisite"`
}

func (WorkEdgeSummary) workPageEntry() {}

// WorkItemResult is the exact MCP WorkItem projection shape.
type WorkItemResult struct {
	Ref                   string                 `json:"ref"`
	URL                   string                 `json:"url"`
	Title                 string                 `json:"title"`
	Markdown              string                 `json:"markdown"`
	ContentVersion        int64                  `json:"contentVersion"`
	State                 string                 `json:"state"`
	Classification        string                 `json:"classification"`
	ContextSummaries      []WorkContextSummary   `json:"contextSummaries"`
	ProjectMemberships    []WorkReferenceSummary `json:"projectMemberships"`
	PrerequisiteSummaries []WorkReferenceSummary `json:"prerequisiteSummaries"`
	DependentSummaries    []WorkReferenceSummary `json:"dependentSummaries"`
	DeliverySummaries     []WorkDeliverySummary  `json:"deliverySummaries"`
}

// WorkPlanContextResult is the exact MCP PlanContext projection shape.
type WorkPlanContextResult struct {
	Ref                   string                 `json:"ref"`
	WorkPlan              string                 `json:"workPlan"`
	WorkItem              string                 `json:"workItem"`
	DerivedState          string                 `json:"derivedState"`
	Integrity             WorkIntegrity          `json:"integrity"`
	PrerequisiteSummaries []WorkReferenceSummary `json:"prerequisiteSummaries"`
	DeliverySummaries     []WorkDeliverySummary  `json:"deliverySummaries"`
}

// WorkPlanResult is the exact MCP WorkPlan projection shape.
type WorkPlanResult struct {
	Ref             string                 `json:"ref"`
	URL             string                 `json:"url"`
	Title           string                 `json:"title"`
	Markdown        string                 `json:"markdown"`
	PlanningState   string                 `json:"planningState"`
	ProjectState    string                 `json:"projectState"`
	Integrity       WorkIntegrity          `json:"integrity"`
	ItemSummaries   []WorkContextSummary   `json:"itemSummaries"`
	EdgeSummaries   []WorkEdgeSummary      `json:"edgeSummaries"`
	ReadyFrontier   []WorkContextSummary   `json:"readyFrontier"`
	ExcludedMembers []WorkReferenceSummary `json:"excludedMembers"`
	PlanToken       string                 `json:"planToken"`
}

// WorkPageEntry is the closed union accepted in a Work result page.
type WorkPageEntry interface {
	workPageEntry()
}

// WorkPageResult is one non-snapshot result page.
type WorkPageResult struct {
	Kind                  string          `json:"kind"`
	Items                 []WorkPageEntry `json:"items"`
	NextCursor            string          `json:"nextCursor,omitempty"`
	SnapshotConsistency   string          `json:"snapshotConsistency"`
	ReinspectBeforeAction bool            `json:"reinspectBeforeAction"`
}

// WorkItemInspection is an available item result returned by a Work read adapter.
type WorkItemInspection struct {
	Repository      WorkRepositoryResult
	WorkItem        WorkItemResult
	SelectedContext *WorkPlanContextResult
	Page            WorkPageResult
}

// WorkPlanInspection is an available plan result returned by a Work read adapter.
type WorkPlanInspection struct {
	Repository WorkRepositoryResult
	WorkPlan   WorkPlanResult
	Page       WorkPageResult
}

type workProblem struct {
	Code                   string `json:"code"`
	Message                string `json:"message"`
	Retryable              bool   `json:"retryable"`
	RetryAfterMilliseconds int64  `json:"retryAfterMilliseconds,omitempty"`
}

type workReadOutput struct {
	SchemaVersion   string                 `json:"schemaVersion"`
	Status          string                 `json:"status"`
	Repository      *WorkRepositoryResult  `json:"repository,omitempty"`
	WorkItem        *WorkItemResult        `json:"workItem,omitempty"`
	WorkPlan        *WorkPlanResult        `json:"workPlan,omitempty"`
	SelectedContext *WorkPlanContextResult `json:"selectedContext,omitempty"`
	Page            *WorkPageResult        `json:"page,omitempty"`
	Problem         *workProblem           `json:"problem,omitempty"`
}

type workInspectionTools struct {
	executor       *toolExecutor
	reader         WorkReadService
	principal      authenticatedUserLookup
	maxOutputBytes int64
}

type unboundWorkReadService struct{}

func (unboundWorkReadService) InspectWorkItem(context.Context, *user_model.User, WorkItemInspectRequest) (*WorkItemInspection, error) {
	return nil, errors.New("services/work is not bound")
}

func (unboundWorkReadService) InspectWorkPlan(context.Context, *user_model.User, WorkPlanInspectRequest) (*WorkPlanInspection, error) {
	return nil, errors.New("services/work is not bound")
}

func newWorkInspectionTools(executor *toolExecutor, reader WorkReadService, principal authenticatedUserLookup, maxOutputBytes int64) *workInspectionTools {
	return &workInspectionTools{executor: executor, reader: reader, principal: principal, maxOutputBytes: maxOutputBytes}
}

func registerWorkInspectionTools(server *mcpsdk.Server, tools *workInspectionTools) {
	contracts := declaredWorkToolContracts()
	mcpsdk.AddTool(server, contracts[workItemInspectToolName], tools.inspectItem)
	mcpsdk.AddTool(server, contracts[workPlanInspectToolName], tools.inspectPlan)
}

func (tools *workInspectionTools) inspectItem(ctx context.Context, _ *mcpsdk.CallToolRequest, input WorkItemInspectRequest) (*mcpsdk.CallToolResult, workReadOutput, error) {
	input.applyDefaults()
	executionCtx, release, output := tools.begin(ctx, workItemInspectionContent)
	if output != nil {
		return workToolResult(workItemInspectionContent, true), *output, nil
	}
	defer release()
	doer, err := tools.principal(executionCtx)
	if err != nil {
		return workReadErrorResult(workItemInspectionContent, "mutation_failed", "work item inspection failed", false, 0)
	}
	inspection, err := tools.reader.InspectWorkItem(executionCtx, doer, input)
	if err != nil {
		return tools.mapError(ctx, executionCtx, workItemInspectionContent, err)
	}
	if inspection == nil {
		return workReadErrorResult(workItemInspectionContent, "mutation_failed", "work item inspection failed", false, 0)
	}
	outputValue := workReadOutput{
		SchemaVersion: "1", Status: "available", Repository: &inspection.Repository, WorkItem: &inspection.WorkItem,
		SelectedContext: inspection.SelectedContext, Page: &inspection.Page,
	}
	return tools.boundedResult(workItemInspectionContent, outputValue)
}

func (tools *workInspectionTools) inspectPlan(ctx context.Context, _ *mcpsdk.CallToolRequest, input WorkPlanInspectRequest) (*mcpsdk.CallToolResult, workReadOutput, error) {
	input.applyDefaults()
	executionCtx, release, output := tools.begin(ctx, workPlanInspectionContent)
	if output != nil {
		return workToolResult(workPlanInspectionContent, true), *output, nil
	}
	defer release()
	doer, err := tools.principal(executionCtx)
	if err != nil {
		return workReadErrorResult(workPlanInspectionContent, "mutation_failed", "work plan inspection failed", false, 0)
	}
	inspection, err := tools.reader.InspectWorkPlan(executionCtx, doer, input)
	if err != nil {
		return tools.mapError(ctx, executionCtx, workPlanInspectionContent, err)
	}
	if inspection == nil {
		return workReadErrorResult(workPlanInspectionContent, "mutation_failed", "work plan inspection failed", false, 0)
	}
	outputValue := workReadOutput{
		SchemaVersion: "1", Status: "available", Repository: &inspection.Repository, WorkPlan: &inspection.WorkPlan, Page: &inspection.Page,
	}
	return tools.boundedResult(workPlanInspectionContent, outputValue)
}

func (request *WorkItemInspectRequest) applyDefaults() {
	if request.PageKind == "" {
		request.PageKind = "contexts"
	}
	if request.Page != nil && request.Page.Limit == 0 {
		request.Page.Limit = defaultWorkPageItems
	}
}

func (request *WorkPlanInspectRequest) applyDefaults() {
	if request.PageKind == "" {
		request.PageKind = "items"
	}
	if request.Page != nil && request.Page.Limit == 0 {
		request.Page.Limit = defaultWorkPageItems
	}
}

func (tools *workInspectionTools) begin(ctx context.Context, content string) (context.Context, func(), *workReadOutput) {
	executionCtx, release, err := tools.executor.begin(ctx)
	if errors.Is(err, errToolCapacityUnavailable) {
		_, output, _ := workReadErrorResult(content, "busy", "MCP endpoint capacity is currently unavailable", true, 0)
		return nil, nil, &output
	}
	if err != nil {
		_, output, _ := workReadErrorResult(content, "cancelled", "work inspection was cancelled", true, 0)
		return nil, nil, &output
	}
	return executionCtx, release, nil
}

func (tools *workInspectionTools) mapError(parentCtx, executionCtx context.Context, content string, err error) (*mcpsdk.CallToolResult, workReadOutput, error) {
	switch executionFailureCode(parentCtx, executionCtx) {
	case "cancelled":
		return workReadErrorResult(content, "cancelled", "work inspection was cancelled", true, 0)
	case "timeout":
		return workReadErrorResult(content, "timeout", "work inspection timed out", true, 0)
	}
	var failure *WorkReadFailure
	if errors.As(err, &failure) {
		if failure.Kind == WorkReadUnavailable {
			return workToolResult(content, false), workReadOutput{SchemaVersion: "1", Status: "unavailable"}, nil
		}
		switch failure.Kind {
		case WorkReadInvalidInput:
			return workReadErrorResult(content, "invalid_input", "work inspection input is invalid", failure.Retryable, failure.RetryAfterMilliseconds)
		case WorkReadInvalidCursor:
			return workReadErrorResult(content, "invalid_cursor", "work inspection cursor is invalid or stale", failure.Retryable, failure.RetryAfterMilliseconds)
		case WorkReadLimitExceeded:
			return workReadErrorResult(content, "limit_exceeded", "work inspection exceeded a semantic limit", failure.Retryable, failure.RetryAfterMilliseconds)
		}
	}
	return workReadErrorResult(content, "mutation_failed", "work inspection failed", false, 0)
}

func (tools *workInspectionTools) boundedResult(content string, output workReadOutput) (*mcpsdk.CallToolResult, workReadOutput, error) {
	wire, err := json.Marshal(output)
	if err != nil || int64(len(wire)) > tools.maxOutputBytes {
		return workReadErrorResult(content, "limit_exceeded", "work inspection exceeded the output limit", false, 0)
	}
	return workToolResult(content, false), output, nil
}

func workReadErrorResult(content, code, message string, retryable bool, retryAfterMilliseconds int64) (*mcpsdk.CallToolResult, workReadOutput, error) {
	return workToolResult(content, true), workReadOutput{
		SchemaVersion: "1", Status: "error",
		Problem: &workProblem{Code: code, Message: message, Retryable: retryable, RetryAfterMilliseconds: retryAfterMilliseconds},
	}, nil
}

func workToolResult(content string, isError bool) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: content}}, IsError: isError}
}
