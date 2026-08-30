// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"context"

	mcpwork_model "gitea.dev/models/mcpwork"
	issue_service "gitea.dev/services/issue"
	mcpwork_service "gitea.dev/services/mcpwork"
)

// Presence is the desired state of a set-oriented plan fact.
type Presence string

const (
	PresencePresent Presence = "present"
	PresenceAbsent  Presence = "absent"
)

// PlanningState is the semantic lifecycle vocabulary accepted by mutations.
type PlanningState string

const (
	PlanningStateDraft  PlanningState = "draft"
	PlanningStateActive PlanningState = "active"
)

// ItemState is the desired lifecycle state of an Issue.
type ItemState string

const (
	ItemStateOpen   ItemState = "open"
	ItemStateClosed ItemState = "closed"
)

// BeginPlanRequest creates a draft Project when ExistingProjectID is zero, or
// opts one disabled repository Project into draft.
type BeginPlanRequest struct {
	RepositoryID      int64
	ExistingProjectID int64
	Title             string
	Markdown          string
}

// RepositoryLocator identifies the canonical repository boundary resolved by
// interface adapters before a Work mutation reaches native facts.
type RepositoryLocator struct {
	Owner string
	Name  string
}

// ConditionalText protects an overwriting string mutation.
type ConditionalText struct {
	Expected string
	Desired  string
}

// ConditionalMarkdown protects the native Issue content version.
type ConditionalMarkdown struct {
	ExpectedContentVersion int
	Desired                string
}

// ItemRevisionRequest conditionally revises one non-pull Issue.
type ItemRevisionRequest struct {
	RepositoryID int64
	IssueNumber  int64
	Title        *ConditionalText
	Markdown     *ConditionalMarkdown
	DesiredState *ItemState
}

// ItemSelector identifies an existing repository Issue or an earlier local
// creation in the same bounded plan revision.
type ItemSelector struct {
	IssueNumber    int64
	LocalReference string
}

// PlanChangeKind is the closed ADR 0004 plan-change union.
type PlanChangeKind string

const (
	PlanChangeEnsureMember     PlanChangeKind = "ensure_member"
	PlanChangeCreateMember     PlanChangeKind = "create_member"
	PlanChangeEnsureDependency PlanChangeKind = "ensure_dependency"
	PlanChangeSetPlanningState PlanChangeKind = "set_planning_state"
	PlanChangeDeleteDraft      PlanChangeKind = "delete_draft"
)

// PlanChange contains only the fields used by its Kind. Validation rejects
// ambiguous, duplicate, or contradictory targets before native mutation.
type PlanChange struct {
	Kind           PlanChangeKind
	WorkItem       ItemSelector
	Presence       Presence
	LocalReference string
	Title          string
	Markdown       string
	Blocked        ItemSelector
	Prerequisite   ItemSelector
	ExpectedState  PlanningState
	DesiredState   PlanningState
}

// PlanRevisionRequest atomically applies one bounded, plan-centered revision.
type PlanRevisionRequest struct {
	RepositoryID int64
	ProjectID    int64
	// ExpectedPlanToken is enforced for lifecycle changes and draft deletion;
	// set-only revisions accept but ignore it.
	ExpectedPlanToken string
	Changes           []PlanChange
}

// MutationCommit is returned from a WP7 receipt callback. Completion and every
// native timeline/provenance reference belong to the transaction. RunPostCommit
// must run only for a newly applied committed receipt, never for replay,
// rejection, rollback, or an unchanged set operation.
type MutationCommit struct {
	Completion        mcpwork_service.Completion
	CreatedReferences map[string]string
	Effects           []issue_service.PostCommitEffect
	dispatchEffect    func(context.Context, issue_service.PostCommitEffect)
}

// ReceiptMutation is WP4's transaction-local result layered on WP7's frozen
// mutation callback. The returned effects remain inert in the callback.
type ReceiptMutation func(context.Context, mcpwork_service.Operation) (MutationCommit, error)

// ApplyReceiptMutation adapts a Work mutation to WP7's frozen callback and
// dispatches effects only after a newly applied receipt transaction commits.
func ApplyReceiptMutation(ctx context.Context, receipts *mcpwork_service.Service, request mcpwork_service.Request, mutate ReceiptMutation) (*mcpwork_service.Result, MutationCommit, error) {
	var commit MutationCommit
	result, err := receipts.Execute(ctx, request, func(txCtx context.Context, operation mcpwork_service.Operation) (mcpwork_service.Completion, error) {
		var mutationErr error
		commit, mutationErr = mutate(txCtx, operation)
		return commit.Completion, mutationErr
	})
	if err != nil {
		return nil, MutationCommit{}, err
	}
	if !result.Replayed && result.Outcome == mcpwork_model.OutcomeApplied {
		commit.RunPostCommit(ctx)
	}
	return result, commit, nil
}

// RunPostCommit dispatches existing notification, webhook, and index notifier
// fanout. It intentionally provides no rollback signal for committed state.
func (commit MutationCommit) RunPostCommit(ctx context.Context) {
	dispatch := commit.dispatchEffect
	if dispatch == nil {
		dispatch = func(ctx context.Context, effect issue_service.PostCommitEffect) {
			effect.Run(ctx)
		}
	}
	for _, effect := range commit.Effects {
		dispatch(ctx, effect)
	}
}
