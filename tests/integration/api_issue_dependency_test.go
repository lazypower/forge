// Copyright 2024 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package integration

import (
	"fmt"
	"net/http"
	"strconv"
	"testing"

	auth_model "gitea.dev/models/auth"
	"gitea.dev/models/db"
	issues_model "gitea.dev/models/issues"
	"gitea.dev/models/perm"
	access_model "gitea.dev/models/perm/access"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/models/unit"
	"gitea.dev/models/unittest"
	user_model "gitea.dev/models/user"
	"gitea.dev/modules/setting"
	api "gitea.dev/modules/structs"
	repo_service "gitea.dev/services/repository"
	"gitea.dev/tests"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func enableRepoDependencies(t *testing.T, repoID int64) {
	t.Helper()

	repoUnit := unittest.AssertExistsAndLoadBean(t, &repo_model.RepoUnit{RepoID: repoID, Type: unit.TypeIssues})
	repoUnit.IssuesConfig().EnableDependencies = true
	assert.NoError(t, repo_model.UpdateRepoUnitConfig(t.Context(), repoUnit))
}

func TestAPICreateIssueDependencyCrossRepoPermission(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	allowCrossRepository := setting.Service.AllowCrossRepositoryDependencies
	setting.Service.AllowCrossRepositoryDependencies = true
	t.Cleanup(func() {
		setting.Service.AllowCrossRepositoryDependencies = allowCrossRepository
	})

	targetRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	targetIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: targetRepo.ID, Index: 1})
	dependencyRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	assert.True(t, dependencyRepo.IsPrivate)
	dependencyIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: dependencyRepo.ID, Index: 1})

	enableRepoDependencies(t, targetIssue.RepoID)
	enableRepoDependencies(t, dependencyRepo.ID)

	// remove user 40 access from target repository
	_, err := db.DeleteByID[access_model.Access](t.Context(), 30)
	assert.NoError(t, err)

	url := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/dependencies", "user2", "repo1", targetIssue.Index)
	dependencyMeta := &api.IssueMeta{
		Owner: "org3",
		Name:  "repo3",
		Index: dependencyIssue.Index,
	}

	user40 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
	// user40 has no access to both target issue and dependency issue
	writerToken := getUserToken(t, "user40", auth_model.AccessTokenScopeWriteIssue)
	req := NewRequestWithJSON(t, "POST", url, dependencyMeta).
		AddTokenAuth(writerToken)
	MakeRequest(t, req, http.StatusNotFound)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{
		IssueID:      targetIssue.ID,
		DependencyID: dependencyIssue.ID,
	})

	// add user40 as a collaborator to dependency repository with read permission
	assert.NoError(t, repo_service.AddOrUpdateCollaborator(t.Context(), dependencyRepo, user40, perm.AccessModeRead))

	// try again after getting read permission to dependency repository
	req = NewRequestWithJSON(t, "POST", url, dependencyMeta).
		AddTokenAuth(writerToken)
	MakeRequest(t, req, http.StatusNotFound)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{
		IssueID:      targetIssue.ID,
		DependencyID: dependencyIssue.ID,
	})

	// add user40 as a collaborator to target repository with write permission
	assert.NoError(t, repo_service.AddOrUpdateCollaborator(t.Context(), targetRepo, user40, perm.AccessModeWrite))

	req = NewRequestWithJSON(t, "POST", url, dependencyMeta).
		AddTokenAuth(writerToken)
	MakeRequest(t, req, http.StatusCreated)
	unittest.AssertExistsAndLoadBean(t, &issues_model.IssueDependency{
		IssueID:      targetIssue.ID,
		DependencyID: dependencyIssue.ID,
	})
}

func TestAPIDeleteIssueDependencyCrossRepoPermission(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	allowCrossRepository := setting.Service.AllowCrossRepositoryDependencies
	setting.Service.AllowCrossRepositoryDependencies = false
	t.Cleanup(func() {
		setting.Service.AllowCrossRepositoryDependencies = allowCrossRepository
	})

	targetRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	targetIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: targetRepo.ID, Index: 1})
	dependencyRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	assert.True(t, dependencyRepo.IsPrivate)
	dependencyIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: dependencyRepo.ID, Index: 1})

	enableRepoDependencies(t, targetIssue.RepoID)
	enableRepoDependencies(t, dependencyRepo.ID)

	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	assert.NoError(t, issues_model.CreateIssueDependency(t.Context(), user1, targetIssue, dependencyIssue))

	// remove user 40 access from target repository
	_, err := db.DeleteByID[access_model.Access](t.Context(), 30)
	assert.NoError(t, err)

	url := fmt.Sprintf("/api/v1/repos/%s/%s/issues/%d/dependencies", "user2", "repo1", targetIssue.Index)
	dependencyMeta := &api.IssueMeta{
		Owner: "org3",
		Name:  "repo3",
		Index: dependencyIssue.Index,
	}

	user40 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
	// user40 has no access to both target issue and dependency issue
	writerToken := getUserToken(t, "user40", auth_model.AccessTokenScopeWriteIssue)
	req := NewRequestWithJSON(t, "DELETE", url, dependencyMeta).
		AddTokenAuth(writerToken)
	MakeRequest(t, req, http.StatusNotFound)
	unittest.AssertExistsAndLoadBean(t, &issues_model.IssueDependency{
		IssueID:      targetIssue.ID,
		DependencyID: dependencyIssue.ID,
	})

	// add user40 as a collaborator to dependency repository with read permission
	assert.NoError(t, repo_service.AddOrUpdateCollaborator(t.Context(), dependencyRepo, user40, perm.AccessModeRead))

	// try again after getting read permission to dependency repository
	req = NewRequestWithJSON(t, "DELETE", url, dependencyMeta).
		AddTokenAuth(writerToken)
	MakeRequest(t, req, http.StatusNotFound)
	unittest.AssertExistsAndLoadBean(t, &issues_model.IssueDependency{
		IssueID:      targetIssue.ID,
		DependencyID: dependencyIssue.ID,
	})

	// add user40 as a collaborator to target repository with write permission
	assert.NoError(t, repo_service.AddOrUpdateCollaborator(t.Context(), targetRepo, user40, perm.AccessModeWrite))

	req = NewRequestWithJSON(t, "DELETE", url, dependencyMeta).
		AddTokenAuth(writerToken)
	MakeRequest(t, req, http.StatusCreated)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{
		IssueID:      targetIssue.ID,
		DependencyID: dependencyIssue.ID,
	})
}

func TestWebDeleteIssueDependencyCrossRepoPermission(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	allowCrossRepository := setting.Service.AllowCrossRepositoryDependencies
	setting.Service.AllowCrossRepositoryDependencies = false
	t.Cleanup(func() {
		setting.Service.AllowCrossRepositoryDependencies = allowCrossRepository
	})

	targetRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	targetIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: targetRepo.ID, Index: 1})
	dependencyRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	assert.True(t, dependencyRepo.IsPrivate)
	dependencyIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: dependencyRepo.ID, Index: 1})

	enableRepoDependencies(t, targetIssue.RepoID)
	enableRepoDependencies(t, dependencyRepo.ID)

	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	assert.NoError(t, issues_model.CreateIssueDependency(t.Context(), user1, targetIssue, dependencyIssue))

	user40 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
	assert.NoError(t, repo_service.AddOrUpdateCollaborator(t.Context(), targetRepo, user40, perm.AccessModeWrite))

	session := loginUser(t, user40.Name)
	req := NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/repo1/issues/%d/dependency/delete", targetIssue.Index), map[string]string{
		"removeDependencyID": strconv.FormatInt(dependencyIssue.ID, 10),
		"dependencyType":     "blockedBy",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	unittest.AssertExistsAndLoadBean(t, &issues_model.IssueDependency{
		IssueID:      targetIssue.ID,
		DependencyID: dependencyIssue.ID,
	})

	assert.NoError(t, repo_service.AddOrUpdateCollaborator(t.Context(), dependencyRepo, user40, perm.AccessModeRead))

	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/repo1/issues/%d/dependency/delete", targetIssue.Index), map[string]string{
		"removeDependencyID": strconv.FormatInt(dependencyIssue.ID, 10),
		"dependencyType":     "blockedBy",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{
		IssueID:      targetIssue.ID,
		DependencyID: dependencyIssue.ID,
	})
}

func TestWebDeleteIssueBlockingCrossRepoPermission(t *testing.T) {
	defer tests.PrepareTestEnv(t)()
	allowCrossRepository := setting.Service.AllowCrossRepositoryDependencies
	setting.Service.AllowCrossRepositoryDependencies = false
	t.Cleanup(func() {
		setting.Service.AllowCrossRepositoryDependencies = allowCrossRepository
	})

	blockedRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	blockedIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: blockedRepo.ID, Index: 1})
	prerequisiteRepo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 3})
	assert.True(t, prerequisiteRepo.IsPrivate)
	prerequisiteIssue := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: prerequisiteRepo.ID, Index: 1})

	enableRepoDependencies(t, blockedRepo.ID)
	enableRepoDependencies(t, prerequisiteRepo.ID)

	user1 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 1})
	assert.NoError(t, issues_model.CreateIssueDependency(t.Context(), user1, blockedIssue, prerequisiteIssue))

	_, err := db.DeleteByID[access_model.Access](t.Context(), 30)
	assert.NoError(t, err)

	user40 := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 40})
	session := loginUser(t, user40.Name)
	url := fmt.Sprintf("/%s/%s/issues/%d/dependency/delete", prerequisiteRepo.OwnerName, prerequisiteRepo.Name, prerequisiteIssue.Index)
	form := map[string]string{
		"removeDependencyID": strconv.FormatInt(blockedIssue.ID, 10),
		"dependencyType":     "blocking",
	}

	// The private prerequisite remains undisclosed without read permission.
	req := NewRequestWithValues(t, "POST", url, form)
	session.MakeRequest(t, req, http.StatusNotFound)
	unittest.AssertExistsAndLoadBean(t, &issues_model.IssueDependency{
		IssueID:      blockedIssue.ID,
		DependencyID: prerequisiteIssue.ID,
	})

	assert.NoError(t, repo_service.AddOrUpdateCollaborator(t.Context(), prerequisiteRepo, user40, perm.AccessModeRead))

	// Reading the prerequisite is insufficient without write permission on the blocked issue.
	req = NewRequestWithValues(t, "POST", url, form)
	session.MakeRequest(t, req, http.StatusSeeOther)
	unittest.AssertExistsAndLoadBean(t, &issues_model.IssueDependency{
		IssueID:      blockedIssue.ID,
		DependencyID: prerequisiteIssue.ID,
	})

	assert.NoError(t, repo_service.AddOrUpdateCollaborator(t.Context(), blockedRepo, user40, perm.AccessModeWrite))

	// Write on the blocked issue and read-only access to the prerequisite is sufficient.
	req = NewRequestWithValues(t, "POST", url, form)
	session.MakeRequest(t, req, http.StatusSeeOther)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{
		IssueID:      blockedIssue.ID,
		DependencyID: prerequisiteIssue.ID,
	})
}

func TestHTMLRESTIssueDependencyConvergence(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	target := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: 1, Index: 1})
	dependency := unittest.AssertExistsAndLoadBean(t, &issues_model.Issue{RepoID: 1, Index: 4})
	enableRepoDependencies(t, target.RepoID)

	url := fmt.Sprintf("/api/v1/repos/user2/repo1/issues/%d/dependencies", target.Index)
	meta := &api.IssueMeta{Owner: "user2", Name: "repo1", Index: dependency.Index}
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteIssue)
	req := NewRequestWithJSON(t, "POST", url, meta).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)

	session := loginUser(t, "user2")
	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/repo1/issues/%d/dependency/add", target.Index), map[string]string{
		"newDependency": strconv.FormatInt(dependency.ID, 10),
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	req = NewRequestWithJSON(t, "POST", url, meta).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)
	unittest.AssertCount(t, &issues_model.IssueDependency{IssueID: target.ID, DependencyID: dependency.ID}, 1)
	assert.Equal(t, 2, unittest.GetCount(t, &issues_model.Comment{}, unittest.Cond("type = ? AND issue_id IN (?, ?)", issues_model.CommentTypeAddDependency, target.ID, dependency.ID)))

	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/repo1/issues/%d/dependency/delete", target.Index), map[string]string{
		"removeDependencyID": strconv.FormatInt(dependency.ID, 10),
		"dependencyType":     "blockedBy",
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	req = NewRequestWithJSON(t, "DELETE", url, meta).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusCreated)
	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: target.ID, DependencyID: dependency.ID})
	assert.Equal(t, 2, unittest.GetCount(t, &issues_model.Comment{}, unittest.Cond("type = ? AND issue_id IN (?, ?)", issues_model.CommentTypeRemoveDependency, target.ID, dependency.ID)))
}

func TestHTMLRESTIssueDependencyTransitiveCycle(t *testing.T) {
	defer tests.PrepareTestEnv(t)()

	repo := unittest.AssertExistsAndLoadBean(t, &repo_model.Repository{ID: 1})
	enableRepoDependencies(t, repo.ID)
	issues := issues_model.IssueList{
		{RepoID: repo.ID, Index: 10_001, PosterID: 2, Title: "cycle a"},
		{RepoID: repo.ID, Index: 10_002, PosterID: 2, Title: "cycle b"},
		{RepoID: repo.ID, Index: 10_003, PosterID: 2, Title: "cycle c"},
	}
	require.NoError(t, issues_model.InsertIssues(t.Context(), issues...))
	user := unittest.AssertExistsAndLoadBean(t, &user_model.User{ID: 2})
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), user, issues[1], issues[2]))
	require.NoError(t, issues_model.CreateIssueDependency(t.Context(), user, issues[2], issues[0]))
	commentsBefore := unittest.GetCount(t, &issues_model.Comment{})

	url := fmt.Sprintf("/api/v1/repos/user2/repo1/issues/%d/dependencies", issues[0].Index)
	meta := &api.IssueMeta{Owner: "user2", Name: "repo1", Index: issues[1].Index}
	token := getUserToken(t, "user2", auth_model.AccessTokenScopeWriteIssue)
	req := NewRequestWithJSON(t, "POST", url, meta).AddTokenAuth(token)
	MakeRequest(t, req, http.StatusUnprocessableEntity)

	session := loginUser(t, "user2")
	req = NewRequestWithValues(t, "POST", fmt.Sprintf("/user2/repo1/issues/%d/dependency/add", issues[0].Index), map[string]string{
		"newDependency": strconv.FormatInt(issues[1].ID, 10),
	})
	session.MakeRequest(t, req, http.StatusSeeOther)

	unittest.AssertNotExistsBean(t, &issues_model.IssueDependency{IssueID: issues[0].ID, DependencyID: issues[1].ID})
	assert.Equal(t, commentsBefore, unittest.GetCount(t, &issues_model.Comment{}))
}
