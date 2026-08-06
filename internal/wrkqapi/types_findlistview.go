package wrkqapi

// FindListViewParams mirrors the legacy `wrkq find [PATH...]` surface. The CLI
// scopes the raw paths/parent selector through the project-root scoper BEFORE
// these params are sent; the server NEVER reads project-root env/flags. Assignee
// normalization and parent-task resolution are durable read behavior and so are
// owned here on the server side.
type FindListViewParams struct {
	Paths                []string `json:"paths,omitempty"`
	Type                 string   `json:"type,omitempty"` // "t" (task) or "p" (project/container)
	SlugGlob             string   `json:"slugGlob,omitempty"`
	State                string   `json:"state,omitempty"`
	DueBefore            string   `json:"dueBefore,omitempty"`
	DueAfter             string   `json:"dueAfter,omitempty"`
	Kind                 string   `json:"kind,omitempty"`
	Labels               []string `json:"labels,omitempty"`
	Assignee             string   `json:"assignee,omitempty"`
	ClaimedBy            string   `json:"claimedBy,omitempty"`
	ClaimedNode          string   `json:"claimedNode,omitempty"`
	ParentTask           string   `json:"parentTask,omitempty"`
	RequestedByProjectID string   `json:"requestedBy,omitempty"`
	AssignedProjectID    string   `json:"assignedProject,omitempty"`
	CausedBy             string   `json:"causedBy,omitempty"`
	AckPending           bool     `json:"ackPending,omitempty"`
	HasOutcome           bool     `json:"hasOutcome,omitempty"`
	Campaign             string   `json:"campaign,omitempty"`
	Limit                int      `json:"limit,omitempty"`
	Cursor               string   `json:"cursor,omitempty"`
	Sort                 string   `json:"sort,omitempty"`
	Reverse              bool     `json:"reverse,omitempty"`
}

// WrkqFindEntry matches the legacy findResult shape exactly (field order + json
// tags). Marshaled by encoding/json — NOT alphabetical (legacy uses a struct, not
// a map), so field order here is the wire order.
type WrkqFindEntry struct {
	Type                 string   `json:"type"`
	UUID                 string   `json:"uuid"`
	ID                   string   `json:"id"`
	Slug                 string   `json:"slug"`
	Title                string   `json:"title"`
	Path                 string   `json:"path"`
	Specification        string   `json:"specification,omitempty"`
	State                *string  `json:"state,omitempty"`
	Priority             *int     `json:"priority,omitempty"`
	Kind                 *string  `json:"kind,omitempty"`
	Assignee             *string  `json:"assignee,omitempty"`
	AssigneePrincipalRef *string  `json:"assignee_principal_ref,omitempty"`
	ClaimedBy            *string  `json:"claimed_by,omitempty"`
	ClaimedScope         *string  `json:"claimed_scope,omitempty"`
	ClaimedNode          *string  `json:"claimed_node,omitempty"`
	ClaimedAt            *string  `json:"claimed_at,omitempty"`
	ClaimGeneration      int64    `json:"claim_generation,omitempty"`
	ParentTaskID         *string  `json:"parent_task_id,omitempty"`
	RequestedByProjectID *string  `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string  `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string  `json:"acknowledged_at,omitempty"`
	Resolution           *string  `json:"resolution,omitempty"`
	DueAt                *string  `json:"due_at,omitempty"`
	CausedBy             []string `json:"caused_by,omitempty"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
	ETag                 int64    `json:"etag"`
	membership           string
}

// WrkqFindListView is the server-owned COMPATIBILITY list projection for
// `wrkq find`. It owns recursive/filtered task+container search, cursor.Apply +
// limit+1 + sort-validation + BuildNextCursor over the filtered/recursive set,
// and the legacy mixed-type in-memory merge-sort. Not a canonical resource.
type WrkqFindListView struct {
	Items      []WrkqFindEntry `json:"items"`
	NextCursor string          `json:"next_cursor,omitempty"`
}

type findQueryOptions struct {
	paths                []string
	typeFilter           string
	slugGlob             string
	state                string
	dueBefore            string
	dueAfter             string
	kind                 string
	labels               []string
	assigneePrincipalRef string
	claimedBy            string
	claimedNode          string
	parentTaskUUID       string
	requestedByProjectID string
	assignedProjectID    string
	causedByTaskUUID     string
	ackPending           bool
	hasOutcome           bool
	campaignUUID         string
	limit                int
	cursor               string
	sortField            string
	sortDescending       bool
}
