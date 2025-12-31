package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/render"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var attachCmd = &cobra.Command{
	Use:   "attach",
	Short: "Manage task attachments",
	Long:  `Manage file attachments for tasks. Files are stored under attach_dir/tasks/<task_uuid>/`,
}

var attachLsCmd = &cobra.Command{
	Use:   "ls <task>",
	Short: "List attachments for a task",
	Args:  cobra.ExactArgs(1),
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runAttachLs),
}

var attachPutCmd = &cobra.Command{
	Use:   "put <task> <file|-|>",
	Short: "Attach a file to a task",
	Long: `Attach a file to a task. Use '-' to read from stdin.
Files are stored in attach_dir/tasks/<task_uuid>/ and survive task moves/renames.`,
	Args: cobra.ExactArgs(2),
	RunE: appctx.WithApp(appctx.WithActor(), runAttachPut),
}

var attachGetCmd = &cobra.Command{
	Use:   "get <attachment-id>",
	Short: "Get an attachment file",
	Long:  `Copy an attachment file to stdout or a specified path.`,
	Args:  cobra.ExactArgs(1),
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runAttachGet),
}

var attachRmCmd = &cobra.Command{
	Use:   "rm <attachment-id>...",
	Short: "Remove attachment(s)",
	Long:  `Remove attachment metadata and delete the file from disk.`,
	Args:  cobra.MinimumNArgs(1),
	RunE:  appctx.WithApp(appctx.WithActor(), runAttachRm),
}

var (
	attachLsJSON      bool
	attachLsNDJSON    bool
	attachLsPorcelain bool
	attachLsLimit     int
	attachLsCursor    string

	attachPutMime string
	attachPutName string

	attachGetAs string

	attachRmYes bool
)

func init() {
	rootCmd.AddCommand(attachCmd)
	attachCmd.AddCommand(attachLsCmd)
	attachCmd.AddCommand(attachPutCmd)
	attachCmd.AddCommand(attachGetCmd)
	attachCmd.AddCommand(attachRmCmd)

	// attach ls flags
	attachLsCmd.Flags().BoolVar(&attachLsJSON, "json", false, "Output as JSON")
	attachLsCmd.Flags().BoolVar(&attachLsNDJSON, "ndjson", false, "Output as NDJSON")
	attachLsCmd.Flags().BoolVar(&attachLsPorcelain, "porcelain", false, "Machine-readable output")
	attachLsCmd.Flags().IntVar(&attachLsLimit, "limit", 0, "Maximum number of results (0 = no limit)")
	attachLsCmd.Flags().StringVar(&attachLsCursor, "cursor", "", "Pagination cursor from previous page")

	// attach put flags
	attachPutCmd.Flags().StringVar(&attachPutMime, "mime", "", "MIME type (auto-detected if not specified)")
	attachPutCmd.Flags().StringVar(&attachPutName, "name", "", "Filename (defaults to basename of file)")

	// attach get flags
	attachGetCmd.Flags().StringVar(&attachGetAs, "as", "-", "Output path (use '-' for stdout)")

	// attach rm flags
	attachRmCmd.Flags().BoolVar(&attachRmYes, "yes", false, "Skip confirmation")
}

func runAttachLs(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Resolve task
	taskRef := applyProjectRootToSelector(app.Config, args[0], false)
	taskUUID, _, err := selectors.ResolveTask(database, taskRef)
	if err != nil {
		return err
	}

	// Build cursor pagination
	pag, err := cursor.Apply(attachLsCursor, cursor.ApplyOptions{
		SortFields: []string{"created_at"},
		SQLFields:  []string{"a.created_at"},
		Descending: []bool{false}, // ASC
		IDField:    "a.id",
		Limit:      attachLsLimit,
	})
	if err != nil {
		return err
	}

	// Query attachments with SQL-based pagination
	query := `
		SELECT a.uuid, a.id, a.filename, a.relative_path, a.mime_type, a.size_bytes,
		       a.checksum, a.created_at, ac.slug as created_by
		FROM attachments a
		LEFT JOIN actors ac ON a.created_by_actor_uuid = ac.uuid
		WHERE a.task_uuid = ?
	`
	queryArgs := []interface{}{taskUUID}

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
		return fmt.Errorf("failed to query attachments: %w", err)
	}
	defer rows.Close()

	attachments := []map[string]interface{}{}
	for rows.Next() {
		var uuid, id, filename, relativePath, createdAt string
		var mimeType, checksum, createdBy sql.NullString
		var sizeBytes int64

		err := rows.Scan(&uuid, &id, &filename, &relativePath, &mimeType, &sizeBytes,
			&checksum, &createdAt, &createdBy)
		if err != nil {
			return fmt.Errorf("failed to scan attachment: %w", err)
		}

		att := map[string]interface{}{
			"uuid":          uuid,
			"id":            id,
			"filename":      filename,
			"relative_path": relativePath,
			"size_bytes":    sizeBytes,
			"created_at":    createdAt,
		}
		if mimeType.Valid {
			att["mime_type"] = mimeType.String
		}
		if checksum.Valid {
			att["checksum"] = checksum.String
		}
		if createdBy.Valid {
			att["created_by"] = createdBy.String
		}

		attachments = append(attachments, att)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating attachments: %w", err)
	}

	// Check if there are more results (we requested limit+1)
	hasMore := false
	if attachLsLimit > 0 && len(attachments) > attachLsLimit {
		hasMore = true
		attachments = attachments[:attachLsLimit]
	}

	// Generate next cursor if there are more results
	var nextCursorStr string
	if hasMore && len(attachments) > 0 {
		lastAtt := attachments[len(attachments)-1]
		nextCursorStr, _ = cursor.BuildNextCursor(
			[]string{"created_at"},
			[]interface{}{lastAtt["created_at"].(string)},
			lastAtt["id"].(string),
		)
	}

	// Output next_cursor to stderr in porcelain mode
	if attachLsPorcelain && nextCursorStr != "" {
		fmt.Fprintf(os.Stderr, "next_cursor=%s\n", nextCursorStr)
	}

	// Output
	if attachLsJSON {
		return render.RenderJSON(attachments, false)
	}

	if attachLsNDJSON {
		// Convert to []interface{}
		items := make([]interface{}, len(attachments))
		for i, att := range attachments {
			items[i] = att
		}
		return render.RenderNDJSON(items)
	}

	// Table output
	headers := []string{"ID", "Filename", "Size", "MIME Type", "Created"}
	var rows_data [][]string
	for _, att := range attachments {
		sizeStr := fmt.Sprintf("%d", att["size_bytes"])
		mimeStr := ""
		if mime, ok := att["mime_type"]; ok {
			mimeStr = mime.(string)
		}
		rows_data = append(rows_data, []string{
			att["id"].(string),
			att["filename"].(string),
			sizeStr,
			mimeStr,
			att["created_at"].(string),
		})
	}

	// Use Renderer for table output
	renderer := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Porcelain: attachLsPorcelain,
	})
	return renderer.RenderTable(headers, rows_data)
}

func runAttachPut(app *appctx.App, cmd *cobra.Command, args []string) error {
	cfg := app.Config
	database := app.DB
	actorUUID := app.ActorUUID

	// Resolve task
	taskRef := applyProjectRootToSelector(app.Config, args[0], false)
	taskUUID, taskID, err := selectors.ResolveTask(database, taskRef)
	if err != nil {
		return err
	}

	srcPath := args[1]

	// Determine filename
	filename := attachPutName
	if filename == "" {
		if srcPath == "-" {
			return fmt.Errorf("--name is required when reading from stdin")
		}
		filename = filepath.Base(srcPath)
	}

	// Check if filename already exists for this task
	var existingCount int
	err = database.QueryRow(`
		SELECT COUNT(*) FROM attachments WHERE task_uuid = ? AND filename = ?
	`, taskUUID, filename).Scan(&existingCount)
	if err != nil {
		return fmt.Errorf("failed to check existing attachments: %w", err)
	}
	if existingCount > 0 {
		return fmt.Errorf("attachment with filename %q already exists for task %s", filename, taskID)
	}

	// Detect MIME type
	mimeType := attachPutMime
	if mimeType == "" {
		mimeType = attach.DetectMimeType(filename)
	}

	// Validate size if file (not stdin)
	if srcPath != "-" {
		size, err := attach.GetFileSize(srcPath)
		if err != nil {
			return err
		}
		if err := attach.ValidateSize(size, int64(cfg.AttachmentsMaxMB)); err != nil {
			return err
		}
	}

	// Ensure task directory exists
	if err := attach.EnsureTaskDir(cfg.AttachDir, taskUUID); err != nil {
		return err
	}

	// Determine destination path
	relativePath := attach.RelativePath(taskUUID, filename)
	absPath := attach.AbsolutePath(cfg.AttachDir, relativePath)

	// Copy file and compute checksum
	size, checksum, err := attach.CopyFile(srcPath, absPath)
	if err != nil {
		return err
	}

	// Validate size from actual copy
	if err := attach.ValidateSize(size, int64(cfg.AttachmentsMaxMB)); err != nil {
		// Clean up the file we just copied
		os.Remove(absPath)
		return err
	}

	// Insert attachment metadata
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO attachments (id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum, created_by_actor_uuid)
		VALUES ('', ?, ?, ?, ?, ?, ?, ?)
	`, taskUUID, filename, relativePath, mimeType, size, checksum, actorUUID)
	if err != nil {
		os.Remove(absPath) // Clean up file
		return fmt.Errorf("failed to insert attachment: %w", err)
	}

	// Get the attachment UUID and ID
	var attachUUID, attachID string
	lastID, _ := result.LastInsertId()
	err = tx.QueryRow(`
		SELECT uuid, id FROM attachments WHERE rowid = ?
	`, lastID).Scan(&attachUUID, &attachID)
	if err != nil {
		os.Remove(absPath)
		return fmt.Errorf("failed to get attachment ID: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payload := map[string]interface{}{
		"attachment_id": attachID,
		"filename":      filename,
		"size_bytes":    size,
		"mime_type":     mimeType,
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadStr := string(payloadJSON)

	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "attachment",
		ResourceUUID: &attachUUID,
		EventType:    "attachment.created",
		Payload:      &payloadStr,
	}

	if err := eventWriter.LogEvent(tx, event); err != nil {
		os.Remove(absPath)
		return fmt.Errorf("failed to log event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		os.Remove(absPath)
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Attached: %s (%s, %d bytes)\n", attachID, filename, size)
	return nil
}

func runAttachGet(app *appctx.App, cmd *cobra.Command, args []string) error {
	cfg := app.Config
	database := app.DB

	attachmentRef := args[0]

	// Resolve attachment (ID or UUID)
	var attachUUID, relativePath, filename string
	err := database.QueryRow(`
		SELECT uuid, relative_path, filename FROM attachments
		WHERE id = ? OR uuid = ?
	`, attachmentRef, attachmentRef).Scan(&attachUUID, &relativePath, &filename)
	if err == sql.ErrNoRows {
		return fmt.Errorf("attachment not found: %s", attachmentRef)
	}
	if err != nil {
		return fmt.Errorf("failed to resolve attachment: %w", err)
	}

	srcPath := attach.AbsolutePath(cfg.AttachDir, relativePath)
	dstPath := attachGetAs

	// If outputting to stdout, use dash
	if dstPath == "-" {
		dstPath = "-"
	}

	// Copy file
	_, _, err = attach.CopyFile(srcPath, dstPath)
	if err != nil {
		return fmt.Errorf("failed to copy attachment: %w", err)
	}

	if dstPath != "-" {
		fmt.Fprintf(cmd.OutOrStdout(), "Copied %s to %s\n", filename, dstPath)
	}

	return nil
}

func runAttachRm(app *appctx.App, cmd *cobra.Command, args []string) error {
	cfg := app.Config
	database := app.DB
	actorUUID := app.ActorUUID

	for _, attachmentRef := range args {
		// Resolve attachment
		var attachUUID, attachID, relativePath, filename string
		err := database.QueryRow(`
			SELECT uuid, id, relative_path, filename FROM attachments
			WHERE id = ? OR uuid = ?
		`, attachmentRef, attachmentRef).Scan(&attachUUID, &attachID, &relativePath, &filename)
		if err == sql.ErrNoRows {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: attachment not found: %s\n", attachmentRef)
			continue
		}
		if err != nil {
			return fmt.Errorf("failed to resolve attachment %s: %w", attachmentRef, err)
		}

		// Confirm deletion
		if !attachRmYes {
			fmt.Fprintf(cmd.OutOrStdout(), "Delete attachment %s (%s)? [y/N]: ", attachID, filename)
			var response string
			fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				continue
			}
		}

		// Delete file
		if err := attach.DeleteFile(cfg.AttachDir, relativePath); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: failed to delete file for %s: %v\n", attachID, err)
			// Continue to delete metadata anyway
		}

		// Delete metadata in transaction
		tx, err := database.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		_, err = tx.Exec(`DELETE FROM attachments WHERE uuid = ?`, attachUUID)
		if err != nil {
			return fmt.Errorf("failed to delete attachment metadata: %w", err)
		}

		// Log event
		eventWriter := events.NewWriter(database.DB)
		payload := map[string]interface{}{
			"attachment_id": attachID,
			"filename":      filename,
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)

		event := &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "attachment",
			ResourceUUID: &attachUUID,
			EventType:    "attachment.deleted",
			Payload:      &payloadStr,
		}

		if err := eventWriter.LogEvent(tx, event); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Deleted: %s (%s)\n", attachID, filename)
	}

	return nil
}
