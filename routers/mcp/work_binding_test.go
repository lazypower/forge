// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"testing"

	user_model "gitea.dev/models/user"
	work_service "gitea.dev/services/work"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeDomainWorkReader struct {
	item func(context.Context, *user_model.User, work_service.ItemRequest) (*work_service.ItemInspection, error)
	plan func(context.Context, *user_model.User, work_service.PlanRequest) (*work_service.PlanInspection, error)
}

func (reader fakeDomainWorkReader) InspectItem(ctx context.Context, doer *user_model.User, request work_service.ItemRequest) (*work_service.ItemInspection, error) {
	return reader.item(ctx, doer, request)
}

func (reader fakeDomainWorkReader) InspectPlan(ctx context.Context, doer *user_model.User, request work_service.PlanRequest) (*work_service.PlanInspection, error) {
	return reader.plan(ctx, doer, request)
}

func TestBoundWorkReadServiceMapsOnlyThroughDomainReader(t *testing.T) {
	var captured work_service.ItemRequest
	reader := fakeDomainWorkReader{
		item: func(_ context.Context, doer *user_model.User, request work_service.ItemRequest) (*work_service.ItemInspection, error) {
			assert.EqualValues(t, 7, doer.ID)
			captured = request
			return &work_service.ItemInspection{
				Repository: work_service.Repository{Owner: "octo", Name: "forge", URL: "https://forge.example/octo/forge"},
				WorkItem: work_service.WorkItem{
					Ref: "issue/12", URL: "https://forge.example/octo/forge/issues/12", Title: "Mapped", State: "open", Classification: "planned",
				},
				Page: work_service.Page{Kind: "memberships", Items: []any{work_service.Reference{Availability: "undisclosed"}}, SnapshotConsistency: "none", ReinspectBeforeAction: true},
			}, nil
		},
		plan: func(context.Context, *user_model.User, work_service.PlanRequest) (*work_service.PlanInspection, error) {
			return nil, errors.New("unexpected plan call")
		},
	}
	service := newBoundWorkReadService(reader)
	inspection, err := service.InspectWorkItem(t.Context(), &user_model.User{ID: 7}, WorkItemInspectRequest{
		Repository: WorkRepository{Owner: "octo", Name: "forge"}, WorkItem: "issue/12", SelectedPlan: "project/9",
		PageKind: "memberships", Page: &WorkPageRequest{Limit: 4, Cursor: "cursor"},
	})
	require.NoError(t, err)
	assert.Equal(t, work_service.ItemRequest{
		Owner: "octo", Repository: "forge", IssueNumber: 12, SelectedProjectID: 9,
		PageKind: "memberships", Limit: 4, Cursor: "cursor",
	}, captured)
	assert.Equal(t, "issue/12", inspection.WorkItem.Ref)
	require.Len(t, inspection.Page.Items, 1)
	assert.Equal(t, "undisclosed", inspection.Page.Items[0].(WorkReferenceSummary).Availability)
}

func TestBoundWorkReadServiceFailsClosed(t *testing.T) {
	called := false
	reader := fakeDomainWorkReader{
		item: func(context.Context, *user_model.User, work_service.ItemRequest) (*work_service.ItemInspection, error) {
			called = true
			return nil, errors.New("unexpected item read")
		},
		plan: func(context.Context, *user_model.User, work_service.PlanRequest) (*work_service.PlanInspection, error) {
			return nil, &work_service.ReadFailure{Kind: work_service.ReadUnavailable}
		},
	}
	service := newBoundWorkReadService(reader)
	_, err := service.InspectWorkItem(t.Context(), nil, WorkItemInspectRequest{WorkItem: "issue/01"})
	var failure *WorkReadFailure
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, WorkReadInvalidInput, failure.Kind)
	assert.False(t, called)

	_, err = service.InspectWorkPlan(t.Context(), nil, WorkPlanInspectRequest{WorkPlan: "project/9"})
	require.ErrorAs(t, err, &failure)
	assert.Equal(t, WorkReadUnavailable, failure.Kind)

	_, err = mapWorkPage(work_service.Page{Items: []any{struct{}{}}})
	require.Error(t, err)
}
