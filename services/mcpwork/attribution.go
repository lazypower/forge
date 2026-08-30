// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcpwork

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"
)

// ErrClientAttributionRequired is a provenance-profile failure, never a domain rejection.
var ErrClientAttributionRequired = errors.New("valid MCP client attribution is required")

// ClientAttribution records visible runtime annotation or its unavailability, never authority or attestation.
type ClientAttribution struct {
	Harness        string `json:"harness,omitempty"`
	HarnessVersion string `json:"harnessVersion,omitempty"`
	Model          string `json:"model,omitempty"`
	Source         string `json:"source"`
}

// NewClientAttribution bounds and normalizes runtime labels before receipt lookup.
func NewClientAttribution(harness, version, model string) (ClientAttribution, error) {
	if !validAttributionLabel(harness, 128, false) || !validAttributionLabel(model, 128, false) || !validAttributionLabel(version, 64, false) || (harness == "" && version != "") {
		return ClientAttribution{}, ErrClientAttributionRequired
	}
	source := "client-reported"
	if harness == "" && model == "" {
		source = "unavailable"
	}
	return ClientAttribution{
		Harness: strings.TrimSpace(harness), HarnessVersion: strings.TrimSpace(version),
		Model: strings.TrimSpace(model), Source: source,
	}, nil
}

func validAttributionLabel(value string, limit int, required bool) bool {
	if value == "" {
		return !required
	}
	if !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) {
			return false
		}
	}
	trimmed := strings.TrimSpace(value)
	return trimmed != "" && utf8.RuneCountInString(trimmed) <= limit
}
