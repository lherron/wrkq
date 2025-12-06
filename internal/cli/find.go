package cli

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var findCmd = &cobra.Command{
	Use:   "find [PATH...]",
	Short: "Search for tasks and containers",
	Long: `Search for tasks and containers using metadata filters.

Examples:
  wrkq find                                    # Find all non-archived items
  wrkq find portal/**                          # Find items under portal
  wrkq find -type t --state open              # Find open tasks
  wrkq find --slug-glob 'login-*'              # Find items with slug matching pattern
  wrkq find --due-before 2025-12-01            # Find tasks due before date
  wrkq find --state open --due-after 2025-11-01 --json
`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runFind),
}

var (
	findType     string
	findSlugGlob string
	findState    string
	findDueBefore string
	findDueAfter string
	findLimit    int
	findCursor   string
	findPorcelain bool
	findJSON     bool
	findNDJSON   bool
	findPrint0   bool
)

func init() {
	rootCmd.AddCommand(findCmd)

	findCmd.Flags().StringVarP(&findType, "type", "", "", "Filter by type: t (task), p (project/container)")
	findCmd.Flags().StringVar(&findSlugGlob, "slug-glob", "", "Filter by slug glob pattern (e.g. 'login-*')")
	findCmd.Flags().StringVar(&findState, "state", "", "Filter by state: open, completed, archived")
	findCmd.Flags().StringVar(&findDueBefore, "due-before", "", "Filter tasks due before date (YYYY-MM-DD)")
	findCmd.Flags().StringVar(&findDueAfter, "due-after", "", "Filter tasks due after date (YYYY-MM-DD)")
	findCmd.Flags().IntVar(&findLimit, "limit", 0, "Limit number of results")
	findCmd.Flags().StringVar(&findCursor, "cursor", "", "Pagination cursor")
	findCmd.Flags().BoolVar(&findPorcelain, "porcelain", false, "Stable machine-readable output")
	findCmd.Flags().BoolVar(&findJSON, "json", false, "Output as JSON")
	findCmd.Flags().BoolVar(&findNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	findCmd.Flags().BoolVarP(&findPrint0, "print0", "0", false, "NUL-separated output")
}

func runFind(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Build query based on filters with SQL-based pagination
	results, hasMore, err := executeFindQuery(database, findOptions{
		paths:      args,
		typeFilter: findType,
		slugGlob:   findSlugGlob,
		state:      findState,
		dueBefore:  findDueBefore,
		dueAfter:   findDueAfter,
		limit:      findLimit,
		cursor:     findCursor,
	})
	if err != nil {
		return err
	}

	// Generate next cursor if there are more results
	var nextCursorStr string
	if hasMore && len(results) > 0 {
		lastEntry := results[len(results)-1]
		// Use appropriate sort field based on type filter
		if findType == "t" {
			nextCursorStr, _ = cursor.BuildNextCursor(
				[]string{"updated_at"},
				[]interface{}{lastEntry.UpdatedAt},
				lastEntry.ID,
			)
		} else if findType == "p" {
			nextCursorStr, _ = cursor.BuildNextCursor(
				[]string{"path"},
				[]interface{}{lastEntry.Path},
				lastEntry.ID,
			)
		} else {
			// Mixed results - use ID as sort field
			nextCursorStr, _ = cursor.BuildNextCursor(
				[]string{"id"},
				[]interface{}{lastEntry.ID},
				lastEntry.ID,
			)
		}
	}

	// Output next_cursor to stderr in porcelain mode
	if findPorcelain && nextCursorStr != "" {
		fmt.Fprintf(os.Stderr, "next_cursor=%s\n", nextCursorStr)
	}

	// Render output
	if findJSON {
		return render.RenderJSON(results, false)
	}
	if findNDJSON {
		return render.RenderNDJSON(results)
	}
	if findPrint0 {
		return render.RenderNulSeparated(results)
	}

	// Default table output
	return render.RenderTable(results, findPorcelain)
}

type findOptions struct {
	paths      []string
	typeFilter string
	slugGlob   string
	state      string
	dueBefore  string
	dueAfter   string
	limit      int
	cursor     string
}

type findResult struct {
	Type      string  `json:"type"`      // "task" or "container"
	UUID      string  `json:"uuid"`
	ID        string  `json:"id"`
	Slug      string  `json:"slug"`
	Title     string  `json:"title"`
	Path      string  `json:"path"`
	State     *string `json:"state,omitempty"`      // tasks only
	Priority  *int    `json:"priority,omitempty"`   // tasks only
	DueAt     *string `json:"due_at,omitempty"`     // tasks only
	UpdatedAt string  `json:"updated_at,omitempty"` // for cursor pagination
	ETag      int64   `json:"etag"`
}

func executeFindQuery(database *db.DB, opts findOptions) ([]findResult, bool, error) {
	var results []findResult

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

	// If searching both types, apply in-memory pagination
	if searchBoth && opts.limit > 0 {
		// For mixed results, sort by ID for consistent pagination
		// (keeping original order since queries already returned ordered results)
		if len(results) > opts.limit {
			hasMore = true
			results = results[:opts.limit]
		}
	}

	return results, hasMore, nil
}

func findTasks(database *db.DB, opts findOptions, skipPagination bool) ([]findResult, bool, error) {
	// Build cursor pagination (only if not mixing with containers)
	var pag *cursor.ApplyResult
	var err error
	if !skipPagination {
		pag, err = cursor.Apply(opts.cursor, cursor.ApplyOptions{
			SortFields: []string{"updated_at"},
			SQLFields:  []string{"t.updated_at"},
			Descending: []bool{true},
			IDField:    "t.id",
			Limit:      opts.limit,
		})
		if err != nil {
			return nil, false, err
		}
	}

	query := `
		SELECT t.uuid, t.id, t.slug, t.title, t.state, t.priority, t.due_at, t.etag,
		       cp.path || '/' || t.slug AS path, t.updated_at
		FROM tasks t
		JOIN v_container_paths cp ON cp.uuid = t.project_uuid
		WHERE 1=1
	`
	args := []interface{}{}

	// Filter by state (default: exclude archived)
	if opts.state != "" {
		query += " AND t.state = ?"
		args = append(args, opts.state)
	} else {
		// Default: exclude archived
		query += " AND t.state != 'archived'"
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
				pathConditions = append(pathConditions, "(cp.path || '/' || t.slug) LIKE ? || '%'")
				args = append(args, p)
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
		query += " ORDER BY t.updated_at DESC"
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
	defer rows.Close()

	var results []findResult
	for rows.Next() {
		var r findResult
		var state, dueAt sql.NullString
		var priority sql.NullInt64

		err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &state, &priority, &dueAt, &r.ETag, &r.Path, &r.UpdatedAt)
		if err != nil {
			return nil, false, fmt.Errorf("scan failed: %w", err)
		}

		r.Type = "task"
		if state.Valid {
			r.State = &state.String
		}
		if priority.Valid {
			p := int(priority.Int64)
			r.Priority = &p
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
			SortFields: []string{"path"},
			SQLFields:  []string{"cp.path"},
			Descending: []bool{false}, // ASC
			IDField:    "c.id",
			Limit:      opts.limit,
		})
		if err != nil {
			return nil, false, err
		}
	}

	query := `
		SELECT c.uuid, c.id, c.slug, COALESCE(c.title, c.slug) as title, c.etag,
		       cp.path
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
				pathConditions = append(pathConditions, "cp.path LIKE ? || '%'")
				args = append(args, p)
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
		query += " ORDER BY cp.path"
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
	defer rows.Close()

	var results []findResult
	for rows.Next() {
		var r findResult

		err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &r.ETag, &r.Path)
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
