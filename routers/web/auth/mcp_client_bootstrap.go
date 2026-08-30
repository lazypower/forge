// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package auth

import (
	"encoding/json" //nolint:depguard // the closed profile requires DisallowUnknownFields
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/services/context"
	"gitea.dev/services/oauth2_provider"
)

type mcpClientBootstrapRate struct {
	windowStart time.Time
	instance    int
	sources     map[string]int
}

type mcpClientBootstrapAdmission struct {
	mu               sync.Mutex
	capacity         chan struct{}
	rateWindow       time.Duration
	perSourceRate    int
	instanceRate     int
	maxSourceBuckets int
	rate             mcpClientBootstrapRate
}

var (
	mcpBootstrapAdmissionMu sync.Mutex
	mcpBootstrapAdmission   *mcpClientBootstrapAdmission
)

func newMCPClientBootstrapAdmission(capacity int, rateWindow time.Duration, perSourceRate, instanceRate, maxSourceBuckets int) *mcpClientBootstrapAdmission {
	return &mcpClientBootstrapAdmission{
		capacity:         make(chan struct{}, capacity),
		rateWindow:       rateWindow,
		perSourceRate:    perSourceRate,
		instanceRate:     instanceRate,
		maxSourceBuckets: maxSourceBuckets,
		rate: mcpClientBootstrapRate{
			sources: make(map[string]int),
		},
	}
}

func currentMCPClientBootstrapAdmission() *mcpClientBootstrapAdmission {
	mcpBootstrapAdmissionMu.Lock()
	defer mcpBootstrapAdmissionMu.Unlock()
	wantCapacity := setting.MCP.ClientBootstrapMaxInFlightRequests
	if mcpBootstrapAdmission == nil || cap(mcpBootstrapAdmission.capacity) != wantCapacity ||
		mcpBootstrapAdmission.rateWindow != setting.MCP.ClientBootstrapRateWindow ||
		mcpBootstrapAdmission.perSourceRate != setting.MCP.ClientBootstrapPerSourceRate ||
		mcpBootstrapAdmission.instanceRate != setting.MCP.ClientBootstrapInstanceRate ||
		mcpBootstrapAdmission.maxSourceBuckets != setting.MCP.ClientBootstrapMaxSourceBuckets {
		mcpBootstrapAdmission = newMCPClientBootstrapAdmission(
			wantCapacity,
			setting.MCP.ClientBootstrapRateWindow,
			setting.MCP.ClientBootstrapPerSourceRate,
			setting.MCP.ClientBootstrapInstanceRate,
			setting.MCP.ClientBootstrapMaxSourceBuckets,
		)
	}
	return mcpBootstrapAdmission
}

func (admission *mcpClientBootstrapAdmission) allow(source string, now time.Time) bool {
	admission.mu.Lock()
	defer admission.mu.Unlock()
	if admission.rate.windowStart.IsZero() || now.Sub(admission.rate.windowStart) >= admission.rateWindow || now.Before(admission.rate.windowStart) {
		admission.rate.windowStart = now
		admission.rate.instance = 0
		admission.rate.sources = make(map[string]int)
	}
	if admission.rate.instance >= admission.instanceRate {
		return false
	}
	sourceCount, exists := admission.rate.sources[source]
	if !exists && len(admission.rate.sources) >= admission.maxSourceBuckets {
		return false
	}
	if sourceCount >= admission.perSourceRate {
		return false
	}
	admission.rate.instance++
	admission.rate.sources[source] = sourceCount + 1
	return true
}

func (admission *mcpClientBootstrapAdmission) acquire() (func(), bool) {
	select {
	case admission.capacity <- struct{}{}:
		return func() { <-admission.capacity }, true
	default:
		return nil, false
	}
}

func mcpBootstrapSource(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil && net.ParseIP(host) != nil {
		return host
	}
	if net.ParseIP(remoteAddr) != nil {
		return remoteAddr
	}
	return "unknown"
}

type mcpClientRegistrationError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RegisterMCPClient handles the constrained public MCP client bootstrap.
func RegisterMCPClient(ctx *context.Context) {
	if !setting.MCP.ClientBootstrapEnabled {
		http.NotFound(ctx.Resp, ctx.Req)
		return
	}
	mediaType, _, err := mime.ParseMediaType(ctx.Req.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		writeMCPClientRegistrationError(ctx, http.StatusBadRequest, "invalid_client_metadata", "content type must be application/json")
		return
	}
	admission := currentMCPClientBootstrapAdmission()
	if !admission.allow(mcpBootstrapSource(ctx.Req.RemoteAddr), time.Now()) {
		ctx.Resp.Header().Set("Retry-After", strconv.FormatInt(max(1, int64(setting.MCP.ClientBootstrapRateWindow/time.Second)), 10))
		writeMCPClientRegistrationError(ctx, http.StatusTooManyRequests, "temporarily_unavailable", "client bootstrap rate limit reached")
		return
	}
	release, ok := admission.acquire()
	if !ok {
		writeMCPClientRegistrationError(ctx, http.StatusServiceUnavailable, "temporarily_unavailable", "client bootstrap capacity unavailable")
		return
	}
	defer release()

	ctx.Req.Body = http.MaxBytesReader(ctx.Resp, ctx.Req.Body, setting.MCP.ClientBootstrapMaxRequestBodyBytes)
	decoder := json.NewDecoder(ctx.Req.Body)
	decoder.DisallowUnknownFields()
	var request oauth2_provider.MCPClientRegistrationRequest
	if err := decoder.Decode(&request); err != nil {
		description := "invalid client metadata"
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			description = "client metadata exceeds the request limit"
		}
		writeMCPClientRegistrationError(ctx, http.StatusBadRequest, "invalid_client_metadata", description)
		return
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		writeMCPClientRegistrationError(ctx, http.StatusBadRequest, "invalid_client_metadata", "client metadata must be one JSON object")
		return
	}
	response, err := oauth2_provider.CreateMCPClientBootstrap(ctx, request, time.Now())
	if err != nil {
		switch {
		case errors.Is(err, oauth2_provider.ErrInvalidMCPClientMetadata):
			writeMCPClientRegistrationError(ctx, http.StatusBadRequest, "invalid_client_metadata", "client metadata is outside the supported MCP profile")
		case errors.Is(err, auth_model.ErrMCPRegistrationCapacity):
			writeMCPClientRegistrationError(ctx, http.StatusTooManyRequests, "temporarily_unavailable", "provisional registration capacity reached")
		default:
			log.Error("Unable to create MCP client registration: %v", err)
			writeMCPClientRegistrationError(ctx, http.StatusInternalServerError, "server_error", "client registration failed")
		}
		return
	}
	ctx.JSON(http.StatusCreated, response)
}

func writeMCPClientRegistrationError(ctx *context.Context, status int, code, description string) {
	ctx.JSON(status, mcpClientRegistrationError{Error: code, ErrorDescription: description})
}
