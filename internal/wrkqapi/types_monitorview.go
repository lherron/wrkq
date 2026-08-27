package wrkqapi

// WrkqMonitorEvent is one matched monitor event row. It is the server projection
// of the legacy monitorEventLine (the per-event NDJSON record `wrkq monitor watch`
// emits). The `type` discriminator is owned CLIENT-side (the mirror re-stamps
// "wrkq.monitor.event" on render) so the wire DTO carries only the data fields.
// Field order is the legacy monitorEventLine struct order MINUS the type field:
// id, timestamp, resource_type, resource_uuid?, resource_id?, event_type, payload?.
type WrkqMonitorEvent struct {
	ID           int64   `json:"id"`
	Timestamp    string  `json:"timestamp"`
	ResourceType string  `json:"resource_type"`
	ResourceUUID *string `json:"resource_uuid,omitempty"`
	ResourceID   *string `json:"resource_id,omitempty"`
	EventType    string  `json:"event_type"`
	Payload      *string `json:"payload,omitempty"`
}

// WrkqMonitorEventsView is the bounded ASCENDING event_log page for
// `wrkq monitor watch`. The server advances over `e.id > since_cursor` (ASC) up to
// `limit`, applies the server-owned event filter (task/comment selector match,
// comment→task backfill, state-only, explicit event-type filter), and returns the
// matched rows plus the high-water cursor of the LAST raw row scanned (NOT the last
// matched row) so the client never re-scans an event it already advanced past.
// next_cursor is the same value as a string token for symmetry with the other
// list views; high_water is the int form the client carries between polls.
type WrkqMonitorEventsView struct {
	Items     []WrkqMonitorEvent `json:"items"`
	HighWater int64              `json:"high_water"`
}

// WrkqMonitorStateView is the SINGLE authoritative `--until` condition snapshot.
// It does NOT sleep, time out, stall, or emit terminal lines — it evaluates the
// condition exactly once against current task state and returns whether it is met
// plus the still-unmet task friendly IDs. The CLIENT owns the poll loop and the
// timeout/stall clocks; it re-calls stateView each cycle.
type WrkqMonitorStateView struct {
	Met   bool     `json:"met"`
	Unmet []string `json:"unmet"`
}

// WrkqWatchEvent matches the legacy internal/cli watchEvent shape EXACTLY (field
// order + json tags + pointer/omitempty). It is DISTINCT from WrkqLogEvent: it
// INCLUDES resource_id, uses a STRING timestamp (raw event_log.timestamp, not a
// parsed time.Time), and resource_uuid is a nullable pointer (omitempty), NOT a
// required string. Field order is the legacy watchEvent struct order (legacy
// actor_uuid/actor_slug/actor_id attribution fields removed — principal-only):
// id, timestamp, principal_ref?, scope_ref?,
// resource_type, resource_uuid?, resource_id?, event_type, etag?, payload?.
type WrkqWatchEvent struct {
	ID           int64   `json:"id"`
	Timestamp    string  `json:"timestamp"`
	PrincipalRef *string `json:"principal_ref,omitempty"`
	ScopeRef     *string `json:"scope_ref,omitempty"`
	ResourceType string  `json:"resource_type"`
	ResourceUUID *string `json:"resource_uuid,omitempty"`
	ResourceID   *string `json:"resource_id,omitempty"`
	EventType    string  `json:"event_type"`
	ETag         *int64  `json:"etag,omitempty"`
	Payload      *string `json:"payload,omitempty"`
}

// WrkqHistoryTailView is the bounded ASCENDING raw event_log read model for
// `wrkq watch` / `wrkq monitor watch --raw`. It is a SIBLING of HistoryListView in
// the `history` namespace (generic audit-log tailing) but uses the legacy
// watchEvent row shape (NOT WrkqLogEvent). The server advances over
// `e.id > since_cursor` (ASC) up to `limit`, hydrates actor slug/id + resource_id,
// and returns the rows plus the high-water cursor of the last row scanned. The CLI
// repeats it for --follow, prints the deprecation warning caller-side, and renders
// human/NDJSON locally.
type WrkqHistoryTailView struct {
	Items     []WrkqWatchEvent `json:"items"`
	HighWater int64            `json:"high_water"`
}

// MonitorEventsViewParams carries the already-CALLER-scoped task selectors plus the
// monotonic cursor. The server resolves each selector to (uuid, friendlyID) every
// call (cheap; a bad selector hard-gates with WRKQ_VALIDATION before any scan).
// `tasks` empty = match ALL applicable task/comment events (legacy unfiltered feed).
type MonitorEventsViewParams struct {
	Tasks      []string `json:"tasks,omitempty"`
	StateOnly  bool     `json:"stateOnly,omitempty"`
	EventTypes []string `json:"eventTypes,omitempty"`
	Cursor     int64    `json:"cursor"`
	Limit      int      `json:"limit,omitempty"`
	// LastN, when > 0, makes eventsView a START-CURSOR RESOLUTION call for the
	// `--last N` replay: it returns HighWater = the legacy start cursor
	// (COALESCE(MIN(id),0)-1 over the last N EXISTING event_log rows by id) with an
	// empty page. The client then streams forward from that cursor applying its
	// selectors/filters, exactly as legacy does. This resolves the cursor from
	// actual row identity (gap-independent), not high_water-N arithmetic.
	LastN int64 `json:"lastN,omitempty"`
}

// MonitorStateViewParams carries the watched task selectors and the --until
// condition for the single authoritative snapshot.
type MonitorStateViewParams struct {
	Tasks     []string `json:"tasks"`
	Condition string   `json:"condition"`
}

// HistoryTailViewParams carries the monotonic cursor + bounded limit for the raw
// ASCENDING event_log tail.
type HistoryTailViewParams struct {
	Cursor int64 `json:"cursor"`
	Limit  int   `json:"limit,omitempty"`
}

// monitorRow is the server-side decoded event used by the filter. It mirrors the
// fields of the legacy watchEvent the filter inspects.
type monitorRow struct {
	ID           int64
	Timestamp    string
	ResourceType string
	ResourceUUID *string
	ResourceID   *string
	EventType    string
	Payload      *string
}

// monitorCollabRefs carries the room/task an envelope event belongs to so a
// room or task selector matches the conversation without the client parsing
// payloads.
type monitorCollabRefs struct {
	roomUUID string
	taskUUID string
}

type monitorEventFilter struct {
	taskUUIDs       []string
	taskFriendlyIDs []string
	roomUUIDs       []string
	envelopeUUIDs   []string
	stateOnly       bool
	eventTypes      []string
}

// monitorSelectorSet is the resolved selector inventory for one monitor call.
// Task and collaboration selectors coexist deliberately: a task room's key IS
// the task id, so `wrkq monitor watch T-07613` shows the task's state changes
// and its conversation on ONE selector.
type monitorSelectorSet struct {
	taskUUIDs           []string
	taskFriendlyIDs     []string
	roomUUIDs           []string
	envelopeUUIDs       []string
	envelopeFriendlyIDs []string
	// sawEnvelopeSelector records that the caller named an EN- selector, which
	// is what makes an envelope --until condition legal.
	sawEnvelopeSelector bool
	sawTaskSelector     bool
}

type monitorCondition struct {
	kind   string
	states map[string]bool
}

// isEnvelopeCondition reports whether a condition evaluates envelope
// dispositions rather than task lifecycle states.
func (c monitorCondition) isEnvelopeCondition() bool {
	return c.kind == "envelope-acked" || c.kind == "envelope-terminal"
}
