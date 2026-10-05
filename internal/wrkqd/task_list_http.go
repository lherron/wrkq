package wrkqd

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/taskmember"
)

type findOptions struct {
	paths                []string
	typeFilter           string
	slugGlob             string
	state                string
	dueBefore            string
	dueAfter             string
	kind                 string
	assigneePrincipalRef string
	parentTaskUUID       string
	requestedByProjectID string
	assignedProjectID    string
	causedByTaskUUID     string
	ackPending           bool
	limit                int
	cursor               string
	sortField            string
	sortDescending       bool
}

type findResult struct {
	OpenSubtaskCount     int      `json:"open_subtask_count"`
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
}

func findTasks(database *db.DB, opts findOptions, skipPagination bool) ([]findResult, bool, error) {
	var pag *cursor.ApplyResult
	var err error
	if !skipPagination {
		pag, err = cursor.Apply(opts.cursor, cursor.ApplyOptions{
			SortFields: []string{opts.sortField}, SQLFields: []string{findTaskSortSQL(opts.sortField)},
			Descending: []bool{opts.sortDescending}, IDField: "t.id", Limit: opts.limit,
		})
		if err != nil {
			return nil, false, err
		}
	}

	query := `
		SELECT t.uuid, t.id, t.slug, t.title, t.specification, t.state, t.priority, t.kind,
		       t.assignee_principal_ref, t.parent_task_uuid, t.requested_by_project_id,
		       t.assigned_project_id, t.acknowledged_at, t.resolution, t.due_at, t.etag,
		       cp.path || '/' || t.slug, t.created_at, t.updated_at, (SELECT COUNT(*) FROM tasks st WHERE st.subtask_owner_uuid=t.uuid AND st.state NOT IN ('completed','cancelled','archived','deleted'))
		FROM tasks t JOIN v_container_paths cp ON cp.uuid = t.project_uuid WHERE ` + taskmember.Filter("t", false)
	args := []interface{}{}
	switch opts.state {
	case "all":
	case "":
		query += " AND t.state NOT IN ('archived', 'deleted', 'idea')"
	default:
		query += " AND t.state = ?"
		args = append(args, opts.state)
	}
	if opts.kind != "" {
		query += " AND t.kind = ?"
		args = append(args, opts.kind)
	}
	if opts.assigneePrincipalRef != "" {
		query += " AND t.assignee_principal_ref = ?"
		args = append(args, opts.assigneePrincipalRef)
	}
	if opts.parentTaskUUID != "" {
		query += " AND t.parent_task_uuid = ?"
		args = append(args, opts.parentTaskUUID)
	}
	if opts.requestedByProjectID != "" {
		query += " AND t.requested_by_project_id = ?"
		args = append(args, opts.requestedByProjectID)
	}
	if opts.assignedProjectID != "" {
		query += " AND t.assigned_project_id = ?"
		args = append(args, opts.assignedProjectID)
	}
	if opts.causedByTaskUUID != "" {
		query += " AND EXISTS (SELECT 1 FROM task_causes tc WHERE tc.task_uuid = t.uuid AND tc.caused_by_task_uuid = ?)"
		args = append(args, opts.causedByTaskUUID)
	}
	if opts.ackPending {
		query += " AND t.acknowledged_at IS NULL AND t.state IN ('completed', 'cancelled')"
	}
	if opts.dueBefore != "" {
		value, err := time.Parse("2006-01-02", opts.dueBefore)
		if err != nil {
			return nil, false, fmt.Errorf("invalid due-before date: %w", err)
		}
		query += " AND t.due_at IS NOT NULL AND t.due_at < ?"
		args = append(args, value.Format(time.RFC3339))
	}
	if opts.dueAfter != "" {
		value, err := time.Parse("2006-01-02", opts.dueAfter)
		if err != nil {
			return nil, false, fmt.Errorf("invalid due-after date: %w", err)
		}
		query += " AND t.due_at IS NOT NULL AND t.due_at > ?"
		args = append(args, value.Format(time.RFC3339))
	}
	if opts.slugGlob != "" {
		query += " AND t.slug GLOB ?"
		args = append(args, paths.GlobToSQLPattern(opts.slugGlob))
	}
	if len(opts.paths) > 0 {
		conditions := make([]string, 0, len(opts.paths))
		for _, path := range opts.paths {
			if strings.Contains(path, "*") {
				conditions = append(conditions, "(cp.path || '/' || t.slug) GLOB ?")
				args = append(args, paths.GlobToSQLPattern(path))
			} else {
				conditions = append(conditions, "((cp.path || '/' || t.slug) = ? OR (cp.path || '/' || t.slug) LIKE ? || '/%')")
				args = append(args, path, path)
			}
		}
		query += " AND (" + strings.Join(conditions, " OR ") + ")"
	}
	if pag != nil && pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		args = append(args, pag.Params...)
	}
	if pag != nil {
		query += " " + pag.OrderByClause
	} else {
		query += " ORDER BY " + findTaskSortSQL(opts.sortField)
		if opts.sortDescending {
			query += " DESC"
		} else {
			query += " ASC"
		}
		if opts.sortField != "id" {
			if opts.sortDescending {
				query += ", t.id DESC"
			} else {
				query += ", t.id ASC"
			}
		}
	}
	if pag != nil && pag.LimitClause != "" {
		query += " " + pag.LimitClause
		args = append(args, *pag.LimitParam)
	}

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, false, fmt.Errorf("query failed: %w", err)
	}
	defer func() { _ = rows.Close() }()
	results := []findResult{}
	for rows.Next() {
		var result findResult
		var state, kind, assignee, parentTaskUUID, dueAt sql.NullString
		var requestedBy, assignedProject, acknowledgedAt, resolution sql.NullString
		var priority sql.NullInt64
		if err := rows.Scan(&result.UUID, &result.ID, &result.Slug, &result.Title, &result.Specification, &state, &priority, &kind,
			&assignee, &parentTaskUUID, &requestedBy, &assignedProject, &acknowledgedAt, &resolution,
			&dueAt, &result.ETag, &result.Path, &result.CreatedAt, &result.UpdatedAt, &result.OpenSubtaskCount); err != nil {
			return nil, false, fmt.Errorf("scan failed: %w", err)
		}
		result.Type = "task"
		if state.Valid {
			result.State = &state.String
		}
		if priority.Valid {
			value := int(priority.Int64)
			result.Priority = &value
		}
		if kind.Valid {
			result.Kind = &kind.String
		}
		if assignee.Valid {
			result.AssigneePrincipalRef = &assignee.String
			display := attribution.PrincipalHandle(assignee.String)
			result.Assignee = &display
		}
		if parentTaskUUID.Valid {
			var parentID string
			if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", parentTaskUUID.String).Scan(&parentID); err == nil {
				result.ParentTaskID = &parentID
			}
		}
		if requestedBy.Valid {
			result.RequestedByProjectID = &requestedBy.String
		}
		if assignedProject.Valid {
			result.AssignedProjectID = &assignedProject.String
		}
		if acknowledgedAt.Valid {
			result.AcknowledgedAt = &acknowledgedAt.String
		}
		if resolution.Valid {
			result.Resolution = &resolution.String
		}
		if dueAt.Valid {
			result.DueAt = &dueAt.String
		}
		results = append(results, result)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	for i := range results {
		ids, err := store.CausedByIDs(database, results[i].UUID)
		if err != nil {
			return nil, false, err
		}
		if len(ids) > 0 {
			results[i].CausedBy = ids
		}
	}
	hasMore := false
	if !skipPagination && opts.limit > 0 && len(results) > opts.limit {
		hasMore = true
		results = results[:opts.limit]
	}
	return results, hasMore, nil
}

func findTaskSortSQL(field string) string {
	switch field {
	case "id":
		return "t.id"
	case "created_at":
		return "t.created_at"
	case "path":
		return "cp.path || '/' || t.slug"
	default:
		return "t.updated_at"
	}
}

type tasksListRequest struct {
	Project    string   `json:"project,omitempty"`
	Filter     string   `json:"filter,omitempty"`
	Sort       string   `json:"sort,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	Cursor     string   `json:"cursor,omitempty"`
	PathPrefix []string `json:"path_prefix,omitempty"`
	Assignee   string   `json:"assignee,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	ParentTask string   `json:"parent_task,omitempty"`
	DueBefore  string   `json:"due_before,omitempty"`
	DueAfter   string   `json:"due_after,omitempty"`
	SlugGlob   string   `json:"slug_glob,omitempty"`
}

func (s *daemonServer) handleTasksList(w http.ResponseWriter, r *http.Request) {
	var req tasksListRequest
	if !s.decodePost(w, r, &req) {
		return
	}

	var pathsFilter []string

	if req.Project != "" {
		projectUUID, _, err := selectors.ResolveContainer(s.db, req.Project)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		var projectPath string
		if err := s.db.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", projectUUID).Scan(&projectPath); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		pathsFilter = append(pathsFilter, projectPath)
	}

	for _, prefix := range req.PathPrefix {
		trimmed := strings.Trim(prefix, "/")
		if trimmed != "" {
			pathsFilter = append(pathsFilter, trimmed)
		}
	}

	var assigneePrincipalRef string
	if req.Assignee != "" {
		principalRef, err := attribution.NormalizeCompat(req.Assignee)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		assigneePrincipalRef = principalRef
	}

	var parentTaskUUID string
	if req.ParentTask != "" {
		uuid, _, err := selectors.ResolveTask(s.db, req.ParentTask)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		parentTaskUUID = uuid
	}

	stateFilter := ""
	switch req.Filter {
	case "all":
		stateFilter = "all"
	case "deleted":
		stateFilter = "deleted"
	case "active", "":
		stateFilter = ""
	default:
		stateFilter = req.Filter
	}

	opts := findOptions{
		paths:                pathsFilter,
		typeFilter:           "t",
		slugGlob:             req.SlugGlob,
		state:                stateFilter,
		dueBefore:            req.DueBefore,
		dueAfter:             req.DueAfter,
		kind:                 req.Kind,
		assigneePrincipalRef: assigneePrincipalRef,
		parentTaskUUID:       parentTaskUUID,
		limit:                req.Limit,
		cursor:               req.Cursor,
	}

	results, hasMore, err := findTasks(s.db, opts, false)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	var nextCursor string
	if hasMore && len(results) > 0 {
		lastEntry := results[len(results)-1]
		nextCursor, _ = cursor.BuildNextCursor(
			[]string{"updated_at"},
			[]interface{}{lastEntry.UpdatedAt},
			lastEntry.ID,
		)
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"tasks":       results,
		"next_cursor": nextCursor,
	})
}
