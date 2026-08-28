// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadWorkFrom(t *testing.T) {
	original := Work
	defer func() { Work = original }()

	t.Run("defaults", func(t *testing.T) {
		Work = original
		cfg, err := NewConfigProviderFromData("")
		require.NoError(t, err)
		require.NoError(t, loadWorkFrom(cfg))

		assert.Equal(t, defaultWorkMaxGraphNodes, Work.MaxGraphNodes)
		assert.Equal(t, defaultWorkMaxPlanItems, Work.MaxPlanItems)
		assert.Equal(t, defaultWorkMaxProjectionItems, Work.MaxProjectionItems)
		assert.Equal(t, defaultWorkDefaultPageItems, Work.DefaultPageItems)
		assert.Equal(t, defaultWorkMaxPageItems, Work.MaxPageItems)
		assert.Equal(t, defaultWorkMaxPlanRevisionChanges, Work.MaxPlanRevisionChanges)
		assert.Equal(t, defaultWorkMaxPlanRevisionCreatedItems, Work.MaxPlanRevisionCreatedItems)
		assert.Equal(t, defaultWorkMaxTitleCharacters, Work.MaxTitleCharacters)
		assert.EqualValues(t, defaultWorkMaxMarkdownBytes, Work.MaxMarkdownBytes)
		assert.EqualValues(t, defaultWorkMaxOutputBytes, Work.MaxOutputBytes)
	})

	t.Run("safe custom bounds", func(t *testing.T) {
		Work = original
		cfg, err := NewConfigProviderFromData(`
[work]
MAX_GRAPH_NODES = 500
MAX_PLAN_ITEMS = 400
MAX_PROJECTION_ITEMS = 80
DEFAULT_PAGE_ITEMS = 20
MAX_PAGE_ITEMS = 80
MAX_PLAN_REVISION_CHANGES = 30
MAX_PLAN_REVISION_CREATED_ITEMS = 10
MAX_TITLE_CHARACTERS = 200
MAX_MARKDOWN_BYTES = 32768
MAX_OUTPUT_BYTES = 524288
`)
		require.NoError(t, err)
		require.NoError(t, loadWorkFrom(cfg))

		assert.Equal(t, 500, Work.MaxGraphNodes)
		assert.Equal(t, 400, Work.MaxPlanItems)
		assert.Equal(t, 80, Work.MaxProjectionItems)
		assert.Equal(t, 20, Work.DefaultPageItems)
		assert.Equal(t, 80, Work.MaxPageItems)
		assert.Equal(t, 30, Work.MaxPlanRevisionChanges)
		assert.Equal(t, 10, Work.MaxPlanRevisionCreatedItems)
		assert.Equal(t, 200, Work.MaxTitleCharacters)
		assert.EqualValues(t, 32768, Work.MaxMarkdownBytes)
		assert.EqualValues(t, 524288, Work.MaxOutputBytes)
	})

	for _, test := range []struct {
		name    string
		config  string
		wantErr string
	}{
		{name: "non-positive graph bound", config: "MAX_GRAPH_NODES = 0", wantErr: "[work] MAX_GRAPH_NODES must be between 1 and 10000"},
		{name: "unsafe page bound", config: "MAX_PAGE_ITEMS = 101", wantErr: "[work] MAX_PAGE_ITEMS must be between 1 and 100"},
		{name: "default page exceeds maximum", config: "DEFAULT_PAGE_ITEMS = 50\nMAX_PAGE_ITEMS = 25", wantErr: "[work] DEFAULT_PAGE_ITEMS must not exceed MAX_PAGE_ITEMS"},
		{name: "page exceeds projection", config: "MAX_PROJECTION_ITEMS = 50\nMAX_PAGE_ITEMS = 75", wantErr: "[work] MAX_PAGE_ITEMS must not exceed MAX_PROJECTION_ITEMS"},
		{name: "created items exceed changes", config: "MAX_PLAN_REVISION_CHANGES = 10\nMAX_PLAN_REVISION_CREATED_ITEMS = 11", wantErr: "[work] MAX_PLAN_REVISION_CREATED_ITEMS must not exceed MAX_PLAN_REVISION_CHANGES"},
		{name: "unsafe output bound", config: "MAX_OUTPUT_BYTES = 1048577", wantErr: "[work] MAX_OUTPUT_BYTES must be between 1 and 1048576"},
	} {
		t.Run(test.name, func(t *testing.T) {
			Work = original
			cfg, err := NewConfigProviderFromData("[work]\n" + test.config + "\n")
			require.NoError(t, err)
			assert.EqualError(t, loadWorkFrom(cfg), test.wantErr)
		})
	}
}
