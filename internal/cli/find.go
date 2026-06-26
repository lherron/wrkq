package cli

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/render"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var findCmd = &cobra.Command{
	Use:   "find [PATH...]",
	Short: "Search for tasks and containers",
	Long: `Search for tasks and containers using metadata filters.

Examples:
  wrkq find                                    # Find active items (excludes archived, deleted, idea)
  wrkq find portal/**                          # Find items under portal
  wrkq find -type t --state open              # Find open tasks
  wrkq find --slug-glob 'login-*'              # Find items with slug matching pattern
  wrkq find --due-before 2025-12-01            # Find tasks due before date
  wrkq find --state open --due-after 2025-11-01 --json
  wrkq find --kind bug --state open            # Find open bugs
  wrkq find --assignee agent-claude            # Find tasks assigned to agent-claude
  wrkq find --parent-task T-00001              # Find subtasks of T-00001
  wrkq find --assigned-project rex             # Find tasks assigned to a project
  wrkq find --requested-by agent-spaces        # Find tasks requested by a project
  wrkq find --ack-pending                       # Find completed/cancelled tasks awaiting ack
`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runFind),
}

var (
	findType            string
	findSlugGlob        string
	findState           string
	findDueBefore       string
	findDueAfter        string
	findKind            string
	findAssignee        string
	findParentTask      string
	findRequestedBy     string
	findAssignedProject string
	findAckPending      bool
	findLimit           int
	findCursor          string
	findSort            string
	findReverse         bool
	findPorcelain       bool
	findJSON            bool
	findNDJSON          bool
	findPretty          bool
	findPrint0          bool
)

func init() {
	rootCmd.AddCommand(findCmd)

	findCmd.Flags().StringVarP(&findType, "type", "", "", "Filter by type: t (task), p (project/container)")
	findCmd.Flags().StringVar(&findSlugGlob, "slug-glob", "", "Filter by slug glob pattern (e.g. 'login-*')")
	findCmd.Flags().StringVar(&findState, "state", "", "Filter by state: idea, draft, open, in_progress, completed, blocked, cancelled, archived, deleted, or 'all' for everything")
	findCmd.Flags().StringVar(&findDueBefore, "due-before", "", "Filter tasks due before date (YYYY-MM-DD)")
	findCmd.Flags().StringVar(&findDueAfter, "due-after", "", "Filter tasks due after date (YYYY-MM-DD)")
	findCmd.Flags().StringVar(&findKind, "kind", "", "Filter by task kind: task, subtask, spike, bug, chore")
	findCmd.Flags().StringVar(&findAssignee, "assignee", "", "Filter by assignee principal ref or bare agent slug")
	findCmd.Flags().StringVar(&findParentTask, "parent-task", "", "Filter subtasks of a specific parent task (ID or path)")
	findCmd.Flags().StringVar(&findRequestedBy, "requested-by", "", "Filter by requester project ID")
	findCmd.Flags().StringVar(&findAssignedProject, "assigned-project", "", "Filter by assignee project ID")
	findCmd.Flags().BoolVar(&findAckPending, "ack-pending", false, "Filter for ack-pending tasks (acknowledged_at is null; completed/cancelled)")
	findCmd.Flags().IntVar(&findLimit, "limit", 0, "Limit number of results")
	findCmd.Flags().StringVar(&findCursor, "cursor", "", "Pagination cursor")
	findCmd.Flags().StringVar(&findSort, "sort", "", "Sort by field: updated_at, created_at, id, path")
	findCmd.Flags().BoolVar(&findReverse, "reverse", false, "Reverse sort order")
	findCmd.Flags().BoolVar(&findPorcelain, "porcelain", false, "Stable machine-readable output")
	findCmd.Flags().BoolVar(&findJSON, "json", false, "Output as JSON")
	findCmd.Flags().BoolVar(&findNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	findCmd.Flags().BoolVar(&findPretty, "pretty", false, "Force human-readable table output even when not a TTY")
	findCmd.Flags().BoolVarP(&findPrint0, "print0", "0", false, "NUL-separated output")
}

func runFind(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	args = applyProjectRootToPaths(app.Config, args, true)

	// Normalize assignee to a principal ref if provided.
	var assigneePrincipalRef string
	if findAssignee != "" {
		principalRef, err := attribution.NormalizeCompat(findAssignee)
		if err != nil {
			return fmt.Errorf("failed to resolve assignee: %w", err)
		}
		assigneePrincipalRef = principalRef
	}

	// Resolve parent task to UUID if provided
	var parentTaskUUID string
	if findParentTask != "" {
		parentRef := applyProjectRootToSelector(app.Config, findParentTask, false)
		uuid, _, err := selectors.ResolveTask(database, parentRef)
		if err != nil {
			return fmt.Errorf("failed to resolve parent task: %w", err)
		}
		parentTaskUUID = uuid
	}

	// Build query based on filters with SQL-based pagination
	sortField, sortDescending, err := normalizeFindSort(findSort, findReverse, findType)
	if err != nil {
		return err
	}

	results, hasMore, err := executeFindQuery(database, findOptions{
		paths:                args,
		typeFilter:           findType,
		slugGlob:             findSlugGlob,
		state:                findState,
		dueBefore:            findDueBefore,
		dueAfter:             findDueAfter,
		kind:                 findKind,
		assigneePrincipalRef: assigneePrincipalRef,
		parentTaskUUID:       parentTaskUUID,
		requestedByProjectID: findRequestedBy,
		assignedProjectID:    findAssignedProject,
		ackPending:           findAckPending,
		limit:                findLimit,
		cursor:               findCursor,
		sortField:            sortField,
		sortDescending:       sortDescending,
	})
	if err != nil {
		return err
	}

	// Generate next cursor if there are more results
	var nextCursorStr string
	if hasMore && len(results) > 0 {
		lastEntry := results[len(results)-1]
		nextCursorStr, _ = cursor.BuildNextCursor(
			[]string{sortField},
			[]interface{}{findResultSortValue(lastEntry, sortField)},
			lastEntry.ID,
		)
	}

	sel, err := resolveOutputMode(cmd, app.Config, outputShapeList, outputResolveOptions{
		Allow:      []outputMode{outputModeTable, outputModeHuman, outputModeJSON, outputModeNDJSON, outputModeYAML, outputModeTSV},
		DefaultTTY: outputModeTable,
	})
	if err != nil {
		return err
	}
	if findPretty {
		sel = outputSelection{Mode: outputModeTable}
	}
	if cmd.Flag("json") == nil {
		switch {
		case findJSON && findNDJSON:
			return fmt.Errorf("choose only one output mode")
		case findJSON:
			sel = outputSelection{Mode: outputModeJSON, Stable: findPorcelain}
		case findNDJSON:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: findPorcelain}
		case findPorcelain:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: true}
		default:
			sel = outputSelection{Mode: outputModeTable}
		}
	}

	if sel.Stable && nextCursorStr != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", nextCursorStr)
	}
	if findPrint0 {
		var paths []string
		for _, result := range results {
			paths = append(paths, result.Path)
		}
		if len(paths) > 0 {
			fmt.Fprint(cmd.OutOrStdout(), strings.Join(paths, "\x00"))
			fmt.Fprint(cmd.OutOrStdout(), "\x00")
		}
		return nil
	}

	switch sel.Mode {
	case outputModeJSON:
		return writeJSONOutput(cmd.OutOrStdout(), sel, results)
	case outputModeNDJSON:
		return writeNDJSONOutput(cmd.OutOrStdout(), results)
	case outputModeYAML:
		return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatYAML}).RenderYAML(results)
	}

	headers := []string{"Type", "ID", "Path", "Title", "State", "UpdatedAt"}
	rowsData := make([][]string, 0, len(results))
	for _, result := range results {
		state := ""
		if result.State != nil {
			state = *result.State
		}
		rowsData = append(rowsData, []string{result.Type, result.ID, result.Path, result.Title, state, result.UpdatedAt})
	}
	if sel.Mode == outputModeTSV {
		return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatTSV}).RenderTSV(headers, rowsData)
	}
	return render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: sel.Stable,
	}).RenderTable(headers, rowsData)
}

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
	ackPending           bool
	limit                int
	cursor               string
	sortField            string
	sortDescending       bool
}

type findResult struct {
	Type                 string  `json:"type"` // "task" or "container"
	UUID                 string  `json:"uuid"`
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	Title                string  `json:"title"`
	Path                 string  `json:"path"`
	Specification        string  `json:"specification,omitempty"`
	State                *string `json:"state,omitempty"`                   // tasks only
	Priority             *int    `json:"priority,omitempty"`                // tasks only
	Kind                 *string `json:"kind,omitempty"`                    // tasks only
	Assignee             *string `json:"assignee,omitempty"`                // tasks only (principal display)
	AssigneePrincipalRef *string `json:"assignee_principal_ref,omitempty"`  // tasks only
	ParentTaskID         *string `json:"parent_task_id,omitempty"`          // subtasks only
	RequestedByProjectID *string `json:"requested_by_project_id,omitempty"` // tasks only
	AssignedProjectID    *string `json:"assigned_project_id,omitempty"`     // tasks only
	AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`         // tasks only
	Resolution           *string `json:"resolution,omitempty"`              // tasks only
	DueAt                *string `json:"due_at,omitempty"`                  // tasks only
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	ETag                 int64   `json:"etag"`
}

func executeFindQuery(database *db.DB, opts findOptions) ([]findResult, bool, error) {
	results := []findResult{}

	// Determine what to search
	searchTasks := opts.typeFilter == "" || opts.typeFilter == "t"
	searchContainers := opts.typeFilter == "" || opts.typeFilter == "p"
	searchBoth := searchTasks && searchContainers

	var hasMore bool

	// Search tasks
	if searchTasks {
		tasks, taskHasMore, err := findTasks(database, opts, searchBoth)
		if err != nil {
			return nil, false, fmt.Errorf("finding tasks: %w", err)
		}
		results = append(results, tasks...)
		if !searchBoth {
			hasMore = taskHasMore
		}
	}

	// Search containers
	if searchContainers {
		containers, containerHasMore, err := findContainers(database, opts, searchBoth)
		if err != nil {
			return nil, false, fmt.Errorf("finding containers: %w", err)
		}
		results = append(results, containers...)
		if !searchBoth {
			hasMore = containerHasMore
		}
	}

	// If searching both types, apply in-memory sorting and pagination
	if searchBoth && opts.limit > 0 {
		sortFindResults(results, opts.sortField, opts.sortDescending)
		if len(results) > opts.limit {
			hasMore = true
			results = results[:opts.limit]
		}
	} else if searchBoth {
		sortFindResults(results, opts.sortField, opts.sortDescending)
	}

	return results, hasMore, nil
}

func findTasks(database *db.DB, opts findOptions, skipPagination bool) ([]findResult, bool, error) {
	// Build cursor pagination (only if not mixing with containers)
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
			return nil, false, err
		}
	}

	query := `
		SELECT t.uuid, t.id, t.slug, t.title, t.specification, t.state, t.priority, t.kind,
		       t.assignee_principal_ref, t.parent_task_uuid, t.requested_by_project_id,
		       t.assigned_project_id, t.acknowledged_at, t.resolution, t.due_at, t.etag,
		       cp.path || '/' || t.slug AS path, t.created_at, t.updated_at
		FROM tasks t
		JOIN v_container_paths cp ON cp.uuid = t.project_uuid
		WHERE 1=1
	`
	args := []interface{}{}

	// Filter by state (default: exclude archived, deleted, and idea)
	switch opts.state {
	case "all":
		// Include all states (no filter)
	case "":
		// Default: exclude archived, deleted, and idea
		query += " AND t.state NOT IN ('archived', 'deleted', 'idea')"
	default:
		query += " AND t.state = ?"
		args = append(args, opts.state)
	}

	// Filter by kind
	if opts.kind != "" {
		query += " AND t.kind = ?"
		args = append(args, opts.kind)
	}

	// Filter by assignee
	if opts.assigneePrincipalRef != "" {
		query += " AND t.assignee_principal_ref = ?"
		args = append(args, opts.assigneePrincipalRef)
	}

	// Filter by parent task
	if opts.parentTaskUUID != "" {
		query += " AND t.parent_task_uuid = ?"
		args = append(args, opts.parentTaskUUID)
	}

	// Filter by requested-by project
	if opts.requestedByProjectID != "" {
		query += " AND t.requested_by_project_id = ?"
		args = append(args, opts.requestedByProjectID)
	}

	// Filter by assigned project
	if opts.assignedProjectID != "" {
		query += " AND t.assigned_project_id = ?"
		args = append(args, opts.assignedProjectID)
	}

	// Filter by ack pending
	if opts.ackPending {
		query += " AND t.acknowledged_at IS NULL AND t.state IN ('completed', 'cancelled')"
	}

	// Filter by due date
	if opts.dueBefore != "" {
		dueBeforeTime, err := time.Parse("2006-01-02", opts.dueBefore)
		if err != nil {
			return nil, false, fmt.Errorf("invalid due-before date: %w", err)
		}
		query += " AND t.due_at IS NOT NULL AND t.due_at < ?"
		args = append(args, dueBeforeTime.Format(time.RFC3339))
	}

	if opts.dueAfter != "" {
		dueAfterTime, err := time.Parse("2006-01-02", opts.dueAfter)
		if err != nil {
			return nil, false, fmt.Errorf("invalid due-after date: %w", err)
		}
		query += " AND t.due_at IS NOT NULL AND t.due_at > ?"
		args = append(args, dueAfterTime.Format(time.RFC3339))
	}

	// Filter by slug glob
	if opts.slugGlob != "" {
		// Convert glob to SQL GLOB pattern
		pattern := paths.GlobToSQLPattern(opts.slugGlob)
		query += " AND t.slug GLOB ?"
		args = append(args, pattern)
	}

	// Filter by path prefix
	if len(opts.paths) > 0 {
		// Build OR conditions for each path
		pathConditions := []string{}
		for _, p := range opts.paths {
			// Support glob patterns in paths
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

	// Add cursor WHERE clause if present
	if pag != nil && pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		args = append(args, pag.Params...)
	}

	// Add ORDER BY
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

	// Add LIMIT
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
		var r findResult
		var specification string
		var state, kind, assigneePrincipalRef, parentTaskUUID, dueAt sql.NullString
		var requestedBy, assignedProject, acknowledgedAt, resolution sql.NullString
		var priority sql.NullInt64

		err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &specification, &state, &priority, &kind,
			&assigneePrincipalRef, &parentTaskUUID, &requestedBy, &assignedProject,
			&acknowledgedAt, &resolution, &dueAt, &r.ETag, &r.Path, &r.CreatedAt, &r.UpdatedAt)
		if err != nil {
			return nil, false, fmt.Errorf("scan failed: %w", err)
		}

		r.Type = "task"
		r.Specification = specification
		if state.Valid {
			r.State = &state.String
		}
		if priority.Valid {
			p := int(priority.Int64)
			r.Priority = &p
		}
		if kind.Valid {
			r.Kind = &kind.String
		}
		if assigneePrincipalRef.Valid {
			r.AssigneePrincipalRef = &assigneePrincipalRef.String
			display := attribution.PrincipalHandle(assigneePrincipalRef.String)
			r.Assignee = &display
		}
		if parentTaskUUID.Valid {
			// Get parent task ID
			var parentID string
			if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", parentTaskUUID.String).Scan(&parentID); err == nil {
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
		return nil, false, err
	}

	// Check if there are more results (we requested limit+1)
	hasMore := false
	if !skipPagination && opts.limit > 0 && len(results) > opts.limit {
		hasMore = true
		results = results[:opts.limit]
	}

	return results, hasMore, nil
}

func findContainers(database *db.DB, opts findOptions, skipPagination bool) ([]findResult, bool, error) {
	// Build cursor pagination (only if not mixing with tasks)
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
			return nil, false, err
		}
	}

	query := `
		SELECT c.uuid, c.id, c.slug, COALESCE(c.title, c.slug) as title, c.etag,
		       cp.path, c.created_at, c.updated_at
		FROM containers c
		JOIN v_container_paths cp ON cp.uuid = c.uuid
		WHERE c.archived_at IS NULL
	`
	args := []interface{}{}

	// Filter by slug glob
	if opts.slugGlob != "" {
		pattern := paths.GlobToSQLPattern(opts.slugGlob)
		query += " AND c.slug GLOB ?"
		args = append(args, pattern)
	}

	// Filter by path prefix
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

	// Add cursor WHERE clause if present
	if pag != nil && pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		args = append(args, pag.Params...)
	}

	// Add ORDER BY
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

	// Add LIMIT
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
		var r findResult

		err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &r.ETag, &r.Path, &r.CreatedAt, &r.UpdatedAt)
		if err != nil {
			return nil, false, fmt.Errorf("scan failed: %w", err)
		}

		r.Type = "container"
		results = append(results, r)
	}

	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	// Check if there are more results (we requested limit+1)
	hasMore := false
	if !skipPagination && opts.limit > 0 && len(results) > opts.limit {
		hasMore = true
		results = results[:opts.limit]
	}

	return results, hasMore, nil
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

func sortFindResults(results []findResult, field string, descending bool) {
	sort.SliceStable(results, func(i, j int) bool {
		left := findResultSortValue(results[i], field)
		right := findResultSortValue(results[j], field)
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

func findResultSortValue(result findResult, field string) string {
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
