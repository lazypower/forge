// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package work composes authoritative Work projections from native Forge facts.
package work

import (
	"context"
	"errors"
	"fmt"

	user_model "gitea.dev/models/user"
)

// ReadFailureKind is a transport-neutral Work inspection failure.
type ReadFailureKind string

const (
	ReadUnavailable       ReadFailureKind = "unavailable"
	ReadInvalidInput      ReadFailureKind = "invalid_input"
	ReadInvalidCursor     ReadFailureKind = "invalid_cursor"
	ReadInvalidDependency ReadFailureKind = "invalid_dependency"
	ReadLimitExceeded     ReadFailureKind = "limit_exceeded"
)

var (
	ErrUnavailable       = errors.New("work inspection unavailable")
	ErrInvalidInput      = errors.New("invalid work inspection input")
	ErrInvalidCursor     = errors.New("invalid work inspection cursor")
	ErrInvalidDependency = errors.New("invalid work dependency")
	ErrLimitExceeded     = errors.New("work inspection limit exceeded")
)

// ReadFailure classifies a safe error without exposing its underlying cause.
type ReadFailure struct {
	Kind  ReadFailureKind
	Cause error
}

func (failure *ReadFailure) Error() string { return fmt.Sprintf("work read failed: %s", failure.Kind) }
func (failure *ReadFailure) Unwrap() error { return failure.Cause }

// Reader is the stable read contract for human and protocol adapters.
type Reader interface {
	InspectItem(context.Context, *user_model.User, ItemRequest) (*ItemInspection, error)
	InspectPlan(context.Context, *user_model.User, PlanRequest) (*PlanInspection, error)
}

// ItemRequest identifies one Issue-centered projection.
type ItemRequest struct {
	Owner             string
	Repository        string
	IssueNumber       int64
	SelectedProjectID int64
	PageKind          string
	Limit             int
	Cursor            string
}

// PlanRequest identifies one repository Project projection.
type PlanRequest struct {
	Owner      string
	Repository string
	ProjectID  int64
	PageKind   string
	Limit      int
	Cursor     string
}

// Repository is the canonical locator returned with a readable Work object.
type Repository struct {
	Owner string
	Name  string
	URL   string
}

// Reference is one permission-filtered native object.
type Reference struct {
	Availability string
	Repository   *Repository
	Ref          string
	URL          string
	Label        string
	State        string
}

// IntegrityConcern is one safe, disclosed composition concern.
type IntegrityConcern struct {
	Code    string
	Message string
}

// Integrity reports whether a bounded graph was composed completely.
type Integrity struct {
	Status   string
	Concerns []IntegrityConcern
}

// Delivery is one effective closing pull request at its frozen revision.
type Delivery struct {
	Repository Repository
	Ref        string
	URL        string
	State      string
	Revision   string
	CheckState string
}

// ContextSummary is the state of one Issue in one planning Project.
type ContextSummary struct {
	Ref             string
	WorkPlan        string
	DerivedState    string
	IntegrityStatus string
}

// Edge is one blocked-Issue to prerequisite relationship.
type Edge struct {
	Blocked      Reference
	Prerequisite Reference
}

// WorkItem is the ADR 0003 Issue-centered projection.
//
//nolint:revive // WorkItem is the canonical domain name defined by ADR 0003.
type WorkItem struct {
	Ref                   string
	URL                   string
	Title                 string
	Markdown              string
	ContentVersion        int64
	State                 string
	Classification        string
	ContextSummaries      []ContextSummary
	ProjectMemberships    []Reference
	PrerequisiteSummaries []Reference
	DependentSummaries    []Reference
	DeliverySummaries     []Delivery
}

// PlanContext is the ADR 0003 state of one WorkItem in one WorkPlan.
type PlanContext struct {
	Ref                   string
	WorkPlan              string
	WorkItem              string
	DerivedState          string
	Integrity             Integrity
	PrerequisiteSummaries []Reference
	DeliverySummaries     []Delivery
}

// WorkPlan is the ADR 0003 repository Project projection.
//
//nolint:revive // WorkPlan is the canonical domain name defined by ADR 0003.
type WorkPlan struct {
	Ref             string
	URL             string
	Title           string
	Markdown        string
	PlanningState   string
	ProjectState    string
	Integrity       Integrity
	ItemSummaries   []ContextSummary
	EdgeSummaries   []Edge
	ReadyFrontier   []ContextSummary
	ExcludedMembers []Reference
	PlanToken       string
}

// Page is explicitly non-snapshot and must be reinspected before action.
type Page struct {
	Kind                  string
	Items                 []any
	NextCursor            string
	SnapshotConsistency   string
	ReinspectBeforeAction bool
}

// ItemInspection is one available Issue-centered result.
type ItemInspection struct {
	Repository      Repository
	WorkItem        WorkItem
	SelectedContext *PlanContext
	Page            Page
}

// PlanInspection is one available Project-centered result.
type PlanInspection struct {
	Repository Repository
	WorkPlan   WorkPlan
	Page       Page
}
