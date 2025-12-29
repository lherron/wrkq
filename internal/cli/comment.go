package cli

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/render"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var commentCmd = &cobra.Command{
	Use:   "comment",
	Short: "Manage task comments",
	Long: `Manage comments on tasks. Comments are immutable, append-only notes that support
collaboration between humans and coding agents.`,
}

var commentLsCmd = &cobra.Command{
	Use:   "ls <task>...",
	Short: "List comments for task(s)",
	Long: `List comments attached to one or more tasks.
By default, only non-deleted comments are shown, ordered by created_at ascending.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runCommentLs,
}

var (
	commentLsJSON          bool
	commentLsNDJSON        bool
	commentLsYAML          bool
	commentLsTSV           bool
	commentLsPorcelain     bool
	commentLsIncludeDeleted bool
	commentLsLimit         int
	commentLsCursor        string
	commentLsFields        string
	commentLsSort          string
	commentLsReverse       bool
)

func init() {
	rootCmd.AddCommand(commentCmd)
	commentCmd.AddCommand(commentLsCmd)

	// comment ls flags
	commentLsCmd.Flags().BoolVar(&commentLsJSON, "json", false, "Output as JSON")
	commentLsCmd.Flags().BoolVar(&commentLsNDJSON, "ndjson", false, "Output as NDJSON")
	commentLsCmd.Flags().BoolVar(&commentLsYAML, "yaml", false, "Output as YAML")
	commentLsCmd.Flags().BoolVar(&commentLsTSV, "tsv", false, "Output as TSV")
	commentLsCmd.Flags().BoolVar(&commentLsPorcelain, "porcelain", false, "Machine-readable output")
	commentLsCmd.Flags().BoolVar(&commentLsIncludeDeleted, "include-deleted", false, "Include soft-deleted comments")
	commentLsCmd.Flags().IntVar(&commentLsLimit, "limit", 0, "Maximum number of results (0 = no limit)")
	commentLsCmd.Flags().StringVar(&commentLsCursor, "cursor", "", "Pagination cursor from previous page")
	commentLsCmd.Flags().StringVar(&commentLsFields, "fields", "", "Comma-separated fields to include")
	commentLsCmd.Flags().StringVar(&commentLsSort, "sort", "created_at", "Sort field (default: created_at)")
	commentLsCmd.Flags().BoolVar(&commentLsReverse, "reverse", false, "Reverse sort order")
}

func runCommentLs(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Determine sort field and direction
	sortField := commentLsSort
	if sortField == "" {
		sortField = "created_at"
	}
	descending := commentLsReverse

	// Build cursor pagination
	pag, err := cursor.Apply(commentLsCursor, cursor.ApplyOptions{
		SortFields: []string{sortField},
		SQLFields:  []string{"c." + sortField},
		Descending: []bool{descending},
		IDField:    "c.id",
		Limit:      commentLsLimit,
	})
	if err != nil {
		return err
	}

	allComments := []map[string]interface{}{}

	// For each task argument, resolve and list comments
	for _, taskArg := range args {
		// Remove t: prefix if present
		taskRef := taskArg
		if strings.HasPrefix(taskRef, "t:") {
			taskRef = taskRef[2:]
		}

		// Resolve task
		taskUUID, taskID, err := selectors.ResolveTask(database, taskRef)
		if err != nil {
			return fmt.Errorf("failed to resolve task %s: %w", taskArg, err)
		}

		// Query comments with SQL-based pagination
		query := `
			SELECT c.uuid, c.id, c.task_uuid, c.actor_uuid, c.body, c.meta, c.etag,
			       c.created_at, c.updated_at, c.deleted_at, c.deleted_by_actor_uuid,
			       a.slug as actor_slug, a.role as actor_role,
			       t.id as task_id
			FROM comments c
			LEFT JOIN actors a ON c.actor_uuid = a.uuid
			LEFT JOIN tasks t ON c.task_uuid = t.uuid
			WHERE c.task_uuid = ?
		`
		queryArgs := []interface{}{taskUUID}

		if !commentLsIncludeDeleted {
			query += " AND c.deleted_at IS NULL"
		}

		// Add cursor WHERE clause if present
		if pag.WhereClause != "" {
			query += " AND " + pag.WhereClause
			queryArgs = append(queryArgs, pag.Params...)
		}

		// Add ORDER BY
		query += " " + pag.OrderByClause

		// Add LIMIT
		if pag.LimitClause != "" {
			query += " " + pag.LimitClause
			queryArgs = append(queryArgs, *pag.LimitParam)
		}

		rows, err := database.Query(query, queryArgs...)
		if err != nil {
			return fmt.Errorf("failed to query comments for task %s: %w", taskID, err)
		}

		for rows.Next() {
			var uuid, id, taskUUID, actorUUID, body, createdAt string
			var actorSlug, actorRole, taskIDStr string
			var meta, updatedAt, deletedAt, deletedByActorUUID sql.NullString
			var etag int64

			err := rows.Scan(&uuid, &id, &taskUUID, &actorUUID, &body, &meta, &etag,
				&createdAt, &updatedAt, &deletedAt, &deletedByActorUUID,
				&actorSlug, &actorRole, &taskIDStr)
			if err != nil {
				rows.Close()
				return fmt.Errorf("failed to scan comment: %w", err)
			}

			comment := map[string]interface{}{
				"uuid":        uuid,
				"id":          id,
				"task_uuid":   taskUUID,
				"task_id":     taskIDStr,
				"actor_uuid":  actorUUID,
				"actor_slug":  actorSlug,
				"actor_role":  actorRole,
				"body":        body,
				"etag":        etag,
				"created_at":  createdAt,
			}

			if meta.Valid && meta.String != "" {
				comment["meta"] = meta.String
			}
			if updatedAt.Valid {
				comment["updated_at"] = updatedAt.String
			}
			if deletedAt.Valid {
				comment["deleted_at"] = deletedAt.String
			}
			if deletedByActorUUID.Valid {
				comment["deleted_by_actor_uuid"] = deletedByActorUUID.String
			}

			allComments = append(allComments, comment)
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			return fmt.Errorf("error iterating comments: %w", err)
		}
	}

	// Check if there are more results (we requested limit+1)
	hasMore := false
	if commentLsLimit > 0 && len(allComments) > commentLsLimit {
		hasMore = true
		allComments = allComments[:commentLsLimit]
	}

	// Generate next cursor if there are more results
	var nextCursorStr string
	if hasMore && len(allComments) > 0 {
		lastComment := allComments[len(allComments)-1]
		nextCursorStr, _ = cursor.BuildNextCursor(
			[]string{sortField},
			[]interface{}{lastComment[sortField].(string)},
			lastComment["id"].(string),
		)
	}

	// Output next_cursor to stderr in porcelain mode
	if commentLsPorcelain && nextCursorStr != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", nextCursorStr)
	}

	// Output
	if commentLsJSON {
		return render.RenderJSON(allComments, false)
	}

	if commentLsNDJSON {
		items := make([]interface{}, len(allComments))
		for i, c := range allComments {
			items[i] = c
		}
		return render.RenderNDJSON(items)
	}

	if commentLsYAML {
		// YAML output not yet implemented, fall back to JSON
		return render.RenderJSON(allComments, false)
	}

	if commentLsTSV {
		// TSV output not yet implemented, fall back to table
		// Continue to table output below
	}

	// Table output
	headers := []string{"ID", "Task", "Actor", "Created", "Body Preview"}
	var rowsData [][]string
	for _, comment := range allComments {
		body := comment["body"].(string)
		// Truncate body for table view
		bodyPreview := strings.ReplaceAll(body, "\n", " ")
		if len(bodyPreview) > 50 {
			bodyPreview = bodyPreview[:47] + "..."
		}

		rowsData = append(rowsData, []string{
			comment["id"].(string),
			comment["task_id"].(string),
			comment["actor_slug"].(string),
			comment["created_at"].(string),
			bodyPreview,
		})
	}

	renderer := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Porcelain: commentLsPorcelain,
	})
	return renderer.RenderTable(headers, rowsData)
}
