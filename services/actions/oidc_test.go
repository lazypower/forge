// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"net/url"
	"strings"
	"testing"

	actions_model "code.gitea.io/gitea/models/actions"
	"code.gitea.io/gitea/models/db"
	"code.gitea.io/gitea/models/perm"
	repo_model "code.gitea.io/gitea/models/repo"
	"code.gitea.io/gitea/models/unittest"
	actions_module "code.gitea.io/gitea/modules/actions"
	"code.gitea.io/gitea/modules/git"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/test"
	webhook_module "code.gitea.io/gitea/modules/webhook"

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
		{"missing", url.Values{}, false},
		{"empty", url.Values{"audience": {""}}, false},
		{"duplicate", url.Values{"audience": {"one", "two"}}, false},
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
	jobContext, err := generateTaskContext(task)
	require.NoError(t, err)
	assert.NotContains(t, jobContext.AsMap(), "actions_id_token_request_url")
	assert.NotContains(t, jobContext.AsMap(), "actions_id_token_request_token")
	permissions.IDTokenAccessMode = perm.AccessModeWrite
	task.Job.TokenPermissions = &permissions
	_, err = db.GetEngine(t.Context()).ID(task.Job.ID).Cols("token_permissions").Update(task.Job)
	require.NoError(t, err)
	jobContext, err = generateTaskContext(task)
	require.NoError(t, err)
	assert.Equal(t, OIDCTokenRequestURL(), jobContext.AsMap()["actions_id_token_request_url"])
	assert.Equal(t, task.Token, jobContext.AsMap()["actions_id_token_request_token"])
}
