package wrkqapi

import "time"

// HistoryListViewParams mirrors the legacy `wrkq log <PATHSPEC|ID>` surface. The
// CLI scopes the raw target through the project-root scoper BEFORE these params
// are sent; the server NEVER reads project-root env/flags. Resource resolution
// (friendly ID / UUID → resource_type+resource_uuid among task/container/actor)
// is durable read behavior and so is owned here on the server side.
type HistoryListViewParams struct {
	Target string `json:"target"`
	Since  string `json:"since,omitempty"`
	Until  string `json:"until,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Cursor string `json:"cursor,omitempty"`
}

// WrkqLogEvent matches the legacy logEvent shape EXACTLY (field order + json tags
// + pointer/omitempty). Marshaled by encoding/json — NOT alphabetical (legacy
// uses a struct, not a map), so the field order here is the wire order. payload
// stays a STRING (the raw event_log.payload); --patch is rendered CLIENT-side.
type WrkqLogEvent struct {
	ID           int64     `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	PrincipalRef *string   `json:"principal_ref,omitempty"`
	ScopeRef     *string   `json:"scope_ref,omitempty"`
	ResourceType string    `json:"resource_type"`
	ResourceUUID string    `json:"resource_uuid"`
	EventType    string    `json:"event_type"`
	ETag         *int64    `json:"etag,omitempty"`
	Payload      *string   `json:"payload,omitempty"`
}

// WrkqHistoryListView is the server-owned CLI COMPATIBILITY history read model for
// `wrkq log`. It reads the generic event_log table (NOT workflow_events, which is
// the wrkf.event.query substrate), resolves the caller-scoped target to exactly
// one (resource_type, resource_uuid), and owns cursor.Apply + limit+1 +
// BuildNextCursor over event_log.id DESC. Not a canonical resource/event-stream
// API. Field order is legacy struct order, NOT alphabetical.
type WrkqHistoryListView struct {
	Items      []WrkqLogEvent `json:"items"`
	NextCursor string         `json:"next_cursor,omitempty"`
}
