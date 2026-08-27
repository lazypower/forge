// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"net/http"
	"strings"
	"testing"

	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/routers"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
)

const mcpDiscoverBody = `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`

func newMCPDiscoverRequest(t *testing.T, path string) *RequestWrapper {
	t.Helper()
	return NewRequestWithBody(t, http.MethodPost, path, strings.NewReader(mcpDiscoverBody)).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "application/json, text/event-stream").
		SetHeader("MCP-Protocol-Version", "2026-07-28").
		SetHeader("MCP-Method", "server/discover")
}

func TestMCPRoute(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("disabled", func(t *testing.T) {
		defer test.MockVariableValue(&setting.MCP.Enabled, false)()
		defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

		MakeRequest(t, NewRequest(t, http.MethodGet, "/mcp"), http.StatusNotFound)
	})

	t.Run("enabled under configured subpath", func(t *testing.T) {
		defer test.MockVariableValue(&setting.MCP.Enabled, true)()
		defer test.MockVariableValue(&setting.AppSubURL, "/forge")()
		defer test.MockVariableValue(&setting.UseSubURLPath, true)()
		defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

		resp := MakeRequest(t, newMCPDiscoverRequest(t, "/forge/mcp"), http.StatusOK)
		assert.True(t, resp.Flushed)
		assert.Contains(t, resp.Body.String(), `"name":"forge"`)
		assert.Contains(t, resp.Body.String(), `"supportedVersions"`)
	})

	t.Run("maintenance mode", func(t *testing.T) {
		defer test.MockVariableValue(&setting.MCP.Enabled, true)()
		defer mockSystemConfig(t, setting.Config().Instance.MaintenanceMode, setting.MaintenanceModeType{AdminWebAccessOnly: true})()
		defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

		MakeRequest(t, newMCPDiscoverRequest(t, "/mcp"), http.StatusServiceUnavailable)
	})
}
