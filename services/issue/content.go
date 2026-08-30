// Copyright 2019 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"context"

	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	access_model "gitea.dev/models/perm/access"
	user_model "gitea.dev/models/user"
)

// ChangeContent changes issue content, as the given user.
func ChangeContent(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, content string, contentVersion int) error {
	if err := issue.LoadRepo(ctx); err != nil {
		return err
	}

	if user_model.IsUserBlockedBy(ctx, doer, issue.PosterID, issue.Repo.OwnerID) {
		if isAdmin, _ := access_model.IsUserRepoAdmin(ctx, issue.Repo, doer); !isAdmin {
			return user_model.ErrBlockedUser
		}
	}

	var effects []PostCommitEffect
	if err := db.WithTx(ctx, func(txCtx context.Context) error {
		result, txEffects, err := ReviseWorkIssueInTx(txCtx, issue, doer, issues_model.ConditionalIssueRevision{
			ExpectedContentVersion: &contentVersion, DesiredContent: &content,
		})
		if err != nil {
			return err
		}
		*issue = *result.Issue
		effects = txEffects
		return nil
	}); err != nil {
		return err
	}
	for _, effect := range effects {
		effect.Run(ctx)
	}

	return nil
}
