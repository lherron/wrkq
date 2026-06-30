package cli

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
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
	commentLsJSON           bool
	commentLsNDJSON         bool
	commentLsYAML           bool
	commentLsTSV            bool
	commentLsPorcelain      bool
	commentLsIncludeDeleted bool
	commentLsLimit          int
	commentLsCursor         string
	commentLsFields         string
	commentLsSort           string
	commentLsReverse        bool
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
	defer func() { _ = database.Close() }()

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
		taskRef := strings.TrimPrefix(taskArg, "t:")
		taskRef = applyProjectRootToSelector(cfg, taskRef, false)

		// Resolve task
		taskUUID, taskID, err := selectors.ResolveTask(database, taskRef)
		if err != nil {
			return fmt.Errorf("failed to resolve task %s: %w", taskArg, err)
		}

		// Query comments with SQL-based pagination
		query := `
			SELECT c.uuid, c.id, c.task_uuid, c.body, c.meta, c.etag,
			       c.created_at, c.updated_at, c.deleted_at,
			       c.created_by_principal_ref, c.created_by_scope_ref,
			       c.deleted_by_principal_ref, c.deleted_by_scope_ref,
			       t.id as task_id
			FROM comments c
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
			var uuid, id, taskUUID, body, createdAt string
			var taskIDStr string
			var meta, updatedAt, deletedAt sql.NullString
			var createdByPrincipalRef, createdByScopeRef sql.NullString
			var deletedByPrincipalRef, deletedByScopeRef sql.NullString
			var etag int64

			err := rows.Scan(&uuid, &id, &taskUUID, &body, &meta, &etag,
				&createdAt, &updatedAt, &deletedAt,
				&createdByPrincipalRef, &createdByScopeRef,
				&deletedByPrincipalRef, &deletedByScopeRef,
				&taskIDStr)
			if err != nil {
				_ = rows.Close()
				return fmt.Errorf("failed to scan comment: %w", err)
			}

			comment := map[string]interface{}{
				"uuid":       uuid,
				"id":         id,
				"task_uuid":  taskUUID,
				"task_id":    taskIDStr,
				"body":       body,
				"etag":       etag,
				"created_at": createdAt,
			}
			if createdByPrincipalRef.Valid {
				comment["created_by_principal_ref"] = createdByPrincipalRef.String
			}
			if createdByScopeRef.Valid {
				comment["created_by_scope_ref"] = createdByScopeRef.String
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
			if deletedByPrincipalRef.Valid {
				comment["deleted_by_principal_ref"] = deletedByPrincipalRef.String
			}
			if deletedByScopeRef.Valid {
				comment["deleted_by_scope_ref"] = deletedByScopeRef.String
			}

			allComments = append(allComments, comment)
		}
		_ = rows.Close()

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

	sel, err := resolveOutputMode(cmd, cfg, outputShapeList, outputResolveOptions{
		Allow:      []outputMode{outputModeTable, outputModeHuman, outputModeJSON, outputModeNDJSON, outputModeYAML, outputModeTSV},
		DefaultTTY: outputModeTable,
	})
	if err != nil {
		return err
	}
	if cmd.Flag("json") == nil {
		switch {
		case (commentLsJSON && commentLsNDJSON) || (commentLsJSON && commentLsYAML) || (commentLsJSON && commentLsTSV) ||
			(commentLsNDJSON && commentLsYAML) || (commentLsNDJSON && commentLsTSV) || (commentLsYAML && commentLsTSV):
			return fmt.Errorf("choose only one output mode")
		case commentLsJSON:
			sel = outputSelection{Mode: outputModeJSON, Stable: commentLsPorcelain}
		case commentLsNDJSON:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: commentLsPorcelain}
		case commentLsYAML:
			sel = outputSelection{Mode: outputModeYAML, Stable: commentLsPorcelain}
		case commentLsTSV:
			sel = outputSelection{Mode: outputModeTSV, Stable: commentLsPorcelain}
		case commentLsPorcelain:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: true}
		default:
			sel = outputSelection{Mode: outputModeTable}
		}
	}

	if sel.Stable && nextCursorStr != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", nextCursorStr)
	}

	headers := []string{"ID", "Task", "Author", "Created", "Body Preview"}
	var rowsData [][]string
	for _, comment := range allComments {
		body := comment["body"].(string)
		// Truncate body for table view
		bodyPreview := strings.ReplaceAll(body, "\n", " ")
		if len(bodyPreview) > 50 {
			bodyPreview = bodyPreview[:47] + "..."
		}

		author := ""
		if ref, ok := comment["created_by_principal_ref"].(string); ok {
			author = attribution.PrincipalHandle(ref)
		}

		rowsData = append(rowsData, []string{
			comment["id"].(string),
			comment["task_id"].(string),
			author,
			comment["created_at"].(string),
			bodyPreview,
		})
	}

	switch sel.Mode {
	case outputModeJSON:
		return writeJSONOutput(cmd.OutOrStdout(), sel, allComments)
	case outputModeNDJSON:
		return writeNDJSONOutput(cmd.OutOrStdout(), allComments)
	case outputModeYAML:
		return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatYAML}).RenderYAML(allComments)
	case outputModeTSV:
		return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatTSV}).RenderTSV(headers, rowsData)
	}

	renderer := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Porcelain: sel.Stable,
	})
	return renderer.RenderTable(headers, rowsData)
}
