// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadMCPFrom(t *testing.T) {
	original := MCP
	originalAppURL := AppURL
	defer func() {
		MCP = original
		AppURL = originalAppURL
	}()

	t.Run("defaults", func(t *testing.T) {
		MCP = original
		AppURL = originalAppURL
		cfg, err := NewConfigProviderFromData("")
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))

		assert.False(t, MCP.Enabled)
		assert.EqualValues(t, defaultMCPMaxRequestBodyBytes, MCP.MaxRequestBodyBytes)
	})

	t.Run("configured", func(t *testing.T) {
		MCP = original
		AppURL = "https://forge.example/"
		cfg, err := NewConfigProviderFromData(`
[mcp]
ENABLED = true
MAX_REQUEST_BODY_BYTES = 2048
`)
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))

		assert.True(t, MCP.Enabled)
		assert.EqualValues(t, 2048, MCP.MaxRequestBodyBytes)
	})

	t.Run("non-positive body limit", func(t *testing.T) {
		MCP = original
		AppURL = originalAppURL
		cfg, err := NewConfigProviderFromData(`
[mcp]
MAX_REQUEST_BODY_BYTES = -1
`)
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))

		assert.EqualValues(t, defaultMCPMaxRequestBodyBytes, MCP.MaxRequestBodyBytes)
	})

	t.Run("requires HTTPS", func(t *testing.T) {
		MCP = original
		AppURL = "http://forge.example/"
		cfg, err := NewConfigProviderFromData(`
[mcp]
ENABLED = true
`)
		require.NoError(t, err)

		err = loadMCPFrom(cfg)

		assert.EqualError(t, err, "[mcp] ENABLED requires an HTTPS ROOT_URL")
	})
}
