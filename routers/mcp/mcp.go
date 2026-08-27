// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"gitea.dev/modules/setting"
	pull_service "gitea.dev/services/pull"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RoutePath is the instance-relative MCP endpoint path.
const RoutePath = "/mcp"

// NewEndpoint returns Forge's stateless MCP endpoint.
func NewEndpoint() http.Handler {
	tool := newPullRequestInspectionTool(setting.MCP.MaxInFlightRequests, setting.MCP.ExecutionTimeout, pull_service.InspectPullRequest, authenticatedUser)
	server := newServer(tool)
	return newAuthenticatedEndpoint(server, setting.MCP.MaxRequestBodyBytes, newPATVerifier())
}

func newAuthenticatedEndpoint(server *mcpsdk.Server, maxRequestBodyBytes int64, verifier mcpauth.TokenVerifier) http.Handler {
	streamable := newStreamableEndpoint(server, maxRequestBodyBytes)
	authenticated := mcpauth.RequireBearerToken(verifier, &mcpauth.RequireBearerTokenOptions{
		Scopes:                 []string{string(readRepositoryScope)},
		AllowMissingExpiration: true,
	})(streamable)
	return http.NewCrossOriginProtection().Handler(requireBearerHeader(authenticated))
}

func newServer(tool *pullRequestInspectionTool) *mcpsdk.Server {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "forge",
		Version: setting.AppVer,
	}, &mcpsdk.ServerOptions{
		Capabilities: &mcpsdk.ServerCapabilities{},
	})
	registerPullRequestInspectionTool(server, tool)
	return server
}

func newStreamableEndpoint(server *mcpsdk.Server, maxRequestBodyBytes int64) http.Handler {
	streamable := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server },
		&mcpsdk.StreamableHTTPOptions{
			Stateless:                    true,
			MaxRequestBodyBytes:          maxRequestBodyBytes,
			PropagateRequestCancellation: true,
		},
	)
	return streamable
}

type queryCredentialContextKey struct{}

// QueryCredentialBoundary removes MCP query credentials before request metadata is captured.
func QueryCredentialBoundary(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if isMCPRoutePath(req.URL.Path) && removeMCPQueryCredentials(req) {
			req = req.WithContext(context.WithValue(req.Context(), queryCredentialContextKey{}, true))
		}
		next.ServeHTTP(w, req)
	})
}

func isMCPRoutePath(path string) bool {
	return path == RoutePath || setting.AppSubURL != "" && path == setting.AppSubURL+RoutePath
}

func queryCredentialWasRemoved(ctx context.Context) bool {
	removed, _ := ctx.Value(queryCredentialContextKey{}).(bool)
	return removed
}

func removeMCPQueryCredentials(req *http.Request) bool {
	if req.URL.RawQuery == "" {
		return false
	}
	parts := strings.Split(req.URL.RawQuery, "&")
	retained := make([]string, 0, len(parts))
	removed := false
	for _, part := range parts {
		key, _, _ := strings.Cut(part, "=")
		decoded, err := url.QueryUnescape(key)
		if err == nil && (decoded == "token" || decoded == "access_token") {
			removed = true
			continue
		}
		retained = append(retained, part)
	}
	if !removed {
		return false
	}
	req.URL.RawQuery = strings.Join(retained, "&")
	req.URL.ForceQuery = false
	requestPath, _, hasQuery := strings.Cut(req.RequestURI, "?")
	if !hasQuery {
		requestPath = req.URL.EscapedPath()
	}
	req.RequestURI = requestPath
	if req.URL.RawQuery != "" {
		req.RequestURI += "?" + req.URL.RawQuery
	}
	return true
}
