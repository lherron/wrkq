//go:build wrkq_local

package wrkqapi

import (
	"context"
	"errors"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/store"
)

// HandoffCreate writes a pending handoff (handoff.created) or returns an
// idempotent replay. The caller supplies the effective project scope + actor; the
// server resolves the project container row UUID by slug and persists. dryRun
// projects the prospective handoff without writing.
func (a *API) HandoffCreate(ctx context.Context, p HandoffCreateParams) (*WrkqHandoffCreateResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	resolved, validationErr := validateHandoffCreate(p)
	if validationErr != nil {
		return nil, validationErr
	}

	projectContainerUUID := a.lookupProjectContainerUUID(ctx, resolved.ProjectID)
	agentPrincipalRef := "agent:" + resolved.AgentID
	createdByAgentID := strings.TrimSpace(p.ActorAgentID)
	if createdByAgentID == "" {
		createdByAgentID = resolved.AgentID
	}
	createdByPrincipalRef := strings.TrimSpace(p.PrincipalRef)
	if createdByPrincipalRef == "" {
		createdByPrincipalRef = "agent:" + createdByAgentID
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
		AgentPrincipalRef:     &agentPrincipalRef,
		ProjectContainerUUID:  projectContainerUUID,
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

	handoff, err := store.AcknowledgeHandoff(ctx, a.db, ref, store.AcknowledgeHandoffArgs{
		Note:         p.Note,
		ActorAgentID: strings.TrimSpace(p.ActorAgentID),
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
	expected := "agent:" + agentID + ":project:" + projectID
	canonical := strings.TrimSpace(p.ScopeRef)
	if canonical == "" {

		canonical = expected
	} else if canonical != expected {

		return scope.ResolvedScope{}, NewValidationError(
			"scopeRef must be exactly the canonical project scope for agentId/projectId ("+expected+")",
			map[string]any{"field": "scopeRef", "scopeRef": canonical, "expected": expected})
	}

	parsed, perr := scope.ParseScopeRef(canonical)
	if perr != nil || parsed.AgentID != agentID || parsed.ProjectID != projectID || parsed.TaskID != "" || parsed.RoleName != "" {
		return scope.ResolvedScope{}, NewValidationError(
			"handoff create requires a valid canonical project scope (agent:<agentId>:project:<projectId>)",
			map[string]any{"field": "scope", "scopeRef": canonical})
	}
	return scope.ResolvedScope{
		AgentID:      agentID,
		ProjectID:    projectID,
		CanonicalRef: canonical,
	}, nil
}

// lookupProjectContainerUUID resolves the project container UUID by slug
// (best-effort; nil when not found), mirroring legacy lookupHandoffScopeRows'
// container resolution.
func (a *API) lookupProjectContainerUUID(ctx context.Context, projectID string) *string {
	if strings.TrimSpace(projectID) == "" {
		return nil
	}
	var u string
	if err := a.db.QueryRowContext(ctx,
		`SELECT uuid FROM containers WHERE slug = ? AND parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root') LIMIT 1`,
		projectID).Scan(&u); err == nil {
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
		AgentPrincipalRef:          h.AgentPrincipalRef,
		ProjectContainerUUID:       h.ProjectContainerUUID,
		CreatedByAgentID:           h.CreatedByAgentID,
		CreatedByPrincipalRef:      h.CreatedByPrincipalRef,
		Title:                      h.Title,
		Body:                       h.Body,
		Status:                     h.Status,
		IdempotencyKey:             h.IdempotencyKey,
		AcknowledgedAt:             h.AcknowledgedAt,
		AcknowledgedByAgentID:      h.AcknowledgedByAgentID,
		AcknowledgedByPrincipalRef: h.AcknowledgedByPrincipalRef,
		AcknowledgementNote:        h.AcknowledgementNote,
		Meta:                       h.Meta,
		ETag:                       h.ETag,
		CreatedAt:                  h.CreatedAt,
		UpdatedAt:                  h.UpdatedAt,
	}
}

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
