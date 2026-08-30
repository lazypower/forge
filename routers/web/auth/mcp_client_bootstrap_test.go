// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"gitea.dev/modules/setting"
	test_module "gitea.dev/modules/test"
	"gitea.dev/services/contexttest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMCPClientBootstrapAdmissionBounds(t *testing.T) {
	now := time.Unix(100, 0)
	admission := newMCPClientBootstrapAdmission(1, time.Minute, 2, 3, 2)
	assert.True(t, admission.allow("192.0.2.1", now))
	assert.True(t, admission.allow("192.0.2.1", now))
	assert.False(t, admission.allow("192.0.2.1", now))
	assert.True(t, admission.allow("192.0.2.2", now))
	assert.False(t, admission.allow("192.0.2.3", now), "instance and source-bucket bounds are both finite")
	assert.True(t, admission.allow("192.0.2.3", now.Add(time.Minute)), "expired source buckets are released")

	release, ok := admission.acquire()
	require.True(t, ok)
	_, ok = admission.acquire()
	assert.False(t, ok)
	release()
	_, ok = admission.acquire()
	assert.True(t, ok)
}

func TestMCPBootstrapSourceUsesDirectAddressOnly(t *testing.T) {
	assert.Equal(t, "192.0.2.10", mcpBootstrapSource("192.0.2.10:49152"))
	assert.Equal(t, "2001:db8::10", mcpBootstrapSource("[2001:db8::10]:49152"))
	assert.Equal(t, "unknown", mcpBootstrapSource("forwarded.example"))
}

func TestRegisterMCPClientRejectsMalformedRequestsBeforePersistence(t *testing.T) {
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapEnabled, true)()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapMaxRequestBodyBytes, int64(96))()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapMaxInFlightRequests, 2)()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapRateWindow, time.Minute)()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapPerSourceRate, 20)()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapInstanceRate, 20)()
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapMaxSourceBuckets, 20)()

	tests := []struct {
		name, contentType, body string
		wantStatus              int
	}{
		{name: "content type", body: `{}`, wantStatus: http.StatusBadRequest},
		{name: "unsupported scope", contentType: "application/json", body: `{"client_name":"Harness","scope":"write:repository"}`, wantStatus: http.StatusBadRequest},
		{name: "unknown authority field", contentType: "application/json", body: `{"client_name":"Harness","resource":"https://forge.example/mcp"}`, wantStatus: http.StatusBadRequest},
		{name: "unknown software statement", contentType: "application/json", body: `{"client_name":"Harness","software_statement":"payload"}`, wantStatus: http.StatusBadRequest},
		{name: "trailing object", contentType: "application/json", body: `{} {}`, wantStatus: http.StatusBadRequest},
		{name: "oversize", contentType: "application/json", body: `{"client_name":"` + strings.Repeat("x", 100) + `"}`, wantStatus: http.StatusBadRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mcpBootstrapAdmissionMu.Lock()
			mcpBootstrapAdmission = nil
			mcpBootstrapAdmissionMu.Unlock()
			ctx, recorder := contexttest.MockContext(t, "POST /login/oauth/register")
			ctx.Req.Body = io.NopCloser(strings.NewReader(test.body))
			ctx.Req.Header.Set("Content-Type", test.contentType)
			ctx.Req.RemoteAddr = "192.0.2.10:49152"
			RegisterMCPClient(ctx)
			assert.Equal(t, test.wantStatus, recorder.Code)
			assert.NotContains(t, recorder.Body.String(), "write:repository")
			assert.NotContains(t, recorder.Body.String(), "payload")
		})
	}
}

func TestRegisterMCPClientDisabledIsUndiscoverable(t *testing.T) {
	defer test_module.MockVariableValue(&setting.MCP.ClientBootstrapEnabled, false)()
	ctx, recorder := contexttest.MockContext(t, "POST /login/oauth/register")
	RegisterMCPClient(ctx)
	assert.Equal(t, http.StatusNotFound, recorder.Code)
}
