// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unittest"
	"gitea.dev/modules/json"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/routers"
	"gitea.dev/services/oauth2_provider"
	"gitea.dev/tests"

	"github.com/PuerkitoBio/goquery"
	"github.com/golang-jwt/jwt/v5"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/oauth2"
)

type mcpOAuthHTTPTrace struct {
	base http.RoundTripper

	mu             sync.Mutex
	requests       map[string]int
	tokenResponses []oauth2_provider.AccessTokenResponse
}

var errMCPChallengeRecorded = errors.New("challenge recorded")

type mcpOAuthChallengeRecorder struct {
	token string

	mu        sync.Mutex
	status    int
	challenge string
	body      string
}

func (recorder *mcpOAuthChallengeRecorder) TokenSource(context.Context) (oauth2.TokenSource, error) {
	if recorder.token == "" {
		return oauth2.StaticTokenSource(nil), nil
	}
	return oauth2.StaticTokenSource(&oauth2.Token{AccessToken: recorder.token}), nil
}

func (recorder *mcpOAuthChallengeRecorder) Authorize(_ context.Context, _ *http.Request, resp *http.Response) error {
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	recorder.mu.Lock()
	recorder.status = resp.StatusCode
	recorder.challenge = resp.Header.Get("WWW-Authenticate")
	recorder.body = string(body)
	recorder.mu.Unlock()
	return errMCPChallengeRecorded
}

func (recorder *mcpOAuthChallengeRecorder) response() (int, string, string) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	return recorder.status, recorder.challenge, recorder.body
}

func (trace *mcpOAuthHTTPTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := trace.base.RoundTrip(req)
	if err != nil {
		return nil, err
	}

	trace.mu.Lock()
	trace.requests[req.Method+" "+req.URL.Path]++
	trace.mu.Unlock()

	if req.Method == http.MethodPost && strings.HasSuffix(req.URL.Path, "/login/oauth/access_token") && resp.StatusCode == http.StatusOK {
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		resp.Body = io.NopCloser(strings.NewReader(string(body)))
		var tokenResponse oauth2_provider.AccessTokenResponse
		if err := json.Unmarshal(body, &tokenResponse); err != nil {
			return nil, fmt.Errorf("decode OAuth token response: %w", err)
		}
		trace.mu.Lock()
		trace.tokenResponses = append(trace.tokenResponses, tokenResponse)
		trace.mu.Unlock()
	}
	return resp, nil
}

func (trace *mcpOAuthHTTPTrace) requestCount(method, path string) int {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return trace.requests[method+" "+path]
}

func (trace *mcpOAuthHTTPTrace) tokens() []oauth2_provider.AccessTokenResponse {
	trace.mu.Lock()
	defer trace.mu.Unlock()
	return append([]oauth2_provider.AccessTokenResponse(nil), trace.tokenResponses...)
}

func authorizeMCPThroughForge(t *testing.T, client *http.Client, callbackURL string, callbackCount *atomic.Int64, authorizationCode *string) mcpauth.AuthorizationCodeFetcher {
	t.Helper()
	return func(ctx context.Context, args *mcpauth.AuthorizationArgs) (*mcpauth.AuthorizationResult, error) {
		authorizationURL, err := url.Parse(args.URL)
		if err != nil {
			return nil, err
		}
		query := authorizationURL.Query()
		if authorizationURL.Path != "/forge/login/oauth/authorize" || query.Get("resource") != setting.MCPResource() ||
			query.Get("scope") != string(auth_model.AccessTokenScopeReadRepository) || query.Get("code_challenge_method") != "S256" ||
			query.Get("code_challenge") == "" || query.Get("state") == "" || query.Get("redirect_uri") != callbackURL {
			return nil, errors.New("SDK authorization request does not match the Forge MCP profile")
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, authorizationURL.String(), nil)
		if err != nil {
			return nil, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		document, err := goquery.NewDocumentFromReader(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK || document.Find(`form[action="/forge/login/oauth/grant"]`).Length() != 1 {
			return nil, fmt.Errorf("Forge did not present MCP consent: status %d", resp.StatusCode)
		}
		for name, expected := range map[string]string{
			"client_id": query.Get("client_id"), "redirect_uri": callbackURL, "state": query.Get("state"),
			"scope": query.Get("scope"), "resource": query.Get("resource"),
		} {
			actual, exists := document.Find(fmt.Sprintf(`input[name="%s"]`, name)).Attr("value")
			if !exists || actual != expected {
				return nil, fmt.Errorf("Forge consent did not preserve %s", name)
			}
		}

		consent := url.Values{
			"client_id":    {query.Get("client_id")},
			"redirect_uri": {callbackURL},
			"state":        {query.Get("state")},
			"scope":        {query.Get("scope")},
			"resource":     {query.Get("resource")},
			"granted":      {"true"},
		}
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimSuffix(setting.AppURL, "/")+"/login/oauth/grant", strings.NewReader(consent.Encode()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err = client.Do(req)
		if err != nil {
			return nil, err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Request.URL.Scheme != "http" || resp.Request.URL.Host != strings.TrimPrefix(callbackURL, "http://") {
			return nil, fmt.Errorf("Forge did not complete the loopback callback: status %d", resp.StatusCode)
		}
		if callbackCount.Load() != 1 {
			return nil, fmt.Errorf("loopback callback count is %d", callbackCount.Load())
		}
		*authorizationCode = resp.Request.URL.Query().Get("code")
		return &mcpauth.AuthorizationResult{
			Code:  *authorizationCode,
			State: resp.Request.URL.Query().Get("state"),
			Iss:   resp.Request.URL.Query().Get("iss"),
		}, nil
	}
}

func assertMCPTokenClaims(t *testing.T, tokenValue, issuer, subject, resource string) *oauth2_provider.Token {
	t.Helper()
	token, err := oauth2_provider.ParseToken(tokenValue, oauth2_provider.DefaultSigningKey)
	require.NoError(t, err)
	assert.Equal(t, oauth2_provider.KindAccessToken, token.Kind)
	assert.Equal(t, issuer, token.Issuer)
	assert.Equal(t, subject, token.Subject)
	assert.Equal(t, []string{resource}, []string(token.Audience))
	return token
}

func loginUserAtSubpath(t *testing.T, userName string) *TestSession {
	t.Helper()
	resp := MakeRequest(t, NewRequestWithValues(t, http.MethodPost, setting.AppSubURL+"/user/login", map[string]string{
		"user_name": userName,
		"password":  userPassword,
	}), http.StatusSeeOther)
	session := emptyTestSession(t)
	baseURL, err := url.Parse(setting.AppURL)
	require.NoError(t, err)
	setCookie := http.Header{"Set-Cookie": resp.Header().Values("Set-Cookie")}
	session.jar.SetCookies(baseURL, (&http.Response{Header: setCookie}).Cookies())
	return session
}

func signMCPConformanceAccessToken(t *testing.T, grantID int64, issuer, subject string, audiences []string, expiresAt time.Time) string {
	t.Helper()
	token, err := (&oauth2_provider.Token{
		GrantID: grantID,
		Kind:    oauth2_provider.KindAccessToken,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer: issuer, Subject: subject, Audience: jwt.ClaimStrings(audiences), ExpiresAt: jwt.NewNumericDate(expiresAt),
		},
	}).SignToken(oauth2_provider.DefaultSigningKey)
	require.NoError(t, err)
	return token
}

func assertMCPSecretAbsent(t *testing.T, body, secret, message string) {
	t.Helper()
	assert.Zero(t, strings.Count(body, secret), message)
}

func assertOfficialMCPChallenge(t *testing.T, endpoint string, client *http.Client, token string, expectedStatus int, expectedError, resource, authorizationCode string) {
	t.Helper()
	recorder := &mcpOAuthChallengeRecorder{token: token}
	mcpClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "forge-oauth-negative", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	session, err := mcpClient.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: client, OAuthHandler: recorder, DisableStandaloneSSE: true,
	}, nil)
	if session != nil {
		t.Cleanup(func() { require.NoError(t, session.Close()) })
	}
	require.ErrorIs(t, err, errMCPChallengeRecorded)
	status, challenge, body := recorder.response()
	assert.Equal(t, expectedStatus, status)
	assert.Contains(t, challenge, `resource_metadata="`+setting.MCPProtectedResourceMetadataURL()+`"`)
	assert.Contains(t, challenge, `scope="read:repository"`)
	assert.Contains(t, challenge, `error="`+expectedError+`"`)
	if token != "" {
		assertMCPSecretAbsent(t, body, token, "challenge body disclosed the bearer token")
	}
	assertMCPSecretAbsent(t, body, resource, "challenge body disclosed the protected resource")
	if authorizationCode != "" {
		assertMCPSecretAbsent(t, body, authorizationCode, "challenge body disclosed an authorization code")
	}
}

func TestMCPOAuthConformanceWithOfficialClient(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	var productionRoutes http.Handler
	forgeServer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		productionRoutes.ServeHTTP(w, req)
	}))
	forgeServer.StartTLS()
	defer forgeServer.Close()

	defer test.MockVariableValue(&setting.AppURL, forgeServer.URL+"/forge/")()
	defer test.MockVariableValue(&setting.AppSubURL, "/forge")()
	defer test.MockVariableValue(&setting.UseSubURLPath, true)()
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	defer test.MockVariableValue(&setting.OAuth2.Enabled, true)()
	defer test.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, forgeServer.URL+"/forge")()
	defer test.MockVariableValue(&setting.OAuth2.InvalidateRefreshTokens, false)()
	require.NoError(t, auth_model.Init(t.Context()))
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()
	productionRoutes = testWebRoutes

	login := loginUserAtSubpath(t, "user5")
	var callbackCount atomic.Int64
	var authorizationCode string
	callbackServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		callbackCount.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer callbackServer.Close()

	trace := &mcpOAuthHTTPTrace{base: forgeServer.Client().Transport, requests: map[string]int{}}
	httpClient := &http.Client{Transport: trace, Jar: login.jar}

	var initialClientToken *oauth2.Token
	oauthHandler, err := mcpauth.NewAuthorizationCodeHandler(&mcpauth.AuthorizationCodeHandlerConfig{
		PreregisteredClient: &oauthex.ClientCredentials{
			ClientID: auth_model.MCPBuiltinOAuth2ApplicationClientID,
			Issuer:   strings.TrimSuffix(setting.AppURL, "/"),
		},
		RedirectURL:              callbackServer.URL,
		AuthorizationCodeFetcher: authorizeMCPThroughForge(t, httpClient, callbackServer.URL, &callbackCount, &authorizationCode),
		Client:                   httpClient,
		NewTokenSource: func(ctx context.Context, config *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
			initialClientToken = token
			return config.TokenSource(ctx, token), nil
		},
	})
	require.NoError(t, err)

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "forge-oauth-conformance", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint:             forgeServer.URL + "/forge/mcp",
		HTTPClient:           httpClient,
		OAuthHandler:         oauthHandler,
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)

	assert.GreaterOrEqual(t, trace.requestCount(http.MethodPost, "/forge/mcp"), 2)
	assert.Equal(t, 1, trace.requestCount(http.MethodGet, "/forge/.well-known/oauth-protected-resource/mcp"))
	assert.Equal(t, 1, trace.requestCount(http.MethodGet, "/forge/.well-known/openid-configuration"))
	assert.Equal(t, 1, trace.requestCount(http.MethodGet, "/forge/login/oauth/authorize"))
	assert.Equal(t, 1, trace.requestCount(http.MethodPost, "/forge/login/oauth/grant"))
	assert.Equal(t, 1, trace.requestCount(http.MethodPost, "/forge/login/oauth/access_token"))
	assert.Equal(t, int64(1), callbackCount.Load())
	require.NotEmpty(t, authorizationCode)

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	pr, err := issues_model.GetPullRequestByIndex(t.Context(), repo.ID, 3)
	require.NoError(t, err)
	result, structured := callMCPPullRequestInspect(t, session, map[string]any{
		"owner": repo.OwnerName, "repository": repo.Name, "number": pr.Index,
	})
	assert.False(t, result.IsError)
	assert.Equal(t, "available", structured["status"])

	_, missing := callMCPPullRequestInspect(t, session, map[string]any{
		"owner": "private-owner", "repository": "private-repository", "number": 1,
	})
	assert.Equal(t, map[string]any{"status": "unavailable"}, missing)
	_, denied := callMCPPullRequestInspect(t, session, map[string]any{
		"owner": "org3", "repository": "repo3", "number": 2,
	})
	assert.Equal(t, missing, denied)

	issued := trace.tokens()
	require.Len(t, issued, 1)
	resource := setting.MCPResource()
	subject := strconv.FormatInt(5, 10)
	initialAccess := assertMCPTokenClaims(t, issued[0].AccessToken, oauth2_provider.TokenIssuer(), subject, resource)
	require.NotEmpty(t, issued[0].RefreshToken)
	initialRefresh, err := oauth2_provider.ParseToken(issued[0].RefreshToken, oauth2_provider.DefaultSigningKey)
	require.NoError(t, err)
	assert.Equal(t, []string{resource}, []string(initialRefresh.Audience))
	assert.Equal(t, subject, initialRefresh.Subject)
	assert.Equal(t, initialAccess.Issuer, initialRefresh.Issuer)

	app, err := auth_model.GetOAuth2ApplicationByClientID(t.Context(), auth_model.MCPBuiltinOAuth2ApplicationClientID)
	require.NoError(t, err)
	grant, err := app.GetGrantByUserID(t.Context(), 5)
	require.NoError(t, err)
	require.NotNil(t, grant)
	counterBeforeRefresh := grant.Counter
	assert.Positive(t, counterBeforeRefresh)
	require.NotNil(t, initialClientToken)
	// Expire the SDK's cached token directly so refresh coverage stays deterministic without sleeping.
	initialClientToken.Expiry = time.Now().Add(-time.Minute)

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, "pull_request.inspect", tools.Tools[0].Name)
	issued = trace.tokens()
	require.Len(t, issued, 2)
	replacement := assertMCPTokenClaims(t, issued[1].AccessToken, initialAccess.Issuer, initialAccess.Subject, resource)
	assert.Positive(t, replacement.IssuedAt.Time.UnixNano())
	assert.NotEqual(t, sha256.Sum256([]byte(issued[0].RefreshToken)), sha256.Sum256([]byte(issued[1].RefreshToken)), "refresh token was not rotated")
	replacementRefresh, err := oauth2_provider.ParseToken(issued[1].RefreshToken, oauth2_provider.DefaultSigningKey)
	require.NoError(t, err)
	assert.Equal(t, initialRefresh.Issuer, replacementRefresh.Issuer)
	assert.Equal(t, initialRefresh.Subject, replacementRefresh.Subject)
	assert.Equal(t, []string{resource}, []string(replacementRefresh.Audience))

	grant = unittest.AssertExistsAndLoadBean(t, &auth_model.OAuth2Grant{ID: grant.ID})
	assert.Equal(t, counterBeforeRefresh+1, grant.Counter)

	replayValues := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {app.ClientID},
		"refresh_token": {issued[0].RefreshToken},
	}
	replayResp, err := httpClient.PostForm(strings.TrimSuffix(setting.AppURL, "/")+"/login/oauth/access_token", replayValues)
	require.NoError(t, err)
	replayBody, err := io.ReadAll(replayResp.Body)
	replayResp.Body.Close()
	require.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, replayResp.StatusCode)
	assertMCPSecretAbsent(t, string(replayBody), issued[0].RefreshToken, "refresh replay response disclosed the used token")
	assertMCPSecretAbsent(t, string(replayBody), resource, "refresh replay response disclosed the protected resource")
	assertMCPSecretAbsent(t, string(replayBody), authorizationCode, "refresh replay response disclosed the authorization code")
	var replayError oauth2_provider.AccessTokenError
	require.NoError(t, json.Unmarshal(replayBody, &replayError))
	assert.Equal(t, oauth2_provider.AccessTokenErrorCode(oauth2_provider.AccessTokenErrorCodeUnauthorizedClient), replayError.ErrorCode)
	assert.Equal(t, "token was already used", replayError.ErrorDescription)

	t.Run("real client challenges", func(t *testing.T) {
		validUntil := time.Now().Add(time.Hour)
		cases := []struct {
			name, token, oauthError string
			status                  int
		}{
			{name: "missing", status: http.StatusUnauthorized, oauthError: "invalid_token"},
			{name: "invalid", token: "not.a.valid.token", status: http.StatusUnauthorized, oauthError: "invalid_token"},
			{name: "expired", token: signMCPConformanceAccessToken(t, grant.ID, initialAccess.Issuer, subject, []string{resource}, time.Now().Add(-time.Minute)), status: http.StatusUnauthorized, oauthError: "invalid_token"},
			{name: "missing audience", token: signMCPConformanceAccessToken(t, grant.ID, initialAccess.Issuer, subject, nil, validUntil), status: http.StatusUnauthorized, oauthError: "invalid_token"},
			{name: "wrong audience", token: signMCPConformanceAccessToken(t, grant.ID, initialAccess.Issuer, subject, []string{resource + "/other"}, validUntil), status: http.StatusUnauthorized, oauthError: "invalid_token"},
			{name: "multiple audience", token: signMCPConformanceAccessToken(t, grant.ID, initialAccess.Issuer, subject, []string{resource, resource + "/other"}, validUntil), status: http.StatusUnauthorized, oauthError: "invalid_token"},
			{name: "PAT in OAuth mode", token: newPersistedMCPPAT(t, 5, "mcp-oauth-conformance-pat", auth_model.AccessTokenScopeReadRepository), status: http.StatusUnauthorized, oauthError: "invalid_token"},
		}
		for _, testCase := range cases {
			t.Run(testCase.name, func(t *testing.T) {
				assertOfficialMCPChallenge(t, forgeServer.URL+"/forge/mcp", httpClient, testCase.token, testCase.status, testCase.oauthError, resource, authorizationCode)
			})
		}

		_, err := db.GetEngine(t.Context()).ID(grant.ID).Cols("scope").Update(&auth_model.OAuth2Grant{Scope: string(auth_model.AccessTokenScopeReadUser)})
		require.NoError(t, err)
		assertOfficialMCPChallenge(t, forgeServer.URL+"/forge/mcp", httpClient, issued[1].AccessToken, http.StatusForbidden, "insufficient_scope", resource, authorizationCode)
		_, err = db.GetEngine(t.Context()).ID(grant.ID).Cols("scope").Update(&auth_model.OAuth2Grant{Scope: string(auth_model.AccessTokenScopeReadRepository)})
		require.NoError(t, err)
	})

	t.Run("unrelated Forge resources", func(t *testing.T) {
		client := &http.Client{
			Transport: trace,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		for _, testCase := range []struct {
			name, path string
			status     int
		}{
			{name: "REST", path: "/forge/api/v1/user", status: http.StatusUnauthorized},
			{name: "web", path: "/forge/user/settings", status: http.StatusSeeOther},
			{name: "package", path: "/forge/api/packages/user5/generic/private-package/1/private-file", status: http.StatusUnauthorized},
		} {
			t.Run(testCase.name, func(t *testing.T) {
				req, err := http.NewRequest(http.MethodGet, forgeServer.URL+testCase.path, nil)
				require.NoError(t, err)
				req.Header.Set("Authorization", "Bearer "+issued[1].AccessToken)
				resp, err := client.Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				require.NoError(t, err)
				assert.Equal(t, testCase.status, resp.StatusCode)
				for _, secret := range []string{issued[1].AccessToken, issued[1].RefreshToken, authorizationCode, resource, "private-package"} {
					assertMCPSecretAbsent(t, string(body), secret, "unrelated route disclosed OAuth or private-resource data")
				}
			})
		}
	})

	t.Run("TLS trust boundary", func(t *testing.T) {
		untrusted := &http.Client{Timeout: 2 * time.Second}
		resp, err := untrusted.Get(forgeServer.URL + "/forge/.well-known/oauth-protected-resource/mcp")
		if resp != nil {
			resp.Body.Close()
		}
		assert.Error(t, err)
	})

	require.NoError(t, session.Close())
	t.Run("OAuth token in PAT mode", func(t *testing.T) {
		setting.MCP.Authentication = setting.MCPAuthenticationProfilePAT
		patRoutes := routers.NormalRoutes()
		testWebRoutes = patRoutes
		productionRoutes = patRoutes
		recorder := &mcpOAuthChallengeRecorder{token: issued[1].AccessToken}
		patClient := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "forge-pat-negative", Version: "1"}, nil)
		patSession, err := patClient.Connect(t.Context(), &mcpsdk.StreamableClientTransport{
			Endpoint: forgeServer.URL + "/forge/mcp", HTTPClient: httpClient, OAuthHandler: recorder, DisableStandaloneSSE: true,
		}, nil)
		if patSession != nil {
			t.Cleanup(func() { require.NoError(t, patSession.Close()) })
		}
		require.ErrorIs(t, err, errMCPChallengeRecorded)
		status, challenge, body := recorder.response()
		assert.Equal(t, http.StatusUnauthorized, status)
		assert.Contains(t, challenge, "Bearer")
		assert.Contains(t, challenge, `scope="read:repository"`)
		assert.NotContains(t, challenge, "resource_metadata")
		assert.NotContains(t, challenge, "error=")
		assertMCPSecretAbsent(t, body, issued[1].AccessToken, "PAT profile challenge disclosed the OAuth token")
		assertMCPSecretAbsent(t, body, resource, "PAT profile challenge disclosed the OAuth resource")
	})
}
