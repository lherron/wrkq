//go:build wrkq_local

package wrkqapi

import (
	"context"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// Room verbs: read a room, its log and members; hide it; join or leave it.

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
	room := roomDTO(state)
	return &room, nil
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
		dto := roomDTO(state)
		// Discovery, and ONLY discovery: the default listing drops what has gone
		// stale or been hidden. Neither omission touches say, delivery, or
		// obligations — `--all` is the whole ledger, one flag away.
		if !p.All && (dto.Activity == string(domain.RoomActivityStale) ||
			domain.RoomHasLabel(state.row.Labels, domain.RoomLabelHidden)) {
			continue
		}
		result.Items = append(result.Items, dto)
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
	if state.derived {
		// No row means no envelopes. Return the room itself so the caller learns
		// the room exists-in-principle and is simply empty, rather than an error.
		return &WrkqRoomLogView{Room: roomDTO(state), Items: []WrkqEnvelope{}}, nil
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
	rows, err := a.store.Rooms.ListEnvelopes(ctx, params)
	if err != nil {
		return nil, mapRoomStoreError(err, p.Room)
	}
	if params.NewestFirst {
		for left, right := 0, len(rows)-1; left < right; left, right = left+1, right-1 {
			rows[left], rows[right] = rows[right], rows[left]
		}
	}
	view := &WrkqRoomLogView{Room: roomDTO(state), Items: make([]WrkqEnvelope, 0, len(rows))}
	for index := range rows {
		envelope, eerr := a.envelopeDTO(ctx, &rows[index], state)
		if eerr != nil {
			return nil, eerr
		}
		view.Items = append(view.Items, *envelope)
	}
	return view, nil
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
	// Labelling is state: the row has to exist to carry it.
	if current, err = a.materializeRoom(ctx, attr, current); err != nil {
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
	room := roomDTO(refreshed)
	return &room, nil
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
	if !join && state.derived {
		// Leaving a room with no row: the postcondition already holds and there
		// is nothing to record. Do NOT materialize — that would open a room as a
		// side effect of declining to be in it.
		return a.RoomMembersView(ctx, RoomMembersViewParams{Room: p.Room, PrincipalRef: p.PrincipalRef})
	}
	// Joining is state, so the row has to exist first.
	if state, err = a.materializeRoom(ctx, attr, state); err != nil {
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
	if state.derived {
		return &WrkqRoomMembersView{Room: roomDTO(state), Items: []WrkqRoomMember{}}, nil
	}
	members, err := a.store.Rooms.ListMembers(state.row.UUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	attendance, err := a.store.Rooms.LatestAttendance(state.row.UUID)
	if err != nil {
		return nil, NewInternalError(err)
	}
	view := &WrkqRoomMembersView{Room: roomDTO(state), Items: make([]WrkqRoomMember, 0, len(members))}
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
