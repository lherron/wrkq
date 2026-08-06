//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
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

	uuids, friendlyIDs, err := a.resolveMonitorSelectors(p.Tasks)
	if err != nil {
		return nil, err
	}

	filter := monitorEventFilter{
		taskUUIDs:       uuids,
		taskFriendlyIDs: friendlyIDs,
		stateOnly:       p.StateOnly,
		eventTypes:      p.EventTypes,
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
		event, commentTaskUUID, commentTaskID, err := scanMonitorRow(rows)
		if err != nil {
			return nil, NewInternalError(err)
		}
		view.HighWater = event.ID

		normalized := filter
		if event.ResourceType == "comment" {
			if commentTaskUUID != "" && !containsMonitorString(normalized.taskUUIDs, commentTaskUUID) {
				normalized.taskUUIDs = append(append([]string{}, normalized.taskUUIDs...), commentTaskUUID)
			}
			if commentTaskID != "" && !containsMonitorString(normalized.taskFriendlyIDs, commentTaskID) {
				normalized.taskFriendlyIDs = append(append([]string{}, normalized.taskFriendlyIDs...), commentTaskID)
			}
			if monitorPayloadTaskID(event.Payload) == "" && commentTaskUUID != "" {
				payloadBytes, _ := json.Marshal(map[string]string{"task_id": commentTaskUUID})
				payloadString := string(payloadBytes)
				event.Payload = &payloadString
			}
		}

		if !isMonitorEventIncluded(event, normalized) {
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

	uuids, friendlyIDs, err := a.resolveMonitorSelectors(p.Tasks)
	if err != nil {
		return nil, err
	}
	if len(uuids) == 0 {
		return nil, NewValidationError("monitor --until requires at least one task selector", nil)
	}

	met, unmet, evalErr := a.evaluateMonitorCondition(ctx, condition, uuids, friendlyIDs)
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
func isMonitorEventIncluded(event monitorRow, filter monitorEventFilter) bool {
	if filter.stateOnly && !isMonitorStateChangeEvent(event) {
		return false
	}
	if len(filter.eventTypes) > 0 && !containsMonitorString(filter.eventTypes, event.EventType) {
		return false
	}

	if event.ResourceType == "task" && strings.HasPrefix(event.EventType, "task.") {
		if len(filter.taskUUIDs) == 0 && len(filter.taskFriendlyIDs) == 0 {
			return true
		}
		return event.ResourceUUID != nil && containsMonitorString(filter.taskUUIDs, *event.ResourceUUID)
	}

	if event.ResourceType == "comment" && strings.HasPrefix(event.EventType, "comment.") {
		if len(filter.taskUUIDs) == 0 && len(filter.taskFriendlyIDs) == 0 {
			return true
		}
		taskID := monitorPayloadTaskID(event.Payload)
		return containsMonitorString(filter.taskUUIDs, taskID) || containsMonitorString(filter.taskFriendlyIDs, taskID)
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
		return monitorCondition{}, fmt.Errorf("invalid --until condition %q: expected state=<s>[,<s>...] or all-terminal", raw)
	}
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
func (a *API) resolveMonitorSelectors(in []string) (uuids []string, friendlyIDs []string, err error) {
	for _, selector := range in {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			continue
		}
		uuid, friendlyID, rerr := selectors.ResolveTask(a.db, selector)
		if rerr != nil {
			return nil, nil, NewValidationError(fmt.Sprintf("invalid task selector %q: %s", selector, rerr.Error()), nil)
		}
		if !containsMonitorString(uuids, uuid) {
			uuids = append(uuids, uuid)
		}
		if friendlyID != "" && !containsMonitorString(friendlyIDs, friendlyID) {
			friendlyIDs = append(friendlyIDs, friendlyID)
		}
	}
	return uuids, friendlyIDs, nil
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
	           ELSE NULL
	       END as resource_id,
	       CASE e.resource_type
	           WHEN 'comment' THEN (SELECT task_uuid FROM comments WHERE uuid = e.resource_uuid)
	           ELSE NULL
	       END as comment_task_uuid,
	       CASE e.resource_type
	           WHEN 'comment' THEN (SELECT t.id FROM comments c JOIN tasks t ON t.uuid = c.task_uuid WHERE c.uuid = e.resource_uuid)
	           ELSE NULL
	       END as comment_task_id
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

func scanMonitorRow(rows *sql.Rows) (monitorRow, string, string, error) {
	var event monitorRow
	var resourceID, commentTaskUUID, commentTaskID sql.NullString
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
	); err != nil {
		return monitorRow{}, "", "", fmt.Errorf("scan monitor event: %w", err)
	}
	if resourceID.Valid {
		event.ResourceID = &resourceID.String
	}
	return event, valueOrEmptyString(commentTaskUUID), valueOrEmptyString(commentTaskID), nil
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
