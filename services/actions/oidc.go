// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	actions_module "gitea.dev/modules/actions"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/setting"
	"gitea.dev/services/oauth2_provider"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	actionsOIDCPath          = "/api/actions/oidc"
	actionsOIDCTokenPath     = actionsOIDCPath + "/token"
	actionsOIDCAPIVersion    = "2.0"
	actionsOIDCTokenExpiry   = 5 * time.Minute
	actionsOIDCClockSkew     = 30 * time.Second
	maxOIDCAudienceByteCount = 255
)

var ErrInvalidOIDCAudience = errors.New("invalid OIDC audience")

type actionsOIDCClaims struct {
	jwt.RegisteredClaims
	Actor                string `json:"actor"`
	ActorID              int64  `json:"actor_id"`
	Repository           string `json:"repository"`
	RepositoryID         int64  `json:"repository_id"`
	RepositoryOwner      string `json:"repository_owner"`
	RepositoryOwnerID    int64  `json:"repository_owner_id"`
	RunID                int64  `json:"run_id"`
	RunNumber            int64  `json:"run_number"`
	RunAttempt           int64  `json:"run_attempt"`
	Workflow             string `json:"workflow"`
	WorkflowRef          string `json:"workflow_ref"`
	WorkflowSHA          string `json:"workflow_sha"`
	WorkflowRepository   string `json:"workflow_repository"`
	WorkflowRepositoryID int64  `json:"workflow_repository_id"`
	EventName            string `json:"event_name"`
	Ref                  string `json:"ref"`
	RefType              string `json:"ref_type"`
	SHA                  string `json:"sha"`
	Job                  string `json:"job"`
}

func OIDCIssuer() string { return strings.TrimSuffix(setting.AppURL, "/") + actionsOIDCPath }
func OIDCTokenRequestURL() string {
	return strings.TrimSuffix(setting.AppURL, "/") + actionsOIDCTokenPath + "?api-version=" + actionsOIDCAPIVersion
}
func OIDCTokenExpiry() time.Duration { return actionsOIDCTokenExpiry }

func ValidateOIDCAudience(query url.Values) (string, error) {
	values, ok := query["audience"]
	if !ok || len(values) != 1 || len(query) < 1 || len(query) > 2 {
		return "", ErrInvalidOIDCAudience
	}
	if len(query) == 2 {
		versions, ok := query["api-version"]
		if !ok || len(versions) != 1 || versions[0] != actionsOIDCAPIVersion {
			return "", ErrInvalidOIDCAudience
		}
	}
	audience := values[0]
	if audience == "" || len(audience) > maxOIDCAudienceByteCount || !utf8.ValidString(audience) || strings.TrimSpace(audience) != audience {
		return "", ErrInvalidOIDCAudience
	}
	for _, r := range audience {
		if unicode.IsControl(r) {
			return "", ErrInvalidOIDCAudience
		}
	}
	return audience, nil
}

func TaskAllowsOIDCToken(ctx context.Context, task *actions_model.ActionTask) (bool, error) {
	if err := task.LoadJob(ctx); err != nil {
		return false, err
	}
	if err := task.Job.LoadRepo(ctx); err != nil {
		return false, err
	}
	if err := task.Job.Repo.LoadOwner(ctx); err != nil {
		return false, err
	}
	repoCfg := task.Job.Repo.MustGetUnit(ctx, unit.TypeActions).ActionsConfig()
	ownerCfg, err := actions_model.GetOwnerActionsConfig(ctx, task.Job.Repo.OwnerID)
	if err != nil {
		return false, err
	}
	var declared repo_model.ActionsTokenPermissions
	if task.Job.TokenPermissions != nil {
		declared = *task.Job.TokenPermissions
	} else if repoCfg.OverrideOwnerConfig {
		declared = repoCfg.GetDefaultTokenPermissions()
	} else {
		declared = ownerCfg.GetDefaultTokenPermissions()
	}
	if repoCfg.OverrideOwnerConfig {
		declared = repoCfg.ClampPermissions(declared)
	} else {
		declared = ownerCfg.ClampPermissions(declared)
	}
	return declared.IDTokenAccessMode >= perm.AccessModeWrite, nil
}

func ValidateOIDCTask(ctx context.Context, task *actions_model.ActionTask) error {
	if task == nil {
		return errors.New("missing Actions task")
	}
	if err := task.LoadAttributes(ctx); err != nil {
		return err
	}
	if task.Job == nil || task.Job.Run == nil || task.Status != actions_model.StatusRunning || task.Job.Status != actions_model.StatusRunning || task.Job.Run.Status != actions_model.StatusRunning || task.Job.TaskID != task.ID || task.JobID != task.Job.ID || task.RepoID != task.Job.RepoID || task.Attempt <= 0 || task.Attempt != task.Job.Attempt {
		return errors.New("Actions task is not the current running attempt")
	}
	return nil
}

func CreateOIDCToken(ctx context.Context, task *actions_model.ActionTask, audience string) (string, error) {
	if !setting.Actions.WorkloadIdentityEnabled {
		return "", errors.New("Actions workload identity is disabled")
	}
	validatedAudience, err := ValidateOIDCAudience(url.Values{"audience": {audience}})
	if err != nil {
		return "", err
	}
	if err := ValidateOIDCTask(ctx, task); err != nil {
		return "", err
	}
	allowed, err := TaskAllowsOIDCToken(ctx, task)
	if err != nil {
		return "", err
	}
	if !allowed {
		return "", errors.New("Actions task lacks id-token write permission")
	}
	signingKey := oauth2_provider.DefaultSigningKey
	if signingKey == nil || signingKey.IsSymmetric() {
		return "", errors.New("missing asymmetric OIDC signing key")
	}
	run := task.Job.Run
	if err := run.Repo.LoadOwner(ctx); err != nil {
		return "", err
	}
	ref, sha, refType := resolveOIDCRefs(run)
	workflowRepository, workflowPath, workflowSHA, err := resolveOIDCWorkflow(ctx, run, task.Job)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := &actionsOIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{Issuer: OIDCIssuer(), Subject: fmt.Sprintf("repo:%d/%d:ref:%s", run.Repo.OwnerID, run.Repo.ID, url.PathEscape(ref)), Audience: jwt.ClaimStrings{validatedAudience}, ExpiresAt: jwt.NewNumericDate(now.Add(actionsOIDCTokenExpiry)), NotBefore: jwt.NewNumericDate(now.Add(-actionsOIDCClockSkew)), IssuedAt: jwt.NewNumericDate(now), ID: uuid.NewString()},
		Actor:            run.TriggerUser.Name, ActorID: run.TriggerUser.ID, Repository: run.Repo.FullName(), RepositoryID: run.Repo.ID, RepositoryOwner: run.Repo.OwnerName, RepositoryOwnerID: run.Repo.OwnerID,
		RunID: run.ID, RunNumber: run.Index, RunAttempt: task.Attempt, Workflow: workflowPath, WorkflowRef: fmt.Sprintf("%s/%s@%s", workflowRepository.FullName(), workflowPath, workflowSHA), WorkflowSHA: workflowSHA,
		WorkflowRepository: workflowRepository.FullName(), WorkflowRepositoryID: workflowRepository.ID,
		EventName: run.TriggerEvent, Ref: ref, RefType: refType, SHA: sha, Job: task.Job.JobID,
	}
	token := jwt.NewWithClaims(signingKey.SigningMethod(), claims)
	signingKey.PreProcessToken(token)
	return token.SignedString(signingKey.SignKey())
}

func resolveOIDCWorkflow(ctx context.Context, run *actions_model.ActionRun, job *actions_model.ActionRunJob) (*repo_model.Repository, string, string, error) {
	if run.WorkflowRepoID <= 0 || run.WorkflowCommitSHA == "" {
		return nil, "", "", errors.New("Actions run has no authoritative workflow source")
	}
	if job.ParentJobID != 0 || job.WorkflowSourceRepoID != run.WorkflowRepoID || job.WorkflowSourceCommitSHA != run.WorkflowCommitSHA {
		return nil, "", "", errors.New("Actions workload identity does not support reusable workflow jobs")
	}
	workflowRepository, err := repo_model.GetRepositoryByID(ctx, run.WorkflowRepoID)
	if err != nil {
		return nil, "", "", err
	}
	if err := workflowRepository.LoadOwner(ctx); err != nil {
		return nil, "", "", err
	}
	gitRepo, err := gitrepo.OpenRepository(ctx, workflowRepository)
	if err != nil {
		return nil, "", "", err
	}
	defer gitRepo.Close()
	commit, err := gitRepo.GetCommit(run.WorkflowCommitSHA)
	if err != nil {
		return nil, "", "", err
	}
	var workflowDir string
	var entries git.Entries
	if run.IsScopedRun {
		workflowDir, entries, err = actions_module.ListScopedWorkflows(commit)
	} else {
		workflowDir, entries, err = actions_module.ListWorkflows(commit)
	}
	if err != nil {
		return nil, "", "", err
	}
	for _, entry := range entries {
		if entry.Name() == run.WorkflowID {
			return workflowRepository, path.Join(workflowDir, run.WorkflowID), run.WorkflowCommitSHA, nil
		}
	}
	return nil, "", "", fmt.Errorf("workflow %q not found in source repository %d at commit %s", run.WorkflowID, run.WorkflowRepoID, run.WorkflowCommitSHA)
}

func resolveOIDCRefs(run *actions_model.ActionRun) (ref, sha, refType string) {
	ref, sha = run.Ref, run.CommitSHA
	if payload, err := run.GetPullRequestEventPayload(); err == nil && payload.PullRequest != nil && payload.PullRequest.Base != nil && run.TriggerEvent == actions_module.GithubEventPullRequestTarget {
		ref, sha = git.BranchPrefix+payload.PullRequest.Base.Name, payload.PullRequest.Base.Sha
	}
	refName := git.RefName(ref)
	refType = string(refName.RefType())
	if refName.IsPull() {
		refType = string(git.RefTypeBranch)
	}
	return ref, sha, refType
}
