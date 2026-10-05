//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/store"
)

// Wire mapping for rooms: domain rows to DTOs, and store errors to domain
// errors.

// roomDTO is the wire shape of one room's read-time state.
func roomDTO(state *roomState) WrkqRoom {
	room := state.row
	labels := room.Labels
	if labels == nil {
		labels = []string{}
	}
	dto := WrkqRoom{
		UUID: room.UUID, ID: room.ID, Key: state.key, Kind: string(room.Kind),
		Work: string(state.work), Activity: string(state.activity),
		Labels: labels, WorkRef: state.workRef, Links: state.links,
		OpenedByPrincipalRef: room.OpenedByPrincipalRef, OpenedAt: toRFC3339(room.OpenedAt),
		LastActivityAt:   state.lastActivity,
		OpenSubtaskCount: state.openSubtaskCount,
		MemberCount:      state.memberCount, MessageCount: state.messageCount,
		ETag: room.ETag, CreatedAt: toRFC3339(room.CreatedAt), UpdatedAt: toRFC3339(room.UpdatedAt),
	}
	if dto.Links == nil {
		dto.Links = []WrkqRoomLink{}
	}
	return dto
}

// envelopeListDTO lists envelopes and renders each one a keep filter (nil keeps
// all) admits against its memoized room state.
func (a *API) envelopeListDTO(ctx context.Context, rooms roomStates, params store.EnvelopeListParams,
	keep func(*domain.Envelope, *roomState) bool) ([]WrkqEnvelope, error) {
	rows, err := a.store.Rooms.ListEnvelopes(ctx, params)
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}
	result := make([]WrkqEnvelope, 0, len(rows))
	for index := range rows {
		state, serr := a.roomStateFor(ctx, rooms, rows[index].RoomUUID)
		if serr != nil {
			return nil, serr
		}
		if keep != nil && !keep(&rows[index], state) {
			continue
		}
		dto, eerr := a.envelopeDTO(ctx, &rows[index], state)
		if eerr != nil {
			return nil, eerr
		}
		result = append(result, *dto)
	}
	return result, nil
}

// envelopeView renders one envelope against its room's current state.
func (a *API) envelopeView(ctx context.Context, envelope *domain.Envelope) (*WrkqEnvelope, error) {
	state, err := a.loadRoomState(ctx, envelope.RoomUUID)
	if err != nil {
		return nil, err
	}
	return a.envelopeDTO(ctx, envelope, state)
}

func (a *API) envelopeDTO(ctx context.Context, envelope *domain.Envelope, room *roomState) (*WrkqEnvelope, error) {
	_, sequence, err := id.Parse(envelope.ID)
	if err != nil {
		return nil, NewInternalError(fmt.Errorf("invalid envelope ledger id %q: %w", envelope.ID, err))
	}
	dto := &WrkqEnvelope{
		UUID: envelope.UUID, ID: envelope.ID, RoomUUID: envelope.RoomUUID,
		MessageSeq: int64(sequence),
		RoomKey:    room.key, RoomKind: string(room.row.Kind), GroupID: envelope.GroupID,
		From:       WrkqEnvelopeParty{PrincipalRef: envelope.FromPrincipalRef, ScopeRef: envelope.FromScopeRef},
		ReplyTo:    envelopeReplyTo(envelope),
		Obligation: string(envelope.Obligation), Body: envelope.Body,
		State: string(envelope.State), Terminal: domain.IsEnvelopeTerminal(envelope.State), ExpiresAt: envelope.ExpiresAt, Delivery: string(envelope.Delivery),
		RetryAt:     envelope.RetryAt,
		DeferReason: envelope.DeferReason, TerminalActor: envelope.TerminalActor,
		MaterializationIntent: envelope.MaterializationIntent,
		RespondToPrincipalRef: envelope.RespondToPrincipalRef,
		IdempotencyKey:        envelope.IdempotencyKey,
		Meta:                  map[string]any{},
		PresentedTo:           []WrkqEnvelopePresentation{},
		ETag:                  envelope.ETag, CreatedAt: toRFC3339(envelope.CreatedAt),
		UpdatedAt: toRFC3339(envelope.UpdatedAt),
	}
	if envelope.FailureReason != nil {
		reason := string(*envelope.FailureReason)
		dto.FailureReason = &reason
	}
	if envelope.State == domain.EnvelopeStateAcked {
		var payload string
		err := a.db.QueryRowContext(ctx, `SELECT payload FROM event_log
			WHERE resource_type = 'envelope' AND resource_uuid = ? AND event_type = 'envelope.acked'
			ORDER BY id DESC LIMIT 1`, envelope.UUID).Scan(&payload)
		if err != nil && err != sql.ErrNoRows {
			return nil, NewInternalError(err)
		}
		if err == nil {
			var event struct {
				Reason string `json:"reason"`
			}
			if jerr := json.Unmarshal([]byte(payload), &event); jerr != nil {
				return nil, NewInternalError(fmt.Errorf("decode envelope.acked payload: %w", jerr))
			}
			if strings.TrimSpace(event.Reason) != "" {
				dto.Reason = &event.Reason
			}
		}
	}
	if envelope.Meta != nil {
		dto.Meta = parseMeta(*envelope.Meta)
	}
	if envelope.ToPrincipalRef != nil {
		dto.To = &WrkqEnvelopeParty{PrincipalRef: *envelope.ToPrincipalRef, ScopeRef: envelope.ToScopeRef}
	}
	if envelope.TaskUUID != nil {
		var taskID string
		if err := a.db.QueryRowContext(ctx, "SELECT id FROM tasks WHERE uuid = ?", *envelope.TaskUUID).Scan(&taskID); err == nil {
			dto.TaskID = &taskID
		}
	}
	if envelope.RetryPromiseUUID != nil {
		var promiseID string
		if err := a.db.QueryRowContext(ctx, "SELECT id FROM promises WHERE uuid = ?", *envelope.RetryPromiseUUID).Scan(&promiseID); err == nil {
			dto.RetryPromiseID = &promiseID
		}
	}

	rows, err := a.db.QueryContext(ctx, `SELECT member_ref, node, runtime_id, host_session_id,
		 generation, run_id, drive_attempt_id, input_id, delivery_outcome, presented_at
		 FROM envelope_presentations WHERE envelope_uuid = ? ORDER BY presented_at, uuid`, envelope.UUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var item WrkqEnvelopePresentation
		if err := rows.Scan(&item.MemberRef, &item.Node, &item.RuntimeID, &item.HostSessionID,
			&item.Generation, &item.RunID, &item.DriveAttemptID, &item.InputID, &item.DeliveryOutcome,
			&item.PresentedAt); err != nil {
			return nil, NewInternalError(err)
		}
		item.PresentedAt = toRFC3339(item.PresentedAt)
		dto.PresentedTo = append(dto.PresentedTo, item)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}
	return dto, nil
}

// envelopeReplyTo is the addressee token that answers one envelope: its sender's
// SEAT, or the sender's principal when it has no seat. Consumers print it
// verbatim rather than shortening it to a bare name (T-07638).
func envelopeReplyTo(envelope *domain.Envelope) string {
	if envelope.FromScopeRef != nil && strings.TrimSpace(*envelope.FromScopeRef) != "" {
		return *envelope.FromScopeRef
	}
	return envelope.FromPrincipalRef
}

func presentationDTO(presentation *domain.EnvelopePresentation) *WrkqEnvelopePresentation {
	return &WrkqEnvelopePresentation{
		MemberRef: presentation.MemberRef, Node: presentation.Node,
		RuntimeID: presentation.RuntimeID, HostSessionID: presentation.HostSessionID,
		Generation: presentation.Generation, RunID: presentation.RunID,
		DriveAttemptID: presentation.DriveAttemptID, InputID: presentation.InputID,
		DeliveryOutcome: presentation.DeliveryOutcome,
		PresentedAt:     toRFC3339(presentation.PresentedAt),
	}
}

func optionalString(value string) *string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func mapRoomStoreError(err error, selector string) error {
	var roomMissing *store.RoomNotFoundError
	if errors.As(err, &roomMissing) {
		if selector == "" {
			selector = roomMissing.Selector
		}
		return NewNotFoundError(selector, "room")
	}
	var envelopeMissing *store.EnvelopeNotFoundError
	if errors.As(err, &envelopeMissing) {
		if selector == "" {
			selector = envelopeMissing.Selector
		}
		return NewNotFoundError(selector, "envelope")
	}
	var wrongState *store.EnvelopeWrongStateError
	if errors.As(err, &wrongState) {
		return NewWrongStateError(map[string]any{
			"envelope": wrongState.Envelope, "state": string(wrongState.State), "verb": wrongState.Verb,
		})
	}
	var alreadyPresented *store.EnvelopeAlreadyPresentedError
	if errors.As(err, &alreadyPresented) {
		return newError(CodeAlreadyPresented, "already_presented", false, map[string]any{
			"envelope": alreadyPresented.Envelope, "presentation": presentationDTO(&alreadyPresented.Presentation),
		}, err)
	}
	var dischargeInvalid *store.EnvelopeDischargeInvalidError
	if errors.As(err, &dischargeInvalid) {
		return NewValidationError("invalid discharge envelope", map[string]any{"envelope": dischargeInvalid.Envelope, "reason": dischargeInvalid.Reason})
	}
	var runtimeMismatch *store.EnvelopeRuntimeMismatchError
	if errors.As(err, &runtimeMismatch) {
		return NewConflictError("envelope runtime does not own the newest presentation", map[string]any{
			"envelope": runtimeMismatch.Envelope, "runtime": runtimeMismatch.Runtime,
		})
	}
	var cursorInvalid *store.CollaborationCursorInvalidError
	if errors.As(err, &cursorInvalid) {
		return NewCursorInvalidError(cursorInvalid.Expected, cursorInvalid.Current)
	}
	var mismatch *domain.ETagMismatchError
	if errors.As(err, &mismatch) {
		return NewConflictError("etag precondition failed", map[string]any{
			"expectedEtag": mismatch.Expected, "currentEtag": mismatch.Actual,
		})
	}
	var invalid *domain.InvalidValueError
	if errors.As(err, &invalid) {
		return NewValidationError(invalid.Error(), map[string]any{
			"field": invalid.Field, "value": invalid.Value, "allowed": invalid.Allowed,
		})
	}
	return NewInternalError(err)
}
