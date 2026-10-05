//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// The T-07612 §4 routing table: which room one say lands in.

// routedSay is the outcome of the §4 routing table for one say.
type routedSay struct {
	room *domain.Room
	// taskTagUUID tags the envelope with the task it was routed via, even when
	// strict campaign coalesce landed the envelope in the campaign room.
	taskTagUUID *string
	// impliedTo is the addressee a target-handle ref implies when the caller
	// named no --to.
	impliedTo string
	// notice is advisory text the routing itself wants surfaced (never an
	// error): a pair room opened by a scope-less caller.
	notice string
}

// routeSay implements T-07612 §4 exactly, first match wins.
func (a *API) routeSay(ctx context.Context, attr attribution.Attribution, senderScope string, p RoomSayParams) (*routedSay, error) {
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
		return a.routeToHandle(ctx, attr, senderScope, ref, p)
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
	anchorUUID, err := a.taskRoomAnchor(ctx, taskUUID)
	if err != nil {
		return nil, err
	}
	campaignUUID, err := a.effectiveCampaignForTask(ctx, anchorUUID)
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
	room, err := a.ensureTaskRoom(attr, anchorUUID)
	if err != nil {
		return nil, err
	}
	return &routedSay{room: room, taskTagUUID: &taskUUID}, nil
}

// taskRoomAnchor preserves the exact message subject while sharing the owner's room.
func (a *API) taskRoomAnchor(ctx context.Context, taskUUID string) (string, error) {
	var anchor string
	err := a.db.QueryRowContext(ctx, `SELECT COALESCE(subtask_owner_uuid, uuid) FROM tasks WHERE uuid = ?`, taskUUID).Scan(&anchor)
	if err == sql.ErrNoRows {
		return "", NewNotFoundError(taskUUID, "task")
	}
	if err != nil {
		return "", NewInternalError(err)
	}
	return anchor, nil
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
		  FROM tasks subject
		  JOIN tasks t ON t.uuid = COALESCE(subject.subtask_owner_uuid, subject.uuid)
		  LEFT JOIN containers resident ON resident.uuid = t.project_uuid
		  LEFT JOIN containers enrolled ON enrolled.uuid = t.campaign_uuid
		 WHERE subject.uuid = ?`, taskUUID).
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

// containerRoomKind is the §4 rule 3 entitlement gate: campaign-adorned ->
// campaign room; project-kind -> project room; any other container kind is a
// typed refusal. Say routing and room reads BOTH call this, so they can never
// disagree about which containers have rooms.
func (a *API) containerRoomKind(ctx context.Context, selector, containerUUID string) (domain.RoomKind, error) {
	var kind string
	var campaignState sql.NullString
	if err := a.db.QueryRowContext(ctx,
		"SELECT kind, campaign_state FROM containers WHERE uuid = ?", containerUUID).Scan(&kind, &campaignState); err != nil {
		if err == sql.ErrNoRows {
			return "", NewNotFoundError(selector, "container")
		}
		return "", NewInternalError(err)
	}
	switch {
	case campaignState.Valid && campaignState.String != "":
		return domain.RoomKindCampaign, nil
	case kind == string(domain.ContainerKindProject):
		return domain.RoomKindProject, nil
	}
	return "", NewValidationError(
		"room_kind_unsupported: only campaign-adorned and project containers have rooms",
		map[string]any{
			"reason": "room_kind_unsupported", "container": selector,
			"kind": kind, "expected": "campaign-adorned container or project",
		})
}

// routeToContainerUUID is §4 rule 3: the container's entitled room, opened on
// first say.
func (a *API) routeToContainerUUID(ctx context.Context, attr attribution.Attribution, selector, containerUUID string) (*routedSay, error) {
	roomKind, kerr := a.containerRoomKind(ctx, selector, containerUUID)
	if kerr != nil {
		return nil, kerr
	}
	room, err := a.ensureContainerRoom(attr, containerUUID, roomKind)
	if err != nil {
		return nil, err
	}
	return &routedSay{room: room}, nil
}

// routeToHandle is §4 rule 4: the room is derived from the work context of the
// two parties, and the TARGET wins.
func (a *API) routeToHandle(ctx context.Context, attr attribution.Attribution, senderScope, ref string, p RoomSayParams) (*routedSay, error) {
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

	// Neither task-scoped → an ad-hoc pair room. The sender's member ref is its
	// seat when it has one; a scope-less caller (a human, or a seat that forgot
	// HRC_SESSION_REF) is admitted as the principal it is — the same rule
	// resolveAddressees applies on the reply side — with an advisory notice. A
	// say is never refused for who the caller is (Lance ruling 2026-08-29);
	// the old hard refusal here broke every human→:primary ingress (Discord
	// #hcs → vesta@hcs:primary, 2026-08-30).
	senderMember := senderScope
	senderScoped := true
	routingNotice := ""
	if senderScope == "" {
		senderMember = attr.PrincipalRef
		senderScoped = false
		routingNotice = "no caller scope: pair room keyed on principal " + attr.PrincipalRef +
			"; a seat should set HRC_SESSION_REF so its pair rooms follow the seat, not the principal"
	}
	if !p.New {
		// §4: reuse the exact-pair room whose activity reads `active`, else open a
		// new one. Reuse keys on the SAME projection `wrkc show` prints; there is
		// no lifecycle left to key on.
		existing, ferr := a.store.Rooms.FindAdhocPairRoom(
			senderMember, targetHandle, roomActiveSince(time.Now().UTC()))
		if ferr != nil {
			return nil, NewInternalError(ferr)
		}
		if existing != nil {
			return &routedSay{room: existing, impliedTo: targetHandle, notice: routingNotice}, nil
		}
	}
	targetPrincipal, perr := attribution.NormalizeCompat(target.AgentID)
	if perr != nil {
		return nil, NewValidationError("invalid agent handle: "+perr.Error(), map[string]any{"field": "ref"})
	}
	room, cerr := a.store.Rooms.CreateWithAttribution(attr, store.RoomCreateParams{
		Kind: domain.RoomKindAdhoc,
		Members: []store.RoomMemberSeed{
			{MemberRef: senderMember, MemberPrincipalRef: attr.PrincipalRef, Scoped: senderScoped, Source: domain.RoomMemberSourceSpoke},
			{MemberRef: targetHandle, MemberPrincipalRef: targetPrincipal, Scoped: true, Source: domain.RoomMemberSourceAddressed},
		},
	})
	if cerr != nil {
		return nil, mapRoomStoreError(cerr, ref)
	}
	return &routedSay{room: room, impliedTo: targetHandle, notice: routingNotice}, nil
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
