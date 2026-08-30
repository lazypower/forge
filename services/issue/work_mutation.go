// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package issue

import (
	"context"

	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	user_model "gitea.dev/models/user"
	notify_service "gitea.dev/services/notify"
)

// PostCommitEffect describes one existing notifier call that must run only
// after the surrounding domain transaction commits.
type PostCommitEffect struct {
	kind       string
	doer       *user_model.User
	issue      *issues_model.Issue
	comment    *issues_model.Comment
	mentions   []*user_model.User
	oldTitle   string
	oldContent string
	closed     bool
}

// Run dispatches the existing notifier fanout. Notifiers own their error
// logging; a delivery failure cannot turn committed domain state into failure.
func (effect PostCommitEffect) Run(ctx context.Context) {
	switch effect.kind {
	case "new":
		notify_service.NewIssue(ctx, effect.issue, effect.mentions)
	case "title":
		notify_service.IssueChangeTitle(ctx, effect.doer, effect.issue, effect.oldTitle)
	case "content":
		notify_service.IssueChangeContent(ctx, effect.doer, effect.issue, effect.oldContent)
	case "status":
		notify_service.IssueChangeStatus(ctx, effect.doer, "", effect.issue, effect.comment, effect.closed)
	}
}

// NewWorkIssueInTx creates one ordinary Issue and persists mention relations in
// the caller-owned Work transaction. It intentionally excludes labels,
// attachments, assignments, and templates from the bounded Work contract.
func NewWorkIssueInTx(ctx context.Context, repo *repo_model.Repository, issue *issues_model.Issue) (PostCommitEffect, error) {
	if err := issue.LoadPoster(ctx); err != nil {
		return PostCommitEffect{}, err
	}
	if user_model.IsUserBlockedBy(ctx, issue.Poster, repo.OwnerID) {
		return PostCommitEffect{}, user_model.ErrBlockedUser
	}
	if err := issues_model.NewIssue(ctx, repo, issue, nil, nil); err != nil {
		return PostCommitEffect{}, err
	}
	mentions, err := issues_model.FindAndUpdateIssueMentions(ctx, issue, issue.Poster, issue.Content)
	if err != nil {
		return PostCommitEffect{}, err
	}
	return PostCommitEffect{kind: "new", issue: issue, mentions: mentions}, nil
}

// ReviseWorkIssueInTx conditionally changes title and Markdown and returns
// timeline provenance plus post-commit notifier effects.
func ReviseWorkIssueInTx(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, revision issues_model.ConditionalIssueRevision) (*issues_model.ConditionalIssueRevisionResult, []PostCommitEffect, error) {
	result, err := issues_model.ReviseIssueConditionally(ctx, issue.ID, doer, revision)
	if err != nil {
		return nil, nil, err
	}
	effects := make([]PostCommitEffect, 0, 2)
	if result.TitleChanged {
		effects = append(effects, PostCommitEffect{kind: "title", doer: doer, issue: result.Issue, oldTitle: result.OldTitle})
	}
	if result.BodyChanged {
		effects = append(effects, PostCommitEffect{kind: "content", doer: doer, issue: result.Issue, oldContent: result.OldContent})
	}
	return result, effects, nil
}

// SetWorkIssueStateInTx applies close or reopen as a desired-state operation.
func SetWorkIssueStateInTx(ctx context.Context, issue *issues_model.Issue, doer *user_model.User, closed bool) (*issues_model.Comment, bool, *PostCommitEffect, error) {
	comment, changed, err := issues_model.SetIssueStateInWorkTx(ctx, issue, doer, closed)
	if err != nil || !changed {
		return comment, changed, nil, err
	}
	if closed {
		if _, err := issues_model.FinishIssueStopwatch(ctx, doer, issue); err != nil {
			return nil, false, nil, err
		}
	}
	effect := &PostCommitEffect{kind: "status", doer: doer, issue: issue, comment: comment, closed: closed}
	return comment, true, effect, nil
}
