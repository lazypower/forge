// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"testing"
	"time"

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
		assert.Equal(t, defaultMCPMaxInFlightRequests, MCP.MaxInFlightRequests)
		assert.Equal(t, defaultMCPExecutionTimeout, MCP.ExecutionTimeout)
	})

	t.Run("configured", func(t *testing.T) {
		MCP = original
		AppURL = "https://forge.example/"
		cfg, err := NewConfigProviderFromData(`
[mcp]
ENABLED = true
MAX_REQUEST_BODY_BYTES = 2048
MAX_IN_FLIGHT_REQUESTS = 4
EXECUTION_TIMEOUT = 15s
`)
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))

		assert.True(t, MCP.Enabled)
		assert.EqualValues(t, 2048, MCP.MaxRequestBodyBytes)
		assert.Equal(t, 4, MCP.MaxInFlightRequests)
		assert.Equal(t, 15*time.Second, MCP.ExecutionTimeout)
	})

	t.Run("non-positive body limit", func(t *testing.T) {
		MCP = original
		AppURL = originalAppURL
		cfg, err := NewConfigProviderFromData(`
[mcp]
MAX_REQUEST_BODY_BYTES = -1
MAX_IN_FLIGHT_REQUESTS = -1
EXECUTION_TIMEOUT = -1s
`)
		require.NoError(t, err)

		require.NoError(t, loadMCPFrom(cfg))

		assert.EqualValues(t, defaultMCPMaxRequestBodyBytes, MCP.MaxRequestBodyBytes)
		assert.Equal(t, defaultMCPMaxInFlightRequests, MCP.MaxInFlightRequests)
		assert.Equal(t, defaultMCPExecutionTimeout, MCP.ExecutionTimeout)
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
