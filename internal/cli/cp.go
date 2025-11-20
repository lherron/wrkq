package cli

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var cpCmd = &cobra.Command{
	Use:   "cp <source>... <destination>",
	Short: "Copy tasks and containers",
	Args:  cobra.MinimumNArgs(2),
	RunE:  runCp,
}

var (
	cpDryRun          bool
	cpJobs            int
	cpContinueOnError bool
	cpWithAttachments bool
	cpShallow         bool
	cpRecursive       bool
	cpOverwrite       bool
	cpYes             bool
	cpNullglob        bool
	cpIfMatch         int64
	cpJSON            bool
	cpNDJSON          bool
	cpPorcelain       bool
	cpOne             bool
	cpZero            bool
)

func init() {
	rootCmd.AddCommand(cpCmd)
	cpCmd.Flags().BoolVar(&cpDryRun, "dry-run", false, "Show what would be copied")
	cpCmd.Flags().IntVarP(&cpJobs, "jobs", "j", 1, "Parallel workers")
	cpCmd.Flags().BoolVar(&cpContinueOnError, "continue-on-error", false, "Continue on errors")
	cpCmd.Flags().BoolVar(&cpWithAttachments, "with-attachments", false, "Copy attachment files")
	cpCmd.Flags().BoolVar(&cpShallow, "shallow", false, "Skip attachments entirely")
	cpCmd.Flags().BoolVarP(&cpRecursive, "recursive", "r", false, "Copy containers recursively")
	cpCmd.Flags().BoolVar(&cpOverwrite, "overwrite", false, "Overwrite existing tasks")
	cpCmd.Flags().BoolVar(&cpYes, "yes", false, "Skip confirmation prompts")
	cpCmd.Flags().BoolVar(&cpNullglob, "nullglob", false, "Zero matches is not an error")
	cpCmd.Flags().Int64Var(&cpIfMatch, "if-match", 0, "Conditional copy based on source etag")
	cpCmd.Flags().BoolVar(&cpJSON, "json", false, "Output JSON")
	cpCmd.Flags().BoolVar(&cpNDJSON, "ndjson", false, "Output NDJSON")
	cpCmd.Flags().BoolVar(&cpPorcelain, "porcelain", false, "Machine-readable output")
	cpCmd.Flags().BoolVar(&cpOne, "1", false, "One per line")
	cpCmd.Flags().BoolVar(&cpZero, "0", false, "NUL-separated output")
}

type copyResult struct {
	SourceID      string `json:"source_id"`
	SourceUUID    string `json:"source_uuid"`
	DestID        string `json:"dest_id"`
	DestUUID      string `json:"dest_uuid"`
	DestPath      string `json:"dest_path"`
	Attachments   int    `json:"attachments_copied,omitempty"`
	WithFiles     bool   `json:"with_files,omitempty"`
}

func runCp(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	actorIdentifier := cmd.Flag("as").Value.String()
	if actorIdentifier == "" {
		actorIdentifier = cfg.GetActorID()
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer database.Close()

	resolver := actors.NewResolver(database.DB)
	actorUUID, err := resolver.Resolve(actorIdentifier)
	if err != nil {
		return err
	}

	// Validate mutually exclusive flags
	if cpWithAttachments && cpShallow {
		return fmt.Errorf("--with-attachments and --shallow are mutually exclusive")
	}

	sources := args[:len(args)-1]
	destination := args[len(args)-1]

	// Handle stdin
	if len(sources) == 1 && sources[0] == "-" {
		scanner := bufio.NewScanner(cmd.InOrStdin())
		sources = nil
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				sources = append(sources, line)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
	}

	// Resolve destination container
	destUUID, err := resolveDestinationContainer(database, destination)
	if err != nil {
		if !cpNullglob {
			return err
		}
		return nil
	}

	// Resolve source tasks
	var sourceTasks []string
	for _, src := range sources {
		taskUUID, _, err := resolveTask(database, src)
		if err != nil {
			if !cpNullglob {
				return err
			}
			continue
		}
		sourceTasks = append(sourceTasks, taskUUID)
	}

	if len(sourceTasks) == 0 {
		if !cpNullglob {
			return fmt.Errorf("no tasks found to copy")
		}
		return nil
	}

	// Dry run output
	if cpDryRun {
		return showCopyPlan(cmd, database, sourceTasks, destUUID)
	}

	// Confirmation prompt
	if !cpYes && len(sourceTasks) > 5 {
		fmt.Fprintf(cmd.ErrOrStderr(), "Copy %d tasks? [y/N] ", len(sourceTasks))
		reader := bufio.NewReader(cmd.InOrStdin())
		response, _ := reader.ReadString('\n')
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(response)), "y") {
			return fmt.Errorf("aborted")
		}
	}

	// Execute copy operation
	op := &bulk.Operation{
		Jobs:            cpJobs,
		ContinueOnError: cpContinueOnError,
		ShowProgress:    !cpJSON && !cpNDJSON && !cpPorcelain,
	}

	var results []copyResult
	result := op.Execute(sourceTasks, func(taskUUID string) error {
		res, err := copyTask(database, cfg.AttachDir, actorUUID, taskUUID, destUUID)
		if err == nil && res != nil {
			results = append(results, *res)
		}
		return err
	})

	// Output results
	if cpJSON {
		return render.RenderJSON(results, false)
	}
	if cpNDJSON {
		// Convert to []interface{} for NDJSON rendering
		items := make([]interface{}, len(results))
		for i, r := range results {
			items[i] = r
		}
		return render.RenderNDJSON(items)
	}
	if cpPorcelain || cpOne || cpZero {
		delimiter := "\n"
		if cpZero {
			delimiter = "\x00"
		}
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s%s", r.DestID, delimiter)
		}
		return nil
	}

	result.PrintSummary(cmd.OutOrStdout())

	if result.Failed > 0 {
		if cpContinueOnError {
			os.Exit(5) // Partial success
		}
		os.Exit(1)
	}

	return nil
}

func resolveDestinationContainer(database *db.DB, dest string) (string, error) {
	if id.IsFriendlyID(dest) && dest[0] == 'P' {
		var uuid string
		err := database.QueryRow("SELECT uuid FROM containers WHERE id = ?", dest).Scan(&uuid)
		if err == nil {
			return uuid, nil
		}
	}

	segments := paths.SplitPath(dest)
	var parentUUID *string

	for _, segment := range segments {
		slug, _ := paths.NormalizeSlug(segment)
		var uuid string

		if parentUUID == nil {
			database.QueryRow("SELECT uuid FROM containers WHERE slug = ? AND parent_uuid IS NULL", slug).Scan(&uuid)
		} else {
			database.QueryRow("SELECT uuid FROM containers WHERE slug = ? AND parent_uuid = ?", slug, *parentUUID).Scan(&uuid)
		}

		if uuid == "" {
			return "", fmt.Errorf("container not found: %s", dest)
		}
		parentUUID = &uuid
	}

	return *parentUUID, nil
}

func showCopyPlan(cmd *cobra.Command, database *db.DB, sourceTasks []string, destUUID string) error {
	var totalAttachments int
	var totalFiles int64

	fmt.Fprintf(cmd.OutOrStdout(), "Would copy %d task(s):\n\n", len(sourceTasks))

	for _, taskUUID := range sourceTasks {
		var sourceID, slug string
		err := database.QueryRow("SELECT id, slug FROM tasks WHERE uuid = ?", taskUUID).Scan(&sourceID, &slug)
		if err != nil {
			continue
		}

		var destPath string
		database.QueryRow("SELECT path FROM container_paths WHERE uuid = ?", destUUID).Scan(&destPath)

		fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s) → %s/%s (new)\n", sourceID, slug, destPath, slug)

		// Count attachments
		if !cpShallow {
			var count int
			var size sql.NullInt64
			database.QueryRow("SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM attachments WHERE task_uuid = ?", taskUUID).Scan(&count, &size)
			if count > 0 {
				totalAttachments += count
				if size.Valid {
					totalFiles += size.Int64
				}
			}
		}
	}

	if totalAttachments > 0 && !cpShallow {
		fmt.Fprintf(cmd.OutOrStdout(), "\nAttachments:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  %d file(s) (%.1f MB total)\n", totalAttachments, float64(totalFiles)/(1024*1024))
		if cpWithAttachments {
			fmt.Fprintf(cmd.OutOrStdout(), "  Files will be copied to new location\n")
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "  Only metadata will be copied (use --with-attachments to copy files)\n")
		}
	}

	return nil
}

func copyTask(database *db.DB, attachDir, actorUUID, sourceUUID, destUUID string) (*copyResult, error) {
	tx, err := database.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// Check etag if requested
	if cpIfMatch > 0 {
		var etag int64
		err = tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", sourceUUID).Scan(&etag)
		if err != nil {
			return nil, err
		}
		if etag != cpIfMatch {
			return nil, fmt.Errorf("etag mismatch: expected %d, got %d", cpIfMatch, etag)
		}
	}

	// Fetch source task
	var sourceID, slug, title, state string
	var priority int
	var body, labels *string
	var startAt, dueAt *string

	err = tx.QueryRow(`
		SELECT id, slug, title, state, priority, body, labels, start_at, due_at
		FROM tasks WHERE uuid = ?
	`, sourceUUID).Scan(&sourceID, &slug, &title, &state, &priority, &body, &labels, &startAt, &dueAt)
	if err != nil {
		return nil, err
	}

	// Check for existing task with same slug in destination
	var existingUUID string
	tx.QueryRow("SELECT uuid FROM tasks WHERE project_uuid = ? AND slug = ?", destUUID, slug).Scan(&existingUUID)

	if existingUUID != "" && !cpOverwrite {
		return nil, fmt.Errorf("task with slug '%s' already exists in destination container", slug)
	}

	var newUUID, newID string

	if cpOverwrite && existingUUID != "" {
		// Update existing task
		_, err = tx.Exec(`
			UPDATE tasks SET title = ?, state = ?, priority = ?, body = ?, labels = ?,
				start_at = ?, due_at = ?, updated_at = CURRENT_TIMESTAMP,
				updated_by_actor_uuid = ?, etag = etag + 1
			WHERE uuid = ?
		`, title, state, priority, body, labels, startAt, dueAt, actorUUID, existingUUID)
		if err != nil {
			return nil, err
		}

		newUUID = existingUUID
		tx.QueryRow("SELECT id FROM tasks WHERE uuid = ?", newUUID).Scan(&newID)
	} else {
		// Insert new task (omit id and uuid to let triggers generate them)
		result, err := tx.Exec(`
			INSERT INTO tasks (slug, title, project_uuid, state, priority, body, labels,
				start_at, due_at, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, slug, title, destUUID, state, priority, body, labels, startAt, dueAt, actorUUID, actorUUID)
		if err != nil {
			return nil, err
		}

		rowID, _ := result.LastInsertId()
		err = tx.QueryRow("SELECT uuid, id FROM tasks WHERE rowid = ?", rowID).Scan(&newUUID, &newID)
		if err != nil {
			return nil, err
		}
	}

	// Handle attachments
	var attachmentCount int
	if !cpShallow {
		attachmentCount, err = copyAttachments(tx, attachDir, sourceUUID, newUUID)
		if err != nil {
			return nil, err
		}
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payloadData := map[string]interface{}{
		"source_id":   sourceID,
		"source_uuid": sourceUUID,
	}
	if attachmentCount > 0 {
		payloadData["attachment_count"] = attachmentCount
		payloadData["with_files"] = cpWithAttachments
	}
	payloadJSON, _ := json.Marshal(payloadData)
	payloadStr := string(payloadJSON)

	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &newUUID,
		EventType:    "task.copied",
		Payload:      &payloadStr,
	}); err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Get destination path
	var destPath string
	database.QueryRow("SELECT path FROM task_paths WHERE uuid = ?", newUUID).Scan(&destPath)

	return &copyResult{
		SourceID:    sourceID,
		SourceUUID:  sourceUUID,
		DestID:      newID,
		DestUUID:    newUUID,
		DestPath:    destPath,
		Attachments: attachmentCount,
		WithFiles:   cpWithAttachments,
	}, nil
}

func copyAttachments(tx *sql.Tx, attachDir, sourceTaskUUID, destTaskUUID string) (int, error) {
	rows, err := tx.Query(`
		SELECT uuid, filename, relative_path, mime_type, size_bytes, checksum
		FROM attachments WHERE task_uuid = ?
	`, sourceTaskUUID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var attUUID, filename, relativePath, mimeType string
		var sizeBytes int64
		var checksum sql.NullString

		if err := rows.Scan(&attUUID, &filename, &relativePath, &mimeType, &sizeBytes, &checksum); err != nil {
			return count, err
		}

		// Always generate new relative path for destination task
		newRelativePath := fmt.Sprintf("tasks/%s/%s", destTaskUUID, filename)

		if cpWithAttachments {
			// Copy file to new location
			sourcePath := filepath.Join(attachDir, relativePath)
			destPath := filepath.Join(attachDir, newRelativePath)

			// Create destination directory
			if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
				return count, err
			}

			// Copy file
			sourceFile, err := os.Open(sourcePath)
			if err != nil {
				return count, err
			}
			defer sourceFile.Close()

			destFile, err := os.Create(destPath)
			if err != nil {
				return count, err
			}
			defer destFile.Close()

			if _, err := io.Copy(destFile, sourceFile); err != nil {
				return count, err
			}
		}
		// If not copying files, just insert metadata with new relative_path

		// Insert attachment metadata
		_, err = tx.Exec(`
			INSERT INTO attachments (id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum)
			VALUES ('', ?, ?, ?, ?, ?, ?)
		`, destTaskUUID, filename, newRelativePath, mimeType, sizeBytes, checksum)
		if err != nil {
			return count, err
		}

		count++
	}

	return count, rows.Err()
}
