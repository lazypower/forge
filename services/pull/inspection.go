// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package pull

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/git"
	"gitea.dev/modules/git/gitcmd"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/glob"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/util"
	git_service "gitea.dev/services/git"
	"gitea.dev/services/gitdiff"
)

const (
	DefaultPullRequestInspectionFileLimit = 25
	MaxPullRequestInspectionFileLimit     = 100
	DefaultPullRequestInspectionDiffFiles = 10
	MaxPullRequestInspectionDiffFiles     = 25
	DefaultPullRequestInspectionDiffLines = 250
	MaxPullRequestInspectionDiffLines     = 1000
	DefaultPullRequestInspectionLineBytes = 128
	MaxPullRequestInspectionLineBytes     = 10_000
	MaxPullRequestInspectionStatuses      = 100
	MaxPullRequestInspectionStatusText    = 2_000
	MaxPullRequestInspectionTitleBytes    = 255
	// MaxPullRequestInspectionResponseBytes includes the structured document and small semantic MCP result envelope, but not JSON-RPC or HTTP framing.
	MaxPullRequestInspectionResponseBytes = 1 << 20
	// MaxPullRequestInspectionDocumentBytes reserves response capacity for that envelope.
	MaxPullRequestInspectionDocumentBytes = 3 << 18

	pullRequestInspectionBaseBudgetBytes     = 32 << 10
	pullRequestInspectionFileBudgetBytes     = 4 << 10
	pullRequestInspectionDiffLineBudgetBytes = 64
	pullRequestInspectionCheckBudgetBytes    = 3*MaxPullRequestInspectionStatusText + 256
	pullRequestInspectionPolicyBudgetBytes   = 64 << 10

	pullRequestInspectionCursorVersion = 1
)

var (
	// ErrPullRequestInspectionUnavailable intentionally covers missing and denied resources.
	ErrPullRequestInspectionUnavailable = errors.New("pull request inspection unavailable")
	ErrPullRequestInspectionCursor      = errors.New("invalid pull request inspection cursor")
	ErrPullRequestInspectionCursorStale = errors.New("pull request inspection cursor is stale")
	ErrPullRequestInspectionHeadChanged = errors.New("pull request head changed")
	ErrPullRequestInspectionLimit       = errors.New("pull request inspection limit exceeded")
)

type InspectionRequest struct {
	Owner                string
	Repository           string
	Index                int64
	ExpectedHeadRevision string
	ChangedFiles         *InspectionPageRequest
	Diff                 *InspectionDiffRequest
	Checks               bool
	Policy               bool
}

type InspectionPageRequest struct {
	Cursor string
	Limit  int
}

type InspectionDiffRequest struct {
	Cursor            string
	FileLimit         int
	LinesPerFile      int
	MaxLineCharacters int
}

type Inspection struct {
	Repository InspectionRepository
	Metadata   InspectionMetadata
	Revisions  InspectionRevisions
	Summary    *gitdiff.DiffShortStat
	Files      *InspectionFilePage
	Diff       *InspectionDiffPage
	Checks     *InspectionChecks
	Policy     *InspectionPolicy
}

type InspectionRepository struct {
	ID       int64
	Owner    string
	Name     string
	FullName string
}

type InspectionMetadata struct {
	ID         int64
	Index      int64
	Title      string
	Author     string
	State      string
	IsDraft    bool
	IsLocked   bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
	ClosedAt   *time.Time
	MergedAt   *time.Time
	BaseBranch string
	HeadBranch string
}

type InspectionRevisions struct {
	InternalHead          string
	InternalHeadAvailable bool
	Target                string
	TargetAvailable       bool
	ComparisonBase        string
	LiveSource            string
	LiveSourceAvailable   bool
	LiveSourceDiverged    bool
	Merged                string
}

type pullRequestRevisionInspection struct {
	Revisions  InspectionRevisions
	Comparison git_service.CompareInfo
}

type InspectionFile struct {
	Name         string
	OldName      string
	Type         gitdiff.DiffFileType
	Addition     int
	Deletion     int
	EntryMode    string
	OldEntryMode string
	IsBinary     bool
	IsLFS        bool
	IsRenamed    bool
	IsSubmodule  bool
}

type InspectionFilePage struct {
	Files      []InspectionFile
	NextCursor string
}

type InspectionDiffLine struct {
	Type      gitdiff.DiffLineType
	Content   string
	LeftLine  int
	RightLine int
}

type InspectionDiffSection struct {
	Lines []InspectionDiffLine
}

type InspectionDiffFile struct {
	InspectionFile
	ContentIncomplete bool
	Sections          []InspectionDiffSection
}

type InspectionDiffPage struct {
	Files      []InspectionDiffFile
	NextCursor string
}

type InspectionCheck struct {
	ID           int64
	RepositoryID int64
	Revision     string
	Context      string
	State        commitstatus.CommitStatusState
	Description  string
	TargetURL    string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Truncated    bool
}

type InspectionChecks struct {
	Revision string
	Checks   []InspectionCheck
	State    commitstatus.CommitStatusState

	commitStatuses []*git_model.CommitStatus
}

type InspectionBlocker string

const (
	PullRequestInspectionBlockerApprovals             InspectionBlocker = "approvals"
	PullRequestInspectionBlockerRejectedReview        InspectionBlocker = "rejected_review"
	PullRequestInspectionBlockerOfficialReviewRequest InspectionBlocker = "official_review_request"
	PullRequestInspectionBlockerOutdatedBranch        InspectionBlocker = "outdated_branch"
	PullRequestInspectionBlockerProtectedFiles        InspectionBlocker = "protected_files"
	PullRequestInspectionBlockerRequiredChecksMissing InspectionBlocker = "required_checks_missing"
	PullRequestInspectionBlockerRequiredChecksFailing InspectionBlocker = "required_checks_failing"
)

type InspectionPolicy struct {
	Protected                 bool
	StatusChecksEnabled       bool
	RequiredContexts          []string
	MissingRequiredContexts   []string
	RequiredChecksState       commitstatus.CommitStatusState
	RequiredApprovals         int64
	GrantedApprovals          int64
	IgnoreStaleApprovals      bool
	BlockOnRejectedReviews    bool
	BlockOnOfficialRequests   bool
	BlockOnOutdatedBranch     bool
	ChangedProtectedFileCount int
	Blockers                  []InspectionBlocker
}

func (p *InspectionPolicy) HasBlocker(blocker InspectionBlocker) bool {
	return p != nil && slices.Contains(p.Blockers, blocker)
}

func (p *InspectionPolicy) IsContextRequired(context string) bool {
	if p == nil {
		return false
	}
	for _, required := range p.RequiredContexts {
		if requiredContextMatcher(required)(context) {
			return true
		}
	}
	return false
}

type pullRequestInspectionCursor struct {
	Version        int    `json:"v"`
	Kind           string `json:"k"`
	RepositoryID   int64  `json:"r"`
	PullRequestID  int64  `json:"p"`
	InternalHead   string `json:"h"`
	Target         string `json:"t"`
	ComparisonBase string `json:"b"`
	NextFile       string `json:"n"`
}

func InspectPullRequest(ctx context.Context, doer *user_model.User, req InspectionRequest) (*Inspection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	repo, err := repo_model.GetRepositoryByOwnerAndName(ctx, req.Owner, req.Repository)
	if err != nil {
		if repo_model.IsErrRepoNotExist(err) {
			return nil, ErrPullRequestInspectionUnavailable
		}
		return nil, fmt.Errorf("GetRepositoryByOwnerAndName: %w", err)
	}
	permission, err := access_model.GetDoerRepoPermission(ctx, repo, doer)
	if err != nil {
		return nil, fmt.Errorf("GetDoerRepoPermission: %w", err)
	}
	if !permission.CanRead(unit.TypePullRequests) {
		return nil, ErrPullRequestInspectionUnavailable
	}
	pr, err := issues_model.GetPullRequestByIndex(ctx, repo.ID, req.Index)
	if err != nil {
		if issues_model.IsErrPullRequestNotExist(err) {
			return nil, ErrPullRequestInspectionUnavailable
		}
		return nil, fmt.Errorf("GetPullRequestByIndex: %w", err)
	}
	return inspectPermittedPullRequest(ctx, doer, repo, pr, permission, req)
}

func inspectLoadedPullRequest(ctx context.Context, doer *user_model.User, repo *repo_model.Repository, pr *issues_model.PullRequest, req InspectionRequest) (*Inspection, error) {
	permission, err := access_model.GetDoerRepoPermission(ctx, repo, doer)
	if err != nil {
		return nil, fmt.Errorf("GetDoerRepoPermission: %w", err)
	}
	if !permission.CanRead(unit.TypePullRequests) {
		return nil, ErrPullRequestInspectionUnavailable
	}
	return inspectPermittedPullRequest(ctx, doer, repo, pr, permission, req)
}

func inspectPermittedPullRequest(ctx context.Context, doer *user_model.User, repo *repo_model.Repository, pr *issues_model.PullRequest, permission access_model.Permission, req InspectionRequest) (*Inspection, error) {
	if pr == nil || pr.BaseRepoID != repo.ID || pr.Issue == nil || !pr.Issue.IsPull {
		return nil, ErrPullRequestInspectionUnavailable
	}
	if (req.ChangedFiles != nil || req.Diff != nil) && !permission.CanRead(unit.TypeCode) {
		return nil, ErrPullRequestInspectionUnavailable
	}
	if err := validatePullRequestInspectionRequest(req); err != nil {
		return nil, err
	}

	if err := pr.LoadBaseRepo(ctx); err != nil {
		return nil, fmt.Errorf("LoadBaseRepo: %w", err)
	}
	if err := pr.LoadHeadRepo(ctx); err != nil {
		return nil, fmt.Errorf("LoadHeadRepo: %w", err)
	}
	if err := pr.Issue.LoadPoster(ctx); err != nil {
		return nil, fmt.Errorf("LoadPoster: %w", err)
	}

	baseGitRepo, err := gitrepo.OpenRepository(ctx, repo)
	if err != nil {
		return nil, fmt.Errorf("OpenRepository: %w", err)
	}
	defer baseGitRepo.Close()

	resolveLiveSource := pr.HeadRepoID == pr.BaseRepoID
	if !resolveLiveSource && pr.HeadRepo != nil {
		headPermission, err := access_model.GetDoerRepoPermission(ctx, pr.HeadRepo, doer)
		if err != nil {
			return nil, fmt.Errorf("GetDoerRepoPermission: %w", err)
		}
		resolveLiveSource = headPermission.CanRead(unit.TypeCode)
	}
	revisionInspection, revisionErr := resolvePullRequestInspectionRevisions(ctx, pr, baseGitRepo, resolveLiveSource, false)
	result := &Inspection{
		Repository: InspectionRepository{
			ID: repo.ID, Owner: repo.OwnerName, Name: repo.Name, FullName: repo.FullName(),
		},
		Metadata: pullRequestInspectionMetadata(pr),
	}
	if revisionInspection != nil {
		result.Revisions = revisionInspection.Revisions
	}
	if revisionErr != nil {
		return result, revisionErr
	}
	if req.ExpectedHeadRevision != "" && req.ExpectedHeadRevision != revisionInspection.Revisions.InternalHead {
		return nil, ErrPullRequestInspectionHeadChanged
	}

	if req.ChangedFiles != nil || req.Diff != nil {
		if !result.Revisions.InternalHeadAvailable || result.Revisions.ComparisonBase == "" {
			return nil, fmt.Errorf("resolve diff revisions: %w", util.ErrNotExist)
		}
		result.Summary, err = gitdiff.GetDiffShortStat(ctx, repo, baseGitRepo, result.Revisions.ComparisonBase, result.Revisions.InternalHead)
		if err != nil {
			return nil, fmt.Errorf("GetDiffShortStat: %w", err)
		}
	}
	if req.ChangedFiles != nil {
		result.Files, err = inspectPullRequestFiles(ctx, repo, pr, baseGitRepo, result.Revisions, *req.ChangedFiles)
		if err != nil {
			return nil, err
		}
	}
	if req.Diff != nil {
		result.Diff, err = inspectPullRequestDiff(ctx, repo, pr, baseGitRepo, result.Revisions, *req.Diff)
		if err != nil {
			return nil, err
		}
	}
	var inspectionErr error
	if req.Checks || req.Policy {
		result.Checks, inspectionErr = inspectPullRequestChecks(ctx, repo, result.Revisions.InternalHead, permission.CanRead(unit.TypeActions))
		if result.Checks == nil {
			result.Checks = &InspectionChecks{Revision: result.Revisions.InternalHead}
		}
		if req.Policy && errors.Is(inspectionErr, ErrPullRequestInspectionLimit) {
			// Exact policy decisions use the frozen head and remain private; the public
			// check projection is still empty and returns the explicit limit error.
			result.Checks.commitStatuses, err = git_model.GetLatestCommitStatus(ctx, repo.ID, result.Revisions.InternalHead, db.ListOptionsAll)
			if err != nil {
				inspectionErr = errors.Join(inspectionErr, fmt.Errorf("GetLatestCommitStatus for policy: %w", err))
			}
		}
	}
	if req.Policy {
		result.Policy, err = inspectPullRequestPolicy(ctx, pr, result.Checks)
		inspectionErr = errors.Join(inspectionErr, err)
	}
	if !req.Checks {
		result.Checks = nil
	}
	if err := ValidatePullRequestInspectionDocument(result); err != nil {
		return nil, err
	}
	return result, inspectionErr
}

// ValidatePullRequestInspectionDocument enforces the product-owned structured
// document bound before a transport adds its small semantic-result envelope.
func ValidatePullRequestInspectionDocument(document any) error {
	output, err := json.Marshal(document)
	if err != nil {
		return fmt.Errorf("marshal pull request inspection: %w", err)
	}
	if len(output) > MaxPullRequestInspectionDocumentBytes {
		return fmt.Errorf("output: %w", ErrPullRequestInspectionLimit)
	}
	return nil
}

func resolvePullRequestInspectionRevisions(ctx context.Context, pr *issues_model.PullRequest, baseGitRepo *git.Repository, resolveLiveSource, includeCommits bool) (*pullRequestRevisionInspection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := pr.LoadBaseRepo(ctx); err != nil {
		return nil, fmt.Errorf("LoadBaseRepo: %w", err)
	}
	if err := pr.LoadHeadRepo(ctx); err != nil {
		return nil, fmt.Errorf("LoadHeadRepo: %w", err)
	}

	revisions := InspectionRevisions{Merged: pr.MergedCommitID}
	target, targetErr := baseGitRepo.GetRefCommitID(git.RefNameFromBranch(pr.BaseBranch).String())
	if targetErr == nil {
		revisions.Target = target
		revisions.TargetAvailable = true
	} else if !isPullRequestInspectionMissingRevision(targetErr) {
		return nil, fmt.Errorf("resolve target revision: %w", targetErr)
	}

	var baseRef git.RefName
	if pr.HasMerged || !revisions.TargetAvailable {
		if pr.MergeBase == "" {
			return &pullRequestRevisionInspection{Revisions: revisions}, fmt.Errorf("resolve comparison base: %w", util.ErrNotExist)
		}
		baseRef = git.RefNameFromCommit(pr.MergeBase)
	} else {
		baseRef = git.RefNameFromCommit(revisions.Target)
	}
	comparison, compareErr := git_service.GetCompareInfo(ctx, pr.BaseRepo, pr.BaseRepo, baseGitRepo, baseRef, git.RefName(pr.GetGitHeadRefName()), false, !includeCommits)
	revisions.InternalHead = comparison.HeadCommitID
	revisions.InternalHeadAvailable = comparison.HeadCommitID != ""
	revisions.ComparisonBase = comparison.CompareBase
	if compareErr != nil {
		return &pullRequestRevisionInspection{Revisions: revisions, Comparison: comparison}, fmt.Errorf("GetCompareInfo: %w", compareErr)
	}

	if resolveLiveSource {
		liveSource, liveSourceAvailable, err := resolvePullRequestLiveSource(ctx, pr, baseGitRepo)
		if err != nil {
			return nil, err
		}
		revisions.LiveSource = liveSource
		revisions.LiveSourceAvailable = liveSourceAvailable
		revisions.LiveSourceDiverged = revisions.LiveSourceAvailable && revisions.LiveSource != revisions.InternalHead
	}
	return &pullRequestRevisionInspection{Revisions: revisions, Comparison: comparison}, nil
}

func resolvePullRequestLiveSource(ctx context.Context, pr *issues_model.PullRequest, baseGitRepo *git.Repository) (string, bool, error) {
	if pr.Flow == issues_model.PullRequestFlowAGit {
		return "", false, nil
	}
	if pr.HeadRepo == nil {
		return "", false, nil
	}
	if pr.HeadRepoID == pr.BaseRepoID {
		revision, err := baseGitRepo.GetRefCommitID(git.RefNameFromBranch(pr.HeadBranch).String())
		if err != nil {
			if isPullRequestInspectionMissingRevision(err) {
				return "", false, nil
			}
			return "", false, fmt.Errorf("resolve live source revision: %w", err)
		}
		return revision, true, nil
	}
	headGitRepo, closer, err := gitrepo.RepositoryFromContextOrOpen(ctx, pr.HeadRepo)
	if err != nil {
		return "", false, fmt.Errorf("open source repository: %w", err)
	}
	defer closer.Close()
	revision, err := headGitRepo.GetRefCommitID(git.RefNameFromBranch(pr.HeadBranch).String())
	if err != nil {
		if isPullRequestInspectionMissingRevision(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("resolve live source revision: %w", err)
	}
	return revision, true, nil
}

func inspectPullRequestChecks(ctx context.Context, repo *repo_model.Repository, revision string, canReadActions bool) (*InspectionChecks, error) {
	if revision == "" {
		return &InspectionChecks{}, nil
	}
	count, err := git_model.CountLatestCommitStatus(ctx, repo.ID, revision)
	if err != nil {
		return &InspectionChecks{Revision: revision}, fmt.Errorf("CountLatestCommitStatus: %w", err)
	}
	if count > MaxPullRequestInspectionStatuses {
		return &InspectionChecks{Revision: revision}, fmt.Errorf("statuses: %w", ErrPullRequestInspectionLimit)
	}
	statuses, err := git_model.GetLatestCommitStatus(ctx, repo.ID, revision, db.ListOptions{Page: 1, PageSize: MaxPullRequestInspectionStatuses})
	if err != nil {
		return &InspectionChecks{Revision: revision}, fmt.Errorf("GetLatestCommitStatus: %w", err)
	}
	if !canReadActions {
		git_model.CommitStatusesHideActionsURL(ctx, statuses)
	}
	checks := make([]InspectionCheck, 0, len(statuses))
	for _, status := range statuses {
		contextText, contextTruncated := truncatePullRequestInspectionText(status.Context, MaxPullRequestInspectionStatusText)
		description, descriptionTruncated := truncatePullRequestInspectionText(status.Description, MaxPullRequestInspectionStatusText)
		targetURL, targetURLTruncated := truncatePullRequestInspectionText(status.TargetURL, MaxPullRequestInspectionStatusText)
		checks = append(checks, InspectionCheck{
			ID: status.ID, RepositoryID: status.RepoID, Revision: status.SHA, Context: contextText, State: status.State,
			Description: description, TargetURL: targetURL,
			CreatedAt: status.CreatedUnix.AsTime(), UpdatedAt: status.UpdatedUnix.AsTime(),
			Truncated: contextTruncated || descriptionTruncated || targetURLTruncated,
		})
	}
	state := commitstatus.CommitStatusPending
	if combined := git_model.CalcCommitStatus(statuses); combined != nil {
		state = combined.State
	}
	return &InspectionChecks{Revision: revision, Checks: checks, State: state, commitStatuses: statuses}, nil
}

func inspectPullRequestPolicy(ctx context.Context, pr *issues_model.PullRequest, checks *InspectionChecks) (*InspectionPolicy, error) {
	pb, err := git_model.GetFirstMatchProtectedBranchRule(ctx, pr.BaseRepoID, pr.BaseBranch)
	if err != nil {
		return nil, fmt.Errorf("GetFirstMatchProtectedBranchRule: %w", err)
	}
	return evaluatePullRequestInspectionPolicy(ctx, pr, pb, checks)
}

func evaluatePullRequestInspectionPolicy(ctx context.Context, pr *issues_model.PullRequest, pb *git_model.ProtectedBranch, checks *InspectionChecks) (*InspectionPolicy, error) {
	policy := &InspectionPolicy{}
	if pb == nil {
		return policy, nil
	}
	policy.Protected = true
	policy.StatusChecksEnabled = pb.EnableStatusCheck
	policy.RequiredApprovals = pb.RequiredApprovals
	policy.IgnoreStaleApprovals = pb.IgnoreStaleApprovals
	policy.BlockOnRejectedReviews = pb.BlockOnRejectedReviews
	policy.BlockOnOfficialRequests = pb.BlockOnOfficialReviewRequests
	policy.BlockOnOutdatedBranch = pb.BlockOnOutdatedBranch
	policy.ChangedProtectedFileCount = len(pr.ChangedProtectedFiles)
	if pb.EnableStatusCheck {
		policy.RequiredContexts = slices.Clone(pb.StatusCheckContexts)
	}

	var policyErrors []error
	if err := pr.LoadBaseRepo(ctx); err != nil {
		policyErrors = append(policyErrors, fmt.Errorf("LoadBaseRepo: %w", err))
	} else if effective, effectiveErr := EffectiveRequiredContexts(ctx, pr.BaseRepo, pb); effectiveErr != nil {
		policyErrors = append(policyErrors, effectiveErr)
	} else {
		policy.RequiredContexts = effective
	}
	var statuses []*git_model.CommitStatus
	if checks != nil {
		statuses = checks.commitStatuses
	}
	policy.RequiredChecksState = MergeRequiredContextsCommitStatus(statuses, policy.RequiredContexts)
	policy.MissingRequiredContexts = missingPullRequestInspectionContexts(statuses, policy.RequiredContexts)
	if pb.EnableStatusCheck || len(policy.RequiredContexts) > 0 {
		if policy.RequiredChecksState.IsError() || policy.RequiredChecksState.IsFailure() {
			policy.Blockers = append(policy.Blockers, PullRequestInspectionBlockerRequiredChecksFailing)
		} else if !policy.RequiredChecksState.IsSuccess() {
			policy.Blockers = append(policy.Blockers, PullRequestInspectionBlockerRequiredChecksMissing)
		}
	}

	approvals, err := issues_model.GetGrantedApprovalsCountWithError(ctx, pb, pr)
	policy.GrantedApprovals = approvals
	if err != nil {
		policy.GrantedApprovals = 0
		policyErrors = append(policyErrors, fmt.Errorf("GetGrantedApprovalsCount: %w", err))
	}
	if policy.GrantedApprovals < policy.RequiredApprovals {
		policy.Blockers = append(policy.Blockers, PullRequestInspectionBlockerApprovals)
	}
	blocked, err := issues_model.MergeBlockedByRejectedReviewWithError(ctx, pb, pr)
	if err != nil {
		blocked = pb.BlockOnRejectedReviews
		policyErrors = append(policyErrors, fmt.Errorf("MergeBlockedByRejectedReview: %w", err))
	}
	if blocked {
		policy.Blockers = append(policy.Blockers, PullRequestInspectionBlockerRejectedReview)
	}
	blocked, err = issues_model.MergeBlockedByOfficialReviewRequestsWithError(ctx, pb, pr)
	if err != nil {
		blocked = pb.BlockOnOfficialReviewRequests
		policyErrors = append(policyErrors, fmt.Errorf("MergeBlockedByOfficialReviewRequests: %w", err))
	}
	if blocked {
		policy.Blockers = append(policy.Blockers, PullRequestInspectionBlockerOfficialReviewRequest)
	}
	if issues_model.MergeBlockedByOutdatedBranch(pb, pr) {
		policy.Blockers = append(policy.Blockers, PullRequestInspectionBlockerOutdatedBranch)
	}
	if len(pr.ChangedProtectedFiles) > 0 {
		policy.Blockers = append(policy.Blockers, PullRequestInspectionBlockerProtectedFiles)
	}
	return policy, errors.Join(policyErrors...)
}

func inspectPullRequestFiles(ctx context.Context, repo *repo_model.Repository, pr *issues_model.PullRequest, gitRepo *git.Repository, revisions InspectionRevisions, page InspectionPageRequest) (*InspectionFilePage, error) {
	limit := page.Limit
	if limit == 0 {
		limit = DefaultPullRequestInspectionFileLimit
	}
	skipTo, err := decodePullRequestInspectionCursor(page.Cursor, "files", repo.ID, pr.ID, revisions)
	if err != nil {
		return nil, err
	}
	diff, err := gitdiff.GetDiffForAPI(ctx, gitRepo, &gitdiff.DiffOptions{
		BeforeCommitID: revisions.ComparisonBase, AfterCommitID: revisions.InternalHead, SkipTo: skipTo,
		MaxLines: 0, MaxLineCharacters: MaxPullRequestInspectionLineBytes, MaxFiles: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("GetDiffForAPI: %w", err)
	}
	result := &InspectionFilePage{Files: make([]InspectionFile, 0, len(diff.Files))}
	for _, file := range diff.Files {
		result.Files = append(result.Files, projectPullRequestInspectionFile(file))
	}
	if diff.IsIncomplete && diff.End != "" {
		result.NextCursor, err = encodePullRequestInspectionCursor("files", repo.ID, pr.ID, revisions, diff.End)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func inspectPullRequestDiff(ctx context.Context, repo *repo_model.Repository, pr *issues_model.PullRequest, gitRepo *git.Repository, revisions InspectionRevisions, page InspectionDiffRequest) (*InspectionDiffPage, error) {
	fileLimit := page.FileLimit
	if fileLimit == 0 {
		fileLimit = DefaultPullRequestInspectionDiffFiles
	}
	linesPerFile := page.LinesPerFile
	if linesPerFile == 0 {
		linesPerFile = DefaultPullRequestInspectionDiffLines
	}
	maxLineCharacters := page.MaxLineCharacters
	if maxLineCharacters == 0 {
		maxLineCharacters = DefaultPullRequestInspectionLineBytes
	}
	skipTo, err := decodePullRequestInspectionCursor(page.Cursor, "diff", repo.ID, pr.ID, revisions)
	if err != nil {
		return nil, err
	}
	diff, err := gitdiff.GetDiffForAPI(ctx, gitRepo, &gitdiff.DiffOptions{
		BeforeCommitID: revisions.ComparisonBase, AfterCommitID: revisions.InternalHead, SkipTo: skipTo,
		MaxLines: linesPerFile, MaxLineCharacters: maxLineCharacters, MaxFiles: fileLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("GetDiffForAPI: %w", err)
	}
	result := &InspectionDiffPage{Files: make([]InspectionDiffFile, 0, len(diff.Files))}
	for _, file := range diff.Files {
		projected := InspectionDiffFile{
			InspectionFile: projectPullRequestInspectionFile(file), ContentIncomplete: file.IsIncomplete,
			Sections: make([]InspectionDiffSection, 0, len(file.Sections)),
		}
		for _, section := range file.Sections {
			projectedSection := InspectionDiffSection{Lines: make([]InspectionDiffLine, 0, len(section.Lines))}
			for _, line := range section.Lines {
				content, _ := truncatePullRequestInspectionText(line.Content, maxLineCharacters)
				projectedSection.Lines = append(projectedSection.Lines, InspectionDiffLine{Type: line.Type, Content: content, LeftLine: line.LeftIdx, RightLine: line.RightIdx})
			}
			projected.Sections = append(projected.Sections, projectedSection)
		}
		result.Files = append(result.Files, projected)
	}
	if diff.IsIncomplete && diff.End != "" {
		result.NextCursor, err = encodePullRequestInspectionCursor("diff", repo.ID, pr.ID, revisions, diff.End)
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func validatePullRequestInspectionRequest(req InspectionRequest) error {
	if req.Index < 1 {
		return ErrPullRequestInspectionUnavailable
	}
	if req.ChangedFiles != nil && (req.ChangedFiles.Limit < 0 || req.ChangedFiles.Limit > MaxPullRequestInspectionFileLimit) {
		return fmt.Errorf("changed files: %w", ErrPullRequestInspectionLimit)
	}
	if req.Diff != nil {
		if req.Diff.FileLimit < 0 || req.Diff.FileLimit > MaxPullRequestInspectionDiffFiles ||
			req.Diff.LinesPerFile < 0 || req.Diff.LinesPerFile > MaxPullRequestInspectionDiffLines ||
			req.Diff.MaxLineCharacters < 0 || req.Diff.MaxLineCharacters > MaxPullRequestInspectionLineBytes {
			return fmt.Errorf("diff: %w", ErrPullRequestInspectionLimit)
		}
	}
	budget := int64(pullRequestInspectionBaseBudgetBytes)
	if req.ChangedFiles != nil {
		limit := req.ChangedFiles.Limit
		if limit == 0 {
			limit = DefaultPullRequestInspectionFileLimit
		}
		if err := reservePullRequestInspectionBudget(&budget, int64(limit), pullRequestInspectionFileBudgetBytes); err != nil {
			return fmt.Errorf("changed files: %w", err)
		}
	}
	if req.Diff != nil {
		fileLimit := req.Diff.FileLimit
		if fileLimit == 0 {
			fileLimit = DefaultPullRequestInspectionDiffFiles
		}
		linesPerFile := req.Diff.LinesPerFile
		if linesPerFile == 0 {
			linesPerFile = DefaultPullRequestInspectionDiffLines
		}
		maxLineCharacters := req.Diff.MaxLineCharacters
		if maxLineCharacters == 0 {
			maxLineCharacters = DefaultPullRequestInspectionLineBytes
		}
		if err := reservePullRequestInspectionBudget(&budget, int64(fileLimit), pullRequestInspectionFileBudgetBytes); err != nil {
			return fmt.Errorf("diff files: %w", err)
		}
		if err := reservePullRequestInspectionBudget(&budget, int64(fileLimit)*int64(linesPerFile), int64(maxLineCharacters+pullRequestInspectionDiffLineBudgetBytes)); err != nil {
			return fmt.Errorf("diff content: %w", err)
		}
	}
	if req.Checks || req.Policy {
		if err := reservePullRequestInspectionBudget(&budget, MaxPullRequestInspectionStatuses, pullRequestInspectionCheckBudgetBytes); err != nil {
			return fmt.Errorf("checks: %w", err)
		}
	}
	if req.Policy {
		if err := reservePullRequestInspectionBudget(&budget, 1, pullRequestInspectionPolicyBudgetBytes); err != nil {
			return fmt.Errorf("policy: %w", err)
		}
	}
	return nil
}

func reservePullRequestInspectionBudget(used *int64, count, bytesEach int64) error {
	amount := count * bytesEach
	if amount > MaxPullRequestInspectionDocumentBytes-*used {
		return ErrPullRequestInspectionLimit
	}
	*used += amount
	return nil
}

func pullRequestInspectionMetadata(pr *issues_model.PullRequest) InspectionMetadata {
	state := "open"
	if pr.HasMerged {
		state = "merged"
	} else if pr.Issue.IsClosed {
		state = "closed"
	}
	title, _ := truncatePullRequestInspectionText(pr.Issue.Title, MaxPullRequestInspectionTitleBytes)
	metadata := InspectionMetadata{
		ID: pr.ID, Index: pr.Index, Title: title, State: state, IsDraft: issues_model.HasWorkInProgressPrefix(pr.Issue.Title), IsLocked: pr.Issue.IsLocked,
		CreatedAt: pr.Issue.CreatedUnix.AsTime(), UpdatedAt: pr.Issue.UpdatedUnix.AsTime(), BaseBranch: pr.BaseBranch, HeadBranch: pr.HeadBranch,
	}
	if pr.Issue.Poster != nil {
		metadata.Author = pr.Issue.Poster.Name
	}
	if pr.Issue.ClosedUnix > 0 {
		closed := pr.Issue.ClosedUnix.AsTime()
		metadata.ClosedAt = &closed
	}
	if pr.HasMerged && pr.MergedUnix > 0 {
		merged := pr.MergedUnix.AsTime()
		metadata.MergedAt = &merged
	}
	return metadata
}

func projectPullRequestInspectionFile(file *gitdiff.DiffFile) InspectionFile {
	return InspectionFile{
		Name: file.Name, OldName: file.OldName, Type: file.Type, Addition: file.Addition, Deletion: file.Deletion,
		EntryMode: file.EntryMode, OldEntryMode: file.OldEntryMode, IsBinary: file.IsBin, IsLFS: file.IsLFSFile,
		IsRenamed: file.IsRenamed, IsSubmodule: file.IsSubmodule,
	}
}

func missingPullRequestInspectionContexts(statuses []*git_model.CommitStatus, requiredContexts []string) []string {
	missing := make([]string, 0)
	for _, required := range requiredContexts {
		matcher := requiredContextMatcher(required)
		found := false
		for _, status := range statuses {
			if matcher(status.Context) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, required)
		}
	}
	return missing
}

func requiredContextMatcher(required string) func(string) bool {
	gp, err := globCompile(required)
	if err != nil {
		// Legacy contexts created before glob validation still use exact matching.
		return func(actual string) bool { return actual == required }
	}
	return gp
}

func globCompile(pattern string) (func(string) bool, error) {
	gp, err := glob.Compile(pattern)
	if err != nil {
		return nil, err
	}
	return gp.Match, nil
}

func encodePullRequestInspectionCursor(kind string, repoID, prID int64, revisions InspectionRevisions, nextFile string) (string, error) {
	payload, err := json.Marshal(pullRequestInspectionCursor{
		Version: pullRequestInspectionCursorVersion, Kind: kind, RepositoryID: repoID, PullRequestID: prID,
		InternalHead: revisions.InternalHead, Target: revisions.Target, ComparisonBase: revisions.ComparisonBase, NextFile: nextFile,
	})
	if err != nil {
		return "", fmt.Errorf("marshal inspection cursor: %w", err)
	}
	mac := hmac.New(sha256.New, []byte(setting.SecretKey))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodePullRequestInspectionCursor(encoded, kind string, repoID, prID int64, revisions InspectionRevisions) (string, error) {
	if encoded == "" {
		return "", nil
	}
	payloadPart, signaturePart, ok := strings.Cut(encoded, ".")
	if !ok {
		return "", ErrPullRequestInspectionCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return "", ErrPullRequestInspectionCursor
	}
	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return "", ErrPullRequestInspectionCursor
	}
	mac := hmac.New(sha256.New, []byte(setting.SecretKey))
	_, _ = mac.Write(payload)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return "", ErrPullRequestInspectionCursor
	}
	var cursor pullRequestInspectionCursor
	if json.Unmarshal(payload, &cursor) != nil || cursor.Version != pullRequestInspectionCursorVersion || cursor.Kind != kind || cursor.NextFile == "" {
		return "", ErrPullRequestInspectionCursor
	}
	if cursor.RepositoryID != repoID || cursor.PullRequestID != prID || cursor.InternalHead != revisions.InternalHead || cursor.Target != revisions.Target || cursor.ComparisonBase != revisions.ComparisonBase {
		return "", ErrPullRequestInspectionCursorStale
	}
	return cursor.NextFile, nil
}

func truncatePullRequestInspectionText(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	return strings.ToValidUTF8(value[:limit], ""), true
}

func isPullRequestInspectionMissingRevision(err error) bool {
	return errors.Is(err, util.ErrNotExist) || gitcmd.IsStderr(err, gitcmd.StderrNotValidObjectName) || gitcmd.IsStderr(err, gitcmd.StderrUnknownRevisionOrPath)
}
