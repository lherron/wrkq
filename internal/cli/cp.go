package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/attach"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/spf13/cobra"
)

var cpCmd = &cobra.Command{
	Use:   "cp <src>... <dst>",
	Short: "Copy tasks",
	Long: `Copy one or more tasks to a destination container.

Creates duplicate tasks with new IDs. Use --with-attachments to also copy attachment files.`,
	Args: cobra.MinimumNArgs(2),
	RunE: runCp,
}

var (
	cpWithAttachments bool
	cpShallow         bool
	cpDryRun          bool
	cpYes             bool
)

func init() {
	rootCmd.AddCommand(cpCmd)
	cpCmd.Flags().BoolVar(&cpWithAttachments, "with-attachments", false, "Copy attachment files")
	cpCmd.Flags().BoolVar(&cpShallow, "shallow", false, "Don't copy attachments (default)")
	cpCmd.Flags().BoolVar(&cpDryRun, "dry-run", false, "Show what would be copied without applying")
	cpCmd.Flags().BoolVar(&cpYes, "yes", false, "Skip confirmation prompts")
}

func runCp(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Get actor from --as flag or config
	actorIdentifier := cmd.Flag("as").Value.String()
	if actorIdentifier == "" {
		actorIdentifier = cfg.GetActorID()
	}
	if actorIdentifier == "" {
		return fmt.Errorf("no actor configured (set TODO_ACTOR, TODO_ACTOR_ID, or use --as flag)")
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Resolve actor
	resolver := actors.NewResolver(database.DB)
	actorUUID, err := resolver.Resolve(actorIdentifier)
	if err != nil {
		return fmt.Errorf("failed to resolve actor: %w", err)
	}

	// Split sources and destination
	sources := args[:len(args)-1]
	dst := args[len(args)-1]

	// Resolve destination container
	dstContainerUUID, err := resolveContainer(database, dst)
	if err != nil {
		return fmt.Errorf("destination must be an existing container: %w", err)
	}

	// Copy each source task
	for _, src := range sources {
		if err := copyTask(cmd, database, cfg, actorUUID, src, dstContainerUUID, dst); err != nil {
			return err
		}
	}

	return nil
}

func copyTask(cmd *cobra.Command, database *db.DB, cfg *config.Config, actorUUID, src, dstContainerUUID, dstPath string) error {
	// Resolve source task
	srcTaskUUID, srcPath, err := resolveTask(database, src)
	if err != nil {
		return fmt.Errorf("source must be a task: %w", err)
	}

	// Get source task details
	var slug, title, state, body string
	var priority int
	var startAt, dueAt, labels, completedAt, archivedAt sql.NullString
	var projectUUID string

	err = database.QueryRow(`
		SELECT slug, title, project_uuid, state, priority,
		       start_at, due_at, labels, body,
		       completed_at, archived_at
		FROM tasks WHERE uuid = ?
	`, srcTaskUUID).Scan(
		&slug, &title, &projectUUID, &state, &priority,
		&startAt, &dueAt, &labels, &body,
		&completedAt, &archivedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to get source task: %w", err)
	}

	// Generate new slug if there's a conflict
	newSlug := slug
	var existingCount int
	err = database.QueryRow(`
		SELECT COUNT(*) FROM tasks WHERE project_uuid = ? AND slug = ?
	`, dstContainerUUID, slug).Scan(&existingCount)
	if err != nil {
		return fmt.Errorf("failed to check slug uniqueness: %w", err)
	}

	if existingCount > 0 {
		// Append number to make unique
		for i := 2; ; i++ {
			candidateSlug := fmt.Sprintf("%s-%d", slug, i)
			err = database.QueryRow(`
				SELECT COUNT(*) FROM tasks WHERE project_uuid = ? AND slug = ?
			`, dstContainerUUID, candidateSlug).Scan(&existingCount)
			if err != nil {
				return fmt.Errorf("failed to check slug uniqueness: %w", err)
			}
			if existingCount == 0 {
				newSlug = candidateSlug
				break
			}
		}
	}

	// Validate the new slug
	normalizedSlug, err := paths.NormalizeSlug(newSlug)
	if err != nil {
		return fmt.Errorf("invalid slug %q: %w", newSlug, err)
	}

	if cpDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Would copy task %s -> %s/%s\n", srcPath, dstPath, normalizedSlug)
		return nil
	}

	// Begin transaction
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Insert new task
	result, err := tx.Exec(`
		INSERT INTO tasks (id, slug, title, project_uuid, state, priority,
		                   start_at, due_at, labels, body,
		                   created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, normalizedSlug, title, dstContainerUUID, state, priority,
		startAt, dueAt, labels, body,
		actorUUID, actorUUID)
	if err != nil {
		return fmt.Errorf("failed to create task copy: %w", err)
	}

	// Get the new task's UUID and ID
	rowID, _ := result.LastInsertId()
	var newTaskUUID, newTaskID string
	err = tx.QueryRow(`
		SELECT uuid, id FROM tasks WHERE rowid = ?
	`, rowID).Scan(&newTaskUUID, &newTaskID)
	if err != nil {
		return fmt.Errorf("failed to get new task ID: %w", err)
	}

	// Log creation event
	eventWriter := events.NewWriter(database.DB)
	payload := map[string]interface{}{
		"slug":        normalizedSlug,
		"title":       title,
		"state":       state,
		"copied_from": srcTaskUUID,
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadStr := string(payloadJSON)

	var etag int64 = 1 // New tasks start with etag 1
	event := &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &newTaskUUID,
		EventType:    "task.created",
		ETag:         &etag,
		Payload:      &payloadStr,
	}

	if err := eventWriter.LogEvent(tx, event); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	// Copy attachments if requested
	if cpWithAttachments && !cpShallow {
		if err := copyAttachments(tx, database, cfg, actorUUID, srcTaskUUID, newTaskUUID); err != nil {
			return fmt.Errorf("failed to copy attachments: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Copied task: %s -> %s/%s (%s)\n", srcPath, dstPath, normalizedSlug, newTaskID)
	return nil
}

func copyAttachments(tx *sql.Tx, database *db.DB, cfg *config.Config, actorUUID, srcTaskUUID, dstTaskUUID string) error {
	// Query source task's attachments
	rows, err := tx.Query(`
		SELECT uuid, filename, relative_path, mime_type, size_bytes, checksum
		FROM attachments WHERE task_uuid = ?
	`, srcTaskUUID)
	if err != nil {
		return err
	}
	defer rows.Close()

	var copiedCount int
	for rows.Next() {
		var srcAttUUID, filename, srcRelPath string
		var mimeType, checksum sql.NullString
		var sizeBytes int64

		err := rows.Scan(&srcAttUUID, &filename, &srcRelPath, &mimeType, &sizeBytes, &checksum)
		if err != nil {
			return err
		}

		// Create new relative path for destination task
		dstRelPath := attach.RelativePath(dstTaskUUID, filename)

		// Ensure destination directory exists
		if err := attach.EnsureTaskDir(cfg.AttachDir, dstTaskUUID); err != nil {
			return fmt.Errorf("failed to create destination attachment directory: %w", err)
		}

		// Copy the actual file
		srcAbsPath := attach.AbsolutePath(cfg.AttachDir, srcRelPath)
		dstAbsPath := attach.AbsolutePath(cfg.AttachDir, dstRelPath)

		newSize, newChecksum, err := attach.CopyFile(srcAbsPath, dstAbsPath)
		if err != nil {
			return fmt.Errorf("failed to copy attachment file %s: %w", filename, err)
		}

		// Insert attachment metadata
		_, err = tx.Exec(`
			INSERT INTO attachments (id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum, created_by_actor_uuid)
			VALUES ('', ?, ?, ?, ?, ?, ?, ?)
		`, dstTaskUUID, filename, dstRelPath, mimeType, newSize, newChecksum, actorUUID)
		if err != nil {
			return fmt.Errorf("failed to insert attachment metadata: %w", err)
		}

		copiedCount++
	}

	if err := rows.Err(); err != nil {
		return err
	}

	return nil
}
