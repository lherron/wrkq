package cli

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/config"
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
  todo find                                    # Find all non-archived items
  todo find portal/**                          # Find items under portal
  todo find -type t --state open              # Find open tasks
  todo find --slug-glob 'login-*'              # Find items with slug matching pattern
  todo find --due-before 2025-12-01            # Find tasks due before date
  todo find --state open --due-after 2025-11-01 --json
`,
	RunE: runFind,
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

func runFind(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Build query based on filters
	results, err := executeFindQuery(database, findOptions{
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

	// Apply pagination
	var currentCursor *cursor.Cursor
	if findCursor != "" {
		c, err := cursor.Decode(findCursor)
		if err != nil {
			return fmt.Errorf("invalid cursor: %w", err)
		}
		currentCursor = c

		// Filter results based on cursor (updated_at DESC, id DESC)
		var filtered []findResult
		for _, result := range results {
			// For tasks, we sort by updated_at; for containers, by path
			// Simple approach: filter by ID as tie-breaker
			if result.ID > currentCursor.LastID {
				filtered = append(filtered, result)
			}
		}
		results = filtered
	}

	// Apply limit and generate next cursor
	var nextCursor *cursor.Cursor
	if findLimit > 0 && len(results) > findLimit {
		// Create cursor from the last entry we'll return
		lastEntry := results[findLimit-1]
		// Use updated_at as sort field for tasks (simplified for now)
		nextCursor, _ = cursor.NewCursor(
			[]string{"id"}, // Simplified: just use ID for now
			[]interface{}{lastEntry.ID},
			lastEntry.ID,
		)
		results = results[:findLimit]
	}

	// Output next_cursor to stderr in porcelain mode
	if findPorcelain && nextCursor != nil {
		encoded, err := nextCursor.Encode()
		if err == nil {
			fmt.Fprintf(os.Stderr, "next_cursor=%s\n", encoded)
		}
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
	Type     string  `json:"type"`      // "task" or "container"
	UUID     string  `json:"uuid"`
	ID       string  `json:"id"`
	Slug     string  `json:"slug"`
	Title    string  `json:"title"`
	Path     string  `json:"path"`
	State    *string `json:"state,omitempty"`    // tasks only
	Priority *int    `json:"priority,omitempty"` // tasks only
	DueAt    *string `json:"due_at,omitempty"`   // tasks only
	ETag     int64   `json:"etag"`
}

func executeFindQuery(database *db.DB, opts findOptions) ([]findResult, error) {
	var results []findResult

	// Determine what to search
	searchTasks := opts.typeFilter == "" || opts.typeFilter == "t"
	searchContainers := opts.typeFilter == "" || opts.typeFilter == "p"

	// Search tasks
	if searchTasks {
		tasks, err := findTasks(database, opts)
		if err != nil {
			return nil, fmt.Errorf("finding tasks: %w", err)
		}
		results = append(results, tasks...)
	}

	// Search containers
	if searchContainers {
		containers, err := findContainers(database, opts)
		if err != nil {
			return nil, fmt.Errorf("finding containers: %w", err)
		}
		results = append(results, containers...)
	}

	// Don't apply limit here - let caller do it after checking for more results
	return results, nil
}

func findTasks(database *db.DB, opts findOptions) ([]findResult, error) {
	query := `
		SELECT t.uuid, t.id, t.slug, t.title, t.state, t.priority, t.due_at, t.etag,
		       cp.path || '/' || t.slug AS path
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
			return nil, fmt.Errorf("invalid due-before date: %w", err)
		}
		query += " AND t.due_at IS NOT NULL AND t.due_at < ?"
		args = append(args, dueBeforeTime.Format(time.RFC3339))
	}

	if opts.dueAfter != "" {
		dueAfterTime, err := time.Parse("2006-01-02", opts.dueAfter)
		if err != nil {
			return nil, fmt.Errorf("invalid due-after date: %w", err)
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

	query += " ORDER BY t.updated_at DESC"

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var results []findResult
	for rows.Next() {
		var r findResult
		var state, dueAt sql.NullString
		var priority sql.NullInt64

		err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &state, &priority, &dueAt, &r.ETag, &r.Path)
		if err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
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

	return results, rows.Err()
}

func findContainers(database *db.DB, opts findOptions) ([]findResult, error) {
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

	query += " ORDER BY cp.path"

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var results []findResult
	for rows.Next() {
		var r findResult

		err := rows.Scan(&r.UUID, &r.ID, &r.Slug, &r.Title, &r.ETag, &r.Path)
		if err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}

		r.Type = "container"
		results = append(results, r)
	}

	return results, rows.Err()
}
