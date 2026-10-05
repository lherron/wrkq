//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/store"
)

// RoomSay routes one say per T-07612 §4, fans it out to one envelope per
// addressee, discharges the sender's own standing obligations from the same
// counterparty (reply is the ack), and returns the receipt.
func (a *API) RoomSay(ctx context.Context, p RoomSayParams) (*WrkqRoomSayResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	body := strings.TrimSpace(p.Body)
	if body == "" {
		return nil, NewValidationError("say body is required", map[string]any{"field": "body"})
	}
	senderScope, err := normalizeRoomScopeRef(p.ScopeRef)
	if err != nil {
		return nil, err
	}
	if p.FYI && len(p.To) == 0 {
		return nil, NewValidationError("--fyi requires --to; a say without an addressee is a log entry", map[string]any{"field": "to"})
	}
	if strings.TrimSpace(p.TTL) != "" && len(p.To) == 0 {
		return nil, NewValidationError("--ttl requires --to", map[string]any{"field": "to"})
	}
	if p.Hold && len(p.To) == 0 {
		return nil, NewValidationError("--preempt requires --to", map[string]any{"field": "to"})
	}
	if p.DischargeEnvelopeIDs != nil && len(p.To) == 0 {
		return nil, NewValidationError("dischargeEnvelopeIds requires --to", map[string]any{"field": "to"})
	}
	expiresAt, _, err := normalizePromiseReviewTime("", p.TTL, false)
	if err != nil {
		return nil, NewValidationError(strings.Replace(err.Error(), "reviewIn", "ttl", 1), map[string]any{"field": "ttl"})
	}

	routed, err := a.routeSay(ctx, attr, senderScope, p)
	if err != nil {
		return nil, err
	}

	room, err := a.loadRoomState(ctx, routed.room.UUID)
	if err != nil {
		return nil, err
	}
	// §5: a say into a STALE room writes and carries an advisory notice. There is
	// no room state that can refuse it — agent-to-agent traffic is never blocked
	// — so the notice is computed here, BEFORE the write makes the room active
	// again, and is never an error and never overridable.
	notices := []string{}
	if notice := staleRoomNotice(room); notice != nil {
		notices = append(notices, *notice)
	}
	if routed.notice != "" {
		notices = append(notices, routed.notice)
	}

	addressees, err := a.resolveAddressees(ctx, room, p.To, routed.impliedTo, senderScope, attr.PrincipalRef)
	if err != nil {
		return nil, err
	}
	pairNotices := a.pairRoomSayNotices(room, body, addressees, senderScope, attr.PrincipalRef, p.FYI)
	notices = append(notices, pairNotices...)
	obligation := domain.EnvelopeObligationNone
	switch {
	case len(addressees) > 0 && p.FYI:
		obligation = domain.EnvelopeObligationFYI
	case len(addressees) > 0:
		obligation = domain.EnvelopeObligationReplyRequired
	}

	var respondTo *string
	if strings.TrimSpace(p.RespondTo) != "" {
		normalized, nerr := attribution.NormalizeCompat(p.RespondTo)
		if nerr != nil {
			return nil, NewValidationError("invalid respondTo: "+nerr.Error(), map[string]any{"field": "respondTo"})
		}
		respondTo = &normalized
	}

	// Speaking is membership: the sender is in the room from the first say. It
	// is written inside the envelope transaction so explicit scoped discharge
	// is all-or-nothing with both reply and membership.
	senderRef := attr.PrincipalRef
	senderScoped := false
	if senderScope != "" {
		senderRef = senderScope
		senderScoped = true
	}
	var senderScopePtr *string
	if senderScope != "" {
		senderScopePtr = &senderScope
	}
	// Endpoint affiliation belongs to unowned ad-hoc rooms and project rooms.
	// Resolve the project token at write time and persist its UUID; later reads
	// never map historical scope text through mutable slugs or room membership.
	var senderProjectUUID *string
	if room.row.Kind == domain.RoomKindAdhoc || room.row.Kind == domain.RoomKindProject {
		senderProjectUUID, err = a.scopeProjectUUID(ctx, senderScope)
		if err != nil {
			return nil, err
		}
		for index := range addressees {
			addressees[index].ProjectUUID, err = a.scopeProjectUUID(ctx, addressees[index].ScopeRef)
			if err != nil {
				return nil, err
			}
		}
	}
	createParams := store.EnvelopeCreateParams{
		RoomUUID:              room.row.UUID,
		FromPrincipalRef:      attr.PrincipalRef,
		FromScopeRef:          senderScopePtr,
		FromProjectUUID:       senderProjectUUID,
		SenderMemberRef:       senderRef,
		SenderScoped:          senderScoped,
		Addressees:            addressees,
		Obligation:            obligation,
		Body:                  body,
		TaskUUID:              routed.taskTagUUID,
		RespondToPrincipalRef: respondTo,
		IdempotencyKey:        optionalString(p.IdempotencyKey),
		Meta:                  metaString(p.Meta),
		ExpiresAt:             optionalString(expiresAt),
		Delivery:              domain.EnvelopeDeliveryQueue,
		DischargeEnvelopeIDs:  p.DischargeEnvelopeIDs,
	}
	if p.Hold {
		createParams.Delivery = domain.EnvelopeDeliveryHold
	}
	var created []domain.Envelope
	acked := []string{}
	if p.DischargeEnvelopeIDs != nil {
		var scopedAcked []domain.Envelope
		created, scopedAcked, err = a.store.Rooms.CreateEnvelopesAndDischargeWithAttribution(attr, createParams)
		for i := range scopedAcked {
			acked = append(acked, scopedAcked[i].ID)
		}
	} else {
		created, err = a.store.Rooms.CreateEnvelopesWithAttribution(attr, createParams)
	}
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}

	// Reply is the ack: this say discharges the sender's own standing
	// obligations in this room from each counterparty SEAT it addressed. The
	// match is scope-to-scope; the principal a say was attributed to never
	// enters it. Sibling envelopes of a fan-out addressed to other scopes are
	// untouched, and a deferred envelope is excluded — defer first to hold one
	// back.
	seenCounterparty := map[string]bool{}
	for _, addressee := range addressees {
		if p.DischargeEnvelopeIDs != nil {
			break
		}
		// A counterparty is an ADDRESS: its scope, or its principal only when it
		// has no scope. Two seats of the same agent are two counterparties.
		counterparty := addressee.ScopeRef
		if counterparty == "" {
			counterparty = addressee.PrincipalRef
		}
		if seenCounterparty[counterparty] {
			continue
		}
		seenCounterparty[counterparty] = true
		rows, aerr := a.store.Rooms.AckSenderObligationsWithAttribution(
			attr, room.row.UUID, senderScope, attr.PrincipalRef,
			addressee.ScopeRef, addressee.PrincipalRef)
		if aerr != nil {
			return nil, mapRoomStoreError(aerr, "")
		}
		for index := range rows {
			acked = append(acked, rows[index].ID)
		}
	}

	result := &WrkqRoomSayResult{Acked: acked, Notices: notices}
	if len(notices) > 0 {
		result.Notice = &result.Notices[0]
	}
	refreshed, err := a.loadRoomState(ctx, room.row.UUID)
	if err != nil {
		return nil, err
	}
	result.Room = roomDTO(refreshed)
	for index := range created {
		envelope, eerr := a.envelopeDTO(ctx, &created[index], refreshed)
		if eerr != nil {
			return nil, eerr
		}
		if result.GroupID == "" && envelope.GroupID != nil {
			result.GroupID = *envelope.GroupID
		}
		result.Envelopes = append(result.Envelopes, *envelope)
	}

	// Rooms are talk; comments are record. --record is the ONLY bridge between
	// them, and it is explicit.
	if p.Record {
		taskUUID := routed.taskTagUUID
		if taskUUID == nil {
			taskUUID = refreshed.row.TaskUUID
		}
		if taskUUID == nil {
			return nil, NewValidationError("--record requires a room with a task", map[string]any{
				"room": refreshed.key, "kind": string(refreshed.row.Kind),
			})
		}
		comment, cerr := a.CommentAdd(ctx, CommentAddParams{Task: *taskUUID, Body: body, Actor: p.PrincipalRef})
		if cerr != nil {
			return nil, cerr
		}
		result.RecordedCommentID = &comment.ID
	}
	return result, nil
}

// scopeProjectUUID resolves a project-bearing stored scope handle to the
// current top-level project only while creating an envelope. Empty, malformed,
// unresolved, and non-top-level scope tokens intentionally receive no stamp.
func (a *API) scopeProjectUUID(ctx context.Context, scopeHandle string) (*string, error) {
	if strings.TrimSpace(scopeHandle) == "" {
		return nil, nil
	}
	parsed, err := scope.ParseScopeHandle(scopeHandle)
	if err != nil || parsed.ProjectID == "" {
		return nil, nil
	}
	slug, err := paths.NormalizeSlug(parsed.ProjectID)
	if err != nil {
		return nil, nil
	}
	var uuid string
	err = a.db.QueryRowContext(ctx, `SELECT uuid FROM containers
		WHERE kind = 'project' AND parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root') AND slug = ?`, slug).Scan(&uuid)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, NewInternalError(err)
	}
	return &uuid, nil
}

// pairRoomSayNotices delivers doctrine at the point where a pair room can
// silently cross-discharge two topics. The result is advice only: these reads
// do not introduce a content validation path, an override, or persisted state.
func (a *API) pairRoomSayNotices(room *roomState, body string, addressees []store.EnvelopeAddressee, senderScope, senderPrincipal string, fyi bool) []string {
	if room.row.Kind != domain.RoomKindAdhoc || len(addressees) == 0 {
		return nil
	}

	notices := []string{}
	mentioned := make([]string, 0)
	seen := map[string]bool{}
	for _, taskID := range id.FindTaskIDs(body) {
		if seen[taskID] {
			continue
		}
		seen[taskID] = true
		var live int
		if err := a.db.QueryRow(`SELECT 1 FROM tasks
			WHERE id = ? AND state != 'deleted' AND deleted_at IS NULL`, taskID).Scan(&live); err == nil {
			mentioned = append(mentioned, taskID)
		}
	}
	if len(mentioned) == 1 {
		taskID := mentioned[0]
		notices = append(notices, "this looks like "+taskID+" work — say into "+taskID+" so a reply here does not cross-discharge it")
	} else if len(mentioned) > 1 {
		tasks := strings.Join(mentioned, ", ")
		notices = append(notices, "this looks like "+tasks+" work — say into those task rooms so a reply here does not cross-discharge them")
	}

	if fyi {
		return notices
	}
	for _, addressee := range addressees {
		standing, err := a.store.Rooms.StandingObligationFromSender(
			room.row.UUID, senderScope, senderPrincipal, addressee.ScopeRef, addressee.PrincipalRef)
		if err != nil {
			// Advice must never add a refusal path. If this best-effort read
			// fails, RoomSay still writes and returns successfully.
			continue
		}
		if standing == nil {
			continue
		}
		seat := addressee.ScopeRef
		if seat == "" {
			seat = addressee.PrincipalRef
		}
		notices = append(notices, seat+" still owes you a reply here ("+standing.ID+"); one reply acks both — put a second topic on its task")
	}
	return notices
}
