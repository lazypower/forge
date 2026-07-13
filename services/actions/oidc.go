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

	actions_model "code.gitea.io/gitea/models/actions"
	"code.gitea.io/gitea/models/perm"
	repo_model "code.gitea.io/gitea/models/repo"
	"code.gitea.io/gitea/models/unit"
	actions_module "code.gitea.io/gitea/modules/actions"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/gitrepo"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/services/oauth2_provider"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

const (
	actionsOIDCPath          = "/api/actions/oidc"
	actionsOIDCTokenPath     = actionsOIDCPath + "/token"
	actionsOIDCTokenExpiry   = 5 * time.Minute
	actionsOIDCClockSkew     = 30 * time.Second
	maxOIDCAudienceByteCount = 255
)

var ErrInvalidOIDCAudience = errors.New("invalid OIDC audience")

type actionsOIDCClaims struct {
	jwt.RegisteredClaims
	Actor             string `json:"actor"`
	ActorID           int64  `json:"actor_id"`
	Repository        string `json:"repository"`
	RepositoryID      int64  `json:"repository_id"`
	RepositoryOwner   string `json:"repository_owner"`
	RepositoryOwnerID int64  `json:"repository_owner_id"`
	RunID             int64  `json:"run_id"`
	RunNumber         int64  `json:"run_number"`
	RunAttempt        int64  `json:"run_attempt"`
	Workflow          string `json:"workflow"`
	WorkflowRef       string `json:"workflow_ref"`
	WorkflowSHA       string `json:"workflow_sha"`
	EventName         string `json:"event_name"`
	Ref               string `json:"ref"`
	RefType           string `json:"ref_type"`
	SHA               string `json:"sha"`
	Job               string `json:"job"`
}

func OIDCIssuer() string { return strings.TrimSuffix(setting.AppURL, "/") + actionsOIDCPath }
func OIDCTokenRequestURL() string {
	return strings.TrimSuffix(setting.AppURL, "/") + actionsOIDCTokenPath
}
func OIDCTokenExpiry() time.Duration { return actionsOIDCTokenExpiry }

func ValidateOIDCAudience(query url.Values) (string, error) {
	values, ok := query["audience"]
	if len(query) != 1 || !ok || len(values) != 1 {
		return "", ErrInvalidOIDCAudience
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
	workflowPath, workflowSHA, err := resolveOIDCWorkflow(ctx, run)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Truncate(time.Second)
	claims := &actionsOIDCClaims{
		RegisteredClaims: jwt.RegisteredClaims{Issuer: OIDCIssuer(), Subject: fmt.Sprintf("repo:%d/%d:ref:%s", run.Repo.OwnerID, run.Repo.ID, url.PathEscape(ref)), Audience: jwt.ClaimStrings{validatedAudience}, ExpiresAt: jwt.NewNumericDate(now.Add(actionsOIDCTokenExpiry)), NotBefore: jwt.NewNumericDate(now.Add(-actionsOIDCClockSkew)), IssuedAt: jwt.NewNumericDate(now), ID: uuid.NewString()},
		Actor:            run.TriggerUser.Name, ActorID: run.TriggerUser.ID, Repository: run.Repo.FullName(), RepositoryID: run.Repo.ID, RepositoryOwner: run.Repo.OwnerName, RepositoryOwnerID: run.Repo.OwnerID,
		RunID: run.ID, RunNumber: run.Index, RunAttempt: task.Attempt, Workflow: workflowPath, WorkflowRef: fmt.Sprintf("%s/%s@%s", run.Repo.FullName(), workflowPath, workflowSHA), WorkflowSHA: workflowSHA,
		EventName: run.TriggerEvent, Ref: ref, RefType: refType, SHA: sha, Job: task.Job.JobID,
	}
	token := jwt.NewWithClaims(signingKey.SigningMethod(), claims)
	signingKey.PreProcessToken(token)
	return token.SignedString(signingKey.SignKey())
}

func resolveOIDCWorkflow(ctx context.Context, run *actions_model.ActionRun) (string, string, error) {
	gitRepo, err := gitrepo.OpenRepository(ctx, run.Repo)
	if err != nil {
		return "", "", err
	}
	defer gitRepo.Close()
	commit, err := gitRepo.GetCommit(run.CommitSHA)
	if err != nil {
		return "", "", err
	}
	workflowDir, entries, err := actions_module.ListWorkflows(commit)
	if err != nil {
		return "", "", err
	}
	for _, entry := range entries {
		if entry.Name() == run.WorkflowID {
			return path.Join(workflowDir, run.WorkflowID), run.CommitSHA, nil
		}
	}
	return "", "", fmt.Errorf("workflow %q not found at commit %s", run.WorkflowID, run.CommitSHA)
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
