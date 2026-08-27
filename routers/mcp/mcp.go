// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"net/http"

	"gitea.dev/modules/setting"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// RoutePath is the instance-relative MCP endpoint path.
const RoutePath = "/mcp"

// NewEndpoint returns Forge's stateless MCP endpoint.
func NewEndpoint() http.Handler {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "forge",
		Version: setting.AppVer,
	}, &mcpsdk.ServerOptions{
		Capabilities: &mcpsdk.ServerCapabilities{},
	})
	return newEndpoint(server, setting.MCP.MaxRequestBodyBytes)
}

func newEndpoint(server *mcpsdk.Server, maxRequestBodyBytes int64) http.Handler {
	streamable := mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return server },
		&mcpsdk.StreamableHTTPOptions{
			Stateless:                    true,
			MaxRequestBodyBytes:          maxRequestBodyBytes,
			PropagateRequestCancellation: true,
		},
	)
	return http.NewCrossOriginProtection().Handler(streamable)
}
