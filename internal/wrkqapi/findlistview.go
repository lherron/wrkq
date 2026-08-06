//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

func (e WrkqFindEntry) MarshalJSON() ([]byte, error) {
	type wire WrkqFindEntry
	if e.membership == "" {
		return json.Marshal(wire(e))
	}
	b, err := json.Marshal(wire(e))
	if err != nil {
		return nil, err
	}
	var obj map[string]any
	if err := json.Unmarshal(b, &obj); err != nil {
		return nil, err
	}
	obj["membership"] = e.membership
	return json.Marshal(obj)
}

// FindListView reproduces legacy `wrkq find` byte-for-byte: same filters, same
// recursive path-prefix matching, same searchBoth-vs-single-type cursor handling
// (cursor is applied SQL-side only when filtering to a single type; the mixed set
// is paginated in memory WITHOUT a cursor, matching legacy executeFindQuery).
func (a *API) FindListView(ctx context.Context, p FindListViewParams) (*WrkqFindListView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Normalize assignee to a principal ref (legacy does this in the CLI; it is
	// durable read behavior so it lives on the server here).
	var assigneePrincipalRef string
	if p.Assignee != "" {
		ref, err := attribution.NormalizeCompat(p.Assignee)
		if err != nil {
			return nil, NewValidationError(fmt.Sprintf("failed to resolve assignee: %s", err.Error()), map[string]any{"field": "assignee"})
		}
		assigneePrincipalRef = ref
	}

	// Resolve parent task to UUID (legacy applies project-root scoping to the
	// selector first; the CLI already scoped it before sending).
	var parentTaskUUID string
	if p.ParentTask != "" {
		uuid, _, err := selectors.ResolveTask(a.db, p.ParentTask)
		if err != nil {
			return nil, NewValidationError(fmt.Sprintf("failed to resolve parent task: %s", err.Error()), map[string]any{"field": "parentTask"})
		}
		parentTaskUUID = uuid
	}

	// Resolve the caused-by filter (a task ID) to its UUID.
	var causedByTaskUUID string
	if p.CausedBy != "" {
		uuid, _, err := selectors.ResolveTask(a.db, p.CausedBy)
		if err != nil {
			return nil, NewValidationError(fmt.Sprintf("failed to resolve --caused-by task: %s", err.Error()), map[string]any{"field": "causedBy"})
		}
		causedByTaskUUID = uuid
	}
	var campaignUUID string
	if p.Campaign != "" {
		uuid, _, err := selectors.ResolveContainer(a.db, p.Campaign)
		if err != nil {
			return nil, NewValidationError(fmt.Sprintf("failed to resolve --campaign: %s", err.Error()), map[string]any{"field": "campaign"})
		}
		campaignUUID = uuid
	}

	sortField, descending, err := normalizeFindSort(p.Sort, p.Reverse, p.Type)
	if err != nil {
		return nil, NewValidationError(err.Error(), map[string]any{"field": "sort"})
	}

	opts := findQueryOptions{
		paths:                p.Paths,
		typeFilter:           p.Type,
		slugGlob:             p.SlugGlob,
		state:                p.State,
		dueBefore:            p.DueBefore,
		dueAfter:             p.DueAfter,
		kind:                 p.Kind,
		labels:               uniqueLabels(p.Labels),
		assigneePrincipalRef: assigneePrincipalRef,
		claimedBy:            p.ClaimedBy,
		claimedNode:          p.ClaimedNode,
		parentTaskUUID:       parentTaskUUID,
		requestedByProjectID: p.RequestedByProjectID,
		assignedProjectID:    p.AssignedProjectID,
		causedByTaskUUID:     causedByTaskUUID,
		ackPending:           p.AckPending,
		hasOutcome:           p.HasOutcome,
		campaignUUID:         campaignUUID,
		limit:                p.Limit,
		cursor:               p.Cursor,
		sortField:            sortField,
		sortDescending:       descending,
	}

	results, hasMore, err := a.executeFindQuery(ctx, opts)
	if err != nil {
		return nil, err
	}

	view := &WrkqFindListView{Items: results}
	if hasMore && len(results) > 0 {
		last := results[len(results)-1]
		view.NextCursor, _ = cursor.BuildNextCursor(
			[]string{sortField},
			[]any{findEntrySortValue(last, sortField)},
			last.ID,
		)
	}
	return view, nil
}

func (a *API) executeFindQuery(ctx context.Context, opts findQueryOptions) ([]WrkqFindEntry, bool, error) {
	results := []WrkqFindEntry{}

	searchTasks := opts.typeFilter == "" || opts.typeFilter == "t"
	searchContainers := opts.typeFilter == "" || opts.typeFilter == "p"

	if len(opts.labels) > 0 || opts.claimedBy != "" || opts.claimedNode != "" || opts.hasOutcome {
		searchContainers = false
	}
	if opts.campaignUUID != "" {
		searchTasks = true
		searchContainers = false
	}
	searchBoth := searchTasks && searchContainers

	var hasMore bool

	if searchTasks {
		tasks, taskHasMore, err := a.findTasks(ctx, opts, searchBoth)
		if err != nil {

			return nil, false, prefixFindError("finding tasks: ", err)
		}
		results = append(results, tasks...)
		if !searchBoth {
			hasMore = taskHasMore
		}
	}

	if searchContainers {
		containers, containerHasMore, err := a.findContainers(ctx, opts, searchBoth)
		if err != nil {

			return nil, false, prefixFindError("finding containers: ", err)
		}
		results = append(results, containers...)
		if !searchBoth {
			hasMore = containerHasMore
		}
	}

	if searchBoth && opts.limit > 0 {
		sortFindEntries(results, opts.sortField, opts.sortDescending)
		if len(results) > opts.limit {
			hasMore = true
			results = results[:opts.limit]
		}
	} else if searchBoth {
		sortFindEntries(results, opts.sortField, opts.sortDescending)
	}

	return results, hasMore, nil
}

func (a *API) findTasks(ctx context.Context, opts findQueryOptions, skipPagination bool) ([]WrkqFindEntry, bool, error) {
	var pag *cursor.ApplyResult
	var err error
	if !skipPagination {
		pag, err = cursor.Apply(opts.cursor, cursor.ApplyOptions{
			SortFields: []string{opts.sortField},
			SQLFields:  []string{findTaskSortSQL(opts.sortField)},
			Descending: []bool{opts.sortDescending},
			IDField:    "t.id",
			Limit:      opts.limit,
		})
		if err != nil {
			return nil, false, NewValidationError(err.Error(), map[string]any{"field": "cursor"})
		}
	}

	query := `
		SELECT t.uuid, t.id, t.slug, t.title, t.specification, t.state, t.priority, t.kind,
		       t.assignee_principal_ref, t.claimed_by_principal_ref, t.claimed_scope_ref,
		       t.claimed_node, t.claimed_at, t.claim_generation,
		       t.parent_task_uuid, t.requested_by_project_id,
		       t.assigned_project_id, t.acknowledged_at, t.resolution, t.due_at, t.etag,
		       cp.path || '/' || t.slug AS path, t.created_at, t.updated_at,
		       CASE WHEN ? != '' AND t.project_uuid = ? THEN 'resident'
		            WHEN ? != '' AND t.campaign_uuid = ? THEN 'enrolled' ELSE '' END AS membership
		FROM tasks t
		JOIN v_container_paths cp ON cp.uuid = t.project_uuid
		WHERE 1=1
	`
	args := []any{opts.campaignUUID, opts.campaignUUID, opts.campaignUUID, opts.campaignUUID}
	if opts.campaignUUID != "" {
		query += " AND (t.project_uuid = ? OR t.campaign_uuid = ?)"
		args = append(args, opts.campaignUUID, opts.campaignUUID)
	}

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
	for _, label := range opts.labels {
		query += ` AND EXISTS (
			SELECT 1
			FROM json_each(CASE WHEN json_valid(t.labels) THEN t.labels ELSE '[]' END) AS task_label
			WHERE task_label.type = 'text' AND task_label.value = ?
		)`
		args = append(args, label)
	}
	if opts.assigneePrincipalRef != "" {
		query += " AND t.assignee_principal_ref = ?"
		args = append(args, opts.assigneePrincipalRef)
	}
	if opts.claimedBy != "" {
		query += " AND t.claimed_by_principal_ref = ?"
		args = append(args, opts.claimedBy)
	}
	if opts.claimedNode != "" {
		query += " AND t.claimed_node = ?"
		args = append(args, opts.claimedNode)
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
	if opts.hasOutcome {
		query += " AND t.outcome IS NOT NULL"
	}

	if opts.dueBefore != "" {
		dueBeforeTime, perr := time.Parse("2006-01-02", opts.dueBefore)
		if perr != nil {
			return nil, false, NewValidationError(fmt.Sprintf("invalid due-before date: %s", perr.Error()), map[string]any{"field": "dueBefore"})
		}
		query += " AND t.due_at IS NOT NULL AND t.due_at < ?"
		args = append(args, dueBeforeTime.Format(time.RFC3339))
	}
	if opts.dueAfter != "" {
		dueAfterTime, perr := time.Parse("2006-01-02", opts.dueAfter)
		if perr != nil {
			return nil, false, NewValidationError(fmt.Sprintf("invalid due-after date: %s", perr.Error()), map[string]any{"field": "dueAfter"})
		}
		query += " AND t.due_at IS NOT NULL AND t.due_at > ?"
		args = append(args, dueAfterTime.Format(time.RFC3339))
	}

	if opts.slugGlob != "" {
		pattern := paths.GlobToSQLPattern(opts.slugGlob)
		query += " AND t.slug GLOB ?"
		args = append(args, pattern)
	}

	if len(opts.paths) > 0 {
		pathConditions := []string{}
		for _, p := range opts.paths {
			if strings.Contains(p, "*") {
				pattern := paths.GlobToSQLPattern(p)
				pathConditions = append(pathConditions, "(cp.path || '/' || t.slug) GLOB ?")
				args = append(args, pattern)
			} else {
				pathConditions = append(pathConditions, "((cp.path || '/' || t.slug) = ? OR (cp.path || '/' || t.slug) LIKE ? || '/%')")
				args = append(args, p, p)
			}
		}
		if len(pathConditions) > 0 {
			query += " AND (" + strings.Join(pathConditions, " OR ") + ")"
		}
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

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	results := []WrkqFindEntry{}
	for rows.Next() {
		var r WrkqFindEntry
		var specification string
		var state, kind, assigneePrincipalRef, claimedBy, claimedScope, claimedNode, claimedAt sql.NullString
		var claimGeneration int64
		var parentTaskUUID, dueAt sql.NullString
		var requestedBy, assignedProject, acknowledgedAt, resolution sql.NullString
		var priority sql.NullInt64

		if err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &specification, &state, &priority, &kind,
			&assigneePrincipalRef, &claimedBy, &claimedScope, &claimedNode, &claimedAt, &claimGeneration,
			&parentTaskUUID, &requestedBy, &assignedProject,
			&acknowledgedAt, &resolution, &dueAt, &r.ETag, &r.Path, &r.CreatedAt, &r.UpdatedAt, &r.membership); err != nil {
			return nil, false, NewInternalError(err)
		}

		r.Type = "task"
		r.Specification = specification
		if state.Valid {
			r.State = &state.String
		}
		if priority.Valid {
			pv := int(priority.Int64)
			r.Priority = &pv
		}
		if kind.Valid {
			r.Kind = &kind.String
		}
		if assigneePrincipalRef.Valid {
			r.AssigneePrincipalRef = &assigneePrincipalRef.String
			display := attribution.PrincipalHandle(assigneePrincipalRef.String)
			r.Assignee = &display
		}
		if claimedBy.Valid {
			r.ClaimedBy = &claimedBy.String
			r.ClaimedScope = &claimedScope.String
			r.ClaimedNode = &claimedNode.String
			r.ClaimedAt = &claimedAt.String
		}
		r.ClaimGeneration = claimGeneration
		if parentTaskUUID.Valid {
			var parentID string
			if perr := a.db.QueryRowContext(ctx, "SELECT id FROM tasks WHERE uuid = ?", parentTaskUUID.String).Scan(&parentID); perr == nil {
				r.ParentTaskID = &parentID
			}
		}
		if requestedBy.Valid {
			r.RequestedByProjectID = &requestedBy.String
		}
		if assignedProject.Valid {
			r.AssignedProjectID = &assignedProject.String
		}
		if acknowledgedAt.Valid {
			r.AcknowledgedAt = &acknowledgedAt.String
		}
		if resolution.Valid {
			r.Resolution = &resolution.String
		}
		if dueAt.Valid {
			r.DueAt = &dueAt.String
		}

		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, NewInternalError(err)
	}

	for i := range results {
		causedBy, cerr := store.CausedByIDs(a.db, results[i].UUID)
		if cerr != nil {
			return nil, false, NewInternalError(cerr)
		}
		if len(causedBy) > 0 {
			results[i].CausedBy = causedBy
		}
	}

	hasMore := false
	if !skipPagination && opts.limit > 0 && len(results) > opts.limit {
		hasMore = true
		results = results[:opts.limit]
	}
	return results, hasMore, nil
}

// uniqueLabels collapses duplicate repeated filters without changing their exact
// spelling. Canonical task labels are JSON strings: membership is case-sensitive
// and byte-exact; no trimming or case folding is applied.
func uniqueLabels(labels []string) []string {
	if len(labels) < 2 {
		return labels
	}
	seen := make(map[string]struct{}, len(labels))
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if _, ok := seen[label]; ok {
			continue
		}
		seen[label] = struct{}{}
		out = append(out, label)
	}
	return out
}

func (a *API) findContainers(ctx context.Context, opts findQueryOptions, skipPagination bool) ([]WrkqFindEntry, bool, error) {
	var pag *cursor.ApplyResult
	var err error
	if !skipPagination {
		pag, err = cursor.Apply(opts.cursor, cursor.ApplyOptions{
			SortFields: []string{opts.sortField},
			SQLFields:  []string{findContainerSortSQL(opts.sortField)},
			Descending: []bool{opts.sortDescending},
			IDField:    "c.id",
			Limit:      opts.limit,
		})
		if err != nil {
			return nil, false, NewValidationError(err.Error(), map[string]any{"field": "cursor"})
		}
	}

	query := `
		SELECT c.uuid, c.id, c.slug, COALESCE(c.title, c.slug) as title, c.etag,
		       cp.path, c.created_at, c.updated_at
		FROM containers c
		JOIN v_container_paths cp ON cp.uuid = c.uuid
		WHERE c.archived_at IS NULL
	`
	args := []any{}

	if opts.slugGlob != "" {
		pattern := paths.GlobToSQLPattern(opts.slugGlob)
		query += " AND c.slug GLOB ?"
		args = append(args, pattern)
	}

	if len(opts.paths) > 0 {
		pathConditions := []string{}
		for _, p := range opts.paths {
			if strings.Contains(p, "*") {
				pattern := paths.GlobToSQLPattern(p)
				pathConditions = append(pathConditions, "cp.path GLOB ?")
				args = append(args, pattern)
			} else {
				pathConditions = append(pathConditions, "(cp.path = ? OR cp.path LIKE ? || '/%')")
				args = append(args, p, p)
			}
		}
		if len(pathConditions) > 0 {
			query += " AND (" + strings.Join(pathConditions, " OR ") + ")"
		}
	}

	if pag != nil && pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		args = append(args, pag.Params...)
	}

	if pag != nil {
		query += " " + pag.OrderByClause
	} else {
		query += " ORDER BY " + findContainerSortSQL(opts.sortField)
		if opts.sortDescending {
			query += " DESC"
		} else {
			query += " ASC"
		}
		if opts.sortField != "id" {
			if opts.sortDescending {
				query += ", c.id DESC"
			} else {
				query += ", c.id ASC"
			}
		}
	}

	if pag != nil && pag.LimitClause != "" {
		query += " " + pag.LimitClause
		args = append(args, *pag.LimitParam)
	}

	rows, err := a.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	results := []WrkqFindEntry{}
	for rows.Next() {
		var r WrkqFindEntry
		if err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &r.ETag, &r.Path, &r.CreatedAt, &r.UpdatedAt); err != nil {
			return nil, false, NewInternalError(err)
		}
		r.Type = "container"
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, false, NewInternalError(err)
	}

	hasMore := false
	if !skipPagination && opts.limit > 0 && len(results) > opts.limit {
		hasMore = true
		results = results[:opts.limit]
	}
	return results, hasMore, nil
}

// prefixFindError reproduces legacy executeFindQuery's "finding tasks: %w" /
// "finding containers: %w" wrapping while preserving the underlying domain code
// (so the wire error.data.code and the mirror's stripped message both match the
// legacy raw text). A non-domain error is wrapped as an internal error.
func prefixFindError(prefix string, err error) error {
	if de, ok := err.(*DomainError); ok {
		return newError(de.Code(), prefix+de.Error(), de.Retryable(), de.Data(), de.Unwrap())
	}
	return NewInternalError(fmt.Errorf("%s%w", prefix, err))
}

func normalizeFindSort(field string, reverse bool, typeFilter string) (string, bool, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		switch typeFilter {
		case "p":
			return "path", reverse, nil
		default:
			return "updated_at", !reverse, nil
		}
	}
	switch field {
	case "updated_at", "created_at", "id", "path":
	default:
		return "", false, fmt.Errorf("invalid --sort %q: choose updated_at, created_at, id, or path", field)
	}
	return field, reverse, nil
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

func findContainerSortSQL(field string) string {
	switch field {
	case "id":
		return "c.id"
	case "created_at":
		return "c.created_at"
	case "updated_at":
		return "c.updated_at"
	default:
		return "cp.path"
	}
}

func sortFindEntries(results []WrkqFindEntry, field string, descending bool) {
	sort.SliceStable(results, func(i, j int) bool {
		left := findEntrySortValue(results[i], field)
		right := findEntrySortValue(results[j], field)
		if left == right {
			if descending {
				return results[i].ID > results[j].ID
			}
			return results[i].ID < results[j].ID
		}
		if descending {
			return left > right
		}
		return left < right
	})
}

func findEntrySortValue(result WrkqFindEntry, field string) string {
	switch field {
	case "id":
		return result.ID
	case "created_at":
		return result.CreatedAt
	case "path":
		return result.Path
	default:
		return result.UpdatedAt
	}
}
