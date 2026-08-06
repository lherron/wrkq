package wrkqapi

// LsListViewParams mirrors the legacy `wrkq ls <path...>` surface. Path is the
// single-path form (back-compat); Paths is the multi-path form. When both are
// empty the view lists the top-level (root) containers. The server owns the
// per-path query, the in-memory merge-sort across the COMBINED set, and the
// limit+1 / next-cursor truncation over that combined set — exactly as legacy
// runLs does — so the CLI mirror never re-sorts or re-paginates.
type LsListViewParams struct {
	Path                   string   `json:"path,omitempty"`
	Paths                  []string `json:"paths,omitempty"`
	Sort                   string   `json:"sort,omitempty"`
	Reverse                bool     `json:"reverse,omitempty"`
	Limit                  int      `json:"limit,omitempty"`
	Cursor                 string   `json:"cursor,omitempty"`
	Type                   string   `json:"type,omitempty"` // "p" or "t"
	IncludeHidden          bool     `json:"includeHidden,omitempty"`
	IncludeCampaignMembers bool     `json:"includeCampaignMembers,omitempty"`
}

// WrkqLsEntry matches the legacy lsEntry shape exactly (field order + json tags).
type WrkqLsEntry struct {
	Type                 string  `json:"type"`
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	Title                string  `json:"title,omitempty"`
	Path                 string  `json:"path"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	State                string  `json:"state,omitempty"`
	Kind                 string  `json:"kind,omitempty"`
	TaskCount            *int    `json:"task_count,omitempty"`
	ActiveTaskCount      *int    `json:"active_task_count,omitempty"`
	RequestedByProjectID *string `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`
	Resolution           *string `json:"resolution,omitempty"`
}

// WrkqLsListView is the server-owned COMPATIBILITY list projection for `wrkq ls`.
// It owns mixed task/container listing, rollup counts, in-memory merge-sort, and
// cursor pagination over the merged set. Not a canonical resource.
type WrkqLsListView struct {
	Items      []WrkqLsEntry `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}
