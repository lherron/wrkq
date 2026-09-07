//go:build wrkq_local

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
	// T-08216: ls had no state parameter at all — the draft/open set was welded
	// into its SQL. It now answers the same question as tree, in the same words.
	states, err := resolveStateSelector(p.States, "states")
	if err != nil {
		return nil, err
	}
	lifecycle, err := resolveLifecycle(p.Lifecycle, "lifecycle")
	if err != nil {
		return nil, err
	}
	filter := treeFilter{states: states, lifecycle: lifecycle}
	pruneEmpty := true
	if p.PruneEmpty != nil {
		pruneEmpty = *p.PruneEmpty
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

	paths := p.Paths
	if len(paths) == 0 {
		if p.Path != "" {
			paths = []string{p.Path}
		} else {
			paths = []string{""}
		}
	}

	var entries []WrkqLsEntry
	for _, path := range paths {
		rows, perr := a.lsEntriesForPath(ctx, path, p, filter, pruneEmpty, pag)
		if perr != nil {
			return nil, perr
		}
		entries = append(entries, rows...)
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

// lsEntriesForPath returns the entries for a single path, matching legacy runLs's
// per-path body: top-level containers when path=="", otherwise the container's
// child containers + tasks, or (if the path is not a container) the single task
// at that path. The cursor's per-path WHERE/LIMIT clauses are applied in SQL here,
// exactly as legacy does, before the combined merge-sort in LsListView.
func (a *API) lsEntriesForPath(ctx context.Context, path string, p LsListViewParams, filter treeFilter, pruneEmpty bool, pag *cursor.ApplyResult) ([]WrkqLsEntry, error) {
	var entries []WrkqLsEntry
	if path == "" {
		if p.Type == "" || p.Type == "p" {
			rows, qerr := a.lsQueryContainers(ctx, "(SELECT uuid FROM containers WHERE kind = 'root')", pag, filter, pruneEmpty, "")
			if qerr != nil {
				return nil, qerr
			}
			entries = append(entries, rows...)
		}
		return entries, nil
	}
	containerUUID, _, cerr := selectors.WalkContainerPath(a.db, path)
	if cerr == nil {
		if p.Type == "" || p.Type == "p" {
			rows, qerr := a.lsQueryContainers(ctx, "?", pag, filter, pruneEmpty, path, containerUUID)
			if qerr != nil {
				return nil, qerr
			}
			entries = append(entries, rows...)
		}
		if p.Type == "" || p.Type == "t" {
			rows, qerr := a.lsQueryTasks(ctx, containerUUID, path, filter, pag)
			if qerr != nil {
				return nil, qerr
			}
			entries = append(entries, rows...)
			if p.IncludeCampaignMembers {
				campaignRows, qerr := a.lsQueryCampaignEnrollments(ctx, containerUUID, path, filter, pag)
				if qerr != nil {
					return nil, qerr
				}
				entries = append(entries, campaignRows...)
			}
		}
		return entries, nil
	}
	single, serr := a.lsSingleTask(ctx, path, filter)
	if serr != nil {
		return nil, serr
	}
	if single == nil {
		// T-08216 F4(a): a single-task path is a SELECTION like any other. A task
		// the caller did not ask for is absent, not returned regardless — the
		// filter has to bite here too, or `ls <task-path> --states draft` hands
		// back a completed task.
		return entries, nil
	}
	return append(entries, *single), nil
}

func (a *API) lsQueryCampaignEnrollments(ctx context.Context, campaignUUID, campaignPath string, filter treeFilter, pag *cursor.ApplyResult) ([]WrkqLsEntry, error) {
	var campaignState sql.NullString
	if err := a.db.QueryRowContext(ctx, "SELECT campaign_state FROM containers WHERE uuid = ?", campaignUUID).Scan(&campaignState); err != nil || !campaignState.Valid {
		return nil, nil
	}
	campaignProject := strings.Split(campaignPath, "/")[0]
	query := `WITH RECURSIVE container_ancestors(task_uuid, uuid, parent_uuid, slug, kind) AS (
		SELECT t.uuid, c.uuid, c.parent_uuid, c.slug, c.kind
		  FROM tasks t JOIN containers c ON c.uuid = t.project_uuid
		UNION ALL
		SELECT ca.task_uuid, c.uuid, c.parent_uuid, c.slug, c.kind
		  FROM container_ancestors ca JOIN containers c ON c.uuid = ca.parent_uuid
	)
	SELECT t.id, t.slug, t.title, t.created_at, t.updated_at, t.state, t.kind,
	       t.requested_by_project_id, t.assigned_project_id, t.acknowledged_at, t.resolution,
	       COALESCE((SELECT slug FROM container_ancestors ca
	                  WHERE ca.task_uuid = t.uuid AND ca.kind = 'project' LIMIT 1), '')
	  FROM tasks t WHERE t.campaign_uuid = ? AND t.project_uuid != ?`
	args := []any{campaignUUID, campaignUUID}
	if clause, clauseArgs := filter.sqlPredicate("t"); clause != "" {
		query += clause
		args = append(args, clauseArgs...)
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
		var residentProject string
		if err := rows.Scan(&e.ID, &e.Slug, &e.Title, &e.CreatedAt, &e.UpdatedAt, &e.State, &e.Kind,
			&e.RequestedByProjectID, &e.AssignedProjectID, &e.AcknowledgedAt, &e.Resolution, &residentProject); err != nil {
			return nil, NewInternalError(err)
		}
		e.Type = "task"
		e.Path = e.Slug
		if residentProject != "" && residentProject != campaignProject {
			e.Title += " ↗ " + residentProject
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// lsQueryContainers lists child containers under parentExpr (a SQL expression or
// "?" with parentArgs) and computes rollup counts. pathPrefix is the parent path.
func (a *API) lsQueryContainers(ctx context.Context, parentExpr string, pag *cursor.ApplyResult, filter treeFilter, pruneEmpty bool, pathPrefix string, parentArgs ...any) ([]WrkqLsEntry, error) {
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
		if pruneEmpty {
			// T-08216 F5: pruneEmpty was accepted and never executed on ls, so the
			// approved identical parameter was a no-op here. A child container with
			// no SELECTED content in its subtree is dropped, matching what the same
			// flag means on tree.
			has, herr := a.containerHasSelectedTasks(ctx, uuid, filter)
			if herr != nil {
				return nil, herr
			}
			if !has {
				continue
			}
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

func (a *API) lsQueryTasks(ctx context.Context, containerUUID, pathPrefix string, filter treeFilter, pag *cursor.ApplyResult) ([]WrkqLsEntry, error) {
	query := `SELECT id, slug, title, created_at, updated_at, state, kind,
		requested_by_project_id, assigned_project_id, acknowledged_at, resolution
		FROM tasks WHERE project_uuid = ?`
	args := []any{containerUUID}
	if clause, clauseArgs := filter.sqlPredicate(""); clause != "" {
		query += clause
		args = append(args, clauseArgs...)
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
			&e.RequestedByProjectID, &e.AssignedProjectID, &e.AcknowledgedAt, &e.Resolution); err != nil {
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

func (a *API) lsSingleTask(ctx context.Context, path string, filter treeFilter) (*WrkqLsEntry, error) {
	taskUUID, taskID, terr := selectors.ResolveTaskByPath(a.db, path)
	if terr != nil {

		return nil, NewNotFoundError(path, "path")
	}
	var e WrkqLsEntry
	var slug string
	var archivedAt, deletedAt *string
	if err := a.db.QueryRowContext(ctx, `
		SELECT slug, title, created_at, updated_at, state, kind, requested_by_project_id,
		       assigned_project_id, acknowledged_at, resolution, archived_at, deleted_at
		FROM tasks WHERE uuid = ?`, taskUUID).Scan(
		&slug, &e.Title, &e.CreatedAt, &e.UpdatedAt, &e.State, &e.Kind, &e.RequestedByProjectID,
		&e.AssignedProjectID, &e.AcknowledgedAt, &e.Resolution, &archivedAt, &deletedAt); err != nil {
		return nil, NewInternalError(err)
	}
	if !filter.admits(e.State, archivedAt != nil, deletedAt != nil) {
		return nil, nil
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
		SELECT COUNT(CASE
		               WHEN t.state != 'deleted' AND t.deleted_at IS NULL THEN 1
		             END),
		       COUNT(CASE
		               WHEN t.state IN ('idea','draft','open','in_progress','blocked')
		                AND t.archived_at IS NULL
		                AND t.deleted_at IS NULL
		               THEN 1
		             END)
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

// containerHasSelectedTasks reports whether any task in this container's SUBTREE
// matches the caller's selector. It is the ls-side equivalent of tree's
// hasVisibleContent, and exists so pruneEmpty means the same thing on both.
func (a *API) containerHasSelectedTasks(ctx context.Context, containerUUID string, filter treeFilter) (bool, error) {
	clause, args := filter.sqlPredicate("t")
	query := `
		WITH RECURSIVE descendants(uuid) AS (
			SELECT uuid FROM containers WHERE uuid = ?
			UNION ALL
			SELECT c.uuid FROM containers c JOIN descendants d ON c.parent_uuid = d.uuid
		)
		SELECT EXISTS(
			SELECT 1 FROM descendants d JOIN tasks t ON t.project_uuid = d.uuid
			 WHERE 1=1` + clause + `)`
	queryArgs := append([]any{containerUUID}, args...)
	var exists bool
	if err := a.db.QueryRowContext(ctx, query, queryArgs...).Scan(&exists); err != nil {
		return false, NewInternalError(err)
	}
	return exists, nil
}
