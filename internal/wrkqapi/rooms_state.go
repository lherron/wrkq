//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/selectors"
)

// Room state: resolve a selector to a room (deriving one the ledger has not
// opened) and project its read-time work and activity.

// roomState carries a room row plus everything the DTO and the say gate need,
// resolved once per call.
type roomState struct {
	row     *domain.Room
	key     string
	workRef *WrkqRoomWorkRef
	// derived marks a room the ledger has not materialized: the work IS entitled
	// to a room under the §4 routing table, but nobody has said into it, so no
	// row exists. row.UUID is empty and every count reads zero. Reads render it
	// as the empty room it is; a write verb that ADDS state materializes it
	// first. A read never materializes — see roomOrDerivedForTask.
	derived bool
	// work and activity are the two read-time projections. Neither is stored and
	// neither gates: they are computed here once per call and only ever read.
	work         domain.RoomWork
	activity     domain.RoomActivity
	lastActivity string
	// workState and workTerminalAt name the terminal transition the stale notice
	// quotes back ("task completed 2026-08-27").
	workState        string
	workTerminalAt   string
	openSubtaskCount int
	memberCount      int
	messageCount     int
	lastMessageAt    string
	links            []WrkqRoomLink
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
			return a.roomOrDerivedForTask(ctx, taskUUID)
		case id.TypeContainer:
			return a.roomStateForContainerSelector(ctx, selector)
		}
	}
	if room, err := a.store.Rooms.Get(selector); err == nil {
		return a.hydrateRoomState(ctx, room)
	}
	// A path selector names work. Resolve it the way say does and derive the
	// room it is entitled to, rather than reporting the room missing.
	if containerUUID, _, err := selectors.ResolveContainer(a.db, selector); err == nil {
		return a.roomOrDerivedForContainer(ctx, selector, containerUUID)
	}
	if taskUUID, _, err := selectors.ResolveTask(a.db, selector); err == nil {
		return a.roomOrDerivedForTask(ctx, taskUUID)
	}
	// Nothing resolved: not a room id, not a container, not a task. An R-xxxxx
	// that does not exist lands here and STAYS not-found — an ad-hoc room's
	// identity is its member set, so a selector naming no work derives nothing.
	return nil, NewNotFoundError(selector, "room")
}

// roomOrDerivedForTask resolves the room a task talks in, deriving an empty one
// when the ledger holds no row yet.
//
// It reuses effectiveCampaignForTask — the SAME decision routeToTaskUUID makes —
// and that reuse is load-bearing, not tidiness. A campaign-resident task's say
// coalesces into the campaign room. Deriving a task room here instead would show
// an empty room for a conversation that actually lives in the campaign room, and
// materializing that room later would split the campaign's conversation in two.
// The comment on effectiveCampaignForTask records that exact bug, fixed once
// already on the say side.
func (a *API) roomOrDerivedForTask(ctx context.Context, taskUUID string) (*roomState, error) {
	anchorUUID, err := a.taskRoomAnchor(ctx, taskUUID)
	if err != nil {
		return nil, err
	}
	taskUUID = anchorUUID
	// An EXISTING task room wins, and the order matters. A task room that
	// predates enrolment keeps its own history readable at its own selector
	// while new says route to the campaign room — linked, never merged. Asking
	// about the coalesce first would silently answer a read about the task room
	// with the campaign's traffic.
	room, rerr := a.store.Rooms.GetByTask(taskUUID)
	if rerr != nil {
		return nil, NewInternalError(rerr)
	}
	if room != nil {
		return a.hydrateRoomState(ctx, room)
	}
	// No task room. Follow the coalesce so the derived room is the one a say
	// would actually land in.
	campaignUUID, err := a.effectiveCampaignForTask(ctx, taskUUID)
	if err != nil {
		return nil, err
	}
	if campaignUUID != "" {
		return a.roomOrDerivedForContainer(ctx, campaignUUID, campaignUUID)
	}
	return a.hydrateDerivedRoomState(ctx, &domain.Room{
		Kind: domain.RoomKindTask, TaskUUID: &taskUUID,
	})
}

// roomOrDerivedForContainer resolves a container's room, deriving an empty one
// when the container is entitled to a room nobody has opened. A container that
// is NOT entitled keeps the typed room_kind_unsupported refusal say returns:
// that is a true answer about the object, not a missing room.
func (a *API) roomOrDerivedForContainer(ctx context.Context, selector, containerUUID string) (*roomState, error) {
	roomKind, err := a.containerRoomKind(ctx, selector, containerUUID)
	if err != nil {
		return nil, err
	}
	room, rerr := a.store.Rooms.GetByContainer(containerUUID)
	if rerr != nil {
		return nil, NewInternalError(rerr)
	}
	if room != nil {
		return a.hydrateRoomState(ctx, room)
	}
	return a.hydrateDerivedRoomState(ctx, &domain.Room{
		Kind: roomKind, ContainerUUID: &containerUUID,
	})
}

// hydrateDerivedRoomState projects a room that has no row. Every count query in
// hydrateRoomState keys on row.UUID, which is empty here, so counts read zero
// and last-activity reads empty without a special case. The activity projection
// then lands where it should on its own: an empty room on live work reads quiet,
// and on terminal work reads stale.
func (a *API) hydrateDerivedRoomState(ctx context.Context, row *domain.Room) (*roomState, error) {
	state, err := a.hydrateRoomState(ctx, row)
	if err != nil {
		return nil, err
	}
	state.derived = true
	return state, nil
}

// materializeRoom opens the row behind a derived room so a write verb has
// something to write to. Reads never call it.
func (a *API) materializeRoom(ctx context.Context, attr attribution.Attribution, state *roomState) (*roomState, error) {
	if !state.derived {
		return state, nil
	}
	var room *domain.Room
	var err error
	switch {
	case state.row.TaskUUID != nil:
		room, err = a.ensureTaskRoom(attr, *state.row.TaskUUID)
	case state.row.ContainerUUID != nil:
		room, err = a.ensureContainerRoom(attr, *state.row.ContainerUUID, state.row.Kind)
	default:
		return nil, NewInternalError(fmt.Errorf("derived room %q anchors on nothing", state.key))
	}
	if err != nil {
		return nil, err
	}
	return a.hydrateRoomState(ctx, room)
}

func (a *API) roomStateForContainerSelector(ctx context.Context, selector string) (*roomState, error) {
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return nil, NewNotFoundError(selector, "container")
	}
	return a.roomOrDerivedForContainer(ctx, selector, containerUUID)
}

// roomStates memoizes hydrated room state within one read, so a listing whose
// envelopes share rooms loads each room once instead of once per row
// (T-09997). It lives for a single request: room state is read-time truth.
type roomStates map[string]*roomState

func (a *API) roomStateFor(ctx context.Context, rooms roomStates, roomUUID string) (*roomState, error) {
	if state, ok := rooms[roomUUID]; ok {
		return state, nil
	}
	state, err := a.loadRoomState(ctx, roomUUID)
	if err != nil {
		return nil, err
	}
	rooms[roomUUID] = state
	return state, nil
}

func (a *API) loadRoomState(ctx context.Context, roomUUID string) (*roomState, error) {
	room, err := a.store.Rooms.GetContext(ctx, roomUUID)
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
			       COALESCE(t.completed_at, t.archived_at, t.deleted_at, t.updated_at),
 (SELECT COUNT(*) FROM tasks sub WHERE sub.subtask_owner_uuid = t.uuid AND sub.state NOT IN ('completed','cancelled','archived','deleted'))
			  FROM tasks t LEFT JOIN v_container_paths cp ON cp.uuid = t.project_uuid
			 WHERE t.uuid = ?`, *room.TaskUUID).Scan(&ref.ID, &ref.Path, &taskState, &terminalAt, &state.openSubtaskCount)
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
			SELECT c.id, COALESCE(v.path, c.slug), c.campaign_state, c.updated_at,
 (SELECT COUNT(*) FROM tasks sub JOIN tasks owner ON owner.uuid = sub.subtask_owner_uuid
 LEFT JOIN containers resident ON resident.uuid = owner.project_uuid
 WHERE sub.state NOT IN ('completed','cancelled','archived','deleted')
 AND COALESCE(CASE WHEN resident.campaign_state IS NOT NULL THEN owner.project_uuid END, owner.campaign_uuid) = c.uuid)
			  FROM containers c LEFT JOIN v_container_paths v ON v.uuid = c.uuid
			 WHERE c.uuid = ?`, *room.ContainerUUID).
			Scan(&ref.ID, &ref.Path, &campaignState, &containerUpdatedAt, &state.openSubtaskCount)
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
