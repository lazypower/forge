// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	auth_model "gitea.dev/models/auth"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	"gitea.dev/services/oauth2_provider"

	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	authenticatedUserKey            = "forge.authenticated-user"
	authenticatedOAuthCredentialKey = "forge.authenticated-oauth-credential"
	readRepositoryScope             = auth_model.AccessTokenScopeReadRepository
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

type verifiedOAuthCredential struct {
	Principal      *user_model.User
	Application    *auth_model.OAuth2Application
	Grant          *auth_model.OAuth2Grant
	CredentialID   string
	Profile        auth_model.MCPProfile
	CanonicalScope string
	Scopes         []string
}

func newPATVerifier() mcpauth.TokenVerifier {
	return newPATVerifierWithLookups(auth_model.GetAccessTokenBySHA, user_model.GetUserByID)
}

func newOAuthVerifier() mcpauth.TokenVerifier {
	return func(ctx context.Context, tokenValue string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		verified, err := oauth2_provider.VerifyAccessToken(ctx, tokenValue, setting.MCPResource(), oauth2_provider.DefaultSigningKey)
		if err != nil || verified == nil || verified.Grant == nil || !validMCPPrincipal(verified.Principal) {
			return nil, errInvalidBearerToken
		}
		app, err := auth_model.GetOAuth2ApplicationByID(ctx, verified.Grant.ApplicationID)
		if err != nil {
			return nil, errInvalidBearerToken
		}
		profile, err := oauth2_provider.MCPProfileForAccessToken(app, verified.Grant)
		if err != nil || verified.OAuthScope != profile.CanonicalScope {
			return nil, errInvalidBearerToken
		}
		credential := &verifiedOAuthCredential{
			Principal:      verified.Principal,
			Application:    app,
			Grant:          verified.Grant,
			CredentialID:   verified.CredentialID,
			Profile:        profile.Name,
			CanonicalScope: profile.CanonicalScope,
			Scopes:         append([]string(nil), profile.Scopes...),
		}
		return &mcpauth.TokenInfo{
			Scopes:     append([]string(nil), profile.Scopes...),
			Expiration: verified.ExpiresAt,
			UserID:     strconv.FormatInt(verified.Principal.ID, 10),
			Extra: map[string]any{
				authenticatedUserKey:            verified.Principal,
				authenticatedOAuthCredentialKey: credential,
			},
		}, nil
	}
}

func newPATVerifierWithLookups(findToken accessTokenLookup, findUser userLookup) mcpauth.TokenVerifier {
	return func(ctx context.Context, tokenValue string, _ *http.Request) (*mcpauth.TokenInfo, error) {
		token, err := findToken(ctx, tokenValue)
		if err != nil || token == nil || token.Scope != readRepositoryScope {
			return nil, errInvalidBearerToken
		}
		user, err := findUser(ctx, token.UID)
		if err != nil || !validMCPPrincipal(user) {
			return nil, errInvalidBearerToken
		}
		return &mcpauth.TokenInfo{
			Scopes: []string{string(readRepositoryScope)},
			UserID: strconv.FormatInt(user.ID, 10),
			Extra:  map[string]any{authenticatedUserKey: user},
		}, nil
	}
}

func validMCPPrincipal(user *user_model.User) bool {
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
	if !ok || !validMCPPrincipal(user) {
		return nil, errors.New("authenticated principal unavailable")
	}
	return user, nil
}

func authenticatedOAuthCredential(ctx context.Context) (*verifiedOAuthCredential, error) {
	tokenInfo := mcpauth.TokenInfoFromContext(ctx)
	if tokenInfo == nil {
		return nil, errors.New("verified OAuth credential unavailable")
	}
	credential, ok := tokenInfo.Extra[authenticatedOAuthCredentialKey].(*verifiedOAuthCredential)
	if !ok || credential == nil || !validMCPPrincipal(credential.Principal) || credential.Application == nil || credential.Grant == nil || credential.CredentialID == "" || credential.CanonicalScope == "" {
		return nil, errors.New("verified OAuth credential unavailable")
	}
	return credential, nil
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

func requireOAuthBearerHeader(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		values := req.Header.Values("Authorization")
		queryCredential := queryCredentialWasRemoved(req.Context()) || req.URL.Query().Has("token") || req.URL.Query().Has("access_token")
		if len(values) == 0 && !queryCredential {
			next.ServeHTTP(w, req)
			return
		}
		if len(values) != 1 || queryCredential {
			rejectOAuthBearerCredential(w)
			return
		}
		scheme, token, ok := strings.Cut(values[0], " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || !bearerTokenPattern.MatchString(token) {
			rejectOAuthBearerCredential(w)
			return
		}
		next.ServeHTTP(w, req)
	})
}

func rejectOAuthBearerCredential(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", oauthBearerChallenge("invalid_token"))
	http.Error(w, "invalid bearer token", http.StatusUnauthorized)
}

func oauthBearerChallenge(oauthError string) string {
	challenge := fmt.Sprintf(`Bearer resource_metadata=%q, scope=%q`, setting.MCPProtectedResourceMetadataURL(), string(readRepositoryScope))
	if oauthError != "" {
		challenge += fmt.Sprintf(`, error=%q`, oauthError)
	}
	return challenge
}

type oauthChallengeResponseWriter struct {
	http.ResponseWriter
}

func (w oauthChallengeResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w oauthChallengeResponseWriter) WriteHeader(status int) {
	challenge := w.Header().Get("WWW-Authenticate")
	if challenge == "" {
		challenge = oauthBearerChallenge("")
	}
	switch status {
	case http.StatusUnauthorized:
		w.Header().Set("WWW-Authenticate", challenge+`, error="invalid_token"`)
	case http.StatusForbidden:
		w.Header().Set("WWW-Authenticate", challenge+`, error="insufficient_scope"`)
	}
	w.ResponseWriter.WriteHeader(status)
}

func augmentOAuthChallenges(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		next.ServeHTTP(oauthChallengeResponseWriter{ResponseWriter: w}, req)
	})
}

func validBearerHeader(req *http.Request) bool {
	values := req.Header.Values("Authorization")
	if len(values) != 1 || queryCredentialWasRemoved(req.Context()) || req.URL.Query().Has("token") || req.URL.Query().Has("access_token") {
		return false
	}
	scheme, token, ok := strings.Cut(values[0], " ")
	return ok && strings.EqualFold(scheme, "Bearer") && bearerTokenPattern.MatchString(token)
}
