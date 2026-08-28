// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issues_test

import (
	"context"
	"testing"

	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/unittest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPullReviewPolicyQueriesReturnErrors(t *testing.T) {
	require.NoError(t, unittest.PrepareTestDatabase())
	pr := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: 2})
	pb := &git_model.ProtectedBranch{
		RequiredApprovals:             1,
		BlockOnRejectedReviews:        true,
		BlockOnOfficialReviewRequests: true,
	}

	canceled, cancel := context.WithCancel(t.Context())
	cancel()

	_, err := issues_model.GetGrantedApprovalsCountWithError(canceled, pb, pr)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = issues_model.HasEnoughApprovalsWithError(canceled, pb, pr)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = issues_model.MergeBlockedByRejectedReviewWithError(canceled, pb, pr)
	assert.ErrorIs(t, err, context.Canceled)
	_, err = issues_model.MergeBlockedByOfficialReviewRequestsWithError(canceled, pb, pr)
	assert.ErrorIs(t, err, context.Canceled)

	assert.False(t, issues_model.HasEnoughApprovals(canceled, pb, pr))
	assert.True(t, issues_model.MergeBlockedByRejectedReview(canceled, pb, pr))
	assert.True(t, issues_model.MergeBlockedByOfficialReviewRequests(canceled, pb, pr))
}
