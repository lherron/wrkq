//go:build wrkq_local

package wrkqapi

import (
	"context"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/store"
)

// Envelope dispositions: every state transition after a say — presentation,
// defer, ack, fail, withdraw — and the addressee guards they share.

// EnvelopePresent is the HRC-facing presentation projection and receipt write.
// Preview returns the same projection without mutating the ledger; commit
// records presented_to and emits envelope.presented. The §7 `history:` cue is
// keyed to the RUNTIME so a post-/quit runtime sharing its generation is cold.
func (a *API) EnvelopePresent(ctx context.Context, p EnvelopePresentParams) (*WrkqEnvelopePresentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(ctx, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.Rooms.ExpireDueEnvelopes(attr); err != nil {
		return nil, NewInternalError(err)
	}
	envelope, err := a.store.Rooms.GetEnvelope(strings.TrimSpace(p.Envelope))
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	memberRef := strings.TrimSpace(p.MemberRef)
	if memberRef == "" {
		if envelope.ToScopeRef != nil {
			memberRef = *envelope.ToScopeRef
		} else if envelope.ToPrincipalRef != nil {
			memberRef = *envelope.ToPrincipalRef
		}
	}
	if memberRef == "" {
		return nil, NewValidationError("present requires a memberRef for an envelope with no addressee",
			map[string]any{"field": "memberRef", "envelope": envelope.ID})
	}

	// The cue is decided BEFORE the receipt is written: after it, this runtime
	// has by definition seen the room.
	historyHint := false
	if runtimeID := strings.TrimSpace(p.RuntimeID); runtimeID != "" {
		seen, herr := a.store.Rooms.HasRuntimeSeenRoom(envelope.RoomUUID, runtimeID)
		if herr != nil {
			return nil, NewInternalError(herr)
		}
		historyHint = !seen
	}

	updated := envelope
	recorded := false
	if !p.Preview {
		updated, recorded, err = a.store.Rooms.RecordPresentationWithAttribution(attr, envelope.UUID, store.PresentationRecord{
			MemberRef: memberRef,
			Node:      optionalString(p.Node), RuntimeID: optionalString(p.RuntimeID),
			HostSessionID: optionalString(p.HostSessionID), Generation: optionalString(p.Generation),
			RunID: optionalString(p.RunID), DriveAttemptID: optionalString(p.DriveAttemptID),
			InputID: optionalString(p.InputID), DeliveryOutcome: optionalString(p.DeliveryOutcome),
		})
		if err != nil {
			return nil, mapRoomStoreError(err, p.Envelope)
		}
	}

	state, err := a.loadRoomState(ctx, updated.RoomUUID)
	if err != nil {
		return nil, err
	}
	dto, err := a.envelopeDTO(ctx, updated, state)
	if err != nil {
		return nil, err
	}
	result := &WrkqEnvelopePresentResult{
		Envelope: *dto, Recorded: recorded, MessageCount: state.messageCount,
	}
	// A brand-new room has no prior messages, so there is nothing to cue.
	if state.messageCount <= 1 {
		result.HistoryHint = false
	} else {
		result.HistoryHint = historyHint
	}
	if state.lastMessageAt != "" {
		last := toRFC3339(state.lastMessageAt)
		result.LastMessage = &last
	}
	return result, nil
}

// EnvelopeDefer pauses one obligation. Deferred is paused, NEVER terminal: a
// later reply still acks it. A retry time is backed by a wrkq promise owned by
// the deferring principal, and the deferral re-pends when that time arrives.
func (a *API) EnvelopeDefer(ctx context.Context, p EnvelopeDeferParams) (*WrkqEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(ctx, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	reason := strings.TrimSpace(p.Reason)
	if reason == "" {
		return nil, NewValidationError("defer requires a reason", map[string]any{"field": "reason"})
	}
	envelope, err := a.store.Rooms.GetEnvelope(strings.TrimSpace(p.Envelope))
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	if err := a.requireEnvelopeAddressee(envelope, attr, p.ScopeRef, "defer"); err != nil {
		return nil, err
	}

	disposition := store.EnvelopeDisposition{State: domain.EnvelopeStateDeferred, DeferReason: &reason}
	retryAt, supplied, err := normalizePromiseReviewTime(p.RetryAt, p.RetryAfter, false)
	if err != nil {
		return nil, err
	}
	if supplied {
		disposition.RetryAt = &retryAt
		promise, perr := a.store.Promises.CreateWithAttribution(attr, store.PromiseCreateParams{
			OwnerPrincipalRef: envelopePrincipal(envelope, attr),
			Subject:           "Deferred " + envelope.ID + ": " + reason,
			ReviewAt:          retryAt,
			SubjectTaskUUID:   envelope.TaskUUID,
		})
		if perr != nil {
			return nil, mapPromiseStoreError(perr, "")
		}
		disposition.RetryPromiseUUID = &promise.UUID
	}

	updated, err := a.store.Rooms.DisposeEnvelopeWithAttribution(attr, envelope.UUID, disposition, p.IfMatch)
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	return a.envelopeView(ctx, updated)
}

const envelopeAckReasonConsumedByWait = "consumed_by_wait"

// EnvelopeAck defaults to the OPERATOR-only ack intended for humans such as
// agent:lance clearing mail. consumed_by_wait is the sole caller-scoped path:
// it is accepted only for the exact addressee's pending/presented reply.
func (a *API) EnvelopeAck(ctx context.Context, p EnvelopeAckParams) (*WrkqRoomLogView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(ctx, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if len(p.Envelopes) == 0 {
		return nil, NewValidationError("ack requires at least one envelope", map[string]any{"field": "envelopes"})
	}
	reason := strings.TrimSpace(p.Reason)
	if reason != "" && reason != envelopeAckReasonConsumedByWait {
		return nil, NewValidationError("unsupported caller-scoped ack reason", map[string]any{
			"field": "reason", "reason": p.Reason,
		})
	}
	view := &WrkqRoomLogView{Items: []WrkqEnvelope{}}
	for _, selector := range p.Envelopes {
		envelope, gerr := a.store.Rooms.GetEnvelope(strings.TrimSpace(selector))
		if gerr != nil {
			return nil, mapRoomStoreError(gerr, selector)
		}
		disposition := store.EnvelopeDisposition{State: domain.EnvelopeStateAcked, Reason: "operator"}
		if reason == envelopeAckReasonConsumedByWait {
			claimedScope, aerr := a.requireExactEnvelopeAddressee(envelope, attr, p.ScopeRef, "ack as consumed_by_wait")
			if aerr != nil {
				return nil, aerr
			}
			if envelope.Obligation != domain.EnvelopeObligationReplyRequired ||
				(envelope.State != domain.EnvelopeStatePending && envelope.State != domain.EnvelopeStatePresented) {
				return nil, NewWrongStateError(map[string]any{
					"envelope": envelope.ID, "state": string(envelope.State), "verb": envelopeAckReasonConsumedByWait,
				})
			}
			attr.ScopeRef = claimedScope
			disposition.Reason = envelopeAckReasonConsumedByWait
		} else if strings.TrimSpace(p.Note) != "" {
			disposition.Reason = "operator: " + strings.TrimSpace(p.Note)
		}
		updated, derr := a.store.Rooms.DisposeEnvelopeWithAttribution(attr, envelope.UUID, disposition, 0)
		if derr != nil {
			return nil, mapRoomStoreError(derr, selector)
		}
		state, serr := a.loadRoomState(ctx, updated.RoomUUID)
		if serr != nil {
			return nil, serr
		}
		if view.Room.UUID == "" {
			view.Room = roomDTO(state)
		}
		dto, eerr := a.envelopeDTO(ctx, updated, state)
		if eerr != nil {
			return nil, eerr
		}
		view.Items = append(view.Items, *dto)
	}
	return view, nil
}

// EnvelopeFail is the HRC-facing unsuccessful terminal transition. legacy is
// migration-only; live failures must name a current operational reason.
func (a *API) EnvelopeFail(ctx context.Context, p EnvelopeFailParams) (*WrkqEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(ctx, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	envelope, err := a.store.Rooms.GetEnvelope(strings.TrimSpace(p.Envelope))
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	reason := domain.EnvelopeFailureReason(strings.TrimSpace(p.Reason))
	if err := domain.ValidateEnvelopeFailureReason(reason); err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "reason", "reason": p.Reason})
	}
	if reason == domain.EnvelopeFailureLegacy {
		return nil, NewValidationError("legacy is migration-only and cannot be written by envelope.fail", map[string]any{"field": "reason", "reason": p.Reason})
	}
	// `undeliverable` means the body never reached a reader. That is true of a
	// pending envelope nobody could be born for (rev 5.1 D7) AND of a presented
	// one whose queued copy the harness broker expired before injection (hcs
	// T-07891 amendment 6, 2026-09-02): the receipt says queued, not read.
	if reason == domain.EnvelopeFailureUndeliverable && envelope.State != domain.EnvelopeStatePending && envelope.State != domain.EnvelopeStatePresented && envelope.State != domain.EnvelopeStateFailed {
		return nil, NewWrongStateError(map[string]any{"envelope": envelope.ID, "state": string(envelope.State), "verb": "fail"})
	}
	if reason != domain.EnvelopeFailureUndeliverable && envelope.State != domain.EnvelopeStatePresented && envelope.State != domain.EnvelopeStateFailed {
		return nil, NewWrongStateError(map[string]any{"envelope": envelope.ID, "state": string(envelope.State), "verb": "fail"})
	}
	updated, err := a.store.Rooms.FailEnvelopeWithAttribution(attr, envelope.UUID, reason, p.Runtime, p.Detail)
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	return a.envelopeView(ctx, updated)
}

// EnvelopeWithdraw is the sender cancellation surface. A scope-less operator
// already known to the ledger may override sender ownership, matching wrkc's
// existing operator model without adding a second principal registry.
func (a *API) EnvelopeWithdraw(ctx context.Context, p EnvelopeWithdrawParams) (*WrkqEnvelopeWithdrawResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(ctx, p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	envelope, err := a.store.Rooms.GetEnvelope(strings.TrimSpace(p.Envelope))
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	if envelope.FromPrincipalRef != attr.PrincipalRef {
		scopeRef, serr := normalizeRoomScopeRef(p.ScopeRef)
		if serr != nil {
			return nil, serr
		}
		if scopeRef != "" {
			return nil, NewForbiddenError("only the sender or a scope-less operator may withdraw this envelope", map[string]any{"envelope": envelope.ID})
		}
	}
	withdrawn, err := a.store.Rooms.WithdrawEnvelopesWithAttribution(attr, envelope.UUID, p.Group, strings.TrimSpace(p.Reason))
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	result := &WrkqEnvelopeWithdrawResult{Withdrawn: []WrkqEnvelope{}, Refused: []WrkqEnvelopeWithdrawRefusal{}}
	for i := range withdrawn.Withdrawn {
		dto, derr := a.envelopeView(ctx, &withdrawn.Withdrawn[i])
		if derr != nil {
			return nil, derr
		}
		result.Withdrawn = append(result.Withdrawn, *dto)
	}
	for i := range withdrawn.Refused {
		refusal := WrkqEnvelopeWithdrawRefusal{EnvelopeID: withdrawn.Refused[i].Envelope.ID, Reason: withdrawn.Refused[i].Reason, State: string(withdrawn.Refused[i].Envelope.State)}
		if withdrawn.Refused[i].Presentation != nil {
			refusal.Presentation = presentationDTO(withdrawn.Refused[i].Presentation)
		}
		result.Refused = append(result.Refused, refusal)
	}
	return result, nil
}

// requireEnvelopeAddressee enforces the T-06810 hygiene rule carried by §6: the
// envelope's target must equal the claimed scope. It is not new credential
// machinery — same-UID confusion stays an accepted residual — but a scope may
// not dispose another scope's obligation by typo.
func (a *API) requireEnvelopeAddressee(envelope *domain.Envelope, attr attribution.Attribution, scopeRef, verb string) error {
	claimed, err := normalizeRoomScopeRef(scopeRef)
	if err != nil {
		return err
	}
	if envelope.ToScopeRef != nil {
		if claimed == *envelope.ToScopeRef {
			return nil
		}
		if claimed == "" && envelope.ToPrincipalRef != nil && *envelope.ToPrincipalRef == attr.PrincipalRef {
			return nil
		}
		return NewForbiddenError("only the addressee may "+verb+" this envelope", map[string]any{
			"envelope": envelope.ID, "addressee": *envelope.ToScopeRef, "claimed": claimed,
		})
	}
	if envelope.ToPrincipalRef != nil && *envelope.ToPrincipalRef == attr.PrincipalRef {
		return nil
	}
	return NewForbiddenError("only the addressee may "+verb+" this envelope", map[string]any{
		"envelope": envelope.ID, "claimed": attr.PrincipalRef,
	})
}

// requireExactEnvelopeAddressee is deliberately stricter than the legacy
// defer hygiene check: consumed_by_wait is a caller authority, so BOTH the
// durable principal and durable scope must equal the caller. A scoped envelope
// cannot fall back to same-principal authority when scopeRef is missing.
func (a *API) requireExactEnvelopeAddressee(envelope *domain.Envelope, attr attribution.Attribution, scopeRef, verb string) (string, error) {
	claimedScope, err := normalizeRoomScopeRef(scopeRef)
	if err != nil {
		return "", err
	}
	targetScope := ""
	if envelope.ToScopeRef != nil {
		targetScope = *envelope.ToScopeRef
	}
	if envelope.ToPrincipalRef != nil && *envelope.ToPrincipalRef == attr.PrincipalRef && targetScope == claimedScope {
		return claimedScope, nil
	}
	return "", NewForbiddenError("only the exact addressee may "+verb+" this envelope", map[string]any{
		"envelope": envelope.ID,
		"addresseePrincipalRef": func() string {
			if envelope.ToPrincipalRef == nil {
				return ""
			}
			return *envelope.ToPrincipalRef
		}(),
		"addresseeScopeRef":   targetScope,
		"claimedPrincipalRef": attr.PrincipalRef,
		"claimedScopeRef":     claimedScope,
	})
}

func envelopePrincipal(envelope *domain.Envelope, attr attribution.Attribution) string {
	if envelope.ToPrincipalRef != nil {
		return *envelope.ToPrincipalRef
	}
	return attr.PrincipalRef
}
