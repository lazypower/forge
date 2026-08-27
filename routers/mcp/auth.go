// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	auth_model "gitea.dev/models/auth"
	user_model "gitea.dev/models/user"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	authenticatedUserKey = "forge.authenticated-user"
	readRepositoryScope  = auth_model.AccessTokenScopeReadRepository
)

var bearerTokenPattern = regexp.MustCompile(`^[A-Za-z0-9._~+/\-]+={0,}$`)

type invalidBearerTokenError struct{}

func (invalidBearerTokenError) Error() string { return "invalid bearer token" }
func (invalidBearerTokenError) Unwrap() error { return mcpauth.ErrInvalidToken }

var errInvalidBearerToken error = invalidBearerTokenError{}

type (
	accessTokenLookup func(context.Context, string) (*auth_model.AccessToken, error)
	userLookup        func(context.Context, int64) (*user_model.User, error)
)

func newPATVerifier() mcpauth.TokenVerifier {
	return newPATVerifierWithLookups(auth_model.GetAccessTokenBySHA, user_model.GetUserByID)
}

func newPATVerifierWithLookups(findToken accessTokenLookup, findUser userLookup) mcpauth.TokenVerifier {
	return func(ctx context.Context, tokenValue string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		token, err := findToken(ctx, tokenValue)
		if err != nil || token == nil || token.Scope != readRepositoryScope {
			return nil, errInvalidBearerToken
		}
		user, err := findUser(ctx, token.UID)
		if err != nil || !validPATPrincipal(user) {
			return nil, errInvalidBearerToken
		}
		return &mcpauth.TokenInfo{
			Scopes: []string{string(readRepositoryScope)},
			UserID: strconv.FormatInt(user.ID, 10),
			Extra:  map[string]any{authenticatedUserKey: user},
		}, nil
	}
}

func validPATPrincipal(user *user_model.User) bool {
	if user == nil || user.ID <= 0 || !user.IsActive || user.ProhibitLogin || user.IsGhost() || user.IsGiteaActions() {
		return false
	}
	return user.Type == user_model.UserTypeIndividual || user.Type == user_model.UserTypeBot || user.Type == user_model.UserTypeRemoteUser
}

func authenticatedUser(ctx context.Context) (*user_model.User, error) {
	tokenInfo := mcpauth.TokenInfoFromContext(ctx)
	if tokenInfo == nil {
		return nil, errors.New("authenticated principal unavailable")
	}
	user, ok := tokenInfo.Extra[authenticatedUserKey].(*user_model.User)
	if !ok || !validPATPrincipal(user) {
		return nil, errors.New("authenticated principal unavailable")
	}
	return user, nil
}

func requireBearerHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !validBearerHeader(req) {
			rejectBearerCredential(w)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func rejectBearerCredential(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, "invalid bearer token", http.StatusUnauthorized)
}

func validBearerHeader(req *http.Request) bool {
	values := req.Header.Values("Authorization")
	if len(values) != 1 || queryCredentialWasRemoved(req.Context()) || req.URL.Query().Has("token") || req.URL.Query().Has("access_token") {
		return false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	return ok && strings.EqualFold(scheme, "Bearer") && bearerTokenPattern.MatchString(token)
}
