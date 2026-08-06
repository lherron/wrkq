package wrkqapi

// ProjectsListViewParams mirrors the legacy `wrkq projects` list query. Project
// root scoping is intentionally ignored by this command.
type ProjectsListViewParams struct {
	IncludeArchived bool   `json:"includeArchived,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Cursor          string `json:"cursor,omitempty"`
}

// WrkqProjectEntry extends the projects compatibility row with the nullable
// checkout root registry field.
type WrkqProjectEntry struct {
	Type  string `json:"type"`
	ID    string `json:"id"`
	Slug  string `json:"slug"`
	Title string `json:"title,omitempty"`
	Path  string `json:"path"`
	// Root is the stored host-portable checkout root. It is intentionally not
	// expanded here; consumers expand ~/... for their own host.
	Root *string `json:"root"`
}

// WrkqProjectsListView is the server-owned compatibility projection for
// `wrkq projects`.
type WrkqProjectsListView struct {
	Items      []WrkqProjectEntry `json:"items"`
	NextCursor string             `json:"next_cursor,omitempty"`
}
