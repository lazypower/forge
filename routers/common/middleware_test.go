// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProtocolMiddlewaresNormalizeMCPQueryCredentialsFirst(t *testing.T) {
	defer test.MockVariableValue(&setting.AppSubURL, "/forge")()
	middlewares := ProtocolMiddlewares()
	require.NotEmpty(t, middlewares)
	first, ok := middlewares[0].(func(http.Handler) http.Handler)
	require.True(t, ok)
	var gotQuery, gotRequestURI string
	handler := first(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotQuery = req.URL.RawQuery
		gotRequestURI = req.RequestURI
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequest(http.MethodPost, "/forge/mcp?keep=visible&access_token=TOP-SECRET", nil)
	resp := httptest.NewRecorder()

	handler.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusNoContent, resp.Code)
	assert.Equal(t, "keep=visible", gotQuery)
	assert.Equal(t, "/forge/mcp?keep=visible", gotRequestURI)
}
