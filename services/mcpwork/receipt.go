// Copyright 2026 The Gitea Authors. All rights reserved.
// SPDX-License-Identifier: MIT

// Package mcpwork provides durable idempotency and honest provenance for MCP
// Work mutations. Native Work facts remain owned by their existing services.
package mcpwork

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gitea.dev/models/db"
	mcpwork_model "gitea.dev/models/mcpwork"
	"gitea.dev/modules/json"
	"gitea.dev/modules/timeutil"

	"github.com/google/uuid"
	"github.com/gowebpki/jcs"
)

const (
	actorTrustUnverified = "unverified"
	originMCP            = "mcp"
	recoveryTimeout      = 5 * time.Second
)

var (
	// ErrInvalidRequest means the request cannot safely identify one mutation.
	ErrInvalidRequest = errors.New("invalid MCP Work receipt request")
	// ErrInvalidCompletion means a mutation callback violated the frozen contract.
	ErrInvalidCompletion = errors.New("invalid MCP Work mutation completion")
	// ErrIdempotencyConflict means the key was already used for another request.
	ErrIdempotencyConflict = errors.New("idempotency key already used for another request")
	// ErrOutcomeUnknown means a commit result could not be established safely.
	ErrOutcomeUnknown = errors.New("MCP Work mutation outcome is unknown")
	// ErrReceiptTombstoned means detail was minimized while the key remains reserved.
	ErrReceiptTombstoned = errors.New("MCP Work mutation receipt was retained as a tombstone")
	// ErrReceiptRetentionRequired means policy did not authorize detail minimization.
	ErrReceiptRetentionRequired = errors.New("MCP Work receipt detail must be retained")
)

var localReferencePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

// Authority is the verified OAuth context captured at mutation time. The MCP
// adapter must populate it from the fixed work-write profile, never client data.
type Authority struct {
	PrincipalID        int64
	OAuthApplicationID int64
	OAuthGrantID       int64
	CredentialJTI      string
	Audience           string
	Scope              string
}

// Request contains the complete default-expanded tool input. ExpandedInput
// must be a JSON object containing the same top-level idempotencyKey; the key is
// removed before RFC 8785 canonicalization and is never retained.
type Request struct {
	Tool           string
	SchemaVersion  string
	IdempotencyKey string
	ExpandedInput  json.Value
	Authority      Authority
}

// Operation identifies the receipt being built inside the transaction.
type Operation struct {
	UUID string
}

// ArtifactReference is a stable native Project or Issue link. ArtifactNumber
// is required for Issues and ignored for Projects.
type ArtifactReference struct {
	RepositoryID   int64
	Kind           mcpwork_model.ArtifactKind
	ArtifactID     int64
	ArtifactNumber int64
	LocalReference string
}

// EventReference links provenance to one native event row without copying it.
type EventReference struct {
	RepositoryID int64
	Kind         mcpwork_model.EventKind
	EventID      int64
	ArtifactKind mcpwork_model.ArtifactKind
	ArtifactID   int64
}

// Completion is the only information the domain callback returns to the
// receipt service. Rejected completions require a stable non-sensitive code.
type Completion struct {
	Outcome     mcpwork_model.Outcome
	ProblemCode string
	Artifacts   []ArtifactReference
	Events      []EventReference
}

// Mutation performs all native facts and timeline persistence using txCtx. It
// may run again after a serialization rollback and must perform no external
// effect.
type Mutation func(txCtx context.Context, operation Operation) (Completion, error)

// Result is the safe stored outcome. Credential provenance, digests, and
// artifact/event links are intentionally not exposed; Present rechecks current
// permission before returning links.
type Result struct {
	OperationUUID string
	Outcome       mcpwork_model.Outcome
	ProblemCode   string
	CommittedAt   time.Time
	Replayed      bool
	Tombstoned    bool
}

type (
	transactionRunner func(context.Context, func(context.Context) error) error
	recoveryLookup    func(context.Context, int64, string, string) (*mcpwork_model.Receipt, error)
	clock             func() time.Time
	uuidGenerator     func() string
)

// Service owns receipt policy and the outer serializable transaction.
type Service struct {
	secret         []byte
	runTransaction transactionRunner
	recoverReceipt recoveryLookup
	now            clock
	newUUID        uuidGenerator
}

// NewService constructs a receipt service with a dedicated instance secret.
// The secret must not be shared with cursor or token signing.
func NewService(secret []byte) (*Service, error) {
	if len(secret) < 32 {
		return nil, ErrInvalidRequest
	}
	return &Service{
		secret:         append([]byte(nil), secret...),
		runTransaction: db.WithWorkTx,
		recoverReceipt: mcpwork_model.FindByKey,
		now:            time.Now,
		newUUID:        uuid.NewString,
	}, nil
}

// Execute runs one native mutation and its receipt atomically. A matching
// replay never invokes mutate. A definitely absent ambiguous commit is retried
// once as a complete serializable operation.
func (s *Service) Execute(ctx context.Context, request Request, mutate Mutation) (*Result, error) {
	prepared, err := s.prepare(request)
	if err != nil || mutate == nil {
		return nil, ErrInvalidRequest
	}
	operation := Operation{UUID: s.newUUID()}
	operationID, err := uuid.Parse(operation.UUID)
	if err != nil || operationID.Version() != 4 {
		return nil, ErrInvalidRequest
	}
	for envelopeAttempt := range 2 {
		result, commitCandidate, err := s.executeOnce(ctx, prepared, operation, mutate)
		if err == nil {
			return result, nil
		}
		var callbackErr *mutationError
		if errors.As(err, &callbackErr) {
			return nil, callbackErr.err
		}
		var conflict *db.WorkTransactionConflict
		if errors.As(err, &conflict) {
			return nil, err
		}
		var claimErr *receiptClaimError
		if !commitCandidate && !errors.As(err, &claimErr) {
			return nil, err
		}

		recovered, found, lookupErr := s.recover(ctx, prepared)
		if found {
			return recovered, lookupErr
		}
		if lookupErr != nil {
			return nil, ErrOutcomeUnknown
		}
		if envelopeAttempt == 1 {
			return nil, ErrOutcomeUnknown
		}
	}
	panic("unreachable")
}

type preparedRequest struct {
	request        Request
	audienceDigest string
	keyDigest      string
	requestDigest  string
}

func (s *Service) prepare(request Request) (*preparedRequest, error) {
	if len(request.Tool) == 0 || len(request.Tool) > 64 || len(request.SchemaVersion) == 0 || len(request.SchemaVersion) > 16 ||
		request.Authority.PrincipalID <= 0 || request.Authority.OAuthApplicationID <= 0 || request.Authority.OAuthGrantID <= 0 ||
		request.Authority.Scope == "" || len(request.Authority.Scope) > 255 {
		return nil, ErrInvalidRequest
	}
	credentialID, err := uuid.Parse(request.Authority.CredentialJTI)
	if err != nil || credentialID.Version() != 4 {
		return nil, ErrInvalidRequest
	}
	audience, err := url.Parse(request.Authority.Audience)
	if err != nil || !audience.IsAbs() || audience.Fragment != "" || strings.TrimSpace(request.Authority.Audience) != request.Authority.Audience {
		return nil, ErrInvalidRequest
	}
	if len(request.IdempotencyKey) < 16 || len(request.IdempotencyKey) > 128 || !utf8.ValidString(request.IdempotencyKey) {
		return nil, ErrInvalidRequest
	}
	for _, char := range []byte(request.IdempotencyKey) {
		if char < '!' || char > '~' {
			return nil, ErrInvalidRequest
		}
	}

	var input map[string]json.Value
	if _, err := jcs.Transform(request.ExpandedInput); err != nil {
		return nil, ErrInvalidRequest
	}
	if err := json.Unmarshal(request.ExpandedInput, &input); err != nil || input == nil {
		return nil, ErrInvalidRequest
	}
	rawKey, ok := input["idempotencyKey"]
	if !ok {
		return nil, ErrInvalidRequest
	}
	var expandedKey string
	if json.Unmarshal(rawKey, &expandedKey) != nil || expandedKey != request.IdempotencyKey {
		return nil, ErrInvalidRequest
	}
	delete(input, "idempotencyKey")
	keylessInput, err := json.Marshal(input)
	if err != nil {
		return nil, ErrInvalidRequest
	}
	envelope, err := json.Marshal(struct {
		Tool          string     `json:"tool"`
		SchemaVersion string     `json:"schemaVersion"`
		Input         json.Value `json:"input"`
	}{request.Tool, request.SchemaVersion, keylessInput})
	if err != nil {
		return nil, ErrInvalidRequest
	}
	canonical, err := jcs.Transform(envelope)
	if err != nil {
		return nil, ErrInvalidRequest
	}

	audienceDigest := sha256.Sum256(append([]byte("audience\x00"), []byte(request.Authority.Audience)...))
	return &preparedRequest{
		request:        request,
		audienceDigest: hex.EncodeToString(audienceDigest[:]),
		keyDigest:      s.digest("key\x00", []byte(request.IdempotencyKey)),
		requestDigest:  s.digest("request\x00", canonical),
	}, nil
}

func (s *Service) digest(domain string, value []byte) string {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(domain))
	_, _ = mac.Write(value)
	return hex.EncodeToString(mac.Sum(nil))
}

type mutationError struct{ err error }

func (e *mutationError) Error() string { return "MCP Work mutation callback failed" }
func (e *mutationError) Unwrap() error { return e.err }

type receiptClaimError struct{ err error }

func (e *receiptClaimError) Error() string { return "MCP Work receipt claim failed" }
func (e *receiptClaimError) Unwrap() error { return e.err }

func (s *Service) executeOnce(ctx context.Context, prepared *preparedRequest, operation Operation, mutate Mutation) (*Result, bool, error) {
	var result *Result
	commitCandidate := false
	err := s.runTransaction(ctx, func(txCtx context.Context) error {
		result = nil
		commitCandidate = false
		existing, err := mcpwork_model.FindByKey(txCtx, prepared.request.Authority.PrincipalID, prepared.audienceDigest, prepared.keyDigest)
		if err != nil && !errors.Is(err, mcpwork_model.ErrReceiptNotExist) {
			return err
		}
		if existing != nil {
			result, err = resultFromStored(existing, prepared.requestDigest, true)
			return err
		}

		receipt := &mcpwork_model.Receipt{
			OperationUUID: operation.UUID,
			PrincipalID:   prepared.request.Authority.PrincipalID, AudienceDigest: prepared.audienceDigest,
			KeyDigest: prepared.keyDigest, RequestDigest: prepared.requestDigest,
			Tool: prepared.request.Tool, SchemaVersion: prepared.request.SchemaVersion,
			ApplicationID: prepared.request.Authority.OAuthApplicationID, GrantID: prepared.request.Authority.OAuthGrantID,
			CredentialID: prepared.request.Authority.CredentialJTI, Scope: prepared.request.Authority.Scope,
			ActorTrust: actorTrustUnverified, Origin: originMCP, Outcome: mcpwork_model.OutcomePending,
		}
		if _, err := db.GetEngine(txCtx).Insert(receipt); err != nil {
			return &receiptClaimError{err: err}
		}
		completion, err := mutate(txCtx, operation)
		if err != nil {
			return &mutationError{err: err}
		}
		if err := validateCompletion(completion); err != nil {
			return &mutationError{err: err}
		}
		if err := insertLinks(txCtx, receipt.ID, completion); err != nil {
			return err
		}
		committedAt := s.now().UTC()
		receipt.Outcome = completion.Outcome
		receipt.ProblemCode = completion.ProblemCode
		receipt.CommittedUnix = timeutil.TimeStamp(committedAt.Unix())
		updated, err := db.GetEngine(txCtx).ID(receipt.ID).
			Cols("outcome", "problem_code", "committed_unix").Update(receipt)
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrOutcomeUnknown
		}
		commitCandidate = true
		result = resultFromReceipt(receipt, false)
		return nil
	})
	if err != nil {
		return nil, commitCandidate, err
	}
	return result, commitCandidate, nil
}

func validateCompletion(completion Completion) error {
	switch completion.Outcome {
	case mcpwork_model.OutcomeApplied, mcpwork_model.OutcomeUnchanged:
		if completion.ProblemCode != "" {
			return ErrInvalidCompletion
		}
	case mcpwork_model.OutcomeRejected:
		if completion.ProblemCode == "" || len(completion.ProblemCode) > 64 {
			return ErrInvalidCompletion
		}
	default:
		return ErrInvalidCompletion
	}
	artifactOwners := make(map[string]struct{}, len(completion.Artifacts))
	for _, ref := range completion.Artifacts {
		if ref.RepositoryID <= 0 || ref.ArtifactID <= 0 || (ref.Kind != mcpwork_model.ArtifactKindProject && ref.Kind != mcpwork_model.ArtifactKindIssue) ||
			(ref.Kind == mcpwork_model.ArtifactKindIssue && ref.ArtifactNumber <= 0) ||
			(ref.LocalReference != "" && !localReferencePattern.MatchString(ref.LocalReference)) {
			return ErrInvalidCompletion
		}
		key := artifactKey(ref.RepositoryID, ref.Kind, ref.ArtifactID)
		if _, exists := artifactOwners[key]; exists {
			return ErrInvalidCompletion
		}
		artifactOwners[key] = struct{}{}
	}
	events := make(map[string]struct{}, len(completion.Events))
	for _, ref := range completion.Events {
		if ref.RepositoryID <= 0 || ref.EventID <= 0 || ref.Kind != mcpwork_model.EventKindIssueComment || ref.ArtifactKind != mcpwork_model.ArtifactKindIssue || ref.ArtifactID <= 0 {
			return ErrInvalidCompletion
		}
		if _, exists := artifactOwners[artifactKey(ref.RepositoryID, ref.ArtifactKind, ref.ArtifactID)]; !exists {
			return ErrInvalidCompletion
		}
		key := fmt.Sprintf("%d:%s:%d", ref.RepositoryID, ref.Kind, ref.EventID)
		if _, exists := events[key]; exists {
			return ErrInvalidCompletion
		}
		events[key] = struct{}{}
	}
	return nil
}

func insertLinks(ctx context.Context, receiptID int64, completion Completion) error {
	for _, ref := range completion.Artifacts {
		link := &mcpwork_model.ArtifactLink{
			ReceiptID: receiptID, RepositoryID: ref.RepositoryID, Kind: ref.Kind,
			ArtifactID: ref.ArtifactID, ArtifactNumber: ref.ArtifactNumber, LocalReference: ref.LocalReference,
		}
		if _, err := db.GetEngine(ctx).Insert(link); err != nil {
			return err
		}
	}
	for _, ref := range completion.Events {
		link := &mcpwork_model.EventLink{
			ReceiptID: receiptID, RepositoryID: ref.RepositoryID, Kind: ref.Kind,
			EventID: ref.EventID, ArtifactKind: ref.ArtifactKind, ArtifactID: ref.ArtifactID,
		}
		if _, err := db.GetEngine(ctx).Insert(link); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) recover(ctx context.Context, prepared *preparedRequest) (*Result, bool, error) {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recoveryTimeout)
	defer cancel()
	receipt, err := s.recoverReceipt(recoveryCtx, prepared.request.Authority.PrincipalID, prepared.audienceDigest, prepared.keyDigest)
	if errors.Is(err, mcpwork_model.ErrReceiptNotExist) {
		return nil, false, nil
	}
	if err != nil || receipt == nil {
		return nil, false, err
	}
	result, err := resultFromStored(receipt, prepared.requestDigest, true)
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}

func resultFromStored(receipt *mcpwork_model.Receipt, requestDigest string, replayed bool) (*Result, error) {
	if !hmac.Equal([]byte(receipt.RequestDigest), []byte(requestDigest)) {
		return nil, ErrIdempotencyConflict
	}
	if receipt.TombstonedUnix > 0 {
		return &Result{Replayed: replayed, Tombstoned: true}, ErrReceiptTombstoned
	}
	if receipt.Outcome == mcpwork_model.OutcomePending || receipt.CommittedUnix <= 0 {
		return nil, ErrOutcomeUnknown
	}
	return resultFromReceipt(receipt, replayed), nil
}

func resultFromReceipt(receipt *mcpwork_model.Receipt, replayed bool) *Result {
	return &Result{
		OperationUUID: receipt.OperationUUID, Outcome: receipt.Outcome, ProblemCode: receipt.ProblemCode,
		CommittedAt: receipt.CommittedUnix.AsTime().UTC(), Replayed: replayed,
	}
}

// ReferencePermission rechecks current native read permission for one stable
// artifact before provenance is exposed.
type ReferencePermission func(context.Context, ArtifactReference) (bool, error)

// Presentation contains only human-safe provenance fields and currently
// readable links. OAuth internals and receipt digests remain private.
type Presentation struct {
	Available     bool
	OperationUUID string
	PrincipalID   int64
	CommittedAt   time.Time
	Outcome       mcpwork_model.Outcome
	Origin        string
	ActorTrust    string
	Artifacts     []ArtifactReference
	Events        []EventReference
}

// Present returns provenance only after current permission is rechecked. Event
// links are retained only when their owning artifact is currently readable.
func (s *Service) Present(ctx context.Context, operationUUID string, permitted ReferencePermission) (*Presentation, error) {
	if permitted == nil || operationUUID == "" {
		return nil, ErrInvalidRequest
	}
	receipt, artifactLinks, eventLinks, err := mcpwork_model.GetReceiptByUUID(ctx, operationUUID)
	if err != nil {
		return nil, err
	}
	if receipt.TombstonedUnix > 0 {
		return nil, ErrReceiptTombstoned
	}
	visible := make([]ArtifactReference, 0, len(artifactLinks))
	visibleKeys := make(map[string]struct{}, len(artifactLinks))
	for _, link := range artifactLinks {
		ref := ArtifactReference{RepositoryID: link.RepositoryID, Kind: link.Kind, ArtifactID: link.ArtifactID, ArtifactNumber: link.ArtifactNumber, LocalReference: link.LocalReference}
		allowed, err := permitted(ctx, ref)
		if err != nil {
			return nil, err
		}
		if allowed {
			visible = append(visible, ref)
			visibleKeys[artifactKey(ref.RepositoryID, ref.Kind, ref.ArtifactID)] = struct{}{}
		}
	}
	if len(visible) == 0 {
		return &Presentation{
			OperationUUID: receipt.OperationUUID, CommittedAt: receipt.CommittedUnix.AsTime().UTC(),
			Outcome: receipt.Outcome,
		}, nil
	}
	visibleEvents := make([]EventReference, 0, len(eventLinks))
	for _, link := range eventLinks {
		if _, ok := visibleKeys[artifactKey(link.RepositoryID, link.ArtifactKind, link.ArtifactID)]; !ok {
			continue
		}
		visibleEvents = append(visibleEvents, EventReference{RepositoryID: link.RepositoryID, Kind: link.Kind, EventID: link.EventID, ArtifactKind: link.ArtifactKind, ArtifactID: link.ArtifactID})
	}
	return &Presentation{
		Available: true, OperationUUID: receipt.OperationUUID, PrincipalID: receipt.PrincipalID,
		CommittedAt: receipt.CommittedUnix.AsTime().UTC(), Outcome: receipt.Outcome,
		Origin: receipt.Origin, ActorTrust: receipt.ActorTrust, Artifacts: visible, Events: visibleEvents,
	}, nil
}

const maxProvenancePresentations = 100

// PresentArtifact is the Project/Issue view seam. It authorizes the native
// artifact before looking up any receipt locator.
func (s *Service) PresentArtifact(ctx context.Context, artifact ArtifactReference, permitted ReferencePermission) ([]*Presentation, error) {
	if permitted == nil || validateCompletion(Completion{Outcome: mcpwork_model.OutcomeUnchanged, Artifacts: []ArtifactReference{artifact}}) != nil {
		return nil, ErrInvalidRequest
	}
	allowed, err := permitted(ctx, artifact)
	if err != nil || !allowed {
		return nil, err
	}
	operationUUIDs, err := mcpwork_model.ListOperationUUIDsByArtifact(ctx, artifact.RepositoryID, artifact.Kind, artifact.ArtifactID, maxProvenancePresentations)
	if err != nil {
		return nil, err
	}
	return s.presentOperations(ctx, operationUUIDs, permitted)
}

// PresentEvent is the Issue timeline seam. It authorizes the owning Issue
// before looking up the native comment's provenance.
func (s *Service) PresentEvent(ctx context.Context, event EventReference, owner ArtifactReference, permitted ReferencePermission) ([]*Presentation, error) {
	if permitted == nil || event.RepositoryID != owner.RepositoryID || event.ArtifactKind != owner.Kind || event.ArtifactID != owner.ArtifactID ||
		validateCompletion(Completion{Outcome: mcpwork_model.OutcomeUnchanged, Artifacts: []ArtifactReference{owner}, Events: []EventReference{event}}) != nil {
		return nil, ErrInvalidRequest
	}
	allowed, err := permitted(ctx, owner)
	if err != nil || !allowed {
		return nil, err
	}
	operationUUIDs, err := mcpwork_model.ListOperationUUIDsByEvent(ctx, event.RepositoryID, event.Kind, event.EventID, maxProvenancePresentations)
	if err != nil {
		return nil, err
	}
	return s.presentOperations(ctx, operationUUIDs, permitted)
}

func (s *Service) presentOperations(ctx context.Context, operationUUIDs []string, permitted ReferencePermission) ([]*Presentation, error) {
	presentations := make([]*Presentation, 0, len(operationUUIDs))
	for _, operationUUID := range operationUUIDs {
		presentation, err := s.Present(ctx, operationUUID, permitted)
		if err != nil {
			return nil, err
		}
		if presentation.Available {
			presentations = append(presentations, presentation)
		}
	}
	return presentations, nil
}

func artifactKey(repositoryID int64, kind mcpwork_model.ArtifactKind, artifactID int64) string {
	return fmt.Sprintf("%d:%s:%d", repositoryID, kind, artifactID)
}

// RetentionApproval verifies inside the transaction that detailed provenance
// is eligible for minimization, normally because every affected artifact is
// gone under the native lifecycle authority.
type RetentionApproval func(context.Context, []ArtifactReference) (bool, error)

// Tombstone deletes stable links and clears receipt detail while permanently
// reserving the principal/audience/key and request digest tuple.
func (s *Service) Tombstone(ctx context.Context, operationUUID string, approve RetentionApproval) error {
	if operationUUID == "" || approve == nil {
		return ErrInvalidRequest
	}
	return db.WithWorkTx(ctx, func(txCtx context.Context) error {
		receipt, artifactLinks, _, err := mcpwork_model.GetReceiptByUUID(txCtx, operationUUID)
		if err != nil || receipt == nil {
			return err
		}
		if receipt.TombstonedUnix > 0 {
			return nil
		}
		artifacts := make([]ArtifactReference, 0, len(artifactLinks))
		for _, link := range artifactLinks {
			artifacts = append(artifacts, ArtifactReference{RepositoryID: link.RepositoryID, Kind: link.Kind, ArtifactID: link.ArtifactID, ArtifactNumber: link.ArtifactNumber, LocalReference: link.LocalReference})
		}
		allowed, err := approve(txCtx, artifacts)
		if err != nil {
			return err
		}
		if !allowed {
			return ErrReceiptRetentionRequired
		}
		if _, err := db.GetEngine(txCtx).Where("receipt_id = ?", receipt.ID).Delete(new(mcpwork_model.EventLink)); err != nil {
			return err
		}
		if _, err := db.GetEngine(txCtx).Where("receipt_id = ?", receipt.ID).Delete(new(mcpwork_model.ArtifactLink)); err != nil {
			return err
		}
		tombstonedAt := timeutil.TimeStamp(s.now().UTC().Unix())
		cleared := &mcpwork_model.Receipt{
			OperationUUID: "", Tool: "", SchemaVersion: "", ApplicationID: 0, GrantID: 0,
			CredentialID: "", Scope: "", ActorTrust: "", Origin: "", Outcome: "", ProblemCode: "",
			CreatedUnix: 0, CommittedUnix: 0, TombstonedUnix: tombstonedAt,
		}
		updated, err := db.GetEngine(txCtx).ID(receipt.ID).Cols(
			"operation_uuid", "tool", "schema_version", "application_id", "grant_id", "credential_id", "scope",
			"actor_trust", "origin", "outcome", "problem_code", "created_unix", "committed_unix", "tombstoned_unix",
		).Update(cleared)
		if err != nil {
			return err
		}
		if updated != 1 {
			return ErrOutcomeUnknown
		}
		return nil
	})
}
