package wrkqd

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/store"
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
		       cp.path || '/' || t.slug, t.created_at, t.updated_at
		FROM tasks t JOIN v_container_paths cp ON cp.uuid = t.project_uuid WHERE 1=1`
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
			&dueAt, &result.ETag, &result.Path, &result.CreatedAt, &result.UpdatedAt); err != nil {
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
