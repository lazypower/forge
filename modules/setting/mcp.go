// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"net/url"
)

const defaultMCPMaxRequestBodyBytes = 1 << 20

var MCP = struct {
	Enabled             bool
	MaxRequestBodyBytes int64 `ini:"MAX_REQUEST_BODY_BYTES"`
}{
	Enabled:             false,
	MaxRequestBodyBytes: defaultMCPMaxRequestBodyBytes,
}

func loadMCPFrom(rootCfg ConfigProvider) error {
	mustMapSetting(rootCfg, "mcp", &MCP)
	if MCP.MaxRequestBodyBytes <= 0 {
		MCP.MaxRequestBodyBytes = defaultMCPMaxRequestBodyBytes
	}
	if MCP.Enabled {
		appURL, err := url.Parse(AppURL)
		if err != nil || appURL.Scheme != "https" {
			return errors.New("[mcp] ENABLED requires an HTTPS ROOT_URL")
		}
	}
	return nil
}
