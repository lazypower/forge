// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package actions

import (
	"errors"
	"net/http"
	"strings"

	actions_model "code.gitea.io/gitea/models/actions"
	"code.gitea.io/gitea/modules/json"
	"code.gitea.io/gitea/modules/log"
	"code.gitea.io/gitea/modules/setting"
	"code.gitea.io/gitea/modules/web"
	actions_service "code.gitea.io/gitea/services/actions"
	"code.gitea.io/gitea/services/context"
	"code.gitea.io/gitea/services/oauth2_provider"
)

func registerOIDCRoutes(m *web.Router) {
	m.Group("/oidc", func() {
		m.Get("/.well-known/openid-configuration", oidcWellKnown)
		m.Get("/jwks", oidcKeys)
		m.Get("/token", oidcToken)
	})
}

func workloadIdentityAvailable(ctx *context.Base) bool {
	if !setting.Actions.WorkloadIdentityEnabled {
		ctx.HTTPError(http.StatusNotFound)
		return false
	}
	return true
}

func oidcWellKnown(resp http.ResponseWriter, req *http.Request) {
	ctx := context.NewBaseContext(resp, req)
	if !workloadIdentityAvailable(ctx) {
		return
	}
	key := oauth2_provider.DefaultSigningKey
	if key == nil || key.IsSymmetric() {
		ctx.HTTPError(http.StatusInternalServerError, "OIDC signing key is not asymmetric")
		return
	}
	issuer := actions_service.OIDCIssuer()
	ctx.JSON(http.StatusOK, map[string]any{
		"issuer": issuer, "jwks_uri": issuer + "/jwks", "token_endpoint": issuer + "/token",
		"response_types_supported": []string{"id_token"}, "subject_types_supported": []string{"public"},
		"id_token_signing_alg_values_supported": []string{key.SigningMethod().Alg()},
		"claims_supported":                      []string{"aud", "exp", "iat", "iss", "jti", "nbf", "sub", "actor", "actor_id", "repository", "repository_id", "repository_owner", "repository_owner_id", "ref", "ref_type", "sha", "workflow", "workflow_ref", "workflow_sha", "job", "event_name", "run_id", "run_number", "run_attempt"},
	})
}

func oidcKeys(resp http.ResponseWriter, req *http.Request) {
	ctx := context.NewBaseContext(resp, req)
	if !workloadIdentityAvailable(ctx) {
		return
	}
	key := oauth2_provider.DefaultSigningKey
	if key == nil || key.IsSymmetric() {
		ctx.HTTPError(http.StatusInternalServerError, "OIDC signing key is not asymmetric")
		return
	}
	jwk, err := key.ToJWK()
	if err != nil {
		log.Error("Error converting signing key to JWK: %v", err)
		ctx.HTTPError(http.StatusInternalServerError)
		return
	}
	jwk["use"] = "sig"
	ctx.Resp.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(ctx.Resp).Encode(map[string][]map[string]string{"keys": {jwk}}); err != nil {
		log.Error("Failed to encode OIDC JWKS response: %v", err)
	}
}

func oidcToken(resp http.ResponseWriter, req *http.Request) {
	ctx := context.NewBaseContext(resp, req)
	if !workloadIdentityAvailable(ctx) {
		return
	}
	audience, err := actions_service.ValidateOIDCAudience(req.URL.Query())
	if err != nil {
		ctx.HTTPError(http.StatusBadRequest, "invalid audience")
		return
	}
	task, err := getTaskFromOIDCTokenRequest(ctx)
	if err != nil {
		ctx.HTTPError(http.StatusUnauthorized, "invalid Actions task credential")
		return
	}
	if err := actions_service.ValidateOIDCTask(ctx, task); err != nil {
		ctx.HTTPError(http.StatusForbidden, "Actions task is not eligible for workload identity")
		return
	}
	allowed, err := actions_service.TaskAllowsOIDCToken(ctx, task)
	if err != nil {
		log.Error("Error checking OIDC token permissions: %v", err)
		ctx.HTTPError(http.StatusInternalServerError)
		return
	}
	if !allowed {
		ctx.HTTPError(http.StatusForbidden, "id-token write permission not granted")
		return
	}
	token, err := actions_service.CreateOIDCToken(ctx, task, audience)
	if err != nil {
		log.Error("Error generating OIDC token: %v", err)
		ctx.HTTPError(http.StatusInternalServerError)
		return
	}
	ctx.JSON(http.StatusOK, map[string]string{"value": token})
}

func getTaskFromOIDCTokenRequest(ctx *context.Base) (*actions_model.ActionTask, error) {
	parts := strings.Fields(ctx.Req.Header.Get("Authorization"))
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return nil, errors.New("invalid authorization header")
	}
	return actions_model.GetRunningTaskByToken(ctx, parts[1])
}
