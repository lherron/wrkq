//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// envelopeMaxRoundsEnv is the operational bound carried from T-06810's wave-2
// freeze ruling (default 3 since the 2026-08-28 erratum, env-overridable for burn-in tuning). It moves to
// wrkq with the ledger it bounds; it is not a feature flag and gates no code
// path, only how many kicker-driven turns an undisposed obligation survives.
const envelopeMaxRoundsEnv = "WRKQ_ENVELOPE_MAX_ROUNDS"

// ─── say ──────────────────────────────────────────────────────────────────────

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

	routed, err := a.routeSay(ctx, attr, senderScope, p, body)
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
	notice := staleRoomNotice(room)

	addressees, err := a.resolveAddressees(ctx, room, p.To, routed.impliedTo, senderScope, attr.PrincipalRef)
	if err != nil {
		return nil, err
	}
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

	// Speaking is membership: the sender is in the room from the first say.
	senderRef := attr.PrincipalRef
	senderScoped := false
	if senderScope != "" {
		senderRef = senderScope
		senderScoped = true
	}
	if _, err := a.store.Rooms.AddMemberWithAttribution(attr, room.row.UUID, store.RoomMemberSeed{
		MemberRef: senderRef, MemberPrincipalRef: attr.PrincipalRef,
		Scoped: senderScoped, Source: domain.RoomMemberSourceSpoke,
	}); err != nil {
		return nil, NewInternalError(err)
	}

	var senderScopePtr *string
	if senderScope != "" {
		senderScopePtr = &senderScope
	}
	created, err := a.store.Rooms.CreateEnvelopesWithAttribution(attr, store.EnvelopeCreateParams{
		RoomUUID:              room.row.UUID,
		FromPrincipalRef:      attr.PrincipalRef,
		FromScopeRef:          senderScopePtr,
		Addressees:            addressees,
		Obligation:            obligation,
		Body:                  body,
		TaskUUID:              routed.taskTagUUID,
		RespondToPrincipalRef: respondTo,
		IdempotencyKey:        optionalString(p.IdempotencyKey),
		Meta:                  metaString(p.Meta),
	})
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}

	// Reply is the ack: this say discharges the sender's own standing
	// obligations in this room from each counterparty SEAT it addressed. The
	// match is scope-to-scope; the principal a say was attributed to never
	// enters it. Sibling envelopes of a fan-out addressed to other scopes are
	// untouched, and a deferred envelope is excluded — defer first to hold one
	// back.
	acked := []string{}
	seenCounterparty := map[string]bool{}
	for _, addressee := range addressees {
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

	result := &WrkqRoomSayResult{Acked: acked, Notice: notice}
	refreshed, err := a.loadRoomState(ctx, room.row.UUID)
	if err != nil {
		return nil, err
	}
	dto, err := a.roomDTO(ctx, refreshed)
	if err != nil {
		return nil, err
	}
	result.Room = *dto
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

// routedSay is the outcome of the §4 routing table for one say.
type routedSay struct {
	room *domain.Room
	// taskTagUUID tags the envelope with the task it was routed via, even when
	// strict campaign coalesce landed the envelope in the campaign room.
	taskTagUUID *string
	// impliedTo is the addressee a target-handle ref implies when the caller
	// named no --to.
	impliedTo string
}

// routeSay implements T-07612 §4 exactly, first match wins.
func (a *API) routeSay(ctx context.Context, attr attribution.Attribution, senderScope string, p RoomSayParams, body string) (*routedSay, error) {
	ref := strings.TrimSpace(p.Ref)

	// 4. An agent handle with no ref at all is not a thing: without a ref there
	// is nothing to route on.
	if ref == "" {
		return nil, NewValidationError("say requires a room, task, container, or agent handle", map[string]any{"field": "ref"})
	}

	if kind, _, err := id.Parse(ref); err == nil {
		switch kind {
		// 1. R-xxxxx / EN-xxxxx → that room (an envelope resolves to its room).
		case id.TypeRoom:
			room, gerr := a.store.Rooms.Get(ref)
			if gerr != nil {
				return nil, mapRoomStoreError(gerr, ref)
			}
			return &routedSay{room: room}, nil
		case id.TypeEnvelope:
			envelope, gerr := a.store.Rooms.GetEnvelope(ref)
			if gerr != nil {
				return nil, mapRoomStoreError(gerr, ref)
			}
			room, gerr := a.store.Rooms.Get(envelope.RoomUUID)
			if gerr != nil {
				return nil, mapRoomStoreError(gerr, ref)
			}
			return &routedSay{room: room, taskTagUUID: envelope.TaskUUID}, nil
		// 2. T-xxxxx → the campaign room if the task is in a campaign, else the
		// task room. Strict coalesce; there is no override.
		case id.TypeTask:
			return a.routeToTask(ctx, attr, ref)
		// 3. P-xxxxx → resolve the container's kind.
		case id.TypeContainer:
			return a.routeToContainer(ctx, attr, ref)
		}
	}

	// 4. An agent handle. A bare token with no "@" is a path, not a handle:
	// every §4 rule-4 example carries the project segment.
	if strings.Contains(ref, "@") {
		return a.routeToHandle(ctx, attr, senderScope, ref, p, body)
	}

	// 3. A container path (projects are containers in wrkq, so this single rule
	// is the only container branch).
	if containerUUID, _, err := selectors.ResolveContainer(a.db, ref); err == nil {
		return a.routeToContainerUUID(ctx, attr, ref, containerUUID)
	}
	// 2. A task path.
	if taskUUID, _, err := selectors.ResolveTask(a.db, ref); err == nil {
		return a.routeToTaskUUID(ctx, attr, taskUUID)
	}
	return nil, NewNotFoundError(ref, "room target")
}

func (a *API) routeToTask(ctx context.Context, attr attribution.Attribution, selector string) (*routedSay, error) {
	taskUUID, _, err := selectors.ResolveTask(a.db, selector)
	if err != nil {
		return nil, NewNotFoundError(selector, "task")
	}
	return a.routeToTaskUUID(ctx, attr, taskUUID)
}

// routeToTaskUUID applies strict campaign coalesce: a task inside a campaign
// talks in the campaign's room, tagged with the task it came through.
func (a *API) routeToTaskUUID(ctx context.Context, attr attribution.Attribution, taskUUID string) (*routedSay, error) {
	campaignUUID, err := a.effectiveCampaignForTask(ctx, taskUUID)
	if err != nil {
		return nil, err
	}
	if campaignUUID != "" {
		room, cerr := a.ensureContainerRoom(attr, campaignUUID, domain.RoomKindCampaign)
		if cerr != nil {
			return nil, cerr
		}
		return &routedSay{room: room, taskTagUUID: &taskUUID}, nil
	}
	room, err := a.ensureTaskRoom(attr, taskUUID)
	if err != nil {
		return nil, err
	}
	return &routedSay{room: room, taskTagUUID: &taskUUID}, nil
}

// effectiveCampaignForTask answers "which campaign does this task belong to",
// and it is the ONLY thing rule 2's strict coalesce may consult.
//
// Campaign membership in wrkq has two forms and RESIDENCY is the common one: a
// task whose project_uuid IS the campaign container is a member without any
// campaign_uuid ever being set. Enrolment (campaign_uuid) is the cross-project
// form. Reading only campaign_uuid — as this did first — silently gave every
// resident task its own room and split the campaign's conversation in two.
// Resident wins over enrolled, matching store.campaignUUIDForTaskTx.
//
// Unlike that function this does NOT gate on campaign_state = active. A campaign
// container routes to its campaign room under §4 rule 3 whatever its state, so
// gating here would make `say T-xxxxx` and `say <campaign-path>` disagree about
// the same room for the same campaign — and a completed campaign would start
// minting fresh task rooms for work whose conversation already lives in the
// campaign room. A closed campaign's room reads `work: terminal`, which is
// information on the receipt and never a refusal.
func (a *API) effectiveCampaignForTask(ctx context.Context, taskUUID string) (string, error) {
	var residentUUID string
	var enrolledUUID, residentState, enrolledState sql.NullString
	err := a.db.QueryRowContext(ctx, `
		SELECT t.project_uuid, t.campaign_uuid, resident.campaign_state, enrolled.campaign_state
		  FROM tasks t
		  LEFT JOIN containers resident ON resident.uuid = t.project_uuid
		  LEFT JOIN containers enrolled ON enrolled.uuid = t.campaign_uuid
		 WHERE t.uuid = ?`, taskUUID).
		Scan(&residentUUID, &enrolledUUID, &residentState, &enrolledState)
	if err == sql.ErrNoRows {
		return "", NewNotFoundError(taskUUID, "task")
	}
	if err != nil {
		return "", NewInternalError(err)
	}
	if residentState.Valid && residentState.String != "" {
		return residentUUID, nil
	}
	if enrolledUUID.Valid && enrolledUUID.String != "" && enrolledState.Valid && enrolledState.String != "" {
		return enrolledUUID.String, nil
	}
	return "", nil
}

func (a *API) routeToContainer(ctx context.Context, attr attribution.Attribution, selector string) (*routedSay, error) {
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return nil, NewNotFoundError(selector, "container")
	}
	return a.routeToContainerUUID(ctx, attr, selector, containerUUID)
}

// routeToContainerUUID is §4 rule 3: campaign-adorned → campaign room;
// project-kind → project room; any other container is a typed refusal.
func (a *API) routeToContainerUUID(ctx context.Context, attr attribution.Attribution, selector, containerUUID string) (*routedSay, error) {
	var kind string
	var campaignState sql.NullString
	if err := a.db.QueryRowContext(ctx,
		"SELECT kind, campaign_state FROM containers WHERE uuid = ?", containerUUID).Scan(&kind, &campaignState); err != nil {
		if err == sql.ErrNoRows {
			return nil, NewNotFoundError(selector, "container")
		}
		return nil, NewInternalError(err)
	}
	var roomKind domain.RoomKind
	switch {
	case campaignState.Valid && campaignState.String != "":
		roomKind = domain.RoomKindCampaign
	case kind == string(domain.ContainerKindProject):
		roomKind = domain.RoomKindProject
	default:
		return nil, NewValidationError(
			"room_kind_unsupported: only campaign-adorned and project containers have rooms",
			map[string]any{
				"reason": "room_kind_unsupported", "container": selector,
				"kind": kind, "expected": "campaign-adorned container or project",
			})
	}
	room, err := a.ensureContainerRoom(attr, containerUUID, roomKind)
	if err != nil {
		return nil, err
	}
	return &routedSay{room: room}, nil
}

// routeToHandle is §4 rule 4: the room is derived from the work context of the
// two parties, and the TARGET wins.
func (a *API) routeToHandle(ctx context.Context, attr attribution.Attribution, senderScope, ref string, p RoomSayParams, body string) (*routedSay, error) {
	target, err := scope.ParseScopeHandle(ref)
	if err != nil {
		return nil, NewValidationError("invalid agent handle: "+err.Error(), map[string]any{"field": "ref", "ref": ref})
	}
	targetHandle := scope.FormatScopeHandle(target)

	// Target task-scoped → the target's task room (→ campaign per rule 2), and
	// --to is implied to be the target.
	if taskSelector := taskScopedID(target.TaskID); taskSelector != "" {
		routed, rerr := a.routeToTask(ctx, attr, taskSelector)
		if rerr != nil {
			return nil, rerr
		}
		routed.impliedTo = targetHandle
		return routed, nil
	}

	// Sender task-scoped, target not: a worker escalating to its supervisor
	// lands on the WORK, not in a side channel.
	if senderScope != "" {
		sender, serr := scope.ParseScopeHandle(senderScope)
		if serr == nil {
			if taskSelector := taskScopedID(sender.TaskID); taskSelector != "" {
				routed, rerr := a.routeToTask(ctx, attr, taskSelector)
				if rerr != nil {
					return nil, rerr
				}
				routed.impliedTo = targetHandle
				return routed, nil
			}
		}
	}

	// Neither task-scoped → an ad-hoc pair room.
	if senderScope == "" {
		return nil, NewValidationError(
			"an ad-hoc pair room needs the caller's own scope; set HRC_SESSION_REF or address a task, campaign, or project",
			map[string]any{"field": "scopeRef", "target": targetHandle})
	}
	if !p.New {
		// §4: reuse the exact-pair room whose activity reads `active`, else open a
		// new one. Reuse keys on the SAME projection `wrkc show` prints; there is
		// no lifecycle left to key on.
		existing, ferr := a.store.Rooms.FindAdhocPairRoom(
			senderScope, targetHandle, roomActiveSince(time.Now().UTC()))
		if ferr != nil {
			return nil, NewInternalError(ferr)
		}
		if existing != nil {
			return &routedSay{room: existing, impliedTo: targetHandle}, nil
		}
	}
	targetPrincipal, perr := attribution.NormalizeCompat(target.AgentID)
	if perr != nil {
		return nil, NewValidationError("invalid agent handle: "+perr.Error(), map[string]any{"field": "ref"})
	}
	subject := domain.NormalizeRoomSubject(p.Subject, body)
	room, cerr := a.store.Rooms.CreateWithAttribution(attr, store.RoomCreateParams{
		Kind:    domain.RoomKindAdhoc,
		Subject: optionalString(subject),
		Members: []store.RoomMemberSeed{
			{MemberRef: senderScope, MemberPrincipalRef: attr.PrincipalRef, Scoped: true, Source: domain.RoomMemberSourceSpoke},
			{MemberRef: targetHandle, MemberPrincipalRef: targetPrincipal, Scoped: true, Source: domain.RoomMemberSourceAddressed},
		},
	})
	if cerr != nil {
		return nil, mapRoomStoreError(cerr, ref)
	}
	return &routedSay{room: room, impliedTo: targetHandle}, nil
}

// taskScopedID returns the task selector when a scope handle's task segment is
// a real task id. ":primary", ":minisvc", ":hrcdev" are lanes, not work.
func taskScopedID(taskID string) string {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ""
	}
	if kind, _, err := id.Parse(taskID); err == nil && kind == id.TypeTask {
		return taskID
	}
	return ""
}

func (a *API) ensureTaskRoom(attr attribution.Attribution, taskUUID string) (*domain.Room, error) {
	existing, err := a.store.Rooms.GetByTask(taskUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	if existing != nil {
		return existing, nil
	}
	room, err := a.store.Rooms.CreateWithAttribution(attr, store.RoomCreateParams{
		Kind: domain.RoomKindTask, TaskUUID: &taskUUID,
	})
	if err != nil {
		// A concurrent first say wins the unique index; adopt its room.
		if adopted, gerr := a.store.Rooms.GetByTask(taskUUID); gerr == nil && adopted != nil {
			return adopted, nil
		}
		return nil, mapRoomStoreError(err, taskUUID)
	}
	return room, nil
}

func (a *API) ensureContainerRoom(attr attribution.Attribution, containerUUID string, kind domain.RoomKind) (*domain.Room, error) {
	existing, err := a.store.Rooms.GetByContainer(containerUUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	if existing != nil {
		return existing, nil
	}
	room, err := a.store.Rooms.CreateWithAttribution(attr, store.RoomCreateParams{
		Kind: kind, ContainerUUID: &containerUUID,
	})
	if err != nil {
		if adopted, gerr := a.store.Rooms.GetByContainer(containerUUID); gerr == nil && adopted != nil {
			return adopted, nil
		}
		return nil, mapRoomStoreError(err, containerUUID)
	}
	return room, nil
}

// ─── addressee resolution ─────────────────────────────────────────────────────

// addresseeScope is the resolution context one say carries: the room, its
// members, and the replier's own address. Bare-name resolution reads the
// replier's standing obligations, so it needs to know who is speaking.
type addresseeScope struct {
	room             *roomState
	members          []domain.RoomMember
	replierScope     string
	replierPrincipal string

	obligations       []domain.Envelope
	obligationsLoaded bool
}

// presentedObligations loads the replier's standing obligations in this room
// once per say, and only when a bare name actually needs them.
func (s *addresseeScope) presentedObligations(a *API) ([]domain.Envelope, error) {
	if s.obligationsLoaded {
		return s.obligations, nil
	}
	rows, err := a.store.Rooms.PresentedObligationsForReplier(s.room.row.UUID, s.replierScope, s.replierPrincipal)
	if err != nil {
		return nil, NewInternalError(err)
	}
	s.obligations, s.obligationsLoaded = rows, true
	return s.obligations, nil
}

// resolveAddressees resolves each --to token against the room per §4. A full
// handle is taken verbatim; a bare name resolves against the replier's standing
// obligations first and by room kind last; HRC birth directives ride along
// verbatim in materialization_intent and are never parsed.
func (a *API) resolveAddressees(ctx context.Context, room *roomState, to []string, implied, replierScope, replierPrincipal string) ([]store.EnvelopeAddressee, error) {
	tokens := make([]string, 0, len(to)+1)
	for _, raw := range to {
		for _, part := range strings.Split(raw, ",") {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				tokens = append(tokens, trimmed)
			}
		}
	}
	if len(tokens) == 0 {
		if implied == "" {
			return nil, nil
		}
		tokens = append(tokens, implied)
	}

	members, err := a.store.Rooms.ListMembers(room.row.UUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	resolution := &addresseeScope{
		room: room, members: members,
		replierScope: replierScope, replierPrincipal: replierPrincipal,
	}
	seen := map[string]bool{}
	result := make([]store.EnvelopeAddressee, 0, len(tokens))
	for _, token := range tokens {
		addressee, rerr := a.resolveAddressee(ctx, resolution, token)
		if rerr != nil {
			return nil, rerr
		}
		key := addressee.ScopeRef + "|" + addressee.PrincipalRef
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, *addressee)
	}
	return result, nil
}

func (a *API) resolveAddressee(ctx context.Context, resolution *addresseeScope, token string) (*store.EnvelopeAddressee, error) {
	room := resolution.room
	// HRC birth directives (+node=, +model=) are stored verbatim and never
	// parsed by wrkq: they are HRC's vocabulary, applied at kick.
	handle := token
	var intent *string
	if index := strings.Index(token, "+"); index > 0 {
		handle = strings.TrimSpace(token[:index])
		directives := strings.TrimSpace(token[index:])
		if directives != "" {
			intent = &directives
		}
	}

	// An explicit principal (agent:lance) addresses a scope-less principal
	// directly. It is never kicked or summoned.
	if strings.HasPrefix(handle, "agent:") {
		principal, err := attribution.NormalizeCompat(handle)
		if err != nil {
			return nil, NewValidationError("invalid addressee: "+err.Error(), map[string]any{"field": "to", "to": token})
		}
		if principal != handle {
			// A full ScopeRef was supplied; keep the scope as the address.
			parsed, perr := scope.ParseScopeRef(handle)
			if perr == nil && parsed.ProjectID != "" {
				return &store.EnvelopeAddressee{
					ScopeRef: scope.FormatScopeHandle(parsed), PrincipalRef: principal,
					MaterializationIntent: intent,
				}, nil
			}
		}
		return &store.EnvelopeAddressee{PrincipalRef: principal, MaterializationIntent: intent}, nil
	}

	// A full handle is always accepted verbatim.
	if strings.Contains(handle, "@") {
		parsed, err := scope.ParseScopeHandle(handle)
		if err != nil {
			return nil, NewValidationError("invalid addressee handle: "+err.Error(), map[string]any{"field": "to", "to": token})
		}
		principal, perr := attribution.NormalizeCompat(parsed.AgentID)
		if perr != nil {
			return nil, NewValidationError("invalid addressee: "+perr.Error(), map[string]any{"field": "to", "to": token})
		}
		return &store.EnvelopeAddressee{
			ScopeRef: scope.FormatScopeHandle(parsed), PrincipalRef: principal,
			MaterializationIntent: intent,
		}, nil
	}

	// A bare agent name resolves against the room.
	if err := validateBareAgentName(handle); err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "to", "to": token})
	}

	// The obligation wins over the room's shape. HRC's §7 reply line prints a
	// bare name, and reply-is-ack keys on SCOPES (T-07628): if a bare reply
	// resolved by room kind it would address a seat that never asked, leaving
	// the real obligation to dead-letter while a correct answer sits in the room
	// (T-07638). A supervisor at :primary and a coordinator at any other seat
	// are both answered where they actually stand.
	obligated, err := a.addresseeFromObligation(resolution, handle, intent)
	if err != nil {
		return nil, err
	}
	if obligated != nil {
		return obligated, nil
	}

	matches := make([]domain.RoomMember, 0, 2)
	for _, member := range resolution.members {
		if member.LeftAt != nil {
			continue
		}
		if memberAgentName(member) == handle {
			matches = append(matches, member)
		}
	}
	if len(matches) > 1 {
		refs := make([]string, 0, len(matches))
		for _, match := range matches {
			refs = append(refs, match.MemberRef)
		}
		// Ambiguity ALWAYS refuses, and it refuses with the candidates IN the
		// message: a refusal costs the caller one retry with a full handle, while
		// a silently chosen seat costs a dead-lettered obligation nobody notices
		// (T-07638).
		return nil, NewValidationError("ambiguous addressee "+handle+" in this room: "+
			strings.Join(refs, ", ")+" — address one of them by its full handle", map[string]any{
			"field": "to", "to": token, "candidates": refs,
		})
	}
	if len(matches) == 1 {
		match := matches[0]
		addressee := &store.EnvelopeAddressee{PrincipalRef: match.MemberPrincipalRef, MaterializationIntent: intent}
		if match.Scoped {
			addressee.ScopeRef = match.MemberRef
			// A member IS a scope. The row's principal only records who last
			// spoke from the seat, so the address — and the attribution written
			// onto the envelope — derives from the seat itself (T-07628).
			if parsed, perr := scope.ParseScopeHandle(match.MemberRef); perr == nil {
				if principal, nerr := attribution.NormalizeCompat(parsed.AgentID); nerr == nil {
					addressee.PrincipalRef = principal
				}
			}
		}
		return addressee, nil
	}

	// Not a member here: a principal already known to the ledger as scope-less
	// is addressed directly rather than given a scope it does not have.
	principal, err := attribution.NormalizeCompat(handle)
	if err != nil {
		return nil, NewValidationError("invalid addressee: "+err.Error(), map[string]any{"field": "to", "to": token})
	}
	scopeless, err := a.principalIsKnownScopeless(ctx, principal)
	if err != nil {
		return nil, err
	}
	if scopeless {
		return &store.EnvelopeAddressee{PrincipalRef: principal, MaterializationIntent: intent}, nil
	}

	// Otherwise derive the scope from the room: a task room addresses the
	// task-scoped seat, a campaign or project room addresses :primary.
	derived, err := a.deriveAddresseeScope(ctx, room, handle)
	if err != nil {
		return nil, err
	}
	return &store.EnvelopeAddressee{ScopeRef: derived, PrincipalRef: principal, MaterializationIntent: intent}, nil
}

// addresseeFromObligation resolves a bare name to the seat that is waiting on
// this replier: the most recently PRESENTED reply_required envelope in this room
// sent by an agent of that name. Nil means no such obligation stands and the
// caller falls through to membership, then to the room-derived default.
func (a *API) addresseeFromObligation(resolution *addresseeScope, agentName string, intent *string) (*store.EnvelopeAddressee, error) {
	obligations, err := resolution.presentedObligations(a)
	if err != nil {
		return nil, err
	}
	for index := range obligations {
		envelope := &obligations[index]
		if envelopeSenderAgentName(envelope) != agentName {
			continue
		}
		if envelope.FromScopeRef == nil || strings.TrimSpace(*envelope.FromScopeRef) == "" {
			// A scope-less sender (a human) is addressed as the principal it is.
			return &store.EnvelopeAddressee{
				PrincipalRef: envelope.FromPrincipalRef, MaterializationIntent: intent,
			}, nil
		}
		addressee := &store.EnvelopeAddressee{
			ScopeRef: *envelope.FromScopeRef, PrincipalRef: envelope.FromPrincipalRef,
			MaterializationIntent: intent,
		}
		// The seat is the address, and the attribution derives from it: the
		// principal a say was attributed to is never what the reply targets
		// (T-07628).
		if parsed, perr := scope.ParseScopeHandle(*envelope.FromScopeRef); perr == nil {
			if principal, nerr := attribution.NormalizeCompat(parsed.AgentID); nerr == nil {
				addressee.PrincipalRef = principal
			}
		}
		return addressee, nil
	}
	return nil, nil
}

// envelopeSenderAgentName is the bare name an envelope's sender answers to: the
// agent of its SEAT when it has one, else its principal.
func envelopeSenderAgentName(envelope *domain.Envelope) string {
	if envelope.FromScopeRef != nil && strings.TrimSpace(*envelope.FromScopeRef) != "" {
		if parsed, err := scope.ParseScopeHandle(*envelope.FromScopeRef); err == nil {
			return parsed.AgentID
		}
		return *envelope.FromScopeRef
	}
	return strings.TrimPrefix(envelope.FromPrincipalRef, "agent:")
}

func (a *API) deriveAddresseeScope(ctx context.Context, room *roomState, agentName string) (string, error) {
	switch room.row.Kind {
	case domain.RoomKindAdhoc:
		return "", NewValidationError(
			"unknown addressee "+agentName+" in this ad-hoc room; use a full handle (agent@project:task)",
			map[string]any{"field": "to", "to": agentName, "room": room.key})
	case domain.RoomKindTask:
		project, taskID, err := a.taskRoomHandleParts(ctx, room)
		if err != nil {
			return "", err
		}
		return agentName + "@" + project + ":" + taskID, nil
	default:
		project, err := a.containerProjectSlug(ctx, *room.row.ContainerUUID)
		if err != nil {
			return "", err
		}
		return agentName + "@" + project + ":primary", nil
	}
}

func (a *API) taskRoomHandleParts(ctx context.Context, room *roomState) (string, string, error) {
	var taskID, containerUUID string
	if err := a.db.QueryRowContext(ctx, "SELECT id, project_uuid FROM tasks WHERE uuid = ?", *room.row.TaskUUID).
		Scan(&taskID, &containerUUID); err != nil {
		return "", "", NewInternalError(err)
	}
	project, err := a.containerProjectSlug(ctx, containerUUID)
	if err != nil {
		return "", "", err
	}
	return project, taskID, nil
}

// containerProjectSlug walks a container to its top-level project and returns
// that project's slug: the project segment of an agent scope handle.
func (a *API) containerProjectSlug(ctx context.Context, containerUUID string) (string, error) {
	var slug string
	err := a.db.QueryRowContext(ctx, `WITH RECURSIVE ancestry(uuid, parent_uuid, slug, kind) AS (
		SELECT c.uuid, c.parent_uuid, c.slug, c.kind FROM containers c WHERE c.uuid = ?
		UNION ALL
		SELECT p.uuid, p.parent_uuid, p.slug, p.kind
		  FROM containers p JOIN ancestry a ON p.uuid = a.parent_uuid
	)
	SELECT slug FROM ancestry WHERE kind = 'project' LIMIT 1`, containerUUID).Scan(&slug)
	if err == sql.ErrNoRows {
		return "", NewValidationError("container has no owning project", map[string]any{"container": containerUUID})
	}
	if err != nil {
		return "", NewInternalError(err)
	}
	return slug, nil
}

// principalIsKnownScopeless reports whether the ledger has already seen this
// principal participate WITHOUT an HRC scope. wrkq keeps no registry of humans;
// this is derived from the membership it already holds.
func (a *API) principalIsKnownScopeless(ctx context.Context, principalRef string) (bool, error) {
	var known int
	err := a.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM room_members
		WHERE member_principal_ref = ? AND scoped = 0)`, principalRef).Scan(&known)
	if err != nil {
		return false, NewInternalError(err)
	}
	return known == 1, nil
}

func memberAgentName(member domain.RoomMember) string {
	if !member.Scoped {
		return strings.TrimPrefix(member.MemberPrincipalRef, "agent:")
	}
	if parsed, err := scope.ParseScopeHandle(member.MemberRef); err == nil {
		return parsed.AgentID
	}
	return member.MemberRef
}

func validateBareAgentName(name string) error {
	if !scope.TokenPattern.MatchString(name) {
		return fmt.Errorf("invalid addressee %q: expected an agent name, agent@project[:task], or agent:<id>", name)
	}
	return nil
}

// ─── room verbs ───────────────────────────────────────────────────────────────

// RoomOpen opens an explicit ad-hoc or group room with named members.
func (a *API) RoomOpen(ctx context.Context, p RoomOpenParams) (*WrkqRoom, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Subject) == "" {
		return nil, NewValidationError("open requires a subject", map[string]any{"field": "subject"})
	}
	senderScope, err := normalizeRoomScopeRef(p.ScopeRef)
	if err != nil {
		return nil, err
	}

	seeds := []store.RoomMemberSeed{}
	if senderScope != "" {
		seeds = append(seeds, store.RoomMemberSeed{
			MemberRef: senderScope, MemberPrincipalRef: attr.PrincipalRef,
			Scoped: true, Source: domain.RoomMemberSourceJoined,
		})
	} else {
		seeds = append(seeds, store.RoomMemberSeed{
			MemberRef: attr.PrincipalRef, MemberPrincipalRef: attr.PrincipalRef,
			Scoped: false, Source: domain.RoomMemberSourceJoined,
		})
	}
	for _, raw := range p.Members {
		seed, serr := memberSeedFor(raw, domain.RoomMemberSourceJoined)
		if serr != nil {
			return nil, serr
		}
		seeds = append(seeds, *seed)
	}

	subject := strings.TrimSpace(p.Subject)
	room, err := a.store.Rooms.CreateWithAttribution(attr, store.RoomCreateParams{
		Kind: domain.RoomKindAdhoc, Subject: &subject, Members: seeds,
	})
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}
	state, err := a.loadRoomState(ctx, room.UUID)
	if err != nil {
		return nil, err
	}
	return a.roomDTO(ctx, state)
}

// RoomShow returns one room. Rooms are readable by any principal: membership is
// identity, never an ACL.
func (a *API) RoomShow(ctx context.Context, p RoomShowParams) (*WrkqRoom, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := a.resolveRoomSelector(ctx, p.Room)
	if err != nil {
		return nil, err
	}
	return a.roomDTO(ctx, state)
}

// RoomList lists rooms, optionally restricted to the caller's own scope.
func (a *API) RoomList(ctx context.Context, p RoomListParams) (*WrkqRoomListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}

	params := store.RoomListParams{}
	if trimmed := strings.TrimSpace(p.Kind); trimmed != "" {
		params.Kind = domain.RoomKind(trimmed)
	}
	if strings.EqualFold(strings.TrimSpace(p.Scope), "me") {
		senderScope, serr := normalizeRoomScopeRef(p.ScopeRef)
		if serr != nil {
			return nil, serr
		}
		params.MemberRef = senderScope
		if params.MemberRef == "" {
			params.MemberRef = attr.PrincipalRef
		}
	}
	rows, err := a.store.Rooms.List(params)
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}
	result := &WrkqRoomListResult{Items: make([]WrkqRoom, 0, len(rows))}
	for index := range rows {
		state, serr := a.hydrateRoomState(ctx, &rows[index])
		if serr != nil {
			return nil, serr
		}
		dto, derr := a.roomDTO(ctx, state)
		if derr != nil {
			return nil, derr
		}
		// Discovery, and ONLY discovery: the default listing drops what has gone
		// stale or been hidden. Neither omission touches say, delivery, or
		// obligations — `--all` is the whole ledger, one flag away.
		if !p.All && (dto.Activity == string(domain.RoomActivityStale) ||
			domain.RoomHasLabel(state.row.Labels, domain.RoomLabelHidden)) {
			continue
		}
		result.Items = append(result.Items, *dto)
	}
	return result, nil
}

// RoomLogView returns a room's history. This is the pull an agent makes after
// the §7 `history:` cue; history is never injected.
func (a *API) RoomLogView(ctx context.Context, p RoomLogViewParams) (*WrkqRoomLogView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := a.resolveRoomSelector(ctx, p.Room)
	if err != nil {
		return nil, err
	}
	params := store.EnvelopeListParams{RoomUUID: state.row.UUID}
	if strings.TrimSpace(p.Task) != "" {
		taskUUID, _, terr := selectors.ResolveTask(a.db, p.Task)
		if terr != nil {
			return nil, NewNotFoundError(p.Task, "task")
		}
		params.TaskUUID = taskUUID
	}
	if p.Limit > 0 {
		params.Limit = p.Limit
		params.NewestFirst = true
	}
	rows, err := a.store.Rooms.ListEnvelopes(params)
	if err != nil {
		return nil, mapRoomStoreError(err, p.Room)
	}
	if params.NewestFirst {
		for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
			rows[left], rows[right] = rows[right], rows[left]
		}
	}
	dto, err := a.roomDTO(ctx, state)
	if err != nil {
		return nil, err
	}
	view := &WrkqRoomLogView{Room: *dto, Items: make([]WrkqEnvelope, 0, len(rows))}
	for index := range rows {
		envelope, eerr := a.envelopeDTO(ctx, &rows[index], state)
		if eerr != nil {
			return nil, eerr
		}
		view.Items = append(view.Items, *envelope)
	}
	return view, nil
}

// RoomClose and RoomReopen are REMOVED. Rooms have no lifecycle that can gate
// traffic (T-07612 rev 3), so there is nothing left for either verb to do. They
// stay registered for one burn-in window returning a typed, named refusal —
// old clients get `room_lifecycle_removed` rather than a bare method-not-found
// — and wave 5 deletes them.
func (a *API) RoomClose(ctx context.Context, p RoomLifecycleParams) (*WrkqRoom, error) {
	return nil, roomLifecycleRemoved(ctx, "close", p.Room)
}

func (a *API) RoomReopen(ctx context.Context, p RoomLifecycleParams) (*WrkqRoom, error) {
	return nil, roomLifecycleRemoved(ctx, "reopen", p.Room)
}

func roomLifecycleRemoved(ctx context.Context, verb, room string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return NewValidationError(
		"room_lifecycle_removed: rooms have no lifecycle, so there is nothing to "+verb+
			"; a say into any room you can resolve always writes",
		map[string]any{
			"reason": "room_lifecycle_removed", "verb": verb, "room": room,
			"replacement": "wrkc hide|unhide changes the default listing only",
		})
}

// RoomHide removes a room from the DEFAULT listing. RoomUnhide is its inverse.
// The label is not an ACL and not a gate: any principal may set it, the room
// still accepts says, and its obligations gate and wake unchanged.
func (a *API) RoomHide(ctx context.Context, p RoomLabelParams) (*WrkqRoom, error) {
	return a.roomSetLabel(ctx, p, true)
}

func (a *API) RoomUnhide(ctx context.Context, p RoomLabelParams) (*WrkqRoom, error) {
	return a.roomSetLabel(ctx, p, false)
}

func (a *API) roomSetLabel(ctx context.Context, p RoomLabelParams, on bool) (*WrkqRoom, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	current, err := a.resolveRoomSelector(ctx, p.Room)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.Rooms.SetRoomLabelWithAttribution(
		attr, current.row.UUID, domain.RoomLabelHidden, on); err != nil {
		return nil, mapRoomStoreError(err, p.Room)
	}
	refreshed, err := a.loadRoomState(ctx, current.row.UUID)
	if err != nil {
		return nil, err
	}
	return a.roomDTO(ctx, refreshed)
}

// RoomJoin adds the caller (or, for invite, a named scope) to a room.
func (a *API) RoomJoin(ctx context.Context, p RoomMemberParams) (*WrkqRoomMembersView, error) {
	return a.roomMemberMutation(ctx, p, true)
}

// RoomLeave records that a member has left. Attendance stays readable.
func (a *API) RoomLeave(ctx context.Context, p RoomMemberParams) (*WrkqRoomMembersView, error) {
	return a.roomMemberMutation(ctx, p, false)
}

func (a *API) roomMemberMutation(ctx context.Context, p RoomMemberParams, join bool) (*WrkqRoomMembersView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	state, err := a.resolveRoomSelector(ctx, p.Room)
	if err != nil {
		return nil, err
	}

	member := strings.TrimSpace(p.Member)
	if member == "" {
		senderScope, serr := normalizeRoomScopeRef(p.ScopeRef)
		if serr != nil {
			return nil, serr
		}
		if senderScope != "" {
			member = senderScope
		} else {
			member = attr.PrincipalRef
		}
	}
	seed, err := memberSeedFor(member, domain.RoomMemberSourceJoined)
	if err != nil {
		return nil, err
	}
	if join {
		if _, err := a.store.Rooms.AddMemberWithAttribution(attr, state.row.UUID, *seed); err != nil {
			return nil, mapRoomStoreError(err, p.Room)
		}
	} else if err := a.store.Rooms.RemoveMemberWithAttribution(attr, state.row.UUID, seed.MemberRef); err != nil {
		return nil, mapRoomStoreError(err, p.Room)
	}
	if err := a.store.Rooms.TouchRoomActivity(state.row.UUID); err != nil {
		return nil, NewInternalError(err)
	}
	return a.RoomMembersView(ctx, RoomMembersViewParams{Room: p.Room, PrincipalRef: p.PrincipalRef})
}

// RoomMembersView lists members with their source and latest attendance.
func (a *API) RoomMembersView(ctx context.Context, p RoomMembersViewParams) (*WrkqRoomMembersView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	state, err := a.resolveRoomSelector(ctx, p.Room)
	if err != nil {
		return nil, err
	}
	members, err := a.store.Rooms.ListMembers(state.row.UUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	attendance, err := a.store.Rooms.LatestAttendance(state.row.UUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	dto, err := a.roomDTO(ctx, state)
	if err != nil {
		return nil, err
	}
	view := &WrkqRoomMembersView{Room: *dto, Items: make([]WrkqRoomMember, 0, len(members))}
	for _, member := range members {
		item := WrkqRoomMember{
			MemberRef: member.MemberRef, MemberPrincipalRef: member.MemberPrincipalRef,
			Scoped: member.Scoped, Source: string(member.Source),
			JoinedAt: toRFC3339(member.JoinedAt), LeftAt: member.LeftAt,
		}
		if presentation, ok := attendance[member.MemberRef]; ok {
			item.Attendance = presentationDTO(&presentation)
		}
		view.Items = append(view.Items, item)
	}
	return view, nil
}

func memberSeedFor(raw string, source domain.RoomMemberSource) (*store.RoomMemberSeed, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, NewValidationError("member is required", map[string]any{"field": "member"})
	}
	if strings.Contains(raw, "@") {
		parsed, err := scope.ParseScopeHandle(raw)
		if err != nil {
			return nil, NewValidationError("invalid member handle: "+err.Error(), map[string]any{"field": "member", "member": raw})
		}
		principal, perr := attribution.NormalizeCompat(parsed.AgentID)
		if perr != nil {
			return nil, NewValidationError("invalid member: "+perr.Error(), map[string]any{"field": "member"})
		}
		return &store.RoomMemberSeed{
			MemberRef: scope.FormatScopeHandle(parsed), MemberPrincipalRef: principal,
			Scoped: true, Source: source,
		}, nil
	}
	principal, err := attribution.NormalizeCompat(raw)
	if err != nil {
		return nil, NewValidationError("invalid member: "+err.Error(), map[string]any{"field": "member", "member": raw})
	}
	return &store.RoomMemberSeed{
		MemberRef: principal, MemberPrincipalRef: principal, Scoped: false, Source: source,
	}, nil
}

// ─── envelope verbs ───────────────────────────────────────────────────────────

// EnvelopeShow returns one envelope with its presentation receipts.
func (a *API) EnvelopeShow(ctx context.Context, p EnvelopeShowParams) (*WrkqEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	envelope, err := a.store.Rooms.GetEnvelope(strings.TrimSpace(p.Envelope))
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	state, err := a.loadRoomState(ctx, envelope.RoomUUID)
	if err != nil {
		return nil, err
	}
	return a.envelopeDTO(ctx, envelope, state)
}

// EnvelopeBirthEnvelope returns the BIRTH ENVELOPE of one target scope: the
// lowest-seq `reply_required` envelope addressed to it, in any state, or nil
// when nothing has ever fired at it. fyi never summons and is outside the
// domain.
//
// This is HRC's tier-5 birth designation input (T-07655). The params carry the
// TARGET and nothing else — the sender comes off the ledger row, so a caller
// cannot steer which node a virgin scope is born on.
func (a *API) EnvelopeBirthEnvelope(ctx context.Context, p EnvelopeBirthEnvelopeParams) (*WrkqEnvelopeBirth, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	target, err := normalizeRoomScopeRef(p.ScopeRef)
	if err != nil {
		return nil, err
	}
	if target == "" {
		return nil, NewValidationError("birthEnvelope requires a target scope handle", map[string]any{"field": "scopeRef", "scopeRef": p.ScopeRef})
	}
	envelope, err := a.store.Rooms.BirthEnvelope(target)
	if err != nil {
		return nil, mapRoomStoreError(err, target)
	}
	if envelope == nil {
		return nil, nil
	}
	_, seq, err := id.Parse(envelope.ID)
	if err != nil {
		return nil, NewInternalError(fmt.Errorf("birth envelope %s has no ledger ordinal: %w", envelope.ID, err))
	}
	return &WrkqEnvelopeBirth{
		EnvelopeID: envelope.ID,
		Seq:        int64(seq),
		From:       WrkqEnvelopeParty{PrincipalRef: envelope.FromPrincipalRef, ScopeRef: envelope.FromScopeRef},
	}, nil
}

// EnvelopeInboxView lists the reply_required obligations standing against one
// scope, grouped by room. fyi is never listed: it carries no obligation. Every
// group is a real obligation that gates and wakes: there is no room projection
// that excuses one. A group whose room reads `work: terminal` is INFORMATION a
// renderer may lead with, not a different class of mail.
func (a *API) EnvelopeInboxView(ctx context.Context, p EnvelopeInboxViewParams) (*WrkqEnvelopeInboxView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if _, err := a.store.Rooms.RependDueDeferrals(attr); err != nil {
		return nil, NewInternalError(err)
	}
	target, err := normalizeRoomScopeRef(p.ScopeRef)
	if err != nil {
		return nil, err
	}

	base := store.EnvelopeListParams{Obligations: []domain.EnvelopeObligation{domain.EnvelopeObligationReplyRequired}}
	if target != "" {
		base.ToScopeRef = target
	} else {
		base.ToPrincipalRef = attr.PrincipalRef
	}

	view := &WrkqEnvelopeInboxView{PrincipalRef: attr.PrincipalRef, Groups: []WrkqEnvelopeInboxGroup{}, Deferred: []WrkqEnvelope{}, Dead: []WrkqEnvelope{}}
	if target != "" {
		view.ScopeRef = &target
	}

	standing := base
	standing.States = []domain.EnvelopeState{domain.EnvelopeStatePending, domain.EnvelopeStatePresented}
	rows, err := a.store.Rooms.ListEnvelopes(standing)
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}
	order := []string{}
	grouped := map[string][]domain.Envelope{}
	for index := range rows {
		key := rows[index].RoomUUID
		if _, ok := grouped[key]; !ok {
			order = append(order, key)
		}
		grouped[key] = append(grouped[key], rows[index])
	}
	for _, roomUUID := range order {
		state, serr := a.loadRoomState(ctx, roomUUID)
		if serr != nil {
			return nil, serr
		}
		dto, derr := a.roomDTO(ctx, state)
		if derr != nil {
			return nil, derr
		}
		group := WrkqEnvelopeInboxGroup{Room: *dto}
		for index := range grouped[roomUUID] {
			envelope, eerr := a.envelopeDTO(ctx, &grouped[roomUUID][index], state)
			if eerr != nil {
				return nil, eerr
			}
			group.Items = append(group.Items, *envelope)
		}
		view.Groups = append(view.Groups, group)
	}

	deferred := base
	deferred.States = []domain.EnvelopeState{domain.EnvelopeStateDeferred}
	view.Deferred, err = a.envelopeListDTO(ctx, deferred)
	if err != nil {
		return nil, err
	}
	if p.IncludeDead {
		dead := base
		dead.States = []domain.EnvelopeState{domain.EnvelopeStateDead}
		view.Dead, err = a.envelopeListDTO(ctx, dead)
		if err != nil {
			return nil, err
		}
	}
	return view, nil
}

// EnvelopeDefer pauses one obligation. Deferred is paused, NEVER terminal: a
// later reply still acks it. A retry time is backed by a wrkq promise owned by
// the deferring principal, and the deferral re-pends when that time arrives.
func (a *API) EnvelopeDefer(ctx context.Context, p EnvelopeDeferParams) (*WrkqEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
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
	state, err := a.loadRoomState(ctx, updated.RoomUUID)
	if err != nil {
		return nil, err
	}
	return a.envelopeDTO(ctx, updated, state)
}

// EnvelopeAck is the OPERATOR-only ack, intended for humans such as agent:lance
// clearing dead mail. There is no agent-facing ack: for an agent the reply IS
// the ack, which is why this accepts ANY principal and refuses nothing on
// identity — the surface it is reachable from is the control.
func (a *API) EnvelopeAck(ctx context.Context, p EnvelopeAckParams) (*WrkqRoomLogView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	if len(p.Envelopes) == 0 {
		return nil, NewValidationError("ack requires at least one envelope", map[string]any{"field": "envelopes"})
	}
	view := &WrkqRoomLogView{Items: []WrkqEnvelope{}}
	for _, selector := range p.Envelopes {
		envelope, gerr := a.store.Rooms.GetEnvelope(strings.TrimSpace(selector))
		if gerr != nil {
			return nil, mapRoomStoreError(gerr, selector)
		}
		disposition := store.EnvelopeDisposition{State: domain.EnvelopeStateAcked, Reason: "operator"}
		if strings.TrimSpace(p.Note) != "" {
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
			dto, rerr := a.roomDTO(ctx, state)
			if rerr != nil {
				return nil, rerr
			}
			view.Room = *dto
		}
		dto, eerr := a.envelopeDTO(ctx, updated, state)
		if eerr != nil {
			return nil, eerr
		}
		view.Items = append(view.Items, *dto)
	}
	return view, nil
}

// EnvelopePresent is the HRC-facing presentation projection and receipt write.
// Preview returns the same projection without mutating the ledger; commit
// records presented_to and emits envelope.presented. The §7 `history:` cue is
// keyed to the RUNTIME so a post-/quit runtime sharing its generation is cold.
func (a *API) EnvelopePresent(ctx context.Context, p EnvelopePresentParams) (*WrkqEnvelopePresentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
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

// EnvelopePendingView is the HRC-facing read: the kicker's wake set AND the
// stop-hook predicate in one call. Its sweep re-pends due deferrals, which is
// the periodic-sweep half of §5's wake routing.
//
// The read is UNIFORM over rooms: an obligation wakes and gates whatever its
// room's work, activity, or hidden label says. T-07633 excluded a closed room's
// mail here, which was correct only while a closed room refused a say; with the
// gate gone the addressee always has a reply path, and excluding its mail would
// silently strand a supervisor's follow-up on completed work (T-07642).
func (a *API) EnvelopePendingView(ctx context.Context, p EnvelopePendingViewParams) (*WrkqEnvelopePendingView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	repended, err := a.store.Rooms.RependDueDeferrals(attr)
	if err != nil {
		return nil, NewInternalError(err)
	}

	scopes := make([]string, 0, len(p.Scopes))
	for _, raw := range p.Scopes {
		normalized, nerr := normalizeRoomScopeRef(raw)
		if nerr != nil {
			return nil, nerr
		}
		if normalized != "" {
			scopes = append(scopes, normalized)
		}
	}
	addressee := store.EnvelopeListParams{}
	if len(scopes) > 0 {
		addressee.ToScopeRefs = scopes
	} else {
		own, oerr := normalizeRoomScopeRef(p.ScopeRef)
		if oerr != nil {
			return nil, oerr
		}
		if own != "" {
			addressee.ToScopeRef = own
		} else {
			addressee.ToPrincipalRef = attr.PrincipalRef
		}
	}

	params := addressee
	params.Obligations = []domain.EnvelopeObligation{domain.EnvelopeObligationReplyRequired}
	params.States = []domain.EnvelopeState{domain.EnvelopeStatePending, domain.EnvelopeStatePresented}

	view := &WrkqEnvelopePendingView{Items: []WrkqEnvelope{}, Blocking: []string{}, Repended: repended}
	collect := func(listParams store.EnvelopeListParams) error {
		rows, lerr := a.store.Rooms.ListEnvelopes(listParams)
		if lerr != nil {
			return mapRoomStoreError(lerr, "")
		}
		for index := range rows {
			state, serr := a.loadRoomState(ctx, rows[index].RoomUUID)
			if serr != nil {
				return serr
			}
			dto, eerr := a.envelopeDTO(ctx, &rows[index], state)
			if eerr != nil {
				return eerr
			}
			view.Items = append(view.Items, *dto)
			// The stop-hook refuses a turn end only for what was actually
			// PRESENTED, left neither replied nor deferred, and OBLIGED: a fyi
			// never reaches here presented, because presentation auto-acks it.
			if rows[index].State == domain.EnvelopeStatePresented &&
				rows[index].Obligation == domain.EnvelopeObligationReplyRequired {
				view.Blocking = append(view.Blocking, rows[index].ID)
			}
		}
		return nil
	}

	if err := collect(params); err != nil {
		return nil, err
	}
	if p.IncludeFyi {
		// The opt-in half: a fyi carries no obligation, so it is only ever an
		// item. Its auto-ack at presentation leaves `pending` as the only live
		// fyi state, and it never blocks a turn end nor summons a runtime.
		fyiParams := addressee
		fyiParams.Obligations = []domain.EnvelopeObligation{domain.EnvelopeObligationFYI}
		fyiParams.States = []domain.EnvelopeState{domain.EnvelopeStatePending}
		if err := collect(fyiParams); err != nil {
			return nil, err
		}
	}
	return view, nil
}

// EnvelopeRoundEnded records that a completed kicker turn presented an envelope
// and ended without disposition. Only a still-presented envelope advances, so a
// clear-inbox no-op turn never burns a round.
func (a *API) EnvelopeRoundEnded(ctx context.Context, p EnvelopeRoundParams) (*WrkqEnvelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	attr, err := a.attributionFor(p.PrincipalRef)
	if err != nil {
		return nil, err
	}
	envelope, err := a.store.Rooms.GetEnvelope(strings.TrimSpace(p.Envelope))
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	maxRounds := p.MaxRounds
	if maxRounds <= 0 {
		maxRounds = configuredEnvelopeMaxRounds()
	}
	updated, err := a.store.Rooms.RecordRoundWithAttribution(attr, envelope.UUID, maxRounds)
	if err != nil {
		return nil, mapRoomStoreError(err, p.Envelope)
	}
	state, err := a.loadRoomState(ctx, updated.RoomUUID)
	if err != nil {
		return nil, err
	}
	return a.envelopeDTO(ctx, updated, state)
}

func configuredEnvelopeMaxRounds() int64 {
	if raw := strings.TrimSpace(os.Getenv(envelopeMaxRoundsEnv)); raw != "" {
		if parsed, err := strconv.ParseInt(raw, 10, 64); err == nil && parsed > 0 {
			return parsed
		}
	}
	return domain.DefaultEnvelopeMaxRounds
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

func envelopePrincipal(envelope *domain.Envelope, attr attribution.Attribution) string {
	if envelope.ToPrincipalRef != nil {
		return *envelope.ToPrincipalRef
	}
	return attr.PrincipalRef
}

// ─── projection ───────────────────────────────────────────────────────────────

// roomState carries a room row plus everything the DTO and the say gate need,
// resolved once per call.
type roomState struct {
	row     *domain.Room
	key     string
	workRef *WrkqRoomWorkRef
	// work and activity are the two read-time projections. Neither is stored and
	// neither gates: they are computed here once per call and only ever read.
	work         domain.RoomWork
	activity     domain.RoomActivity
	lastActivity string
	// workState and workTerminalAt name the terminal transition the stale notice
	// quotes back ("task completed 2026-08-27").
	workState      string
	workTerminalAt string
	memberCount    int
	messageCount   int
	lastMessageAt  string
	links          []WrkqRoomLink
}

func (a *API) resolveRoomSelector(ctx context.Context, selector string) (*roomState, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, NewValidationError("room selector is required", map[string]any{"field": "room"})
	}
	if kind, _, err := id.Parse(selector); err == nil {
		switch kind {
		case id.TypeEnvelope:
			envelope, gerr := a.store.Rooms.GetEnvelope(selector)
			if gerr != nil {
				return nil, mapRoomStoreError(gerr, selector)
			}
			return a.loadRoomState(ctx, envelope.RoomUUID)
		case id.TypeTask:
			taskUUID, _, terr := selectors.ResolveTask(a.db, selector)
			if terr != nil {
				return nil, NewNotFoundError(selector, "task")
			}
			room, rerr := a.store.Rooms.GetByTask(taskUUID)
			if rerr != nil {
				return nil, NewInternalError(rerr)
			}
			if room == nil {
				return nil, NewNotFoundError(selector, "room")
			}
			return a.hydrateRoomState(ctx, room)
		case id.TypeContainer:
			return a.roomStateForContainerSelector(ctx, selector)
		}
	}
	if room, err := a.store.Rooms.Get(selector); err == nil {
		return a.hydrateRoomState(ctx, room)
	}
	if containerUUID, _, err := selectors.ResolveContainer(a.db, selector); err == nil {
		room, rerr := a.store.Rooms.GetByContainer(containerUUID)
		if rerr != nil {
			return nil, NewInternalError(rerr)
		}
		if room != nil {
			return a.hydrateRoomState(ctx, room)
		}
	}
	if taskUUID, _, err := selectors.ResolveTask(a.db, selector); err == nil {
		room, rerr := a.store.Rooms.GetByTask(taskUUID)
		if rerr != nil {
			return nil, NewInternalError(rerr)
		}
		if room != nil {
			return a.hydrateRoomState(ctx, room)
		}
	}
	return nil, NewNotFoundError(selector, "room")
}

func (a *API) roomStateForContainerSelector(ctx context.Context, selector string) (*roomState, error) {
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return nil, NewNotFoundError(selector, "container")
	}
	room, rerr := a.store.Rooms.GetByContainer(containerUUID)
	if rerr != nil {
		return nil, NewInternalError(rerr)
	}
	if room == nil {
		return nil, NewNotFoundError(selector, "room")
	}
	return a.hydrateRoomState(ctx, room)
}

func (a *API) loadRoomState(ctx context.Context, roomUUID string) (*roomState, error) {
	room, err := a.store.Rooms.Get(roomUUID)
	if err != nil {
		return nil, mapRoomStoreError(err, roomUUID)
	}
	return a.hydrateRoomState(ctx, room)
}

func (a *API) hydrateRoomState(ctx context.Context, room *domain.Room) (*roomState, error) {
	state := &roomState{row: room, links: []WrkqRoomLink{}}

	switch room.Kind {
	case domain.RoomKindTask:
		ref := &WrkqRoomWorkRef{Type: "task", UUID: *room.TaskUUID}
		var taskState string
		var terminalAt sql.NullString
		err := a.db.QueryRowContext(ctx, `
			SELECT t.id, COALESCE(cp.path || '/' || t.slug, t.slug), t.state,
			       COALESCE(t.completed_at, t.archived_at, t.deleted_at, t.updated_at)
			  FROM tasks t LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			 WHERE t.uuid = ?`, *room.TaskUUID).Scan(&ref.ID, &ref.Path, &taskState, &terminalAt)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, NewNotFoundError(*room.TaskUUID, "room task")
			}
			return nil, NewInternalError(err)
		}
		state.workRef = ref
		state.key = ref.ID
		state.workState = taskState
		state.workTerminalAt = terminalAt.String
		if isTerminalTaskState(taskState) {
			state.work = domain.RoomWorkTerminal
		}
		// A task that later joined a campaign keeps its own room readable and
		// linked; new says route to the campaign room. Never merged. This uses
		// the SAME membership predicate the routing does, so the link can never
		// point somewhere routing would not go.
		campaignUUID, cerr := a.effectiveCampaignForTask(ctx, *room.TaskUUID)
		if cerr != nil {
			return nil, cerr
		}
		if campaignUUID != "" {
			if linked, lerr := a.store.Rooms.GetByContainer(campaignUUID); lerr == nil && linked != nil {
				key, kerr := a.containerRoomKey(ctx, campaignUUID)
				if kerr != nil {
					return nil, kerr
				}
				state.links = append(state.links, WrkqRoomLink{
					Relation: "coalesced_into", Key: key, UUID: linked.UUID, Kind: string(linked.Kind),
				})
			}
		}
	case domain.RoomKindCampaign, domain.RoomKindProject:
		ref := &WrkqRoomWorkRef{Type: "container", UUID: *room.ContainerUUID}
		var campaignState sql.NullString
		var containerUpdatedAt string
		err := a.db.QueryRowContext(ctx, `
			SELECT c.id, COALESCE(v.path, c.slug), c.campaign_state, c.updated_at
			  FROM containers c LEFT JOIN v_container_paths v ON v.uuid = c.uuid
			 WHERE c.uuid = ?`, *room.ContainerUUID).
			Scan(&ref.ID, &ref.Path, &campaignState, &containerUpdatedAt)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, NewNotFoundError(*room.ContainerUUID, "room container")
			}
			return nil, NewInternalError(err)
		}
		state.workRef = ref
		state.key = ref.Path
		if campaignState.Valid {
			state.workState = campaignState.String
			switch campaignState.String {
			case "completed", "cancelled":
				state.work = domain.RoomWorkTerminal
				state.workTerminalAt = containerUpdatedAt
			}
		}
	default:
		if room.ID != nil {
			state.key = *room.ID
		} else {
			state.key = room.UUID
		}
	}

	// An ad-hoc room anchors on no work, so it is never terminal.
	if state.work == "" {
		state.work = domain.RoomWorkOpen
	}

	var newestJoin sql.NullString
	if err := a.db.QueryRowContext(ctx,
		`SELECT
		   (SELECT COUNT(*) FROM room_members WHERE room_uuid = ? AND left_at IS NULL),
		   (SELECT MAX(joined_at) FROM room_members WHERE room_uuid = ?)`,
		room.UUID, room.UUID).Scan(&state.memberCount, &newestJoin); err != nil {
		return nil, NewInternalError(err)
	}
	var lastMessage sql.NullString
	if err := a.db.QueryRowContext(ctx,
		"SELECT COUNT(*), MAX(created_at) FROM envelopes WHERE room_uuid = ?", room.UUID).
		Scan(&state.messageCount, &lastMessage); err != nil {
		return nil, NewInternalError(err)
	}
	if lastMessage.Valid {
		state.lastMessageAt = lastMessage.String
	}

	// The activity clock is TOTAL: opened_at always exists, so a room with no
	// envelope and no join beyond its own still classifies.
	state.lastActivity = domain.RoomLastActivity(
		toRFC3339(room.OpenedAt), toRFC3339(state.lastMessageAt), toRFC3339(newestJoin.String))
	state.activity = domain.RoomActivityFor(state.work, parseRoomTimestamp(state.lastActivity), time.Now().UTC())
	return state, nil
}

// parseRoomTimestamp reads a stored wrkq timestamp. An unparseable one reads as
// the zero time, which classifies the room as quiet rather than crashing a list.
func parseRoomTimestamp(value string) time.Time {
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05Z", "2006-01-02 15:04:05"} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC()
		}
	}
	return time.Time{}
}

// roomActiveSince is the `active` cutoff the store's pair-room reuse compares
// against, in the ledger's own timestamp format.
func roomActiveSince(now time.Time) string {
	return now.Add(-domain.RoomActiveWithin).Format("2006-01-02T15:04:05Z")
}

// staleRoomNotice is §5: advisory, never an error, and only for a room whose
// activity reads `stale`. It quotes the terminal transition and the age of the
// last activity so a supervisor can tell "this seat moved on" from "this seat
// is live", without ever being refused.
func staleRoomNotice(room *roomState) *string {
	if room.activity != domain.RoomActivityStale {
		return nil
	}
	work := "work"
	if room.workRef != nil {
		work = room.workRef.Type
	}
	when := room.workTerminalAt
	if len(when) >= 10 {
		when = when[:10]
	}
	notice := "room " + room.key + " — " + work + " " + room.workState
	if when != "" {
		notice += " " + when
	}
	notice += ", last activity " + humanRoomAge(time.Since(parseRoomTimestamp(room.lastActivity)))
	return &notice
}

// humanRoomAge renders an age at the coarsest unit that still says something:
// a notice reading "6h ago" is read; one reading "6h11m47s ago" is skimmed.
func humanRoomAge(age time.Duration) string {
	switch {
	case age < time.Minute:
		return "just now"
	case age < time.Hour:
		return fmt.Sprintf("%dm ago", int(age.Minutes()))
	case age < 48*time.Hour:
		return fmt.Sprintf("%dh ago", int(age.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(age.Hours()/24))
	}
}

func (a *API) containerRoomKey(ctx context.Context, containerUUID string) (string, error) {
	var path string
	if err := a.db.QueryRowContext(ctx, `SELECT COALESCE(v.path, c.slug)
		 FROM containers c LEFT JOIN v_container_paths v ON v.uuid = c.uuid
		 WHERE c.uuid = ?`, containerUUID).Scan(&path); err != nil {
		return "", NewInternalError(err)
	}
	return path, nil
}

func isTerminalTaskState(state string) bool {
	switch state {
	case "completed", "cancelled", "archived", "deleted":
		return true
	default:
		return false
	}
}

func (a *API) roomDTO(ctx context.Context, state *roomState) (*WrkqRoom, error) {
	_ = ctx
	room := state.row
	labels := room.Labels
	if labels == nil {
		labels = []string{}
	}
	dto := &WrkqRoom{
		UUID: room.UUID, ID: room.ID, Key: state.key, Kind: string(room.Kind),
		Subject: room.Subject, Work: string(state.work), Activity: string(state.activity),
		Labels: labels, WorkRef: state.workRef, Links: state.links,
		OpenedByPrincipalRef: room.OpenedByPrincipalRef, OpenedAt: toRFC3339(room.OpenedAt),
		LastActivityAt: state.lastActivity,
		MemberCount:    state.memberCount, MessageCount: state.messageCount,
		ETag: room.ETag, CreatedAt: toRFC3339(room.CreatedAt), UpdatedAt: toRFC3339(room.UpdatedAt),
	}
	if dto.Links == nil {
		dto.Links = []WrkqRoomLink{}
	}
	return dto, nil
}

func (a *API) envelopeListDTO(ctx context.Context, params store.EnvelopeListParams) ([]WrkqEnvelope, error) {
	rows, err := a.store.Rooms.ListEnvelopes(params)
	if err != nil {
		return nil, mapRoomStoreError(err, "")
	}
	result := make([]WrkqEnvelope, 0, len(rows))
	for index := range rows {
		state, serr := a.loadRoomState(ctx, rows[index].RoomUUID)
		if serr != nil {
			return nil, serr
		}
		dto, eerr := a.envelopeDTO(ctx, &rows[index], state)
		if eerr != nil {
			return nil, eerr
		}
		result = append(result, *dto)
	}
	return result, nil
}

func (a *API) envelopeDTO(ctx context.Context, envelope *domain.Envelope, room *roomState) (*WrkqEnvelope, error) {
	dto := &WrkqEnvelope{
		UUID: envelope.UUID, ID: envelope.ID, RoomUUID: envelope.RoomUUID,
		RoomKey: room.key, RoomKind: string(room.row.Kind), GroupID: envelope.GroupID,
		From:       WrkqEnvelopeParty{PrincipalRef: envelope.FromPrincipalRef, ScopeRef: envelope.FromScopeRef},
		ReplyTo:    envelopeReplyTo(envelope),
		Obligation: string(envelope.Obligation), Body: envelope.Body,
		State: string(envelope.State), Terminal: domain.IsEnvelopeTerminal(envelope.State),
		RoundCount: envelope.RoundCount, RetryAt: envelope.RetryAt,
		DeferReason: envelope.DeferReason, TerminalActor: envelope.TerminalActor,
		MaterializationIntent: envelope.MaterializationIntent,
		RespondToPrincipalRef: envelope.RespondToPrincipalRef,
		IdempotencyKey:        envelope.IdempotencyKey,
		Meta:                  map[string]any{},
		PresentedTo:           []WrkqEnvelopePresentation{},
		ETag:                  envelope.ETag, CreatedAt: toRFC3339(envelope.CreatedAt),
		UpdatedAt: toRFC3339(envelope.UpdatedAt),
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

// ─── helpers ──────────────────────────────────────────────────────────────────

// normalizeRoomScopeRef accepts a canonical ScopeRef or a scope handle and
// returns the handle form wrkq stores. wrkq parses the grammar and nothing more:
// what a scope MEANS at runtime is HRC's business.
func normalizeRoomScopeRef(raw string) (string, error) {
	raw = stripRuntimeLane(strings.TrimSpace(raw))
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "agent:") {
		parsed, err := scope.ParseScopeRef(raw)
		if err != nil {
			return "", NewValidationError("invalid scopeRef: "+err.Error(), map[string]any{"field": "scopeRef", "scopeRef": raw})
		}
		if parsed.ProjectID == "" {
			// A bare principal is not a scope: it is a scope-less identity.
			return "", nil
		}
		return scope.FormatScopeHandle(parsed), nil
	}
	parsed, err := scope.ParseScopeHandle(raw)
	if err != nil {
		return "", NewValidationError("invalid scope handle: "+err.Error(), map[string]any{"field": "scopeRef", "scopeRef": raw})
	}
	if parsed.ProjectID == "" {
		return "", nil
	}
	return scope.FormatScopeHandle(parsed), nil
}

// stripRuntimeLane drops the runtime lane HRC appends to a live session ref
// (HRC_SESSION_REF is "agent:clod:project:wrkq:task:T-07613/lane:main"). A lane
// is execution vocabulary: which pane of a runtime is speaking. A room member is
// a SCOPE, so the lane is discarded here rather than modelled — the boundary
// rule cuts exactly here.
//
// A role suffix (".../reviewer") is part of the scope grammar and is kept: only
// a suffix carrying its own "key:value" shape is a runtime lane, because a role
// name cannot contain a colon.
func stripRuntimeLane(raw string) string {
	slash := strings.Index(raw, "/")
	if slash < 0 {
		return raw
	}
	if strings.Contains(raw[slash+1:], ":") {
		return raw[:slash]
	}
	return raw
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
	var mismatch *domain.ETagMismatchError
	if errors.As(err, &mismatch) {
		return NewConflictError("etag precondition failed", map[string]any{
			"expectedEtag": mismatch.Expected, "currentEtag": mismatch.Actual,
		})
	}
	return NewInternalError(err)
}
