// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

package work

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"

	issues_model "gitea.dev/models/issues"
	project_model "gitea.dev/models/project"
	repo_model "gitea.dev/models/repo"
	"gitea.dev/modules/json"
)

const (
	planTokenVersion  = 1
	planTokenLifetime = 5 * time.Minute
)

var (
	errInvalidPlanToken = errors.New("invalid Work plan token")
	planTokenNow        = time.Now
)

type planTokenPayload struct {
	Version      int    `json:"v"`
	RepositoryID int64  `json:"r"`
	ProjectID    int64  `json:"p"`
	Digest       string `json:"d"`
	ExpiresUnix  int64  `json:"e"`
}

type planMemberFact struct {
	IssueID int64 `json:"i"`
	Closed  bool  `json:"x"`
	Pull    bool  `json:"q"`
}

type planIssueFact struct {
	ID      int64 `json:"i"`
	RepoID  int64 `json:"r"`
	Closed  bool  `json:"c"`
	Pull    bool  `json:"p"`
	Hidden  bool  `json:"h"`
	Missing bool  `json:"m"`
}

type planEdgeFact struct {
	Blocked      int64 `json:"b"`
	Prerequisite int64 `json:"p"`
}

type planDigestFacts struct {
	RepositoryID int64                       `json:"repository"`
	Archived     bool                        `json:"archived"`
	ProjectID    int64                       `json:"project"`
	Planning     project_model.PlanningState `json:"planning"`
	Closed       bool                        `json:"closed"`
	MemberBound  bool                        `json:"memberBound"`
	GraphBound   bool                        `json:"graphBound"`
	Cyclic       bool                        `json:"cyclic"`
	Members      []planMemberFact            `json:"members"`
	Issues       []planIssueFact             `json:"issues"`
	Edges        []planEdgeFact              `json:"edges"`
}

func makePlanToken(secret string, repo *repo_model.Repository, project *project_model.Project, members []project_model.WorkProjectIssue, issues map[int64]*issues_model.Issue, graph *graph, memberBound bool) (string, error) {
	digest, err := digestPlanFacts(repo, project, members, issues, graph, memberBound)
	if err != nil {
		return "", err
	}
	payload := planTokenPayload{
		Version: planTokenVersion, RepositoryID: repo.ID, ProjectID: project.ID, Digest: digest,
		ExpiresUnix: planTokenNow().UTC().Add(planTokenLifetime).Unix(),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	return base64.RawURLEncoding.EncodeToString(raw) + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func validatePlanTokenAgainstCurrent(secret, expected, current string) error {
	expectedPayload, err := decodePlanToken(secret, expected)
	if err != nil {
		return err
	}
	currentPayload, err := decodePlanToken(secret, current)
	if err != nil || expectedPayload.RepositoryID != currentPayload.RepositoryID || expectedPayload.ProjectID != currentPayload.ProjectID ||
		!hmac.Equal([]byte(expectedPayload.Digest), []byte(currentPayload.Digest)) {
		return errInvalidPlanToken
	}
	return nil
}

func decodePlanToken(secret, encoded string) (*planTokenPayload, error) {
	payloadPart, signaturePart, found := strings.Cut(encoded, ".")
	if !found {
		return nil, errInvalidPlanToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(payloadPart)
	if err != nil {
		return nil, errInvalidPlanToken
	}
	signature, err := base64.RawURLEncoding.DecodeString(signaturePart)
	if err != nil {
		return nil, errInvalidPlanToken
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return nil, errInvalidPlanToken
	}
	payload := new(planTokenPayload)
	if json.Unmarshal(raw, payload) != nil || payload.Version != planTokenVersion || payload.RepositoryID <= 0 || payload.ProjectID <= 0 ||
		payload.ExpiresUnix < planTokenNow().UTC().Unix() {
		return nil, errInvalidPlanToken
	}
	return payload, nil
}

func digestPlanFacts(repo *repo_model.Repository, project *project_model.Project, members []project_model.WorkProjectIssue, issues map[int64]*issues_model.Issue, dependencyGraph *graph, memberBound bool) (string, error) {
	facts := planDigestFacts{
		RepositoryID: repo.ID, Archived: repo.IsArchived, ProjectID: project.ID, Planning: project.PlanningState,
		Closed: project.IsClosed, MemberBound: memberBound, GraphBound: dependencyGraph.overBound, Cyclic: dependencyGraph.cyclic,
		Members: make([]planMemberFact, 0, len(members)), Issues: make([]planIssueFact, 0, len(dependencyGraph.nodes)), Edges: []planEdgeFact{},
	}
	for _, member := range members {
		issue := issues[member.IssueID]
		facts.Members = append(facts.Members, planMemberFact{
			IssueID: member.IssueID, Closed: issue != nil && issue.IsClosed, Pull: member.IsPull,
		})
	}
	sort.Slice(facts.Members, func(i, j int) bool { return facts.Members[i].IssueID < facts.Members[j].IssueID })
	issueIDs := make([]int64, 0, len(dependencyGraph.nodes)+len(dependencyGraph.missing))
	for id := range dependencyGraph.nodes {
		issueIDs = append(issueIDs, id)
	}
	for id := range dependencyGraph.missing {
		if !slices.Contains(issueIDs, id) {
			issueIDs = append(issueIDs, id)
		}
	}
	slices.Sort(issueIDs)
	for _, id := range issueIDs {
		issue := dependencyGraph.nodes[id]
		fact := planIssueFact{ID: id, Hidden: dependencyGraph.hidden[id], Missing: dependencyGraph.missing[id]}
		if issue != nil {
			fact.RepoID, fact.Closed, fact.Pull = issue.RepoID, issue.IsClosed, issue.IsPull
		}
		facts.Issues = append(facts.Issues, fact)
		dependencies := slices.Clone(dependencyGraph.edges[id])
		slices.Sort(dependencies)
		for _, dependencyID := range dependencies {
			facts.Edges = append(facts.Edges, planEdgeFact{Blocked: id, Prerequisite: dependencyID})
		}
	}
	raw, err := json.Marshal(facts)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(digest[:]), nil
}
