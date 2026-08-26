// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"net/url"
	"strings"
	"testing"

	actions_model "gitea.dev/models/actions"
	"gitea.dev/models/db"
	"gitea.dev/models/perm"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	actions_module "gitea.dev/modules/actions"
	"gitea.dev/modules/git"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	webhook_module "gitea.dev/modules/webhook"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateOIDCAudience(t *testing.T) {
	tests := []struct {
		name  string
		query url.Values
		valid bool
	}{
		{"valid", url.Values{"audience": {"vault.example"}}, true},
		{"valid toolkit request", url.Values{"api-version": {actionsOIDCAPIVersion}, "audience": {"vault.example"}}, true},
		{"missing", url.Values{}, false},
		{"compatibility parameter only", url.Values{"api-version": {actionsOIDCAPIVersion}}, false},
		{"empty", url.Values{"audience": {""}}, false},
		{"duplicate", url.Values{"audience": {"one", "two"}}, false},
		{"duplicate compatibility parameter", url.Values{"api-version": {actionsOIDCAPIVersion, actionsOIDCAPIVersion}, "audience": {"vault"}}, false},
		{"wrong compatibility version", url.Values{"api-version": {"1.0"}, "audience": {"vault"}}, false},
		{"unknown parameter", url.Values{"audience": {"vault"}, "run_id": {"1"}}, false},
		{"leading whitespace", url.Values{"audience": {" vault"}}, false},
		{"control character", url.Values{"audience": {"vault\nrole"}}, false},
		{"too long", url.Values{"audience": {strings.Repeat("a", maxOIDCAudienceByteCount+1)}}, false},
		{"invalid UTF-8", url.Values{"audience": {string([]byte{0xff})}}, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			audience, err := ValidateOIDCAudience(test.query)
			if test.valid {
				require.NoError(t, err)
				assert.Equal(t, test.query.Get("audience"), audience)
			} else {
				require.ErrorIs(t, err, ErrInvalidOIDCAudience)
				assert.Empty(t, audience)
			}
		})
	}
}

func TestValidateOIDCTask(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	_, err := db.GetEngine(t.Context()).ID(196).Cols("status").Update(&actions_model.ActionRunJob{Status: actions_model.StatusRunning})
	require.NoError(t, err)
	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 51})
	require.NoError(t, ValidateOIDCTask(t.Context(), task))

	tests := []struct {
		name    string
		mutate  func()
		restore func()
	}{
		{"completed task", func() { task.Status = actions_model.StatusSuccess }, func() { task.Status = actions_model.StatusRunning }},
		{"cancelled task", func() { task.Status = actions_model.StatusCancelled }, func() { task.Status = actions_model.StatusRunning }},
		{"cancelling job", func() { task.Job.Status = actions_model.StatusWaiting }, func() { task.Job.Status = actions_model.StatusRunning }},
		{"completed run", func() { task.Job.Run.Status = actions_model.StatusSuccess }, func() { task.Job.Run.Status = actions_model.StatusRunning }},
		{"superseded task", func() { task.Job.TaskID++ }, func() { task.Job.TaskID-- }},
		{"wrong job", func() { task.JobID++ }, func() { task.JobID-- }},
		{"wrong repository", func() { task.RepoID++ }, func() { task.RepoID-- }},
		{"old attempt", func() { task.Attempt-- }, func() { task.Attempt++ }},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			testCase.mutate()
			defer testCase.restore()
			require.Error(t, ValidateOIDCTask(t.Context(), task))
		})
	}
}

func TestResolveOIDCRefsForUntrustedPullRequest(t *testing.T) {
	run := &actions_model.ActionRun{
		Ref:               "refs/pull/17/head",
		CommitSHA:         "trigger-sha",
		IsForkPullRequest: true,
		Event:             webhook_module.HookEventPullRequest,
		EventPayload:      `{"pull_request":{"base":{"label":"main","ref":"main","sha":"base-sha"}}}`,
		TriggerEvent:      actions_module.GithubEventPullRequest,
	}

	ref, sha, refType := resolveOIDCRefs(run)
	assert.Equal(t, "refs/pull/17/head", ref)
	assert.Equal(t, "trigger-sha", sha)
	assert.Equal(t, string(git.RefTypeBranch), refType)

	run.TriggerEvent = actions_module.GithubEventPullRequestTarget
	ref, sha, refType = resolveOIDCRefs(run)
	assert.Equal(t, "refs/heads/main", ref)
	assert.Equal(t, "base-sha", sha)
	assert.Equal(t, string(git.RefTypeBranch), refType)
}

func TestResolveOIDCWorkflowRejectsMissingSourceAndReusableJobs(t *testing.T) {
	run := &actions_model.ActionRun{WorkflowRepoID: 2, WorkflowCommitSHA: "workflow-sha"}

	_, _, _, err := resolveOIDCWorkflow(t.Context(), &actions_model.ActionRun{}, &actions_model.ActionRunJob{})
	require.ErrorContains(t, err, "no authoritative workflow source")

	jobs := []*actions_model.ActionRunJob{
		{ParentJobID: 1, WorkflowSourceRepoID: 2, WorkflowSourceCommitSHA: "workflow-sha"},
		{WorkflowSourceRepoID: 3, WorkflowSourceCommitSHA: "workflow-sha"},
		{WorkflowSourceRepoID: 2, WorkflowSourceCommitSHA: "other-sha"},
	}
	for _, job := range jobs {
		_, _, _, err := resolveOIDCWorkflow(t.Context(), run, job)
		require.ErrorContains(t, err, "does not support reusable workflow jobs")
	}
}

func TestOIDCTokenPermissionDefenseInDepth(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	defer test.MockVariableValue(&setting.Actions.WorkloadIdentityEnabled, true)()
	_, err := db.GetEngine(t.Context()).ID(196).Cols("status").Update(&actions_model.ActionRunJob{Status: actions_model.StatusRunning})
	require.NoError(t, err)
	task := unittest.AssertExistsAndLoadBean(t, &actions_model.ActionTask{ID: 51})
	require.NoError(t, task.LoadJob(t.Context()))
	permissions := repo_model.MakeActionsTokenPermissions(perm.AccessModeNone)
	task.Job.TokenPermissions = &permissions
	_, err = db.GetEngine(t.Context()).ID(task.Job.ID).Cols("token_permissions").Update(task.Job)
	require.NoError(t, err)
	_, err = CreateOIDCToken(t.Context(), task, "vault")
	require.ErrorContains(t, err, "lacks id-token write permission")

	task.Token = strings.Repeat("a", 40)
	jobContext, err := generateTaskContext(t.Context(), task)
	require.NoError(t, err)
	assert.NotContains(t, jobContext.AsMap(), "actions_id_token_request_url")
	assert.NotContains(t, jobContext.AsMap(), "actions_id_token_request_token")
	permissions.IDTokenAccessMode = perm.AccessModeWrite
	task.Job.TokenPermissions = &permissions
	_, err = db.GetEngine(t.Context()).ID(task.Job.ID).Cols("token_permissions").Update(task.Job)
	require.NoError(t, err)
	jobContext, err = generateTaskContext(t.Context(), task)
	require.NoError(t, err)
	assert.Equal(t, OIDCTokenRequestURL(), jobContext.AsMap()["actions_id_token_request_url"])
	assert.Equal(t, task.Token, jobContext.AsMap()["actions_id_token_request_token"])
}
