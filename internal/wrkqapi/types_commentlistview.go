package wrkqapi

// CommentListViewParams mirrors the legacy `wrkq comment ls` surface. The server
// owns cursor.Apply, limit+1, sort/direction validation, and next-cursor
// construction. Legacy accepts MULTIPLE task arguments: it applies the same
// cursor predicate + limit+1 to EACH task's query, accumulates the rows in task
// order, then truncates the combined set at limit and builds the next cursor
// from the last surviving row. `Tasks` carries the multi-task list; `Task` is the
// single-task back-compat field (used when `Tasks` is empty).
type CommentListViewParams struct {
	Task           string   `json:"task,omitempty"`
	Tasks          []string `json:"tasks,omitempty"`
	Limit          int      `json:"limit,omitempty"`
	Cursor         string   `json:"cursor,omitempty"`
	IncludeDeleted bool     `json:"includeDeleted,omitempty"`
	Sort           string   `json:"sort,omitempty"` // default created_at
	Desc           bool     `json:"desc,omitempty"`
}

// WrkqCommentListView is the server-owned COMPATIBILITY list projection for
// `wrkq comment ls`. Items reuse the comment compat row shape; next_cursor is
// the legacy cursor token (empty when there is no further page). Not canonical.
type WrkqCommentListView struct {
	Items      []WrkqCommentCatView `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}
