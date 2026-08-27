// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package setting

import (
	"errors"
	"net/url"
	"time"
)

const (
	defaultMCPMaxRequestBodyBytes = 1 << 20
	defaultMCPMaxInFlightRequests = 8
	defaultMCPExecutionTimeout    = 30 * time.Second
)

var MCP = struct {
	Enabled             bool
	MaxRequestBodyBytes int64         `ini:"MAX_REQUEST_BODY_BYTES"`
	MaxInFlightRequests int           `ini:"MAX_IN_FLIGHT_REQUESTS"`
	ExecutionTimeout    time.Duration `ini:"EXECUTION_TIMEOUT"`
}{
	Enabled:             false,
	MaxRequestBodyBytes: defaultMCPMaxRequestBodyBytes,
	MaxInFlightRequests: defaultMCPMaxInFlightRequests,
	ExecutionTimeout:    defaultMCPExecutionTimeout,
}

func loadMCPFrom(rootCfg ConfigProvider) error {
	mustMapSetting(rootCfg, "mcp", &MCP)
	if MCP.MaxRequestBodyBytes <= 0 {
		MCP.MaxRequestBodyBytes = defaultMCPMaxRequestBodyBytes
	}
	if MCP.MaxInFlightRequests <= 0 {
		MCP.MaxInFlightRequests = defaultMCPMaxInFlightRequests
	}
	if MCP.ExecutionTimeout <= 0 {
		MCP.ExecutionTimeout = defaultMCPExecutionTimeout
	}
	if MCP.Enabled {
		appURL, err := url.Parse(AppURL)
		if err != nil || appURL.Scheme != "https" {
			return errors.New("[mcp] ENABLED requires an HTTPS ROOT_URL")
		}
	}
	return nil
}
