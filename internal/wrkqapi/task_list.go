//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"strings"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/taskmember"
)

// TaskList returns a paginated list of WrkqTask DTOs with optional filters.
func (a *API) TaskList(ctx context.Context, p TaskListParams) (*WrkqTaskListResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	sortField, sqlField, serr := normalizeTaskListSort(p.Sort)
	if serr != nil {
		return nil, serr
	}
	descending, derr := normalizeTaskListDirection(p.Direction)
	if derr != nil {
		return nil, derr
	}

	where := []string{taskmember.Filter("t", p.Subtasks)}
	args := []any{}
	if p.SubtaskOwner != "" {
		if !p.Subtasks {
			return nil, NewValidationError("subtaskOwner requires subtasks", nil)
		}
		owner, _, err := selectors.ResolveTask(a.db, p.SubtaskOwner)
		if err != nil {
			return nil, NewNotFoundError(p.SubtaskOwner, "task")
		}
		where = append(where, "t.subtask_owner_uuid = ?")
		args = append(args, owner)
	}
	if p.OwnerState != "" {
		clause, err := subtaskOwnerStateClause(p.OwnerState, p.Subtasks)
		if err != nil {
			return nil, err
		}
		where = append(where, clause)
	}

	if strings.TrimSpace(p.Path) != "" {
		containerUUID, _, rerr := selectors.ResolveContainer(a.db, p.Path)
		if rerr != nil {
			return nil, NewNotFoundError(p.Path, "container")
		}
		if p.Recursive {
			// Subtree filter: match the target container path and every path
			// nested beneath it. cp.path is the task's container path from
			// v_container_paths (root slug excluded).
			var containerPath string
			perr := a.db.QueryRowContext(ctx, "SELECT path FROM v_container_paths WHERE uuid = ?", containerUUID).Scan(&containerPath)
			switch {
			case perr == sql.ErrNoRows:

				where = append(where, "t.project_uuid = ?")
				args = append(args, containerUUID)
			case perr != nil:
				return nil, NewInternalError(perr)
			default:
				where = append(where, "(cp.path = ? OR cp.path LIKE ? || '/%')")
				args = append(args, containerPath, containerPath)
			}
		} else {
			where = append(where, "t.project_uuid = ?")
			args = append(args, containerUUID)
		}
	}
	if len(p.State) > 0 {
		ph, vals := inClause(p.State)
		where = append(where, "t.state IN ("+ph+")")
		args = append(args, vals...)
	} else if !p.IncludeDeleted {
		where = append(where, "t.state != 'deleted'")
	}
	if len(p.Kind) > 0 {
		ph, vals := inClause(p.Kind)
		where = append(where, "t.kind IN ("+ph+")")
		args = append(args, vals...)
	}
	if strings.TrimSpace(p.Assignee) != "" {
		where = append(where, "t.assignee_principal_ref = ?")
		args = append(args, p.Assignee)
	}
	if strings.TrimSpace(p.ClaimedBy) != "" {
		where = append(where, "t.claimed_by_principal_ref = ?")
		args = append(args, p.ClaimedBy)
	}
	if strings.TrimSpace(p.ClaimedNode) != "" {
		where = append(where, "t.claimed_node = ?")
		args = append(args, p.ClaimedNode)
	}

	limit := p.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}

	page, err := cursor.Apply(p.Cursor, cursor.ApplyOptions{
		SortFields: []string{sortField},
		SQLFields:  []string{sqlField},
		Descending: []bool{descending},
		IDField:    "t.id",
		Limit:      limit,
	})
	if err != nil {
		return nil, NewValidationError("invalid cursor", map[string]any{"field": "cursor"})
	}

	bodyColumns := taskBodyColumns
	if p.Summary {
		bodyColumns = "'' AS description, '' AS specification"
	}
	query := taskSelectSQL(bodyColumns)
	if page.WhereClause != "" {
		where = append(where, page.WhereClause)
		args = append(args, page.Params...)
	}
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " " + page.OrderByClause
	if page.LimitClause != "" {
		query += " " + page.LimitClause
		args = append(args, *page.LimitParam)
	}

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	items := []WrkqTask{}
	for rows.Next() {
		task, _, scanErr := scanTaskRow(rows)
		if scanErr != nil {
			return nil, NewInternalError(scanErr)
		}
		items = append(items, *task)
	}
	if err := rows.Err(); err != nil {
		return nil, NewInternalError(err)
	}

	for i := range items {
		if err := a.loadCausedBy(&items[i]); err != nil {
			return nil, err
		}
	}

	result := &WrkqTaskListResult{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		anchor := result.Items[limit-1]

		next := &cursor.Cursor{
			SortFields: []string{sortField},
			LastValues: []any{taskCursorAnchor(anchor, sortField)},
			LastID:     anchor.ID,
			Descending: []bool{descending},
		}
		if encoded, cerr := next.Encode(); cerr == nil {
			result.NextCursor = encoded
		}
	}
	return result, nil
}

// taskListSortWhitelist maps each accepted sort field to its SQL expression.
// Mirrors the CLI find whitelist (created_at, updated_at, id, path) plus priority.
var taskListSortWhitelist = map[string]string{
	"created_at": "t.created_at",
	"updated_at": "t.updated_at",
	"priority":   "t.priority",
	"id":         "t.id",
	"path":       "cp.path || '/' || t.slug",
}

// normalizeTaskListSort validates the sort field against the whitelist and
// returns its logical name and SQL expression. An empty value defaults to
// created_at; any non-whitelisted value is rejected with WRKQ_VALIDATION.
func normalizeTaskListSort(field string) (logical, sqlExpr string, err error) {
	field = strings.TrimSpace(field)
	if field == "" {
		return "created_at", taskListSortWhitelist["created_at"], nil
	}
	sqlExpr, ok := taskListSortWhitelist[field]
	if !ok {
		return "", "", NewValidationError(
			"invalid sort field: "+field+" (choose created_at, updated_at, priority, id, or path)",
			map[string]any{"field": "sort"},
		)
	}
	return field, sqlExpr, nil
}

// normalizeTaskListDirection maps a direction string to a descending flag. An
// empty value preserves the default (ascending); only "asc"/"desc" are accepted,
// case-sensitively. Any other non-empty value is rejected with WRKQ_VALIDATION.
func normalizeTaskListDirection(direction string) (descending bool, err error) {
	switch strings.TrimSpace(direction) {
	case "", "asc":
		return false, nil
	case "desc":
		return true, nil
	default:
		return false, NewValidationError(
			"invalid direction: "+direction+" (choose asc or desc)",
			map[string]any{"field": "direction"},
		)
	}
}

// taskCursorAnchor returns the raw sort-column value for the given sort field,
// used as the cursor anchor for the next page.
func taskCursorAnchor(t WrkqTask, sortField string) any {
	switch sortField {
	case "priority":
		return t.Priority
	case "id":
		return t.ID
	case "updated_at":
		return t.updatedAtRaw
	case "path":
		return t.Path
	default:
		return t.createdAtRaw
	}
}

// inClause builds a "?, ?, ..." placeholder list and the matching args.
func inClause(values []string) (string, []any) {
	placeholders := make([]string, len(values))
	args := make([]any, len(values))
	for i, v := range values {
		placeholders[i] = "?"
		args[i] = v
	}
	return strings.Join(placeholders, ", "), args
}
