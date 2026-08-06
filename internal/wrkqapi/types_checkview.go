package wrkqapi

// TaskBlockedViewParams selects the task whose incomplete blocking dependencies
// should be enumerated. The selector is already project-root-scoped by the CLI
// caller; the view never reads project-root env/flags.
type TaskBlockedViewParams struct {
	Task string `json:"task"`
}

// WrkqTaskBlockedView is the server-owned COMPATIBILITY read model for
// `wrkq check blocked`. Its shape exactly reproduces the legacy BlockedResult
// JSON object (task_id, task_uuid, is_blocked, blockers[]) so the RPC CLI can
// render byte-identical output and exit-code semantics. Not a canonical resource.
type WrkqTaskBlockedView struct {
	TaskID    string                 `json:"task_id"`
	TaskUUID  string                 `json:"task_uuid"`
	IsBlocked bool                   `json:"is_blocked"`
	Blockers  []WrkqTaskBlockedEntry `json:"blockers"`
}

// WrkqTaskBlockedEntry matches the legacy BlockerEntry shape exactly (field order
// + json tags).
type WrkqTaskBlockedEntry struct {
	ID    string `json:"id"`
	UUID  string `json:"uuid"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	State string `json:"state"`
}

// InboxViewParams carries the already-scoped inbox container path and, when a
// project root is configured, the project ID used to surface ack-pending tasks
// requested by that project. Both values are computed by the CLI caller from the
// neutral project-root transform; the view never reads project-root env/flags.
type InboxViewParams struct {
	InboxPath string `json:"inboxPath"`
	ProjectID string `json:"projectId,omitempty"`
}

// WrkqInboxEntry matches the legacy inboxTask shape exactly (field order + json
// tags). It is the server-owned COMPATIBILITY row for `wrkq check-inbox`, not a
// canonical resource.
type WrkqInboxEntry struct {
	Type                 string  `json:"type"`
	UUID                 string  `json:"uuid"`
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	Title                string  `json:"title"`
	Path                 string  `json:"path"`
	State                *string `json:"state,omitempty"`
	Priority             *int    `json:"priority,omitempty"`
	Kind                 *string `json:"kind,omitempty"`
	DueAt                *string `json:"due_at,omitempty"`
	RequestedByProjectID *string `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`
	Resolution           *string `json:"resolution,omitempty"`
	ETag                 int64   `json:"etag"`
}

// WrkqInboxView is the server-owned COMPATIBILITY list projection for
// `wrkq check-inbox`: open tasks under the inbox container, plus (when a project
// is configured) ack-pending tasks requested by that project. The legacy command
// initialises an empty slice (renders `[]`, never null) and appends both groups
// in the same order; this view reproduces that exactly. Not a canonical resource.
type WrkqInboxView struct {
	Items []WrkqInboxEntry `json:"items"`
}
