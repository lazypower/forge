// Copyright 2020 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package repo

import (
	"testing"

	"gitea.dev/models/unittest"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/assert"
)

func TestCheckProjectColumnChangePermissions(t *testing.T) {
	unittest.PrepareTestEnv(t)
	ctx, _ := contexttest.MockContext(t, "user2/repo1/projects/1/2")
	contexttest.LoadUser(t, ctx, 2)
	contexttest.LoadRepo(t, ctx, 1)
	ctx.SetPathParam("id", "1")
	ctx.SetPathParam("columnID", "2")

	project, column := checkProjectColumnChangePermissions(ctx)
	assert.NotNil(t, project)
	assert.NotNil(t, column)
	assert.False(t, ctx.Written())
}

func TestWorkMutationProblemLocaleKey(t *testing.T) {
	for _, problemCode := range []string{
		"invalid_input", "unavailable", "not_permitted", "conflict", "invalid_plan", "invalid_dependency", "limit_exceeded",
	} {
		assert.Equal(t, "repo.projects.work_planning.problem."+problemCode, workMutationProblemLocaleKey(problemCode))
	}
	assert.Equal(t, "repo.projects.work_planning.problem.mutation_failed", workMutationProblemLocaleKey("future_internal_code"))
	assert.Equal(t, "repo.projects.work_planning.problem.mutation_failed", workMutationProblemLocaleKey(""))
}
