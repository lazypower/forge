// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
	"strings"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	mcpwork_model "gitea.dev/models/mcpwork"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	mcpwork_service "gitea.dev/services/mcpwork"
	"gitea.dev/services/oauth2_provider"
	work_service "gitea.dev/services/work"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	workPlanBeginContent  = "The structured work plan begin result is in structuredContent."
	workItemReviseContent = "The structured work item revision result is in structuredContent."
	workPlanReviseContent = "The structured work plan revision result is in structuredContent."
)

type workPlanBegin struct {
	Kind     string `json:"kind"`
	Title    string `json:"title,omitempty"`
	Markdown string `json:"markdown,omitempty"`
	WorkPlan string `json:"workPlan,omitempty"`
}

type workPlanBeginRequest struct {
	Repository     WorkRepository `json:"repository"`
	IdempotencyKey string         `json:"idempotencyKey"`
	Begin          workPlanBegin  `json:"begin"`
}

type workConditionalTitle struct {
	Expected string `json:"expected"`
	Desired  string `json:"desired"`
}

type workConditionalMarkdown struct {
	ExpectedContentVersion int    `json:"expectedContentVersion"`
	Desired                string `json:"desired"`
}

type workDesiredState struct {
	Desired string `json:"desired"`
}

type workItemReviseRequest struct {
	Repository     WorkRepository           `json:"repository"`
	WorkItem       string                   `json:"workItem"`
	IdempotencyKey string                   `json:"idempotencyKey"`
	Title          *workConditionalTitle    `json:"title,omitempty"`
	Markdown       *workConditionalMarkdown `json:"markdown,omitempty"`
	State          *workDesiredState        `json:"state,omitempty"`
}

type workPlanChange struct {
	Kind           string `json:"kind"`
	WorkItem       string `json:"workItem,omitempty"`
	Presence       string `json:"presence,omitempty"`
	LocalReference string `json:"localReference,omitempty"`
	Title          string `json:"title,omitempty"`
	Markdown       string `json:"markdown,omitempty"`
	Blocked        string `json:"blocked,omitempty"`
	Prerequisite   string `json:"prerequisite,omitempty"`
	Expected       string `json:"expected,omitempty"`
	Desired        string `json:"desired,omitempty"`
}

type workPlanReviseRequest struct {
	Repository        WorkRepository   `json:"repository"`
	WorkPlan          string           `json:"workPlan"`
	IdempotencyKey    string           `json:"idempotencyKey"`
	ExpectedPlanToken string           `json:"expectedPlanToken,omitempty"`
	Changes           []workPlanChange `json:"changes"`
}

type workMutationAuthority struct {
	PrincipalID        int64
	OAuthApplicationID int64
	OAuthGrantID       int64
	CredentialJTI      string
	Audience           string
	Scope              string
}

type workMutationExecution struct {
	Receipt               *mcpwork_service.Result
	Artifacts             []mcpwork_service.ArtifactReference
	CreatedReferences     map[string]string
	CurrentUnavailable    bool
	ProjectionUnavailable bool
}

type workMutationService interface {
	BeginPlan(context.Context, *user_model.User, workMutationAuthority, workPlanBeginRequest) (*workMutationExecution, error)
	ReviseItem(context.Context, *user_model.User, workMutationAuthority, workItemReviseRequest) (*workMutationExecution, error)
	RevisePlan(context.Context, *user_model.User, workMutationAuthority, workPlanReviseRequest) (*workMutationExecution, error)
}

type boundWorkMutationService struct {
	mutations *work_service.MutationService
	receipts  *mcpwork_service.Service
}

func newBoundWorkMutationService(mutations *work_service.MutationService, receipts *mcpwork_service.Service) workMutationService {
	return &boundWorkMutationService{mutations: mutations, receipts: receipts}
}

func newProductionWorkMutationService() workMutationService {
	secret := sha256.Sum256(append([]byte("mcp-work-receipt\x00"), []byte(setting.SecretKey)...))
	receipts, err := mcpwork_service.NewService(secret[:])
	if err != nil {
		panic(err)
	}
	return newBoundWorkMutationService(work_service.NewMutationService(), receipts)
}

func (service *boundWorkMutationService) BeginPlan(ctx context.Context, doer *user_model.User, authority workMutationAuthority, request workPlanBeginRequest) (*workMutationExecution, error) {
	existingProjectID, _ := parseWorkReference(request.Begin.WorkPlan, "project/")
	domainRequest := work_service.BeginPlanRequest{
		ExistingProjectID: existingProjectID, Title: request.Begin.Title, Markdown: request.Begin.Markdown,
	}
	return service.execute(ctx, doer, authority, workPlanBeginToolName, request.IdempotencyKey, request, func(txCtx context.Context, operation mcpwork_service.Operation) (work_service.MutationCommit, error) {
		return service.mutations.BeginPlanForRepositoryInWorkTx(txCtx, doer, work_service.RepositoryLocator{
			Owner: request.Repository.Owner, Name: request.Repository.Name,
		}, domainRequest, operation)
	})
}

func (service *boundWorkMutationService) ReviseItem(ctx context.Context, doer *user_model.User, authority workMutationAuthority, request workItemReviseRequest) (*workMutationExecution, error) {
	issueNumber, _ := parseWorkReference(request.WorkItem, "issue/")
	domainRequest := work_service.ItemRevisionRequest{IssueNumber: issueNumber}
	if request.Title != nil {
		domainRequest.Title = &work_service.ConditionalText{Expected: request.Title.Expected, Desired: request.Title.Desired}
	}
	if request.Markdown != nil {
		domainRequest.Markdown = &work_service.ConditionalMarkdown{
			ExpectedContentVersion: request.Markdown.ExpectedContentVersion, Desired: request.Markdown.Desired,
		}
	}
	if request.State != nil {
		state := work_service.ItemState(request.State.Desired)
		domainRequest.DesiredState = &state
	}
	return service.execute(ctx, doer, authority, workItemReviseToolName, request.IdempotencyKey, request, func(txCtx context.Context, operation mcpwork_service.Operation) (work_service.MutationCommit, error) {
		return service.mutations.ReviseItemForRepositoryInWorkTx(txCtx, doer, work_service.RepositoryLocator{
			Owner: request.Repository.Owner, Name: request.Repository.Name,
		}, domainRequest, operation)
	})
}

func (service *boundWorkMutationService) RevisePlan(ctx context.Context, doer *user_model.User, authority workMutationAuthority, request workPlanReviseRequest) (*workMutationExecution, error) {
	projectID, _ := parseWorkReference(request.WorkPlan, "project/")
	changes := make([]work_service.PlanChange, len(request.Changes))
	for index, change := range request.Changes {
		changes[index] = work_service.PlanChange{
			Kind: work_service.PlanChangeKind(change.Kind), WorkItem: parseWorkItemSelector(change.WorkItem),
			Presence: work_service.Presence(change.Presence), LocalReference: change.LocalReference,
			Title: change.Title, Markdown: change.Markdown, Blocked: parseWorkItemSelector(change.Blocked),
			Prerequisite: parseWorkItemSelector(change.Prerequisite), ExpectedState: work_service.PlanningState(change.Expected),
			DesiredState: work_service.PlanningState(change.Desired),
		}
	}
	domainRequest := work_service.PlanRevisionRequest{ProjectID: projectID, ExpectedPlanToken: request.ExpectedPlanToken, Changes: changes}
	return service.execute(ctx, doer, authority, workPlanReviseToolName, request.IdempotencyKey, request, func(txCtx context.Context, operation mcpwork_service.Operation) (work_service.MutationCommit, error) {
		return service.mutations.RevisePlanForRepositoryInWorkTx(txCtx, doer, work_service.RepositoryLocator{
			Owner: request.Repository.Owner, Name: request.Repository.Name,
		}, domainRequest, operation)
	})
}

func parseWorkItemSelector(value string) work_service.ItemSelector {
	if number, ok := parseWorkReference(value, "issue/"); ok {
		return work_service.ItemSelector{IssueNumber: number}
	}
	if local, ok := strings.CutPrefix(value, "local/"); ok {
		return work_service.ItemSelector{LocalReference: local}
	}
	return work_service.ItemSelector{}
}

func (service *boundWorkMutationService) execute(ctx context.Context, doer *user_model.User, authority workMutationAuthority, tool, key string, input any, mutate work_service.ReceiptMutation) (*workMutationExecution, error) {
	expandedInput, err := json.Marshal(input)
	if err != nil {
		return nil, mcpwork_service.ErrInvalidRequest
	}
	receiptRequest := mcpwork_service.Request{
		Tool: tool, SchemaVersion: "1", IdempotencyKey: key, ExpandedInput: expandedInput,
		Authority: mcpwork_service.Authority{
			PrincipalID: authority.PrincipalID, OAuthApplicationID: authority.OAuthApplicationID,
			OAuthGrantID: authority.OAuthGrantID, CredentialJTI: authority.CredentialJTI,
			Audience: authority.Audience, Scope: authority.Scope,
		},
	}
	receipt, _, err := work_service.ApplyReceiptMutation(ctx, service.receipts, receiptRequest, mutate)
	if err != nil {
		return nil, err
	}
	execution := &workMutationExecution{Receipt: receipt}
	presentation, err := service.receipts.Present(ctx, receipt.OperationUUID, mcpwork_service.CurrentReferencePermission(doer))
	if err != nil {
		execution.ProjectionUnavailable = true
		return execution, nil
	}
	if presentation.Available {
		execution.Artifacts = presentation.Artifacts
		execution.CreatedReferences = make(map[string]string)
		for _, artifact := range presentation.Artifacts {
			if artifact.LocalReference != "" && artifact.ArtifactNumber > 0 {
				execution.CreatedReferences[artifact.LocalReference] = "issue/" + strconv.FormatInt(artifact.ArtifactNumber, 10)
			}
		}
	} else {
		execution.CurrentUnavailable = true
	}
	return execution, nil
}

type workOperationResult struct {
	ID          string    `json:"id"`
	Replayed    bool      `json:"replayed"`
	Changed     bool      `json:"changed"`
	CommittedAt time.Time `json:"committedAt"`
}

type workMutationOutput struct {
	SchemaVersion       string                 `json:"schemaVersion"`
	Status              string                 `json:"status"`
	Operation           *workOperationResult   `json:"operation,omitempty"`
	CreatedReferences   map[string]string      `json:"createdReferences,omitempty"`
	WorkItem            *WorkItemResult        `json:"workItem,omitempty"`
	WorkPlan            *WorkPlanResult        `json:"workPlan,omitempty"`
	SelectedContext     *WorkPlanContextResult `json:"selectedContext,omitempty"`
	CurrentResultStatus string                 `json:"currentResultStatus,omitempty"`
	Problem             *workProblem           `json:"problem,omitempty"`
}

type workMutationTools struct {
	executor       *toolExecutor
	mutations      workMutationService
	reader         WorkReadService
	principal      authenticatedUserLookup
	credential     func(context.Context) (*verifiedOAuthCredential, error)
	maxOutputBytes int64
}

func newWorkMutationTools(executor *toolExecutor, mutations workMutationService, reader WorkReadService, principal authenticatedUserLookup, maxOutputBytes int64) *workMutationTools {
	return &workMutationTools{
		executor: executor, mutations: mutations, reader: reader, principal: principal,
		credential: authenticatedOAuthCredential, maxOutputBytes: maxOutputBytes,
	}
}

func registerWorkMutationTools(server *mcpsdk.Server, tools *workMutationTools) {
	contracts := declaredWorkToolContracts()
	mcpsdk.AddTool(server, contracts[workPlanBeginToolName], tools.beginPlan)
	mcpsdk.AddTool(server, contracts[workItemReviseToolName], tools.reviseItem)
	mcpsdk.AddTool(server, contracts[workPlanReviseToolName], tools.revisePlan)
}

func (tools *workMutationTools) beginPlan(ctx context.Context, _ *mcpsdk.CallToolRequest, input workPlanBeginRequest) (*mcpsdk.CallToolResult, workMutationOutput, error) {
	return tools.execute(ctx, workPlanBeginContent, func(executionCtx context.Context, doer *user_model.User, authority workMutationAuthority) (*workMutationExecution, error) {
		return tools.mutations.BeginPlan(executionCtx, doer, authority, input)
	}, func(executionCtx context.Context, doer *user_model.User, execution *workMutationExecution) (*workMutationOutput, error) {
		return tools.projectPlan(executionCtx, doer, input.Repository, execution, 0)
	})
}

func (tools *workMutationTools) reviseItem(ctx context.Context, _ *mcpsdk.CallToolRequest, input workItemReviseRequest) (*mcpsdk.CallToolResult, workMutationOutput, error) {
	return tools.execute(ctx, workItemReviseContent, func(executionCtx context.Context, doer *user_model.User, authority workMutationAuthority) (*workMutationExecution, error) {
		return tools.mutations.ReviseItem(executionCtx, doer, authority, input)
	}, func(executionCtx context.Context, doer *user_model.User, execution *workMutationExecution) (*workMutationOutput, error) {
		issueNumber, _ := parseWorkReference(input.WorkItem, "issue/")
		return tools.projectItem(executionCtx, doer, input.Repository, execution, issueNumber)
	})
}

func (tools *workMutationTools) revisePlan(ctx context.Context, _ *mcpsdk.CallToolRequest, input workPlanReviseRequest) (*mcpsdk.CallToolResult, workMutationOutput, error) {
	return tools.execute(ctx, workPlanReviseContent, func(executionCtx context.Context, doer *user_model.User, authority workMutationAuthority) (*workMutationExecution, error) {
		return tools.mutations.RevisePlan(executionCtx, doer, authority, input)
	}, func(executionCtx context.Context, doer *user_model.User, execution *workMutationExecution) (*workMutationOutput, error) {
		projectID, _ := parseWorkReference(input.WorkPlan, "project/")
		return tools.projectPlan(executionCtx, doer, input.Repository, execution, projectID)
	})
}

type (
	workMutationCall       func(context.Context, *user_model.User, workMutationAuthority) (*workMutationExecution, error)
	workMutationProjection func(context.Context, *user_model.User, *workMutationExecution) (*workMutationOutput, error)
)

func (tools *workMutationTools) execute(ctx context.Context, content string, mutate workMutationCall, project workMutationProjection) (*mcpsdk.CallToolResult, workMutationOutput, error) {
	executionCtx, release, err := tools.executor.begin(ctx)
	if errors.Is(err, errToolCapacityUnavailable) {
		return workMutationErrorResult(content, "busy", "MCP endpoint capacity is currently unavailable", true)
	}
	if err != nil {
		return workMutationErrorResult(content, "cancelled", "work mutation was cancelled", true)
	}
	defer release()

	doer, err := tools.principal(executionCtx)
	if err != nil {
		return workMutationErrorResult(content, "not_permitted", "work mutation is not permitted", false)
	}
	credential, err := tools.credential(executionCtx)
	if err != nil || credential.Principal.ID != doer.ID ||
		credential.Profile != auth_model.MCPProfileWorkPlanning || credential.CanonicalScope != oauth2_provider.MCPWorkWriteScope {
		return workMutationErrorResult(content, "not_permitted", "work mutation is not permitted", false)
	}
	authority := workMutationAuthority{
		PrincipalID: doer.ID, OAuthApplicationID: credential.Application.ID, OAuthGrantID: credential.Grant.ID,
		CredentialJTI: credential.CredentialID, Audience: setting.MCPResource(), Scope: credential.CanonicalScope,
	}
	execution, err := mutate(executionCtx, doer, authority)
	if err != nil {
		return tools.mapMutationError(ctx, executionCtx, content, err)
	}
	if execution == nil || execution.Receipt == nil {
		return workMutationErrorResult(content, "mutation_failed", "work mutation failed", false)
	}
	output, err := project(executionCtx, doer, execution)
	if err != nil {
		return workMutationErrorResult(content, "mutation_failed", "work mutation failed", false)
	}
	return tools.boundedMutationResult(content, *output)
}

func (tools *workMutationTools) projectItem(ctx context.Context, doer *user_model.User, repository WorkRepository, execution *workMutationExecution, issueNumber int64) (*workMutationOutput, error) {
	output := mutationReceiptOutput(execution)
	if output.Status == "rejected" {
		return &output, nil
	}
	if execution.ProjectionUnavailable {
		output.CurrentResultStatus = "projection_unavailable"
		return &output, nil
	}
	if execution.CurrentUnavailable || len(execution.Artifacts) == 0 {
		output.CurrentResultStatus = "unavailable"
		return &output, nil
	}
	inspection, err := tools.reader.InspectWorkItem(ctx, doer, WorkItemInspectRequest{
		Repository: repository, WorkItem: "issue/" + strconv.FormatInt(issueNumber, 10), PageKind: "contexts",
		Page: &WorkPageRequest{Limit: defaultWorkPageItems},
	})
	if err != nil {
		output.CurrentResultStatus = currentProjectionStatus(err)
		return &output, nil
	}
	if inspection == nil {
		output.CurrentResultStatus = "projection_unavailable"
		return &output, nil
	}
	inspection.normalize()
	output.WorkItem = &inspection.WorkItem
	output.SelectedContext = inspection.SelectedContext
	output.CurrentResultStatus = "available"
	return &output, nil
}

func (tools *workMutationTools) projectPlan(ctx context.Context, doer *user_model.User, repository WorkRepository, execution *workMutationExecution, projectID int64) (*workMutationOutput, error) {
	output := mutationReceiptOutput(execution)
	if output.Status == "rejected" {
		return &output, nil
	}
	if projectID == 0 {
		for _, artifact := range execution.Artifacts {
			if artifact.Kind == mcpwork_model.ArtifactKindProject {
				projectID = artifact.ArtifactID
				break
			}
		}
	}
	if execution.ProjectionUnavailable {
		output.CurrentResultStatus = "projection_unavailable"
		return &output, nil
	}
	if execution.CurrentUnavailable || projectID == 0 || len(execution.Artifacts) == 0 {
		output.CurrentResultStatus = "unavailable"
		return &output, nil
	}
	inspection, err := tools.reader.InspectWorkPlan(ctx, doer, WorkPlanInspectRequest{
		Repository: repository, WorkPlan: "project/" + strconv.FormatInt(projectID, 10), PageKind: "items",
		Page: &WorkPageRequest{Limit: defaultWorkPageItems},
	})
	if err != nil {
		output.CurrentResultStatus = currentProjectionStatus(err)
		return &output, nil
	}
	if inspection == nil {
		output.CurrentResultStatus = "projection_unavailable"
		return &output, nil
	}
	inspection.normalize()
	output.WorkPlan = &inspection.WorkPlan
	output.CurrentResultStatus = "available"
	return &output, nil
}

func mutationReceiptOutput(execution *workMutationExecution) workMutationOutput {
	receipt := execution.Receipt
	status := string(receipt.Outcome)
	output := workMutationOutput{
		SchemaVersion: "1", Status: status,
		Operation: &workOperationResult{
			ID: receipt.OperationUUID, Replayed: receipt.Replayed,
			Changed: receipt.Outcome == mcpwork_model.OutcomeApplied, CommittedAt: receipt.CommittedAt,
		},
	}
	if len(execution.CreatedReferences) > 0 {
		output.CreatedReferences = execution.CreatedReferences
	}
	if receipt.Outcome == mcpwork_model.OutcomeRejected {
		output.Problem = mutationProblem(receipt.ProblemCode, false)
	}
	return output
}

func currentProjectionStatus(err error) string {
	var failure *WorkReadFailure
	if errors.As(err, &failure) && failure.Kind == WorkReadUnavailable {
		return "unavailable"
	}
	return "projection_unavailable"
}

func (tools *workMutationTools) boundedMutationResult(content string, output workMutationOutput) (*mcpsdk.CallToolResult, workMutationOutput, error) {
	wire, err := json.Marshal(output)
	if err == nil && int64(len(wire)) <= tools.maxOutputBytes && validWorkMutationOutput(wire) {
		return workMutationToolResult(content, output.Status == "rejected"), output, nil
	}
	if output.Operation == nil {
		return workMutationErrorResult(content, "limit_exceeded", "work mutation exceeded the output limit", false)
	}
	output.WorkItem = nil
	output.WorkPlan = nil
	output.SelectedContext = nil
	output.CurrentResultStatus = "projection_unavailable"
	wire, err = json.Marshal(output)
	if err != nil || int64(len(wire)) > tools.maxOutputBytes || !validWorkMutationOutput(wire) {
		return workMutationErrorResult(content, "limit_exceeded", "work mutation exceeded the output limit", false)
	}
	return workMutationToolResult(content, output.Status == "rejected"), output, nil
}

func (tools *workMutationTools) mapMutationError(parentCtx, executionCtx context.Context, content string, err error) (*mcpsdk.CallToolResult, workMutationOutput, error) {
	if errors.Is(err, mcpwork_service.ErrOutcomeUnknown) {
		return workMutationErrorResult(content, "outcome_unknown", "retry the identical mutation and idempotency key", true)
	}
	switch executionFailureCode(parentCtx, executionCtx) {
	case "cancelled":
		return workMutationErrorResult(content, "cancelled", "work mutation was cancelled before a known commit", true)
	case "timeout":
		return workMutationErrorResult(content, "timeout", "work mutation timed out before a known commit", true)
	}
	var conflict *db.WorkTransactionConflict
	switch {
	case errors.As(err, &conflict):
		return workMutationErrorResult(content, "conflict", "work mutation encountered a retryable serialization conflict", true)
	case errors.Is(err, mcpwork_service.ErrIdempotencyConflict), errors.Is(err, mcpwork_service.ErrReceiptTombstoned):
		return workMutationErrorResult(content, "idempotency_conflict", "the idempotency key was already used for another request", false)
	case errors.Is(err, mcpwork_service.ErrInvalidRequest), errors.Is(err, mcpwork_service.ErrInvalidCompletion):
		return workMutationErrorResult(content, "invalid_input", "work mutation input is invalid", false)
	default:
		return workMutationErrorResult(content, "mutation_failed", "work mutation failed", false)
	}
}

func mutationProblem(code string, retryable bool) *workProblem {
	messages := map[string]string{
		"invalid_input": "work mutation input is invalid", "unavailable": "work target is unavailable",
		"not_permitted": "work mutation is not permitted", "conflict": "work mutation precondition is stale; reinspect before retrying",
		"invalid_plan": "work plan is invalid", "invalid_dependency": "work dependency is invalid",
		"limit_exceeded": "work mutation exceeded a semantic limit", "mutation_failed": "work mutation failed",
	}
	message := messages[code]
	if message == "" {
		code, message = "mutation_failed", "work mutation failed"
	}
	return &workProblem{Code: code, Message: message, Retryable: retryable}
}

func workMutationErrorResult(content, code, message string, retryable bool) (*mcpsdk.CallToolResult, workMutationOutput, error) {
	return workMutationToolResult(content, true), workMutationOutput{
		SchemaVersion: "1", Status: "error", Problem: &workProblem{Code: code, Message: message, Retryable: retryable},
	}, nil
}

func workMutationToolResult(content string, isError bool) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: content}}, IsError: isError}
}
