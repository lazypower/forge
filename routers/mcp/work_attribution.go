// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"gitea.dev/modules/json"
	mcpwork_service "gitea.dev/services/mcpwork"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

const clientAttributionMetaKey = "io.gitea.forge/clientAttribution"

func workClientAttribution(request *mcpsdk.CallToolRequest) (mcpwork_service.ClientAttribution, error) {
	invalid := func() (mcpwork_service.ClientAttribution, error) {
		return mcpwork_service.ClientAttribution{}, mcpwork_service.ErrClientAttributionRequired
	}
	if request == nil || request.Params == nil {
		return invalid()
	}
	meta := request.Params.GetMeta()
	if supplied, present := meta[mcpsdk.MetaKeyClientInfo]; present {
		// Prevent the SDK's legacy fallback from hiding a malformed explicit override.
		wire, err := json.Marshal(supplied)
		var info *mcpsdk.Implementation
		if err != nil || json.Unmarshal(wire, &info) != nil || info == nil {
			return invalid()
		}
		var fields map[string]json.Value
		if json.Unmarshal(wire, &fields) != nil {
			return invalid()
		}
		if _, present := fields["version"]; present && info.Version == "" {
			return invalid()
		}
	}
	info := request.ClientInfo()
	if info == nil {
		return invalid()
	}
	var model string
	if supplied, present := meta[clientAttributionMetaKey]; present {
		wire, err := json.Marshal(supplied)
		var fields map[string]json.Value
		if err != nil || json.Unmarshal(wire, &fields) != nil || len(fields) != 1 || json.Unmarshal(fields["model"], &model) != nil || model == "" {
			return invalid()
		}
	}
	return mcpwork_service.NewClientAttribution(info.Name, info.Version, model)
}
