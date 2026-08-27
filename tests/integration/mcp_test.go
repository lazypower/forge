// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	activities_model "gitea.dev/models/activities"
	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	git_model "gitea.dev/models/git"
	issues_model "gitea.dev/models/issues"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/commitstatus"
	"gitea.dev/modules/git"
	"gitea.dev/modules/gitrepo"
	"gitea.dev/modules/log"
	"gitea.dev/modules/setting"
	"gitea.dev/modules/test"
	"gitea.dev/modules/util"
	"gitea.dev/modules/web"
	"gitea.dev/routers"
	"gitea.dev/services/oauth2_provider"
	pull_service "gitea.dev/services/pull"
	"gitea.dev/tests"

	"github.com/golang-jwt/jwt/v5"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const mcpDiscoverBody = `{"jsonrpc":"2.0","id":1,"method":"server/discover","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28","io.modelcontextprotocol/clientInfo":{"name":"test","version":"1"},"io.modelcontextprotocol/clientCapabilities":{}}}}`

func newMCPDiscoverRequest(t *testing.T, path string) *RequestWrapper {
	t.Helper()
	return NewRequestWithBody(t, http.MethodPost, path, strings.NewReader(mcpDiscoverBody)).
		SetHeader("Content-Type", "application/json").
		SetHeader("Accept", "application/json, text/event-stream").
		SetHeader("MCP-Protocol-Version", "2026-07-28").
		SetHeader("MCP-Method", "server/discover")
}

func TestMCPRoute(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	t.Run("disabled", func(t *testing.T) {
		defer test.MockVariableValue(&setting.MCP.Enabled, false)()
		defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

		MakeRequest(t, NewRequest(t, http.MethodGet, "/mcp"), http.StatusNotFound)
	})

	t.Run("enabled under configured subpath", func(t *testing.T) {
		token := getUserToken(t, "user1", auth_model.AccessTokenScopeReadRepository)
		defer test.MockVariableValue(&setting.MCP.Enabled, true)()
		defer test.MockVariableValue(&setting.AppSubURL, "/forge")()
		defer test.MockVariableValue(&setting.UseSubURLPath, true)()
		defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

		resp := MakeRequest(t, newMCPDiscoverRequest(t, "/forge/mcp").AddTokenAuth(token), http.StatusOK)
		assert.True(t, resp.Flushed)
		assert.Contains(t, resp.Body.String(), `"name":"forge"`)
		assert.Contains(t, resp.Body.String(), `"supportedVersions"`)
	})

	t.Run("maintenance mode", func(t *testing.T) {
		defer test.MockVariableValue(&setting.MCP.Enabled, true)()
		defer mockSystemConfig(t, setting.Config().Instance.MaintenanceMode, setting.MaintenanceModeType{AdminWebAccessOnly: true})()
		defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

		MakeRequest(t, newMCPDiscoverRequest(t, "/mcp"), http.StatusServiceUnavailable)
	})

	t.Run("OAuth metadata under configured subpath", func(t *testing.T) {
		defer test.MockVariableValue(&setting.MCP.Enabled, true)()
		defer test.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
		defer test.MockVariableValue(&setting.AppURL, "https://forge.example/forge/")()
		defer test.MockVariableValue(&setting.OAuth2.JWTClaimIssuer, "https://forge.example/forge")()
		defer test.MockVariableValue(&setting.AppSubURL, "/forge")()
		defer test.MockVariableValue(&setting.UseSubURLPath, true)()
		defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

		resp := MakeRequest(t, NewRequest(t, http.MethodGet, "/forge"+setting.MCPProtectedResourceMetadataPath()), http.StatusOK)
		assert.Contains(t, resp.Body.String(), `"resource":"https://forge.example/forge/mcp"`)
		assert.Equal(t, "*", resp.Header().Get("Access-Control-Allow-Origin"))
	})
}

type mcpAuthorizationTransport struct {
	token string
	base  http.RoundTripper
}

func (transport mcpAuthorizationTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	request := req.Clone(req.Context())
	request.Header.Set("Authorization", "Bearer "+transport.token)
	return transport.base.RoundTrip(request)
}

func newPersistedMCPPAT(t *testing.T, userID int64, name string, scope auth_model.AccessTokenScope) string {
	t.Helper()
	token := &auth_model.AccessToken{UID: userID, Name: name, Scope: scope}
	require.NoError(t, auth_model.NewAccessToken(t.Context(), token))
	persisted, err := auth_model.GetAccessTokenBySHA(t.Context(), token.Token)
	require.NoError(t, err)
	assert.Equal(t, scope, persisted.Scope)
	return token.Token
}

func connectMCPClient(t *testing.T, endpoint, token string) *mcpsdk.ClientSession {
	t.Helper()
	httpClient := &http.Client{Transport: mcpAuthorizationTransport{token: token, base: http.DefaultTransport}}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "integration-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), &mcpsdk.StreamableClientTransport{
		Endpoint: endpoint, HTTPClient: httpClient, DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, session.Close()) })
	return session
}

func callMCPPullRequestInspect(t *testing.T, session *mcpsdk.ClientSession, arguments map[string]any) (*mcpsdk.CallToolResult, map[string]any) {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "pull_request.inspect", Arguments: arguments})
	require.NoError(t, err)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	return result, structured
}

func disableRepositoryUnit(t *testing.T, unitType unit.Type) {
	t.Helper()
	original := slices.Clone(unit.DisabledRepoUnitsGet())
	disabled := slices.Clone(original)
	if !slices.Contains(disabled, unitType) {
		disabled = append(disabled, unitType)
	}
	unit.DisabledRepoUnitsSet(disabled)
	t.Cleanup(func() { unit.DisabledRepoUnitsSet(original) })
}

func TestMCPRealPATWithOfficialClient(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	token := getUserToken(t, "user1", auth_model.AccessTokenScopeReadRepository)
	patBefore, err := auth_model.GetAccessTokenBySHA(t.Context(), token)
	require.NoError(t, err)
	updatedBefore := patBefore.UpdatedUnix
	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	pr, err := issues_model.GetPullRequestByIndex(t.Context(), repo.ID, 3)
	require.NoError(t, err)
	doer := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	issueBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: pr.IssueID})
	prBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
	repoBefore := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	require.NoError(t, db.Insert(t.Context(), &issues_model.IssueUser{IssueID: pr.IssueID, UID: doer.ID}))
	issueUserBefore := unittest.AssertExistsAndLoadBean(t, &issues_model.IssueUser{IssueID: pr.IssueID, UID: doer.ID})
	rowCountsBefore := map[string]int64{}
	for name, bean := range map[string]any{
		"issue": &issues_model.Issue{}, "pull_request": &issues_model.PullRequest{},
		"issue_user": &issues_model.IssueUser{}, "notification": &activities_model.Notification{},
		"commit_status": &git_model.CommitStatus{},
	} {
		rowCountsBefore[name], err = db.GetEngine(t.Context()).Count(bean)
		require.NoError(t, err)
	}
	gitRepo, err := gitrepo.OpenRepository(t.Context(), repo)
	require.NoError(t, err)
	internalHeadBefore, err := gitRepo.GetRefCommitID(pr.GetGitHeadRefName())
	require.NoError(t, err)
	targetBefore, err := gitRepo.GetBranchCommitID(pr.BaseBranch)
	require.NoError(t, err)
	sourceBefore, err := gitRepo.GetBranchCommitID(pr.HeadBranch)
	require.NoError(t, err)
	gitRepo.Close()
	var queued atomic.Int64
	defer test.MockVariableValue(&pull_service.AddPullRequestToCheckQueue, func(int64) { queued.Add(1) })()

	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()
	httpServer := httptest.NewServer(testWebRoutes)
	defer httpServer.Close()
	httpClient := &http.Client{Transport: mcpAuthorizationTransport{token: token, base: http.DefaultTransport}}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "integration-test", Version: "1"}, nil)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	session, err := client.Connect(ctx, &mcpsdk.StreamableClientTransport{
		Endpoint: httpServer.URL + "/mcp", HTTPClient: httpClient, DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	defer session.Close()

	tools, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.Len(t, tools.Tools, 1)
	assert.Equal(t, "pull_request.inspect", tools.Tools[0].Name)
	result, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "pull_request.inspect", Arguments: map[string]any{
		"owner": repo.OwnerName, "repository": repo.Name, "number": pr.Index,
		"changedFiles": map[string]any{"limit": 1}, "checks": true, "policy": true,
	}})
	require.NoError(t, err)
	assert.False(t, result.IsError)
	structured, ok := result.StructuredContent.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "available", structured["status"])
	inspection, ok := structured["inspection"].(map[string]any)
	require.True(t, ok)
	metadata, ok := inspection["metadata"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, issueBefore.Content, metadata["description"])
	assert.Equal(t, false, metadata["descriptionTruncated"])

	patAfter, err := auth_model.GetAccessTokenBySHA(t.Context(), token)
	require.NoError(t, err)
	assert.Equal(t, updatedBefore, patAfter.UpdatedUnix)
	assert.Zero(t, queued.Load())
	issueAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{ID: pr.IssueID})
	prAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.PullRequest{ID: pr.ID})
	repoAfter := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: repo.ID})
	issueUserAfter := unittest.AssertExistsAndLoadBean(t, &issues_model.IssueUser{IssueID: pr.IssueID, UID: doer.ID})
	assert.Equal(t, issueBefore.UpdatedUnix, issueAfter.UpdatedUnix)
	assert.Equal(t, prBefore.MergedUnix, prAfter.MergedUnix)
	assert.Equal(t, prBefore.Status, prAfter.Status)
	assert.Equal(t, repoBefore.UpdatedUnix, repoAfter.UpdatedUnix)
	assert.Equal(t, issueUserBefore.IsRead, issueUserAfter.IsRead)
	for name, bean := range map[string]any{
		"issue": &issues_model.Issue{}, "pull_request": &issues_model.PullRequest{},
		"issue_user": &issues_model.IssueUser{}, "notification": &activities_model.Notification{},
		"commit_status": &git_model.CommitStatus{},
	} {
		count, err := db.GetEngine(t.Context()).Count(bean)
		require.NoError(t, err)
		assert.Equal(t, rowCountsBefore[name], count, name)
	}
	gitRepo, err = gitrepo.OpenRepository(t.Context(), repo)
	require.NoError(t, err)
	defer gitRepo.Close()
	internalHeadAfter, err := gitRepo.GetRefCommitID(pr.GetGitHeadRefName())
	require.NoError(t, err)
	targetAfter, err := gitRepo.GetBranchCommitID(pr.BaseBranch)
	require.NoError(t, err)
	sourceAfter, err := gitRepo.GetBranchCommitID(pr.HeadBranch)
	require.NoError(t, err)
	assert.Equal(t, internalHeadBefore, internalHeadAfter)
	assert.Equal(t, targetBefore, targetAfter)
	assert.Equal(t, sourceBefore, sourceAfter)
}

func TestMCPAuthenticationBoundary(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeReadRepository)
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()

	t.Run("alternate credentials", func(t *testing.T) {
		tests := []struct {
			name   string
			path   string
			mutate func(*RequestWrapper)
		}{
			{name: "missing", path: "/mcp", mutate: func(*RequestWrapper) {}},
			{name: "Basic", path: "/mcp", mutate: func(req *RequestWrapper) { req.AddBasicAuth("user2", token) }},
			{name: "query", path: "/mcp?access_token=" + token, mutate: func(*RequestWrapper) {}},
			{name: "cookie", path: "/mcp", mutate: func(req *RequestWrapper) { req.AddCookie(&http.Cookie{Name: "token", Value: token}) }},
			{name: "Actions", path: "/mcp", mutate: func(req *RequestWrapper) { req.AddTokenAuth("actions-credential") }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				req := newMCPDiscoverRequest(t, test.path)
				test.mutate(req)
				resp := MakeRequest(t, req, http.StatusUnauthorized)
				assert.NotContains(t, resp.Body.String(), token)
			})
		}
	})

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	t.Run("inactive", func(t *testing.T) {
		user.IsActive = false
		require.NoError(t, user_model.UpdateUserColsNoAutoTime(t.Context(), user, "is_active"))
		defer func() {
			user.IsActive = true
			require.NoError(t, user_model.UpdateUserColsNoAutoTime(t.Context(), user, "is_active"))
		}()
		MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(token), http.StatusUnauthorized)
	})
	t.Run("prohibited", func(t *testing.T) {
		user.ProhibitLogin = true
		require.NoError(t, user_model.UpdateUserColsNoAutoTime(t.Context(), user, "prohibit_login"))
		defer func() {
			user.ProhibitLogin = false
			require.NoError(t, user_model.UpdateUserColsNoAutoTime(t.Context(), user, "prohibit_login"))
		}()
		MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(token), http.StatusUnauthorized)
	})
}

func TestMCPOAuthAuthenticationProfile(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&setting.MCP.Authentication, setting.MCPAuthenticationProfileOAuth)()
	require.NoError(t, auth_model.Init(t.Context()))
	app, err := auth_model.GetOAuth2ApplicationByClientID(t.Context(), auth_model.MCPBuiltinOAuth2ApplicationClientID)
	require.NoError(t, err)
	grant := &auth_model.OAuth2Grant{ApplicationID: app.ID, UserID: 2, Scope: "read:repository"}
	require.NoError(t, db.Insert(t.Context(), grant))
	resource := setting.MCPResource()
	sign := func(audience string, expiresAt time.Time) string {
		token := &oauth2_provider.Token{
			GrantID: grant.ID,
			Kind:    oauth2_provider.KindAccessToken,
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    oauth2_provider.TokenIssuer(),
				Subject:   strconv.FormatInt(grant.UserID, 10),
				Audience:  jwt.ClaimStrings{audience},
				ExpiresAt: jwt.NewNumericDate(expiresAt),
			},
		}
		signed, err := token.SignToken(oauth2_provider.DefaultSigningKey)
		require.NoError(t, err)
		return signed
	}
	accessToken := sign(resource, time.Now().Add(time.Minute))
	pat := getUserToken(t, "user2", auth_model.AccessTokenScopeReadRepository)
	originalRoutes := testWebRoutes
	defer func() { testWebRoutes = originalRoutes }()
	testWebRoutes = routers.NormalRoutes()

	resp := MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(accessToken), http.StatusOK)
	assert.Contains(t, resp.Body.String(), `"name":"forge"`)
	MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(pat), http.StatusUnauthorized)

	for _, testCase := range []struct {
		name  string
		token string
	}{
		{name: "wrong audience", token: sign(resource+"/other", time.Now().Add(time.Minute))},
		{name: "expired", token: sign(resource, time.Now().Add(-time.Minute))},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			failure := MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(testCase.token), http.StatusUnauthorized)
			challenge := failure.Header().Get("WWW-Authenticate")
			assert.Contains(t, challenge, `error="invalid_token"`)
			assert.NotContains(t, failure.Body.String(), testCase.token)
			assert.NotContains(t, failure.Body.String(), resource)
		})
	}

	grant.Scope = "read:user"
	_, err = db.GetEngine(t.Context()).ID(grant.ID).Cols("scope").Update(grant)
	require.NoError(t, err)
	failure := MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(accessToken), http.StatusForbidden)
	assert.Contains(t, failure.Header().Get("WWW-Authenticate"), `error="insufficient_scope"`)
	grant.Scope = "read:repository"
	_, err = db.GetEngine(t.Context()).ID(grant.ID).Cols("scope").Update(grant)
	require.NoError(t, err)

	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	user.ProhibitLogin = true
	require.NoError(t, user_model.UpdateUserColsNoAutoTime(t.Context(), user, "prohibit_login"))
	MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(accessToken), http.StatusUnauthorized)
	user.ProhibitLogin = false
	require.NoError(t, user_model.UpdateUserColsNoAutoTime(t.Context(), user, "prohibit_login"))

	metadata := MakeRequest(t, NewRequest(t, http.MethodGet, setting.MCPProtectedResourceMetadataPath()), http.StatusOK)
	assert.Contains(t, metadata.Body.String(), `"resource":"`+resource+`"`)
	assert.NotContains(t, metadata.Body.String(), "client_secret")

	setting.MCP.Authentication = setting.MCPAuthenticationProfilePAT
	testWebRoutes = routers.NormalRoutes()
	MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(accessToken), http.StatusUnauthorized)
	MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(pat), http.StatusOK)
}

func TestMCPQueryCredentialsAreRedactedBeforeProductionMetadata(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeReadRepository)
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&setting.AppSubURL, "/forge")()
	defer test.MockVariableValue(&setting.UseSubURLPath, true)()
	defer test.MockVariableValue(&setting.Log.AccessLogTemplate, `{{.Ctx.Req.Method}} {{.Ctx.Req.URL.RequestURI}}`)()

	var accessLog bytes.Buffer
	writer := log.NewEventWriterBase("mcp-query-redaction", "test", log.WriterMode{Level: log.INFO})
	writer.OutputWriteCloser = util.NopCloser{Writer: &accessLog}
	accessLogger := log.GetManager().GetLogger("access")
	accessLogger.AddWriters(writer)
	writerRemoved := false
	t.Cleanup(func() {
		if !writerRemoved {
			require.NoError(t, accessLogger.RemoveWriter(writer.GetWriterName()))
		}
	})

	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()
	var diagnosticURI string
	defer web.RouteMock(web.MockAfterMiddlewares, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			diagnosticURI = req.RequestURI
			next.ServeHTTP(w, req)
		})
	})()
	resp := MakeRequest(t, newMCPDiscoverRequest(t, "/forge/mcp?keep=visible&access_token=TOP-SECRET").AddTokenAuth(token), http.StatusUnauthorized)
	removeErr := accessLogger.RemoveWriter(writer.GetWriterName())
	writerRemoved = true
	require.NoError(t, removeErr)

	assert.Equal(t, "invalid bearer token\n", resp.Body.String())
	assert.Equal(t, "/forge/mcp?keep=visible", diagnosticURI)
	assert.Contains(t, accessLog.String(), "POST /mcp?keep=visible")
	assert.NotContains(t, accessLog.String(), "access_token")
	assert.NotContains(t, accessLog.String(), "TOP-SECRET")
}

func TestMCPProjectionPermissionBoundaries(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	user2Token := newPersistedMCPPAT(t, 2, "mcp-projection-user2", auth_model.AccessTokenScopeReadRepository)
	user5Token := newPersistedMCPPAT(t, 5, "mcp-projection-user5", auth_model.AccessTokenScopeReadRepository)
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()
	httpServer := httptest.NewServer(testWebRoutes)
	defer httpServer.Close()
	user2Session := connectMCPClient(t, httpServer.URL+"/mcp", user2Token)
	user5Session := connectMCPClient(t, httpServer.URL+"/mcp", user5Token)

	t.Run("unavailable is nondisclosing", func(t *testing.T) {
		requests := []map[string]any{
			{"owner": "missing-owner", "repository": "missing-repository", "number": 1},
			{"owner": "org3", "repository": "repo3", "number": 2},
			{"owner": "user2", "repository": "repo1", "number": 999999},
		}
		var neutralContent string
		for _, arguments := range requests {
			result, structured := callMCPPullRequestInspect(t, user5Session, arguments)
			assert.False(t, result.IsError)
			assert.Equal(t, map[string]any{"status": "unavailable"}, structured)
			require.Len(t, result.Content, 1)
			content, ok := result.Content[0].(*mcpsdk.TextContent)
			require.True(t, ok)
			if neutralContent == "" {
				neutralContent = content.Text
			}
			assert.Equal(t, neutralContent, content.Text)
			assert.NotContains(t, content.Text, arguments["owner"])
			assert.NotContains(t, content.Text, arguments["repository"])
		}
	})

	t.Run("Pull Requests without Code", func(t *testing.T) {
		disableRepositoryUnit(t, unit.TypeCode)
		base := map[string]any{"owner": "user2", "repository": "repo1", "number": 3}
		_, metadata := callMCPPullRequestInspect(t, user2Session, map[string]any{
			"owner": base["owner"], "repository": base["repository"], "number": base["number"], "checks": true, "policy": true,
		})
		assert.Equal(t, "available", metadata["status"])
		inspection, ok := metadata["inspection"].(map[string]any)
		require.True(t, ok)
		assert.Contains(t, inspection, "metadata")
		assert.Contains(t, inspection, "checks")
		assert.Contains(t, inspection, "policy")

		_, changedFiles := callMCPPullRequestInspect(t, user2Session, map[string]any{
			"owner": base["owner"], "repository": base["repository"], "number": base["number"], "changedFiles": map[string]any{"limit": 1},
		})
		_, diff := callMCPPullRequestInspect(t, user2Session, map[string]any{
			"owner": base["owner"], "repository": base["repository"], "number": base["number"], "diff": map[string]any{"fileLimit": 1},
		})
		assert.Equal(t, map[string]any{"status": "unavailable"}, changedFiles)
		assert.Equal(t, changedFiles, diff)
	})

	t.Run("Actions URL hidden", func(t *testing.T) {
		disableRepositoryUnit(t, unit.TypeActions)
		_, metadata := callMCPPullRequestInspect(t, user2Session, map[string]any{
			"owner": "user2", "repository": "repo1", "number": 3,
		})
		inspection := metadata["inspection"].(map[string]any)
		revisions := inspection["revisions"].(map[string]any)
		revision := revisions["internalHead"].(string)
		repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
		creator := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
		statusContext := "mcp/actions-hidden"
		require.NoError(t, git_model.NewCommitStatus(t.Context(), git_model.NewCommitStatusOptions{
			Repo: repo, Creator: creator, SHA: git.MustIDFromString(revision),
			CommitStatus: &git_model.CommitStatus{
				State: commitstatus.CommitStatusSuccess, Context: statusContext,
				TargetURL: repo.Link() + "/actions/runs/1/jobs/2",
			},
		}))

		_, checked := callMCPPullRequestInspect(t, user2Session, map[string]any{
			"owner": "user2", "repository": "repo1", "number": 3, "checks": true,
		})
		inspection = checked["inspection"].(map[string]any)
		checks := inspection["checks"].(map[string]any)["checks"].([]any)
		var projected map[string]any
		for _, candidate := range checks {
			check := candidate.(map[string]any)
			if check["context"] == statusContext {
				projected = check
				break
			}
		}
		require.NotNil(t, projected)
		assert.Equal(t, revision, projected["revision"])
		assert.Equal(t, "success", projected["state"])
		assert.NotContains(t, projected, "targetURL")
	})
}

func TestMCPPersistedPATScopes(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	tests := []struct {
		name   string
		scope  auth_model.AccessTokenScope
		status int
	}{
		{name: "exact read repository", scope: auth_model.AccessTokenScopeReadRepository, status: http.StatusOK},
		{name: "all", scope: auth_model.AccessTokenScopeAll, status: http.StatusUnauthorized},
		{name: "write repository", scope: auth_model.AccessTokenScopeWriteRepository, status: http.StatusUnauthorized},
		{name: "empty", scope: "", status: http.StatusUnauthorized},
		{name: "invalid", scope: "invalid", status: http.StatusUnauthorized},
		{name: "public only", scope: auth_model.AccessTokenScopePublicOnly, status: http.StatusUnauthorized},
		{name: "mixed", scope: "read:repository,public-only", status: http.StatusUnauthorized},
	}
	tokens := make(map[string]string, len(tests))
	for _, test := range tests {
		tokens[test.name] = newPersistedMCPPAT(t, 2, "mcp-scope-"+strings.ReplaceAll(test.name, " ", "-"), test.scope)
	}
	defer test.MockVariableValue(&setting.MCP.Enabled, true)()
	defer test.MockVariableValue(&testWebRoutes, routers.NormalRoutes())()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resp := MakeRequest(t, newMCPDiscoverRequest(t, "/mcp").AddTokenAuth(tokens[test.name]), test.status)
			assert.NotContains(t, resp.Body.String(), tokens[test.name])
			if test.status == http.StatusUnauthorized {
				assert.Equal(t, "invalid bearer token\n", resp.Body.String())
			} else {
				assert.Contains(t, resp.Body.String(), `"name":"forge"`)
			}
		})
	}
}
