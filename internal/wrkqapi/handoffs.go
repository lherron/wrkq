package wrkqapi

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/store"
)

// ─── Handoff DTO ─────────────────────────────────────────────────────────────

// WrkqHandoff is the stable handoff resource DTO. Its field ORDER + json tags
// reproduce the legacy `handoffJSON` (internal/cli/handoff_create.go) EXACTLY so
// the mirror can render byte-identical output: snake_case keys, legacy struct
// order, pointer/omitempty parity. This is the WrkqHandoff fingerprint pinned by
// TestHandoffDTOFingerprint — any drift is a PROTOCOL CONTRACT change.
type WrkqHandoff struct {
	UUID                       string     `json:"uuid"`
	ID                         string     `json:"id"`
	ScopeRef                   string     `json:"scope_ref"`
	ScopeKind                  string     `json:"scope_kind"`
	AgentID                    string     `json:"agent_id"`
	ProjectID                  string     `json:"project_id"`
	AgentActorUUID             *string    `json:"agent_actor_uuid"`
	AgentPrincipalRef          *string    `json:"agent_principal_ref,omitempty"`
	ProjectContainerUUID       *string    `json:"project_container_uuid"`
	CreatedByAgentID           string     `json:"created_by_agent_id"`
	CreatedByActorUUID         *string    `json:"created_by_actor_uuid"`
	CreatedByPrincipalRef      string     `json:"created_by_principal_ref,omitempty"`
	Title                      string     `json:"title"`
	Body                       string     `json:"body"`
	Status                     string     `json:"status"`
	IdempotencyKey             *string    `json:"idempotency_key"`
	AcknowledgedAt             *time.Time `json:"acknowledged_at"`
	AcknowledgedByAgentID      *string    `json:"acknowledged_by_agent_id"`
	AcknowledgedByActorUUID    *string    `json:"acknowledged_by_actor_uuid"`
	AcknowledgedByPrincipalRef *string    `json:"acknowledged_by_principal_ref,omitempty"`
	AcknowledgementNote        *string    `json:"acknowledgement_note"`
	Meta                       *string    `json:"meta"`
	ETag                       int64      `json:"etag"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

// WrkqHandoffCreateResult is the wrkq.handoff.create envelope: the created (or
// idempotently replayed) handoff + the replay flag.
type WrkqHandoffCreateResult struct {
	Handoff          WrkqHandoff `json:"handoff"`
	IdempotentReplay bool        `json:"idempotentReplay"`
}

// WrkqHandoffListResult is the wrkq.handoff.listView envelope. nextCursor is the
// opaque pagination token (empty when the page is the last).
type WrkqHandoffListResult struct {
	Items      []WrkqHandoff `json:"items"`
	NextCursor string        `json:"nextCursor,omitempty"`
}

// ─── Params ──────────────────────────────────────────────────────────────────

// HandoffCreateParams carries the CALLER-resolved effective scope/actor for a
// handoff create. Scope is caller-owned but NOT project-root: the mirror resolves
// --scope / agent-runtime env via scope.Resolve and enforces self-scope BEFORE
// submitting. The SERVER receives EXPLICIT effective fields and MUST NOT read
// ASP_SCOPE_REF / ASP_HANDLE / ASP_AGENT_ID / ASP_PROJECT.
type HandoffCreateParams struct {
	ScopeRef       string  `json:"scopeRef"`
	AgentID        string  `json:"agentId"`
	ProjectID      string  `json:"projectId"`
	Title          string  `json:"title"`
	Body           string  `json:"body"`
	Meta           *string `json:"meta,omitempty"`
	IdempotencyKey *string `json:"idempotencyKey,omitempty"`
	// ActorAgentID/PrincipalRef are the caller-resolved create attribution. When
	// empty they default to the scope agent (legacy: created_by == scope agent).
	ActorAgentID string `json:"actorAgentId,omitempty"`
	PrincipalRef string `json:"principalRef,omitempty"`
	DryRun       bool   `json:"dryRun,omitempty"`
}

// HandoffGetParams selects a single handoff by friendly ID or UUID.
type HandoffGetParams struct {
	Handoff string `json:"handoff"`
}

// HandoffListViewParams is the caller-scoped handoff list request. scopeRef is
// the CALLER-resolved canonical project scope; the server never derives it from
// env. status is pending|acknowledged|all (default pending).
type HandoffListViewParams struct {
	ScopeRef string `json:"scopeRef"`
	Status   string `json:"status,omitempty"`
	Limit    int    `json:"limit,omitempty"`
	Cursor   string `json:"cursor,omitempty"`
}

// HandoffAcknowledgeParams carries the caller-resolved acting identity for an
// acknowledge. The server owns the etag CAS + the handoff.acknowledged event.
type HandoffAcknowledgeParams struct {
	Handoff      string  `json:"handoff"`
	Note         *string `json:"note,omitempty"`
	ActorAgentID string  `json:"actorAgentId"`
	PrincipalRef string  `json:"principalRef,omitempty"`
	ScopeRef     string  `json:"scopeRef,omitempty"`
	DryRun       bool    `json:"dryRun,omitempty"`
	IfMatch      int64   `json:"ifMatch,omitempty"`
}

// ─── Producer methods ────────────────────────────────────────────────────────

// HandoffCreate writes a pending handoff (handoff.created) or returns an
// idempotent replay. The caller supplies the effective project scope + actor; the
// server resolves the actor/container row UUIDs by slug and persists. dryRun
// projects the prospective handoff without writing.
func (a *API) HandoffCreate(ctx context.Context, p HandoffCreateParams) (*WrkqHandoffCreateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	resolved, validationErr := validateHandoffCreate(p)
	if validationErr != nil {
		return nil, validationErr
	}

	agentActorUUID, projectContainerUUID := a.lookupHandoffScopeRows(ctx, resolved.AgentID, resolved.ProjectID)
	agentPrincipalRef := "agent:" + resolved.AgentID
	createdByAgentID := strings.TrimSpace(p.ActorAgentID)
	if createdByAgentID == "" {
		createdByAgentID = resolved.AgentID
	}
	createdByPrincipalRef := strings.TrimSpace(p.PrincipalRef)
	if createdByPrincipalRef == "" {
		createdByPrincipalRef = "agent:" + createdByAgentID
	}
	createdByActorUUID := agentActorUUID
	if createdByAgentID != resolved.AgentID {
		createdByActorUUID = a.lookupActorUUID(ctx, createdByAgentID)
	}

	args := store.CreateHandoffArgs{
		ScopeRef:              resolved.CanonicalRef,
		ScopeKind:             string(scope.KindProject),
		AgentID:               resolved.AgentID,
		ProjectID:             resolved.ProjectID,
		CreatedByAgentID:      createdByAgentID,
		Title:                 p.Title,
		Body:                  p.Body,
		IdempotencyKey:        p.IdempotencyKey,
		Meta:                  p.Meta,
		AgentActorUUID:        agentActorUUID,
		AgentPrincipalRef:     &agentPrincipalRef,
		ProjectContainerUUID:  projectContainerUUID,
		CreatedByActorUUID:    createdByActorUUID,
		CreatedByPrincipalRef: createdByPrincipalRef,
	}

	if p.DryRun {
		return &WrkqHandoffCreateResult{Handoff: toWrkqHandoff(a.projectedHandoff(ctx, args))}, nil
	}

	tx, err := a.db.Begin()
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	result, err := store.CreateHandoff(ctx, tx, args)
	if err != nil {
		var mismatch *store.HandoffIdempotencyPayloadMismatchError
		if errors.As(err, &mismatch) {
			return nil, NewConflictError(err.Error(), map[string]any{
				"idempotencyKey": mismatch.IdempotencyKey,
				"existingId":     mismatch.ExistingID,
				"existingUuid":   mismatch.ExistingUUID,
				"scopeRef":       mismatch.ScopeRef,
			})
		}
		return nil, mapHandoffStoreError(err, "")
	}
	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}

	return &WrkqHandoffCreateResult{
		Handoff:          toWrkqHandoff(result.Handoff),
		IdempotentReplay: result.IdempotentReplay,
	}, nil
}

// HandoffGet returns a single handoff by friendly ID or UUID. A missing handoff
// is WRKQ_NOT_FOUND with the legacy "handoff not found: <ref>" message.
func (a *API) HandoffGet(ctx context.Context, p HandoffGetParams) (*WrkqHandoff, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(p.Handoff)
	if ref == "" {
		return nil, NewValidationError("handoff id or uuid is required", map[string]any{"field": "handoff"})
	}
	handoff, err := store.GetHandoff(ctx, a.db, ref)
	if err != nil {
		return nil, mapHandoffStoreError(err, ref)
	}
	out := toWrkqHandoff(handoff)
	return &out, nil
}

// HandoffListView returns the caller-scoped handoff page. scopeRef is the
// caller-resolved canonical project scope; the server never reads env.
func (a *API) HandoffListView(ctx context.Context, p HandoffListViewParams) (*WrkqHandoffListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.ScopeRef) == "" {
		return nil, NewValidationError("scopeRef is required", map[string]any{"field": "scopeRef"})
	}
	handoffs, nextCursor, err := store.ListHandoffs(ctx, a.db, store.ListHandoffsOpts{
		ScopeRef: p.ScopeRef,
		Status:   p.Status,
		Limit:    p.Limit,
		Cursor:   strings.TrimSpace(p.Cursor),
	})
	if err != nil {
		return nil, mapHandoffStoreError(err, "")
	}
	items := make([]WrkqHandoff, 0, len(handoffs))
	for _, h := range handoffs {
		items = append(items, toWrkqHandoff(h))
	}
	return &WrkqHandoffListResult{Items: items, NextCursor: nextCursor}, nil
}

// HandoffAcknowledge transitions a pending handoff to acknowledged
// (handoff.acknowledged). The server owns the etag CAS (domain.ETagMismatchError
// → WRKQ_CONFLICT) and the "already acknowledged" mapping. dryRun returns the
// projected post-state without writing.
func (a *API) HandoffAcknowledge(ctx context.Context, p HandoffAcknowledgeParams) (*WrkqHandoff, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ref := strings.TrimSpace(p.Handoff)
	if ref == "" {
		return nil, NewValidationError("handoff id or uuid is required", map[string]any{"field": "handoff"})
	}
	if strings.TrimSpace(p.ActorAgentID) == "" {
		return nil, NewValidationError("actorAgentId is required", map[string]any{"field": "actorAgentId"})
	}
	if p.Note != nil && strings.TrimSpace(*p.Note) == "" {
		return nil, NewValidationError("acknowledgement note cannot be empty", map[string]any{"field": "note"})
	}
	if p.IfMatch < 0 {
		return nil, NewValidationError("ifMatch must be a non-negative etag value", map[string]any{"field": "ifMatch"})
	}

	principalRef := strings.TrimSpace(p.PrincipalRef)
	if principalRef == "" {
		principalRef = "agent:" + strings.TrimSpace(p.ActorAgentID)
	}
	actorUUID := a.lookupActorUUID(ctx, p.ActorAgentID)

	handoff, err := store.AcknowledgeHandoff(ctx, a.db, ref, store.AcknowledgeHandoffArgs{
		Note:         p.Note,
		ActorAgentID: strings.TrimSpace(p.ActorAgentID),
		ActorUUID:    actorUUID,
		PrincipalRef: principalRef,
		ScopeRef:     strings.TrimSpace(p.ScopeRef),
		DryRun:       p.DryRun,
		IfMatch:      p.IfMatch,
	})
	if err != nil {
		return nil, mapHandoffStoreError(err, ref)
	}
	out := toWrkqHandoff(handoff)
	return &out, nil
}

// ─── helpers ─────────────────────────────────────────────────────────────────

// validateHandoffCreate validates the caller-supplied effective scope/title/body
// and returns the resolved scope identity. It mirrors the legacy create gate but
// works from the EXPLICIT params (the server never re-resolves env).
func validateHandoffCreate(p HandoffCreateParams) (scope.ResolvedScope, error) {
	title := strings.TrimSpace(p.Title)
	if title == "" {
		return scope.ResolvedScope{}, NewValidationError("title is required", map[string]any{"field": "title"})
	}
	if strings.TrimSpace(p.Body) == "" {
		return scope.ResolvedScope{}, NewValidationError("body cannot be empty", map[string]any{"field": "body"})
	}
	agentID := strings.TrimSpace(p.AgentID)
	projectID := strings.TrimSpace(p.ProjectID)
	if agentID == "" || projectID == "" {
		return scope.ResolvedScope{}, NewValidationError(
			"handoff create requires an agent/project scope (agentId + projectId)",
			map[string]any{"field": "scope"})
	}
	canonical := strings.TrimSpace(p.ScopeRef)
	if canonical == "" {
		canonical = "agent:" + agentID + ":project:" + projectID
	}
	return scope.ResolvedScope{
		AgentID:      agentID,
		ProjectID:    projectID,
		CanonicalRef: canonical,
	}, nil
}

// lookupHandoffScopeRows resolves the agent actor UUID + project container UUID
// by slug (best-effort; nil when not found), mirroring legacy
// lookupHandoffScopeRows.
func (a *API) lookupHandoffScopeRows(ctx context.Context, agentID, projectID string) (*string, *string) {
	actorUUID := a.lookupActorUUID(ctx, agentID)
	var containerUUID *string
	if projectID != "" {
		var u string
		if err := a.db.QueryRowContext(ctx,
			`SELECT uuid FROM containers WHERE slug = ? AND parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root') LIMIT 1`,
			projectID).Scan(&u); err == nil {
			containerUUID = &u
		}
	}
	return actorUUID, containerUUID
}

// lookupActorUUID resolves an actor UUID by slug (nil when not found).
func (a *API) lookupActorUUID(ctx context.Context, agentID string) *string {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return nil
	}
	var u string
	if err := a.db.QueryRowContext(ctx, "SELECT uuid FROM actors WHERE slug = ? LIMIT 1", agentID).Scan(&u); err == nil {
		return &u
	}
	return nil
}

// projectedHandoff builds the dry-run handoff projection without writing,
// mirroring legacy projectedHandoff (next-id + etag 1 + pending).
func (a *API) projectedHandoff(ctx context.Context, args store.CreateHandoffArgs) store.Handoff {
	return store.ProjectHandoff(ctx, a.db, args)
}

// toWrkqHandoff maps a store.Handoff to the WrkqHandoff DTO (legacy field order).
func toWrkqHandoff(h store.Handoff) WrkqHandoff {
	return WrkqHandoff{
		UUID:                       h.UUID,
		ID:                         h.ID,
		ScopeRef:                   h.ScopeRef,
		ScopeKind:                  h.ScopeKind,
		AgentID:                    h.AgentID,
		ProjectID:                  h.ProjectID,
		AgentActorUUID:             h.AgentActorUUID,
		AgentPrincipalRef:          h.AgentPrincipalRef,
		ProjectContainerUUID:       h.ProjectContainerUUID,
		CreatedByAgentID:           h.CreatedByAgentID,
		CreatedByActorUUID:         h.CreatedByActorUUID,
		CreatedByPrincipalRef:      h.CreatedByPrincipalRef,
		Title:                      h.Title,
		Body:                       h.Body,
		Status:                     h.Status,
		IdempotencyKey:             h.IdempotencyKey,
		AcknowledgedAt:             h.AcknowledgedAt,
		AcknowledgedByAgentID:      h.AcknowledgedByAgentID,
		AcknowledgedByActorUUID:    h.AcknowledgedByActorUUID,
		AcknowledgedByPrincipalRef: h.AcknowledgedByPrincipalRef,
		AcknowledgementNote:        h.AcknowledgementNote,
		Meta:                       h.Meta,
		ETag:                       h.ETag,
		CreatedAt:                  h.CreatedAt,
		UpdatedAt:                  h.UpdatedAt,
	}
}

// (helpers continue below)

// mapHandoffStoreError maps store/domain errors to the typed WRKQ_* errors. The
// not-found message keeps the legacy "handoff not found: <ref>" prefix so the
// mirror can re-derive the legacy CLI wording.
func mapHandoffStoreError(err error, ref string) error {
	if err == nil {
		return nil
	}
	var etagErr *domain.ETagMismatchError
	if errors.As(err, &etagErr) {
		return NewConflictError(err.Error(), map[string]any{"currentEtag": etagErr.Actual})
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	switch {
	case strings.HasPrefix(msg, "handoff not found:"):
		// Preserve the legacy prefix verbatim so the mirror reproduces the CLI wording.
		return NewNotFoundError(ref, "handoff")
	case strings.Contains(lower, "already acknowledged"):
		return NewConflictError(msg, map[string]any{"reason": "already_acknowledged"})
	case strings.Contains(lower, "invalid") || strings.Contains(lower, "required") ||
		strings.Contains(lower, "must ") || strings.Contains(lower, "cannot ") ||
		strings.Contains(lower, "unsupported status"):
		return NewValidationError(msg, nil)
	default:
		return NewInternalError(err)
	}
}
