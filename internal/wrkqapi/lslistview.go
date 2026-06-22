package wrkqapi

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/selectors"
)

// LsListViewParams mirrors the legacy `wrkq ls <path>` surface for one path.
type LsListViewParams struct {
	Path          string `json:"path,omitempty"`
	Sort          string `json:"sort,omitempty"`
	Reverse       bool   `json:"reverse,omitempty"`
	Limit         int    `json:"limit,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
	Type          string `json:"type,omitempty"` // "p" or "t"
	IncludeHidden bool   `json:"includeHidden,omitempty"`
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
	CPProjectID          *string `json:"cp_project_id,omitempty"`
	CPWorkItemID         *string `json:"cp_work_item_id,omitempty"`
	CPRunID              *string `json:"cp_run_id,omitempty"`
	SessionID            *string `json:"session_id,omitempty"`
	RunStatus            *string `json:"run_status,omitempty"`
}

// WrkqLsListView is the server-owned COMPATIBILITY list projection for `wrkq ls`.
// It owns mixed task/container listing, rollup counts, in-memory merge-sort, and
// cursor pagination over the merged set. Not a canonical resource.
type WrkqLsListView struct {
	Items      []WrkqLsEntry `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
}

// LsListView lists one path's children (containers + tasks) with the legacy
// mixed-resource ordering and cursor pagination.
func (a *API) LsListView(ctx context.Context, p LsListViewParams) (*WrkqLsListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	sortField, descending, err := normalizeLsSort(p.Sort, p.Reverse)
	if err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "sort"})
	}
	pag, err := cursor.Apply(p.Cursor, cursor.ApplyOptions{
		SortFields: []string{sortField},
		Descending: []bool{descending},
		IDField:    "id",
		Limit:      p.Limit,
	})
	if err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "cursor"})
	}

	var entries []WrkqLsEntry
	if p.Path == "" {
		if p.Type == "" || p.Type == "p" {
			rows, qerr := a.lsQueryContainers(ctx, "(SELECT uuid FROM containers WHERE kind = 'root')", pag, "")
			if qerr != nil {
				return nil, qerr
			}
			entries = append(entries, rows...)
		}
	} else {
		containerUUID, _, cerr := selectors.WalkContainerPath(a.db, p.Path)
		if cerr == nil {
			if p.Type == "" || p.Type == "p" {
				rows, qerr := a.lsQueryContainers(ctx, "?", pag, p.Path, containerUUID)
				if qerr != nil {
					return nil, qerr
				}
				entries = append(entries, rows...)
			}
			if p.Type == "" || p.Type == "t" {
				rows, qerr := a.lsQueryTasks(ctx, containerUUID, p.Path, p.IncludeHidden, pag)
				if qerr != nil {
					return nil, qerr
				}
				entries = append(entries, rows...)
			}
		} else {
			single, serr := a.lsSingleTask(ctx, p.Path)
			if serr != nil {
				return nil, serr
			}
			entries = append(entries, *single)
		}
	}

	sortLsEntries(entries, sortField, descending)
	nextCursor := ""
	if p.Limit > 0 && len(entries) > p.Limit {
		entries = entries[:p.Limit]
		last := entries[len(entries)-1]
		nextCursor, _ = cursor.BuildNextCursor([]string{sortField}, []any{lsEntrySortValue(last, sortField)}, last.ID)
	}
	return &WrkqLsListView{Items: entries, NextCursor: nextCursor}, nil
}

// lsQueryContainers lists child containers under parentExpr (a SQL expression or
// "?" with parentArgs) and computes rollup counts. pathPrefix is the parent path.
func (a *API) lsQueryContainers(ctx context.Context, parentExpr string, pag *cursor.ApplyResult, pathPrefix string, parentArgs ...any) ([]WrkqLsEntry, error) {
	query := "SELECT uuid, id, slug, title, kind, created_at, updated_at FROM containers WHERE parent_uuid = " + parentExpr
	args := append([]any{}, parentArgs...)
	if pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		args = append(args, pag.Params...)
	}
	query += " " + pag.OrderByClause
	if pag.LimitClause != "" {
		query += " " + pag.LimitClause
		args = append(args, *pag.LimitParam)
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	var out []WrkqLsEntry
	for rows.Next() {
		var uuid, id, slug, kind, createdAt, updatedAt string
		var title *string
		if err := rows.Scan(&uuid, &id, &slug, &title, &kind, &createdAt, &updatedAt); err != nil {
			return nil, NewInternalError(err)
		}
		titleStr := slug
		if title != nil && *title != "" {
			titleStr = *title
		}
		childPath := slug
		if pathPrefix != "" {
			childPath = pathPrefix + "/" + slug
		}
		tc, atc, rerr := a.containerRollupCounts(ctx, uuid)
		if rerr != nil {
			return nil, rerr
		}
		out = append(out, WrkqLsEntry{
			Type: "container", ID: id, Slug: slug, Title: titleStr, Path: childPath,
			CreatedAt: createdAt, UpdatedAt: updatedAt, Kind: kind,
			TaskCount: &tc, ActiveTaskCount: &atc,
		})
	}
	return out, rows.Err()
}

func (a *API) lsQueryTasks(ctx context.Context, containerUUID, pathPrefix string, includeHidden bool, pag *cursor.ApplyResult) ([]WrkqLsEntry, error) {
	query := `SELECT id, slug, title, created_at, updated_at, state, kind,
		requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
		cp_project_id, cp_work_item_id, cp_run_id, cp_session_id, run_status
		FROM tasks WHERE project_uuid = ?`
	args := []any{containerUUID}
	if !includeHidden {
		query += ` AND state IN ('draft', 'open')`
	}
	if pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		args = append(args, pag.Params...)
	}
	query += " " + pag.OrderByClause
	if pag.LimitClause != "" {
		query += " " + pag.LimitClause
		args = append(args, *pag.LimitParam)
	}
	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()
	var out []WrkqLsEntry
	for rows.Next() {
		var e WrkqLsEntry
		var slug string
		if err := rows.Scan(&e.ID, &slug, &e.Title, &e.CreatedAt, &e.UpdatedAt, &e.State, &e.Kind,
			&e.RequestedByProjectID, &e.AssignedProjectID, &e.AcknowledgedAt, &e.Resolution,
			&e.CPProjectID, &e.CPWorkItemID, &e.CPRunID, &e.SessionID, &e.RunStatus); err != nil {
			return nil, NewInternalError(err)
		}
		e.Type = "task"
		e.Slug = slug
		e.Path = slug
		if pathPrefix != "" {
			e.Path = pathPrefix + "/" + slug
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (a *API) lsSingleTask(ctx context.Context, path string) (*WrkqLsEntry, error) {
	taskUUID, taskID, terr := selectors.ResolveTaskByPath(a.db, path)
	if terr != nil {
		// NewNotFoundError formats "<kind> not found: <ref>"; kind "path" + ref
		// path yields legacy's exact "path not found: <path>".
		return nil, NewNotFoundError(path, "path")
	}
	var e WrkqLsEntry
	var slug string
	if err := a.db.QueryRowContext(ctx, `
		SELECT slug, title, created_at, updated_at, state, kind, requested_by_project_id,
		       assigned_project_id, acknowledged_at, resolution,
		       cp_project_id, cp_work_item_id, cp_run_id, cp_session_id, run_status
		FROM tasks WHERE uuid = ?`, taskUUID).Scan(
		&slug, &e.Title, &e.CreatedAt, &e.UpdatedAt, &e.State, &e.Kind, &e.RequestedByProjectID,
		&e.AssignedProjectID, &e.AcknowledgedAt, &e.Resolution,
		&e.CPProjectID, &e.CPWorkItemID, &e.CPRunID, &e.SessionID, &e.RunStatus); err != nil {
		return nil, NewInternalError(err)
	}
	e.Type = "task"
	e.ID = taskID
	e.Slug = slug
	e.Path = path
	return &e, nil
}

func (a *API) containerRollupCounts(ctx context.Context, containerUUID string) (int, int, error) {
	var taskCount, activeTaskCount int
	err := a.db.QueryRowContext(ctx, `
		WITH RECURSIVE descendants(uuid) AS (
			SELECT uuid FROM containers WHERE uuid = ?
			UNION ALL
			SELECT c.uuid FROM containers c JOIN descendants d ON c.parent_uuid = d.uuid
		)
		SELECT COUNT(t.uuid),
		       COALESCE(SUM(CASE WHEN t.state IN ('draft','open','in_progress','blocked')
		                          AND t.archived_at IS NULL AND t.deleted_at IS NULL
		                         THEN 1 ELSE 0 END), 0)
		FROM descendants d LEFT JOIN tasks t ON t.project_uuid = d.uuid`, containerUUID).Scan(&taskCount, &activeTaskCount)
	if err != nil {
		return 0, 0, NewInternalError(err)
	}
	return taskCount, activeTaskCount, nil
}

func normalizeLsSort(field string, reverse bool) (string, bool, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		field = "slug"
	}
	switch field {
	case "slug", "updated_at", "created_at", "id":
	default:
		return "", false, fmt.Errorf("invalid --sort %q: choose slug, updated_at, created_at, or id", field)
	}
	return field, reverse, nil
}

func sortLsEntries(entries []WrkqLsEntry, field string, descending bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		left := lsEntrySortValue(entries[i], field)
		right := lsEntrySortValue(entries[j], field)
		if left == right {
			if descending {
				return entries[i].ID > entries[j].ID
			}
			return entries[i].ID < entries[j].ID
		}
		if descending {
			return left > right
		}
		return left < right
	})
}

func lsEntrySortValue(entry WrkqLsEntry, field string) string {
	switch field {
	case "id":
		return entry.ID
	case "created_at":
		return entry.CreatedAt
	case "updated_at":
		return entry.UpdatedAt
	default:
		return entry.Slug
	}
}

var _ = sql.ErrNoRows
