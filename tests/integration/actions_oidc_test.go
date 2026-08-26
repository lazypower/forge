// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"crypto/rand"
	"crypto/rsa"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	runnerv1 "gitea.dev/actions-proto-go/runner/v1"
	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	actions_service "gitea.dev/services/actions"
	"gitea.dev/services/oauth2_provider"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type oidcIntegrationClaims struct {
	jwt.RegisteredClaims
	Repository           string `json:"repository"`
	Job                  string `json:"job"`
	WorkflowRef          string `json:"workflow_ref"`
	WorkflowSHA          string `json:"workflow_sha"`
	WorkflowRepository   string `json:"workflow_repository"`
	WorkflowRepositoryID int64  `json:"workflow_repository_id"`
}

func TestActionsOIDCTokenIntegration(t *testing.T) {
	onGiteaRun(t, func(t *testing.T, u *url.URL) {
		defer test.MockVariableValue(&setting.Actions.WorkloadIdentityEnabled, true)()
		privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
		require.NoError(t, err)
		signingKey, err := oauth2_provider.CreateJWTSigningKey("RS256", privateKey)
		require.NoError(t, err)
		defer test.MockVariableValue(&oauth2_provider.DefaultSigningKey, signingKey)()

		user2 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		session := loginUser(t, user2.Name)
		token := getTokenForLoggedInUser(t, session, auth_model.AccessTokenScopeWriteRepository, auth_model.AccessTokenScopeWriteUser)

		repo := createActionsTestRepo(t, token, "actions-oidc", false)
		runner := newMockRunner()
		runner.registerAsRepoRunner(t, user2.Name, repo.Name, "mock-runner", []string{"ubuntu-latest"}, false)

		workflowContent := `name: OIDC
on:
  push:
    paths:
      - '.gitea/workflows/oidc.yml'
permissions:
  id-token: write

jobs:
  oidc-job:
    environment: production
    runs-on: ubuntu-latest
    steps:
      - run: echo oidc
`
		workflowPath := ".gitea/workflows/oidc.yml"
		opts := getWorkflowCreateFileOptions(user2, repo.DefaultBranch, "create "+workflowPath, workflowContent)
		createWorkflowFile(t, token, user2.Name, repo.Name, workflowPath, opts)

		task := runner.fetchTask(t)
		contextMap := task.Context.AsMap()
		requestURL, ok := contextMap["actions_id_token_request_url"].(string)
		require.True(t, ok)
		requestToken, ok := contextMap["actions_id_token_request_token"].(string)
		require.True(t, ok)

		// @actions/core appends the audience with an ampersand because GitHub's request URL has a query string.
		parsedURL, err := url.Parse(requestURL + "&audience=" + url.QueryEscape("integration-test"))
		require.NoError(t, err)
		assert.Equal(t, actions_service.OIDCTokenRequestURL(), requestURL)
		assert.Equal(t, "2.0", parsedURL.Query().Get("api-version"))

		req := NewRequest(t, http.MethodGet, parsedURL.RequestURI())
		req.Header.Set("Authorization", "Bearer "+requestToken)
		resp := MakeRequest(t, req, http.StatusOK)
		var tokenResp struct {
			Value string `json:"value"`
		}
		DecodeJSON(t, resp, &tokenResp)
		require.NotEmpty(t, tokenResp.Value)

		var claims oidcIntegrationClaims
		parsed, err := jwt.ParseWithClaims(tokenResp.Value, &claims, func(t *jwt.Token) (any, error) {
			if t.Method == nil || t.Method.Alg() != signingKey.SigningMethod().Alg() {
				return nil, jwt.ErrSignatureInvalid
			}
			return signingKey.VerifyKey(), nil
		})
		require.NoError(t, err)
		require.True(t, parsed.Valid)

		assert.Equal(t, actions_service.OIDCIssuer(), claims.Issuer)
		assert.Contains(t, claims.Audience, "integration-test")
		assert.Equal(t, repo.FullName, claims.Repository)
		assert.Equal(t, "oidc-job", claims.Job)
		assert.WithinDuration(t, claims.IssuedAt.Time.Add(5*time.Minute), claims.ExpiresAt.Time, time.Second)

		shaValue, ok := contextMap["sha"].(string)
		require.True(t, ok)
		workflowRef := repo.FullName + "/" + workflowPath + "@" + shaValue
		assert.Equal(t, workflowRef, claims.WorkflowRef)
		assert.Equal(t, shaValue, claims.WorkflowSHA)
		assert.Equal(t, repo.FullName, claims.WorkflowRepository)
		assert.Equal(t, repo.ID, claims.WorkflowRepositoryID)

		_, err = jwt.Parse(tokenResp.Value, func(token *jwt.Token) (any, error) { return signingKey.VerifyKey(), nil }, jwt.WithAudience("wrong"))
		require.Error(t, err)
		parts := strings.Split(tokenResp.Value, ".")
		require.Len(t, parts, 3)
		parts[1] = "e30"
		_, err = jwt.Parse(strings.Join(parts, "."), func(token *jwt.Token) (any, error) { return signingKey.VerifyKey(), nil })
		require.Error(t, err)

		badQueryURL := *parsedURL
		badQuery := badQueryURL.Query()
		badQuery.Set("repository", "attacker/repository")
		badQueryURL.RawQuery = badQuery.Encode()
		badReq := NewRequest(t, http.MethodGet, badQueryURL.RequestURI())
		badReq.Header.Set("Authorization", "Bearer "+requestToken)
		MakeRequest(t, badReq, http.StatusBadRequest)

		runner.execTask(t, task, &mockTaskOutcome{result: runnerv1.Result_RESULT_SUCCESS})
		completedReq := NewRequest(t, http.MethodGet, parsedURL.RequestURI())
		completedReq.Header.Set("Authorization", "Bearer "+requestToken)
		MakeRequest(t, completedReq, http.StatusUnauthorized)
	})
}
