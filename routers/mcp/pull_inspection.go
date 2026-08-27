// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"time"

	user_model "gitea.dev/models/user"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/services/gitdiff"
	pull_service "gitea.dev/services/pull"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const pullRequestInspectToolName = "pull_request.inspect"

const pullRequestInspectionContent = "The structured pull request inspection result is in structuredContent."

type (
	pullRequestInspectionOperation func(context.Context, *user_model.User, pull_service.InspectionRequest) (*pull_service.Inspection, error)
	authenticatedUserLookup        func(context.Context) (*user_model.User, error)
)

type pullRequestInspectionTool struct {
	capacity  chan struct{}
	timeout   time.Duration
	inspect   pullRequestInspectionOperation
	principal authenticatedUserLookup
}

type pullRequestInspectionInput struct {
	Owner                string                          `json:"owner" jsonschema:"repository owner"`
	Repository           string                          `json:"repository" jsonschema:"repository name"`
	Number               int64                           `json:"number" jsonschema:"pull request number"`
	ExpectedHeadRevision string                          `json:"expectedHeadRevision,omitempty" jsonschema:"optional frozen internal head revision"`
	ChangedFiles         *pullRequestInspectionPageInput `json:"changedFiles,omitempty" jsonschema:"include a bounded page of changed-file metadata"`
	Diff                 *pullRequestInspectionDiffInput `json:"diff,omitempty" jsonschema:"include a bounded page of diff content"`
	Checks               bool                            `json:"checks,omitempty" jsonschema:"include checks for the frozen revision"`
	Policy               bool                            `json:"policy,omitempty" jsonschema:"include merge requirements and evaluated blockers"`
}

type pullRequestInspectionPageInput struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"opaque cursor from a previous response"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum changed files in this page"`
}

type pullRequestInspectionDiffInput struct {
	Cursor            string `json:"cursor,omitempty" jsonschema:"opaque cursor from a previous response"`
	FileLimit         int    `json:"fileLimit,omitempty" jsonschema:"maximum diff files in this page"`
	LinesPerFile      int    `json:"linesPerFile,omitempty" jsonschema:"maximum diff lines per file"`
	MaxLineCharacters int    `json:"maxLineCharacters,omitempty" jsonschema:"maximum characters per diff line"`
}

type pullRequestInspectionOutput struct {
	Status     string                        `json:"status"`
	Inspection *pullRequestInspectionResult  `json:"inspection,omitempty"`
	Failure    *pullRequestInspectionFailure `json:"failure,omitempty"`
}

type pullRequestInspectionFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type pullRequestInspectionResult struct {
	Repository pullRequestInspectionRepository `json:"repository"`
	Metadata   pullRequestInspectionMetadata   `json:"metadata"`
	Revisions  pullRequestInspectionRevisions  `json:"revisions"`
	Summary    *pullRequestInspectionSummary   `json:"summary,omitempty"`
	Files      *pullRequestInspectionFiles     `json:"files,omitempty"`
	Diff       *pullRequestInspectionDiff      `json:"diff,omitempty"`
	Checks     *pullRequestInspectionChecks    `json:"checks,omitempty"`
	Policy     *pullRequestInspectionPolicy    `json:"policy,omitempty"`
}

type pullRequestInspectionRepository struct {
	Owner    string `json:"owner"`
	Name     string `json:"name"`
	FullName string `json:"fullName"`
}

type pullRequestInspectionMetadata struct {
	Number     int64      `json:"number"`
	Title      string     `json:"title"`
	Author     string     `json:"author,omitempty"`
	State      string     `json:"state"`
	Draft      bool       `json:"draft"`
	Locked     bool       `json:"locked"`
	CreatedAt  time.Time  `json:"createdAt"`
	UpdatedAt  time.Time  `json:"updatedAt"`
	ClosedAt   *time.Time `json:"closedAt,omitempty"`
	MergedAt   *time.Time `json:"mergedAt,omitempty"`
	BaseBranch string     `json:"baseBranch"`
	HeadBranch string     `json:"headBranch"`
}

type pullRequestInspectionSummary struct {
	Files     int `json:"files"`
	Additions int `json:"additions"`
	Deletions int `json:"deletions"`
}

type pullRequestInspectionRevisions struct {
	InternalHead          string `json:"internalHead,omitempty"`
	InternalHeadAvailable bool   `json:"internalHeadAvailable"`
	Target                string `json:"target,omitempty"`
	TargetAvailable       bool   `json:"targetAvailable"`
	ComparisonBase        string `json:"comparisonBase,omitempty"`
	LiveSource            string `json:"liveSource,omitempty"`
	LiveSourceAvailable   bool   `json:"liveSourceAvailable"`
	LiveSourceDiverged    bool   `json:"liveSourceDiverged"`
	Merged                string `json:"merged,omitempty"`
}

type pullRequestInspectionFile struct {
	Name         string               `json:"name"`
	OldName      string               `json:"oldName,omitempty"`
	Type         gitdiff.DiffFileType `json:"type"`
	Additions    int                  `json:"additions"`
	Deletions    int                  `json:"deletions"`
	EntryMode    string               `json:"entryMode,omitempty"`
	OldEntryMode string               `json:"oldEntryMode,omitempty"`
	Binary       bool                 `json:"binary"`
	LFS          bool                 `json:"lfs"`
	Renamed      bool                 `json:"renamed"`
	Submodule    bool                 `json:"submodule"`
}

type pullRequestInspectionFiles struct {
	Files      []pullRequestInspectionFile `json:"files"`
	NextCursor string                      `json:"nextCursor,omitempty"`
}

type pullRequestInspectionDiffLine struct {
	Type      gitdiff.DiffLineType `json:"type"`
	Content   string               `json:"content"`
	LeftLine  int                  `json:"leftLine,omitempty"`
	RightLine int                  `json:"rightLine,omitempty"`
}

type pullRequestInspectionDiffSection struct {
	Lines []pullRequestInspectionDiffLine `json:"lines"`
}

type pullRequestInspectionDiffFile struct {
	pullRequestInspectionFile
	ContentIncomplete bool                               `json:"contentIncomplete"`
	Sections          []pullRequestInspectionDiffSection `json:"sections"`
}

type pullRequestInspectionDiff struct {
	Files      []pullRequestInspectionDiffFile `json:"files"`
	NextCursor string                          `json:"nextCursor,omitempty"`
}

type pullRequestInspectionCheck struct {
	Revision    string                         `json:"revision"`
	Context     string                         `json:"context"`
	State       commitstatus.CommitStatusState `json:"state"`
	Description string                         `json:"description,omitempty"`
	TargetURL   string                         `json:"targetURL,omitempty"`
	CreatedAt   time.Time                      `json:"createdAt"`
	UpdatedAt   time.Time                      `json:"updatedAt"`
	Truncated   bool                           `json:"truncated"`
}

type pullRequestInspectionChecks struct {
	Revision string                         `json:"revision"`
	Checks   []pullRequestInspectionCheck   `json:"checks"`
	State    commitstatus.CommitStatusState `json:"state"`
}

type pullRequestInspectionPolicy struct {
	Protected                 bool                             `json:"protected"`
	StatusChecksEnabled       bool                             `json:"statusChecksEnabled"`
	RequiredContexts          []string                         `json:"requiredContexts"`
	MissingRequiredContexts   []string                         `json:"missingRequiredContexts"`
	RequiredChecksState       commitstatus.CommitStatusState   `json:"requiredChecksState"`
	RequiredApprovals         int64                            `json:"requiredApprovals"`
	GrantedApprovals          int64                            `json:"grantedApprovals"`
	IgnoreStaleApprovals      bool                             `json:"ignoreStaleApprovals"`
	BlockOnRejectedReviews    bool                             `json:"blockOnRejectedReviews"`
	BlockOnOfficialRequests   bool                             `json:"blockOnOfficialRequests"`
	BlockOnOutdatedBranch     bool                             `json:"blockOnOutdatedBranch"`
	ChangedProtectedFileCount int                              `json:"changedProtectedFileCount"`
	Blockers                  []pull_service.InspectionBlocker `json:"blockers"`
}

func newPullRequestInspectionTool(maxInFlight int, timeout time.Duration, inspect pullRequestInspectionOperation, principal authenticatedUserLookup) *pullRequestInspectionTool {
	return &pullRequestInspectionTool{capacity: make(chan struct{}, maxInFlight), timeout: timeout, inspect: inspect, principal: principal}
}

func registerPullRequestInspectionTool(server *mcpsdk.Server, tool *pullRequestInspectionTool) {
	closedWorld := false
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name:        pullRequestInspectToolName,
		Description: "Inspect one pull request using Forge's bounded, read-only pull request operation.",
		Annotations: &mcpsdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closedWorld},
	}, tool.call)
}

func (t *pullRequestInspectionTool) call(ctx context.Context, _ *mcpsdk.CallToolRequest, input pullRequestInspectionInput) (*mcpsdk.CallToolResult, pullRequestInspectionOutput, error) {
	select {
	case t.capacity <- struct{}{}:
		defer func() { <-t.capacity }()
	default:
		return failedToolResult("busy", "pull request inspection capacity is currently unavailable")
	}

	doer, err := t.principal(ctx)
	if err != nil {
		return failedToolResult("authentication_failed", "authenticated principal unavailable")
	}
	executionCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()
	inspection, err := t.inspect(executionCtx, doer, input.serviceRequest())
	if err != nil {
		return mapPullRequestInspectionError(ctx, executionCtx, err)
	}
	output := pullRequestInspectionOutput{Status: "available", Inspection: projectPullRequestInspection(inspection)}
	if err := pull_service.ValidatePullRequestInspectionDocument(output); err != nil {
		return mapPullRequestInspectionError(ctx, executionCtx, err)
	}
	return pullRequestInspectionToolResult(false), output, nil
}

func (input pullRequestInspectionInput) serviceRequest() pull_service.InspectionRequest {
	request := pull_service.InspectionRequest{
		Owner: input.Owner, Repository: input.Repository, Index: input.Number, ExpectedHeadRevision: input.ExpectedHeadRevision,
		Checks: input.Checks, Policy: input.Policy,
	}
	if input.ChangedFiles != nil {
		request.ChangedFiles = &pull_service.InspectionPageRequest{Cursor: input.ChangedFiles.Cursor, Limit: input.ChangedFiles.Limit}
	}
	if input.Diff != nil {
		request.Diff = &pull_service.InspectionDiffRequest{
			Cursor: input.Diff.Cursor, FileLimit: input.Diff.FileLimit,
			LinesPerFile: input.Diff.LinesPerFile, MaxLineCharacters: input.Diff.MaxLineCharacters,
		}
	}
	return request
}

func mapPullRequestInspectionError(parentCtx, executionCtx context.Context, err error) (*mcpsdk.CallToolResult, pullRequestInspectionOutput, error) {
	if errors.Is(err, pull_service.ErrPullRequestInspectionUnavailable) {
		return pullRequestInspectionToolResult(false), pullRequestInspectionOutput{Status: "unavailable"}, nil
	}
	if errors.Is(parentCtx.Err(), context.Canceled) {
		return failedToolResult("cancelled", "pull request inspection was cancelled")
	}
	if errors.Is(executionCtx.Err(), context.DeadlineExceeded) {
		return failedToolResult("timeout", "pull request inspection timed out")
	}
	if errors.Is(err, pull_service.ErrPullRequestInspectionLimit) {
		return failedToolResult("limit_exceeded", "pull request inspection exceeded a semantic limit")
	}
	if errors.Is(err, pull_service.ErrPullRequestInspectionHeadChanged) {
		return failedToolResult("head_changed", "pull request head changed")
	}
	if errors.Is(err, pull_service.ErrPullRequestInspectionCursor) || errors.Is(err, pull_service.ErrPullRequestInspectionCursorStale) {
		return failedToolResult("invalid_cursor", "pull request inspection cursor is invalid or stale")
	}
	return failedToolResult("inspection_failed", "pull request inspection failed")
}

func failedToolResult(code, message string) (*mcpsdk.CallToolResult, pullRequestInspectionOutput, error) {
	return pullRequestInspectionToolResult(true), pullRequestInspectionOutput{
		Status: "error", Failure: &pullRequestInspectionFailure{Code: code, Message: message},
	}, nil
}

func pullRequestInspectionToolResult(isError bool) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: pullRequestInspectionContent}},
		IsError: isError,
	}
}

func projectPullRequestInspection(inspection *pull_service.Inspection) *pullRequestInspectionResult {
	if inspection == nil {
		return nil
	}
	result := &pullRequestInspectionResult{
		Repository: pullRequestInspectionRepository{Owner: inspection.Repository.Owner, Name: inspection.Repository.Name, FullName: inspection.Repository.FullName},
		Metadata: pullRequestInspectionMetadata{
			Number: inspection.Metadata.Index, Title: inspection.Metadata.Title, Author: inspection.Metadata.Author,
			State: inspection.Metadata.State, Draft: inspection.Metadata.IsDraft, Locked: inspection.Metadata.IsLocked,
			CreatedAt: inspection.Metadata.CreatedAt, UpdatedAt: inspection.Metadata.UpdatedAt,
			ClosedAt: inspection.Metadata.ClosedAt, MergedAt: inspection.Metadata.MergedAt,
			BaseBranch: inspection.Metadata.BaseBranch, HeadBranch: inspection.Metadata.HeadBranch,
		},
		Revisions: projectPullRequestInspectionRevisions(inspection.Revisions),
		Policy:    projectPullRequestInspectionPolicy(inspection.Policy),
	}
	if inspection.Summary != nil {
		result.Summary = &pullRequestInspectionSummary{Files: inspection.Summary.NumFiles, Additions: inspection.Summary.TotalAddition, Deletions: inspection.Summary.TotalDeletion}
	}
	result.Files = projectPullRequestInspectionFiles(inspection.Files)
	result.Diff = projectPullRequestInspectionDiff(inspection.Diff)
	result.Checks = projectPullRequestInspectionChecks(inspection.Checks)
	return result
}

func projectPullRequestInspectionRevisions(revisions pull_service.InspectionRevisions) pullRequestInspectionRevisions {
	return pullRequestInspectionRevisions{
		InternalHead: revisions.InternalHead, InternalHeadAvailable: revisions.InternalHeadAvailable,
		Target: revisions.Target, TargetAvailable: revisions.TargetAvailable, ComparisonBase: revisions.ComparisonBase,
		LiveSource: revisions.LiveSource, LiveSourceAvailable: revisions.LiveSourceAvailable,
		LiveSourceDiverged: revisions.LiveSourceDiverged, Merged: revisions.Merged,
	}
}

func projectPullRequestInspectionPolicy(policy *pull_service.InspectionPolicy) *pullRequestInspectionPolicy {
	if policy == nil {
		return nil
	}
	return &pullRequestInspectionPolicy{
		Protected: policy.Protected, StatusChecksEnabled: policy.StatusChecksEnabled,
		RequiredContexts: policy.RequiredContexts, MissingRequiredContexts: policy.MissingRequiredContexts,
		RequiredChecksState: policy.RequiredChecksState, RequiredApprovals: policy.RequiredApprovals,
		GrantedApprovals: policy.GrantedApprovals, IgnoreStaleApprovals: policy.IgnoreStaleApprovals,
		BlockOnRejectedReviews: policy.BlockOnRejectedReviews, BlockOnOfficialRequests: policy.BlockOnOfficialRequests,
		BlockOnOutdatedBranch: policy.BlockOnOutdatedBranch, ChangedProtectedFileCount: policy.ChangedProtectedFileCount,
		Blockers: policy.Blockers,
	}
}

func projectPullRequestInspectionFile(file pull_service.InspectionFile) pullRequestInspectionFile {
	return pullRequestInspectionFile{
		Name: file.Name, OldName: file.OldName, Type: file.Type, Additions: file.Addition, Deletions: file.Deletion,
		EntryMode: file.EntryMode, OldEntryMode: file.OldEntryMode, Binary: file.IsBinary, LFS: file.IsLFS,
		Renamed: file.IsRenamed, Submodule: file.IsSubmodule,
	}
}

func projectPullRequestInspectionFiles(files *pull_service.InspectionFilePage) *pullRequestInspectionFiles {
	if files == nil {
		return nil
	}
	result := &pullRequestInspectionFiles{Files: make([]pullRequestInspectionFile, 0, len(files.Files)), NextCursor: files.NextCursor}
	for _, file := range files.Files {
		result.Files = append(result.Files, projectPullRequestInspectionFile(file))
	}
	return result
}

func projectPullRequestInspectionDiff(diff *pull_service.InspectionDiffPage) *pullRequestInspectionDiff {
	if diff == nil {
		return nil
	}
	result := &pullRequestInspectionDiff{Files: make([]pullRequestInspectionDiffFile, 0, len(diff.Files)), NextCursor: diff.NextCursor}
	for _, file := range diff.Files {
		projected := pullRequestInspectionDiffFile{
			pullRequestInspectionFile: projectPullRequestInspectionFile(file.InspectionFile),
			ContentIncomplete:         file.ContentIncomplete,
			Sections:                  make([]pullRequestInspectionDiffSection, 0, len(file.Sections)),
		}
		for _, section := range file.Sections {
			projectedSection := pullRequestInspectionDiffSection{Lines: make([]pullRequestInspectionDiffLine, 0, len(section.Lines))}
			for _, line := range section.Lines {
				projectedSection.Lines = append(projectedSection.Lines, pullRequestInspectionDiffLine{
					Type: line.Type, Content: line.Content, LeftLine: line.LeftLine, RightLine: line.RightLine,
				})
			}
			projected.Sections = append(projected.Sections, projectedSection)
		}
		result.Files = append(result.Files, projected)
	}
	return result
}

func projectPullRequestInspectionChecks(checks *pull_service.InspectionChecks) *pullRequestInspectionChecks {
	if checks == nil {
		return nil
	}
	result := &pullRequestInspectionChecks{Revision: checks.Revision, Checks: make([]pullRequestInspectionCheck, 0, len(checks.Checks)), State: checks.State}
	for _, check := range checks.Checks {
		result.Checks = append(result.Checks, pullRequestInspectionCheck{
			Revision: check.Revision, Context: check.Context, State: check.State, Description: check.Description,
			TargetURL: check.TargetURL, CreatedAt: check.CreatedAt, UpdatedAt: check.UpdatedAt, Truncated: check.Truncated,
		})
	}
	return result
}
