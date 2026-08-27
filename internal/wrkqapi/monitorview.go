//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/selectors"
)

// monitorMaxPageLimit is the server-enforced hard cap on rows returned per
// eventsView / tailView page. It bounds the polling read so a hammering client
// can never pull an unbounded slice in one frame; the client carries the
// high-water cursor across polls and the server enforces the cap regardless of the
// requested limit. (Risk mitigation per T-05115: server-enforced max page size.)
const monitorMaxPageLimit = 1000

// MonitorEventsView reproduces the legacy pollMonitorEvents server-side: it scans
// event_log ASCENDING from `cursor`, hydrates resource_id + comment→task backfill,
// applies isEventIncluded / isStateChangeEvent / event-type filtering, and returns
// the matched rows plus the high-water cursor of the LAST raw row scanned. Selector
// resolution is server-owned: a bad selector returns WRKQ_VALIDATION (the legacy
// exit-code-2 path) before any rows are scanned.
func (a *API) MonitorEventsView(ctx context.Context, p MonitorEventsViewParams) (*WrkqMonitorEventsView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if p.LastN > 0 {
		var cur sql.NullInt64
		if err := a.db.QueryRowContext(ctx,
			`SELECT COALESCE(MIN(id), 0) - 1 FROM (SELECT id FROM event_log ORDER BY id DESC LIMIT ?)`,
			p.LastN,
		).Scan(&cur); err != nil {
			return nil, NewInternalError(fmt.Errorf("resolve last-%d cursor: %w", p.LastN, err))
		}
		start := int64(0)
		if cur.Valid && cur.Int64 > 0 {
			start = cur.Int64
		}
		return &WrkqMonitorEventsView{Items: []WrkqMonitorEvent{}, HighWater: start}, nil
	}

	selected, err := a.resolveMonitorSelectors(p.Tasks)
	if err != nil {
		return nil, err
	}

	filter := monitorEventFilter{
		taskUUIDs:       selected.taskUUIDs,
		taskFriendlyIDs: selected.taskFriendlyIDs,
		roomUUIDs:       selected.roomUUIDs,
		envelopeUUIDs:   selected.envelopeUUIDs,
		stateOnly:       p.StateOnly,
		eventTypes:      p.EventTypes,
		namedSelectors:  selected.sawAnySelector,
	}

	limit := p.Limit
	if limit <= 0 || limit > monitorMaxPageLimit {
		limit = monitorMaxPageLimit
	}

	rows, err := a.db.QueryContext(ctx, monitorEventScanQuery, p.Cursor, limit)
	if err != nil {
		return nil, NewInternalError(fmt.Errorf("query monitor events: %w", err))
	}
	defer func() { _ = rows.Close() }()

	view := &WrkqMonitorEventsView{Items: []WrkqMonitorEvent{}, HighWater: p.Cursor}
	for rows.Next() {
		event, commentTaskUUID, commentTaskID, collab, err := scanMonitorRow(rows)
		if err != nil {
			return nil, NewInternalError(err)
		}
		view.HighWater = event.ID

		// The comment→task hydration IDENTIFIES the event's task; it must never
		// be folded into the caller's selector set. Widening the filter with the
		// row's own task made every comment in the log match every task selector
		// (T-07620), so the refs ride alongside the filter instead.
		if event.ResourceType == "comment" {
			collab.commentTaskUUID = commentTaskUUID
			collab.commentTaskID = commentTaskID
			if monitorPayloadTaskID(event.Payload) == "" && commentTaskUUID != "" {
				payloadBytes, _ := json.Marshal(map[string]string{"task_id": commentTaskUUID})
				payloadString := string(payloadBytes)
				event.Payload = &payloadString
			}
		}

		if !isMonitorEventIncluded(event, filter, collab) {
			continue
		}

		view.Items = append(view.Items, WrkqMonitorEvent(event))
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(fmt.Errorf("iterate monitor events: %w", err))
	}
	return view, nil
}

// MonitorStateView evaluates the --until condition ONCE against current task state
// and returns whether it is met plus the still-unmet task friendly IDs. It never
// sleeps/times out/emits terminal lines — the client owns the loop. Selector and
// condition validation surface WRKQ_VALIDATION (the legacy exit-code-2 path); a
// watched task that no longer exists surfaces WRKQ_INTERNAL (legacy exit-code-3
// "one or more watched tasks no longer exist").
func (a *API) MonitorStateView(ctx context.Context, p MonitorStateViewParams) (*WrkqMonitorStateView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	condition, err := parseMonitorCondition(p.Condition)
	if err != nil {
		return nil, NewValidationError(err.Error(), nil)
	}

	selected, err := a.resolveMonitorSelectors(p.Tasks)
	if err != nil {
		return nil, err
	}

	// A condition and its selectors must agree about what is being watched:
	// state=/all-terminal evaluate task lifecycle, acked/terminal evaluate
	// envelope dispositions. Mixing them would make `unmet` meaningless.
	if condition.isEnvelopeCondition() {
		if len(selected.envelopeUUIDs) == 0 {
			return nil, NewValidationError("monitor --until "+p.Condition+" requires at least one envelope selector (EN-xxxxx)", nil)
		}
		if selected.sawTaskSelector {
			return nil, NewValidationError("monitor --until "+p.Condition+" does not accept task selectors", nil)
		}
		met, unmet, evalErr := a.evaluateEnvelopeCondition(ctx, condition, selected.envelopeUUIDs)
		if evalErr != nil {
			return nil, NewInternalError(evalErr)
		}
		if unmet == nil {
			unmet = []string{}
		}
		return &WrkqMonitorStateView{Met: met, Unmet: unmet}, nil
	}
	if selected.sawEnvelopeSelector {
		return nil, NewValidationError("monitor --until "+p.Condition+" does not accept envelope selectors; use --until acked or --until terminal", nil)
	}
	if len(selected.taskUUIDs) == 0 {
		return nil, NewValidationError("monitor --until requires at least one task selector", nil)
	}

	met, unmet, evalErr := a.evaluateMonitorCondition(ctx, condition, selected.taskUUIDs, selected.taskFriendlyIDs)
	if evalErr != nil {

		return nil, NewInternalError(evalErr)
	}
	if unmet == nil {
		unmet = []string{}
	}
	return &WrkqMonitorStateView{Met: met, Unmet: unmet}, nil
}

// HistoryTailView reproduces legacy watchEvents' per-poll query server-side: a raw
// ASCENDING event_log page from `cursor` with actor slug/id + resource_id
// hydration, returning the rows plus the high-water cursor of the last row scanned.
func (a *API) HistoryTailView(ctx context.Context, p HistoryTailViewParams) (*WrkqHistoryTailView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	limit := p.Limit
	if limit <= 0 || limit > monitorMaxPageLimit {
		limit = monitorMaxPageLimit
	}

	rows, err := a.db.QueryContext(ctx, watchTailScanQuery, p.Cursor, limit)
	if err != nil {
		return nil, NewInternalError(fmt.Errorf("query failed: %w", err))
	}
	defer func() { _ = rows.Close() }()

	view := &WrkqHistoryTailView{Items: []WrkqWatchEvent{}, HighWater: p.Cursor}
	for rows.Next() {
		var e WrkqWatchEvent
		var resourceID sql.NullString
		if err := rows.Scan(
			&e.ID,
			&e.Timestamp,
			&e.PrincipalRef,
			&e.ScopeRef,
			&e.ResourceType,
			&e.ResourceUUID,
			&e.EventType,
			&e.ETag,
			&e.Payload,
			&resourceID,
		); err != nil {
			return nil, NewInternalError(fmt.Errorf("scan failed: %w", err))
		}
		if resourceID.Valid {
			e.ResourceID = &resourceID.String
		}
		view.HighWater = e.ID
		view.Items = append(view.Items, e)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(fmt.Errorf("rows error: %w", err))
	}
	return view, nil
}

// isMonitorEventIncluded is the server port of legacy isEventIncluded: task.*
// matched by resource_uuid ∈ taskUUIDs; comment.* matched by payload.task_id ∈
// taskUUIDs ∪ taskFriendlyIDs; stateOnly applies isMonitorStateChangeEvent; an
// explicit eventTypes filter restricts to those event_type values.
//
// "No selectors" means NO selector of any kind (T-07620): a caller who named a
// room or an envelope has narrowed the feed, so task.*/comment.* must not fall
// through to the unfiltered "emit everything" branch on the strength of the task
// lists being empty.
func isMonitorEventIncluded(event monitorRow, filter monitorEventFilter, collab monitorCollabRefs) bool {
	if filter.stateOnly && !isMonitorStateChangeEvent(event) {
		return false
	}
	if len(filter.eventTypes) > 0 && !containsMonitorString(filter.eventTypes, event.EventType) {
		return false
	}

	// The collaboration ledger rides the SAME event stream as task.*/container.*,
	// so a task room's key being the task id means one selector shows both the
	// task's state changes and its conversation. --state-only keeps excluding
	// them: isMonitorStateChangeEvent is task-only by construction.
	if event.ResourceType == "room" {
		if !filter.hasSelectors() {
			return true
		}
		return event.ResourceUUID != nil && containsMonitorString(filter.roomUUIDs, *event.ResourceUUID)
	}
	if event.ResourceType == "envelope" {
		if !filter.hasSelectors() {
			return true
		}
		if event.ResourceUUID != nil && containsMonitorString(filter.envelopeUUIDs, *event.ResourceUUID) {
			return true
		}
		if collab.roomUUID != "" && containsMonitorString(filter.roomUUIDs, collab.roomUUID) {
			return true
		}
		// An envelope routed via a task is tagged with it even when strict
		// campaign coalesce landed it in the campaign room, so watching the task
		// still shows the traffic that came through it.
		return collab.taskUUID != "" && containsMonitorString(filter.taskUUIDs, collab.taskUUID)
	}

	if event.ResourceType == "task" && strings.HasPrefix(event.EventType, "task.") {
		if !filter.hasSelectors() {
			return true
		}
		return event.ResourceUUID != nil && containsMonitorString(filter.taskUUIDs, *event.ResourceUUID)
	}

	if event.ResourceType == "comment" && strings.HasPrefix(event.EventType, "comment.") {
		if !filter.hasSelectors() {
			return true
		}
		// The comment's task is the payload's task_id; a payload that lost it
		// falls back to the hydrated comments-table refs. Both are matched
		// AGAINST the selected tasks — a comment on an unselected task (or on a
		// container, which carries no task_id at all) never matches.
		if taskID := monitorPayloadTaskID(event.Payload); taskID != "" &&
			(containsMonitorString(filter.taskUUIDs, taskID) || containsMonitorString(filter.taskFriendlyIDs, taskID)) {
			return true
		}
		return containsMonitorString(filter.taskUUIDs, collab.commentTaskUUID) ||
			containsMonitorString(filter.taskFriendlyIDs, collab.commentTaskID)
	}

	return false
}

// isMonitorStateChangeEvent is the server port of legacy isStateChangeEvent: a
// lifecycle state change is task.archived/deleted/restored, or task.updated whose
// payload carries the "state" key. Title/body/priority/comment-only events excluded.
func isMonitorStateChangeEvent(event monitorRow) bool {
	if event.ResourceType != "task" {
		return false
	}
	switch event.EventType {
	case "task.archived", "task.deleted", "task.restored":
		return true
	case "task.updated":
		if event.Payload == nil {
			return false
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*event.Payload), &fields); err != nil {
			return false
		}
		_, ok := fields["state"]
		return ok
	default:
		return false
	}
}

func parseMonitorCondition(raw string) (monitorCondition, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return monitorCondition{}, fmt.Errorf("--until condition is required")
	case raw == "all-terminal":
		return monitorCondition{kind: "all-terminal"}, nil
	// Envelope conditions. terminal = acked|dead: `--wait` blocks until every
	// envelope in the group is terminal, and dead is terminal — a dead-lettered
	// obligation must release the waiter, not hang it.
	case raw == "acked":
		return monitorCondition{kind: "envelope-acked"}, nil
	case raw == "terminal":
		return monitorCondition{kind: "envelope-terminal"}, nil
	case strings.HasPrefix(raw, "state="):
		stateList := strings.TrimPrefix(raw, "state=")
		if stateList == "" {
			return monitorCondition{}, fmt.Errorf("invalid --until condition %q: state list must include at least one task state", raw)
		}
		states := strings.Split(stateList, ",")
		allowed := make(map[string]bool, len(states))
		for _, state := range states {
			if state == "" {
				return monitorCondition{}, fmt.Errorf("invalid --until condition %q: state list contains an empty entry", raw)
			}
			if _, err := domain.ParseState(state); err != nil {
				return monitorCondition{}, fmt.Errorf("invalid --until condition %q: unknown task state %q", raw, state)
			}
			allowed[state] = true
		}
		return monitorCondition{kind: "state", states: allowed}, nil
	default:
		return monitorCondition{}, fmt.Errorf("invalid --until condition %q: expected state=<s>[,<s>...], all-terminal, acked, or terminal", raw)
	}
}

// evaluateEnvelopeCondition evaluates acked/terminal over the selected
// envelopes. An envelope that no longer exists is an error, matching the task
// path's "one or more watched resources no longer exist".
func (a *API) evaluateEnvelopeCondition(ctx context.Context, c monitorCondition, envelopeUUIDs []string) (bool, []string, error) {
	query := "SELECT id, state FROM envelopes WHERE uuid IN (" +
		monitorQuestionMarks(len(envelopeUUIDs)) + ") ORDER BY id"
	rows, err := a.db.QueryContext(ctx, query, monitorStringsToInterfaces(envelopeUUIDs)...)
	if err != nil {
		return false, nil, fmt.Errorf("query envelope state: %w", err)
	}
	defer func() { _ = rows.Close() }()

	unmet := []string{}
	seen := 0
	for rows.Next() {
		var friendlyID, state string
		if err := rows.Scan(&friendlyID, &state); err != nil {
			return false, nil, fmt.Errorf("scan envelope state: %w", err)
		}
		seen++
		satisfied := false
		switch c.kind {
		case "envelope-acked":
			satisfied = state == string(domain.EnvelopeStateAcked)
		case "envelope-terminal":
			satisfied = domain.IsEnvelopeTerminal(domain.EnvelopeState(state))
		}
		if !satisfied {
			unmet = append(unmet, friendlyID)
		}
	}
	if err := rows.Err(); err != nil {
		return false, nil, fmt.Errorf("iterate envelope state: %w", err)
	}
	if seen != len(envelopeUUIDs) {
		return false, unmet, fmt.Errorf("one or more watched envelopes no longer exist")
	}
	return len(unmet) == 0, unmet, nil
}

func (c monitorCondition) satisfiedBy(state string) bool {
	switch c.kind {
	case "all-terminal":
		switch state {
		case "completed", "cancelled", "archived", "deleted":
			return true
		default:
			return false
		}
	case "state":
		return c.states[state]
	default:
		return false
	}
}

// evaluateMonitorCondition is the server port of legacy monitorCondition.evaluate:
// it queries current state for the watched UUIDs and returns met + the unmet task
// friendly IDs. A watched task that no longer exists is an error (legacy
// exit-code-3 path).
func (a *API) evaluateMonitorCondition(ctx context.Context, c monitorCondition, taskUUIDs, friendlyIDs []string) (bool, []string, error) {
	query := "SELECT id, state FROM tasks WHERE uuid IN (" + monitorQuestionMarks(len(taskUUIDs)) + ") ORDER BY id"
	rows, err := a.db.QueryContext(ctx, query, monitorStringsToInterfaces(taskUUIDs)...)
	if err != nil {
		return false, nil, fmt.Errorf("query task state: %w", err)
	}
	defer func() { _ = rows.Close() }()

	unmet := []string{}
	seen := 0
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return false, nil, fmt.Errorf("scan task state: %w", err)
		}
		seen++
		if !c.satisfiedBy(state) {
			unmet = append(unmet, id)
		}
	}
	if err := rows.Err(); err != nil {
		return false, nil, fmt.Errorf("iterate task state: %w", err)
	}
	if seen != len(taskUUIDs) {
		return false, unmet, fmt.Errorf("one or more watched tasks no longer exist")
	}
	return len(unmet) == 0, unmet, nil
}

// resolveMonitorSelectors resolves already-caller-scoped task selector strings to
// (uuids, friendlyIDs). A bad selector returns WRKQ_VALIDATION (the legacy
// exit-code-2 path). Empty/whitespace selectors are skipped.
func (a *API) resolveMonitorSelectors(in []string) (monitorSelectorSet, error) {
	set := monitorSelectorSet{}
	for _, selector := range in {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			continue
		}
		// Any non-empty selector narrows the feed, even one that resolves to a
		// room which does not exist yet. Recording it here (rather than
		// inferring "unfiltered" from empty uuid lists downstream) is what stops
		// an armed-but-not-yet-open room from degrading into "emit everything".
		set.sawAnySelector = true

		if kind, _, perr := id.Parse(selector); perr == nil {
			switch kind {
			case id.TypeContainer:
				// §3.4: following a room is arming `monitor watch <room-key>`,
				// and a campaign/project room's KEY is its container path or
				// P-xxxxx. Every campaign task coalesces into the campaign room
				// (§4 rule 2), so this is the feed a supervisor actually arms.
				roomUUID, err := a.roomUUIDForContainerSelector(selector)
				if err != nil {
					return monitorSelectorSet{}, err
				}
				set.roomUUIDs = appendMonitorUnique(set.roomUUIDs, roomUUID)
				continue
			case id.TypeRoom:
				var roomUUID string
				if err := a.db.QueryRow("SELECT uuid FROM rooms WHERE id = ? OR uuid = ?", selector, selector).Scan(&roomUUID); err != nil {
					return monitorSelectorSet{}, NewValidationError(fmt.Sprintf("invalid room selector %q", selector), nil)
				}
				set.roomUUIDs = appendMonitorUnique(set.roomUUIDs, roomUUID)
				continue
			case id.TypeEnvelope:
				// An EN- selector covers the envelope AND, when that id is a
				// group head, every envelope the same say fanned out to. A
				// sibling's id is nobody's group_id, so it selects only itself.
				uuids, ids, err := a.resolveEnvelopeSelector(selector)
				if err != nil {
					return monitorSelectorSet{}, err
				}
				for index := range uuids {
					set.envelopeUUIDs = appendMonitorUnique(set.envelopeUUIDs, uuids[index])
					set.envelopeFriendlyIDs = appendMonitorUnique(set.envelopeFriendlyIDs, ids[index])
				}
				set.sawEnvelopeSelector = true
				continue
			}
		}

		uuid, friendlyID, rerr := selectors.ResolveTask(a.db, selector)
		if rerr != nil {
			// A path selector is a task path OR a room key: `wrkq/rooms` names
			// the campaign room, not a task. Tasks keep precedence, so this only
			// runs once the task lookup has already missed.
			if _, _, cerr := selectors.ResolveContainer(a.db, selector); cerr == nil {
				roomUUID, err := a.roomUUIDForContainerSelector(selector)
				if err != nil {
					return monitorSelectorSet{}, err
				}
				set.roomUUIDs = appendMonitorUnique(set.roomUUIDs, roomUUID)
				continue
			}
			return monitorSelectorSet{}, NewValidationError(fmt.Sprintf("invalid task selector %q: %s", selector, rerr.Error()), nil)
		}
		set.sawTaskSelector = true
		set.taskUUIDs = appendMonitorUnique(set.taskUUIDs, uuid)
		if friendlyID != "" {
			set.taskFriendlyIDs = appendMonitorUnique(set.taskFriendlyIDs, friendlyID)
		}
		// The task's own room rides the same selector: §3.4's "state changes and
		// the conversation on one selector".
		var roomUUID string
		if err := a.db.QueryRow("SELECT uuid FROM rooms WHERE task_uuid = ?", uuid).Scan(&roomUUID); err == nil {
			set.roomUUIDs = appendMonitorUnique(set.roomUUIDs, roomUUID)
		}
	}
	return set, nil
}

// roomUUIDForContainerSelector resolves a container path or P-xxxxx to that
// container's room uuid. The kind gate is routeToContainerUUID's (§4 rule 3):
// campaign-adorned → campaign room, project-kind → project room, anything else
// is the same typed room_kind_unsupported refusal `wrkc say` gives, so watching
// and saying agree about what has a room.
//
// It returns "" — not an error — when the container qualifies but its room has
// not been opened yet: a supervisor arms the feed BEFORE the first say, and
// selectors are re-resolved on every poll, so the room starts matching the
// moment it exists. set.sawAnySelector keeps that empty result from reading as
// an unfiltered feed.
func (a *API) roomUUIDForContainerSelector(selector string) (string, error) {
	containerUUID, _, err := selectors.ResolveContainer(a.db, selector)
	if err != nil {
		return "", NewValidationError(fmt.Sprintf("invalid container selector %q: %s", selector, err.Error()), nil)
	}
	var kind string
	var campaignState sql.NullString
	if err := a.db.QueryRow(
		"SELECT kind, campaign_state FROM containers WHERE uuid = ?", containerUUID,
	).Scan(&kind, &campaignState); err != nil {
		if err == sql.ErrNoRows {
			return "", NewValidationError(fmt.Sprintf("invalid container selector %q: container not found", selector), nil)
		}
		return "", NewInternalError(err)
	}
	isCampaign := campaignState.Valid && campaignState.String != ""
	if !isCampaign && kind != string(domain.ContainerKindProject) {
		return "", NewValidationError(
			"room_kind_unsupported: only campaign-adorned and project containers have rooms",
			map[string]any{
				"reason": "room_kind_unsupported", "container": selector,
				"kind": kind, "expected": "campaign-adorned container or project",
			})
	}
	var roomUUID string
	if err := a.db.QueryRow(
		"SELECT uuid FROM rooms WHERE container_uuid = ?", containerUUID,
	).Scan(&roomUUID); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", NewInternalError(err)
	}
	return roomUUID, nil
}

func (a *API) resolveEnvelopeSelector(selector string) ([]string, []string, error) {
	rows, err := a.db.Query(`SELECT uuid, id FROM envelopes
		 WHERE id = ? OR uuid = ? OR group_id = ? ORDER BY id`, selector, selector, selector)
	if err != nil {
		return nil, nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	uuids := []string{}
	ids := []string{}
	for rows.Next() {
		var uuid, friendlyID string
		if err := rows.Scan(&uuid, &friendlyID); err != nil {
			return nil, nil, NewInternalError(err)
		}
		uuids = append(uuids, uuid)
		ids = append(ids, friendlyID)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, NewInternalError(err)
	}
	if len(uuids) == 0 {
		return nil, nil, NewValidationError(fmt.Sprintf("invalid envelope selector %q", selector), nil)
	}
	return uuids, ids, nil
}

func appendMonitorUnique(values []string, value string) []string {
	if value == "" || containsMonitorString(values, value) {
		return values
	}
	return append(values, value)
}

// monitorEventScanQuery is the legacy pollMonitorEvents query with a server cursor
// (`e.id > ?`), ASC ordering, and a hard LIMIT for bounded pages. It hydrates
// resource_id and the comment→task backfill columns.
const monitorEventScanQuery = `
	SELECT e.id, e.timestamp, e.resource_type, e.resource_uuid, e.event_type, e.payload,
	       CASE e.resource_type
	           WHEN 'task' THEN (SELECT id FROM tasks WHERE uuid = e.resource_uuid)
	           WHEN 'container' THEN (SELECT id FROM containers WHERE uuid = e.resource_uuid)
	           WHEN 'comment' THEN (SELECT id FROM comments WHERE uuid = e.resource_uuid)
	           -- A derived room has no friendly id: its KEY is its work identity,
	           -- so hydrate that instead of leaving the column blank.
	           WHEN 'room' THEN (SELECT COALESCE(r.id, t.id, cp.path, c.slug)
	                               FROM rooms r
	                               LEFT JOIN tasks t ON t.uuid = r.task_uuid
	                               LEFT JOIN containers c ON c.uuid = r.container_uuid
	                               LEFT JOIN v_container_paths cp ON cp.uuid = r.container_uuid
	                              WHERE r.uuid = e.resource_uuid)
	           WHEN 'envelope' THEN (SELECT id FROM envelopes WHERE uuid = e.resource_uuid)
	           ELSE NULL
	       END as resource_id,
	       CASE e.resource_type
	           WHEN 'comment' THEN (SELECT task_uuid FROM comments WHERE uuid = e.resource_uuid)
	           ELSE NULL
	       END as comment_task_uuid,
	       CASE e.resource_type
	           WHEN 'comment' THEN (SELECT t.id FROM comments c JOIN tasks t ON t.uuid = c.task_uuid WHERE c.uuid = e.resource_uuid)
	           ELSE NULL
	       END as comment_task_id,
	       CASE e.resource_type
	           WHEN 'envelope' THEN (SELECT room_uuid FROM envelopes WHERE uuid = e.resource_uuid)
	           ELSE NULL
	       END as envelope_room_uuid,
	       CASE e.resource_type
	           WHEN 'envelope' THEN (SELECT task_uuid FROM envelopes WHERE uuid = e.resource_uuid)
	           ELSE NULL
	       END as envelope_task_uuid
	FROM event_log e
	WHERE e.id > ?
	ORDER BY e.id ASC
	LIMIT ?
`

// watchTailScanQuery is the legacy watchEvents query with a server cursor and a
// hard LIMIT for bounded pages. It hydrates actor slug/id + resource_id.
const watchTailScanQuery = `
	SELECT e.id, e.timestamp, e.principal_ref, e.scope_ref,
	       e.resource_type, e.resource_uuid, e.event_type, e.etag, e.payload,
	       CASE e.resource_type
	           WHEN 'task' THEN (SELECT id FROM tasks WHERE uuid = e.resource_uuid)
	           WHEN 'container' THEN (SELECT id FROM containers WHERE uuid = e.resource_uuid)
	           ELSE NULL
	       END as resource_id
	FROM event_log e
	WHERE e.id > ?
	ORDER BY e.id ASC
	LIMIT ?
`

func scanMonitorRow(rows *sql.Rows) (monitorRow, string, string, monitorCollabRefs, error) {
	var event monitorRow
	var resourceID, commentTaskUUID, commentTaskID sql.NullString
	var envelopeRoomUUID, envelopeTaskUUID sql.NullString
	if err := rows.Scan(
		&event.ID,
		&event.Timestamp,
		&event.ResourceType,
		&event.ResourceUUID,
		&event.EventType,
		&event.Payload,
		&resourceID,
		&commentTaskUUID,
		&commentTaskID,
		&envelopeRoomUUID,
		&envelopeTaskUUID,
	); err != nil {
		return monitorRow{}, "", "", monitorCollabRefs{}, fmt.Errorf("scan monitor event: %w", err)
	}
	if resourceID.Valid {
		event.ResourceID = &resourceID.String
	}
	collab := monitorCollabRefs{
		roomUUID: valueOrEmptyString(envelopeRoomUUID),
		taskUUID: valueOrEmptyString(envelopeTaskUUID),
	}
	return event, valueOrEmptyString(commentTaskUUID), valueOrEmptyString(commentTaskID), collab, nil
}

func monitorPayloadTaskID(payload *string) string {
	if payload == nil || *payload == "" {
		return ""
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(*payload), &body); err != nil {
		return ""
	}
	value, ok := body["task_id"]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func containsMonitorString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func monitorQuestionMarks(count int) string {
	if count <= 0 {
		return "NULL"
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func monitorStringsToInterfaces(values []string) []interface{} {
	out := make([]interface{}, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func valueOrEmptyString(v sql.NullString) string {
	if v.Valid {
		return v.String
	}
	return ""
}
