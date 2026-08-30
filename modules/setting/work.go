// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"fmt"
)

const (
	defaultWorkMaxGraphNodes               = 1_000
	defaultWorkMaxPlanItems                = 1_000
	defaultWorkMaxProjectionItems          = 100
	defaultWorkDefaultPageItems            = 25
	defaultWorkMaxPageItems                = 100
	defaultWorkMaxPlanRevisionChanges      = 50
	defaultWorkMaxPlanRevisionCreatedItems = 20
	defaultWorkMaxTitleCharacters          = 255
	defaultWorkMaxMarkdownBytes            = 65_536
	defaultWorkMaxOutputBytes              = 1 << 20

	workMaxGraphNodes       = 10_000
	workMaxPlanItems        = 10_000
	workMaxProjectionItems  = 100
	workMaxPageItems        = 100
	workMaxPlanChanges      = 50
	workMaxPlanCreatedItems = 20
	workMaxTitleCharacters  = 255
	workMaxMarkdownBytes    = 65_536
	workMaxOutputBytes      = 1 << 20
)

// Work bounds authoritative planning composition and revision operations.
var Work = struct {
	MaxGraphNodes               int   `ini:"MAX_GRAPH_NODES"`
	MaxPlanItems                int   `ini:"MAX_PLAN_ITEMS"`
	MaxProjectionItems          int   `ini:"MAX_PROJECTION_ITEMS"`
	DefaultPageItems            int   `ini:"DEFAULT_PAGE_ITEMS"`
	MaxPageItems                int   `ini:"MAX_PAGE_ITEMS"`
	MaxPlanRevisionChanges      int   `ini:"MAX_PLAN_REVISION_CHANGES"`
	MaxPlanRevisionCreatedItems int   `ini:"MAX_PLAN_REVISION_CREATED_ITEMS"`
	MaxTitleCharacters          int   `ini:"MAX_TITLE_CHARACTERS"`
	MaxMarkdownBytes            int64 `ini:"MAX_MARKDOWN_BYTES"`
	MaxOutputBytes              int64 `ini:"MAX_OUTPUT_BYTES"`
}{
	MaxGraphNodes:               defaultWorkMaxGraphNodes,
	MaxPlanItems:                defaultWorkMaxPlanItems,
	MaxProjectionItems:          defaultWorkMaxProjectionItems,
	DefaultPageItems:            defaultWorkDefaultPageItems,
	MaxPageItems:                defaultWorkMaxPageItems,
	MaxPlanRevisionChanges:      defaultWorkMaxPlanRevisionChanges,
	MaxPlanRevisionCreatedItems: defaultWorkMaxPlanRevisionCreatedItems,
	MaxTitleCharacters:          defaultWorkMaxTitleCharacters,
	MaxMarkdownBytes:            defaultWorkMaxMarkdownBytes,
	MaxOutputBytes:              defaultWorkMaxOutputBytes,
}

func loadWorkFrom(rootCfg ConfigProvider) error {
	mustMapSetting(rootCfg, "work", &Work)
	for _, bound := range []struct {
		name  string
		value int64
		max   int64
	}{
		{"MAX_GRAPH_NODES", int64(Work.MaxGraphNodes), workMaxGraphNodes},
		{"MAX_PLAN_ITEMS", int64(Work.MaxPlanItems), workMaxPlanItems},
		{"MAX_PROJECTION_ITEMS", int64(Work.MaxProjectionItems), workMaxProjectionItems},
		{"DEFAULT_PAGE_ITEMS", int64(Work.DefaultPageItems), workMaxPageItems},
		{"MAX_PAGE_ITEMS", int64(Work.MaxPageItems), workMaxPageItems},
		{"MAX_PLAN_REVISION_CHANGES", int64(Work.MaxPlanRevisionChanges), workMaxPlanChanges},
		{"MAX_PLAN_REVISION_CREATED_ITEMS", int64(Work.MaxPlanRevisionCreatedItems), workMaxPlanCreatedItems},
		{"MAX_TITLE_CHARACTERS", int64(Work.MaxTitleCharacters), workMaxTitleCharacters},
		{"MAX_MARKDOWN_BYTES", Work.MaxMarkdownBytes, workMaxMarkdownBytes},
		{"MAX_OUTPUT_BYTES", Work.MaxOutputBytes, workMaxOutputBytes},
	} {
		if bound.value < 1 || bound.value > bound.max {
			return fmt.Errorf("[work] %s must be between 1 and %d", bound.name, bound.max)
		}
	}
	if Work.DefaultPageItems > Work.MaxPageItems {
		return errors.New("[work] DEFAULT_PAGE_ITEMS must not exceed MAX_PAGE_ITEMS")
	}
	if Work.MaxPageItems > Work.MaxProjectionItems {
		return errors.New("[work] MAX_PAGE_ITEMS must not exceed MAX_PROJECTION_ITEMS")
	}
	if Work.MaxPlanRevisionCreatedItems > Work.MaxPlanRevisionChanges {
		return errors.New("[work] MAX_PLAN_REVISION_CREATED_ITEMS must not exceed MAX_PLAN_REVISION_CHANGES")
	}
	return nil
}
