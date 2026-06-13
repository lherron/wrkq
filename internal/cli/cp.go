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

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/webhooks"
	"github.com/spf13/cobra"
)

var cpCmd = &cobra.Command{
	Use:   "cp <source>... <destination>",
	Short: "Copy tasks and containers",
	Args:  cobra.MinimumNArgs(2),
	RunE:  appctx.WithApp(appctx.WithActor(), runCp),
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
	SourceID    string `json:"source_id"`
	SourceUUID  string `json:"source_uuid"`
	DestID      string `json:"dest_id"`
	DestUUID    string `json:"dest_uuid"`
	DestPath    string `json:"dest_path"`
	Attachments int    `json:"attachments_copied,omitempty"`
	WithFiles   bool   `json:"with_files,omitempty"`
}

func runCp(app *appctx.App, cmd *cobra.Command, args []string) error {
	cfg := app.Config
	database := app.DB
	attr := app.Attribution()

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

	for i, src := range sources {
		sources[i] = applyProjectRootToSelector(app.Config, src, false)
	}
	destination = applyProjectRootToSelector(app.Config, destination, false)

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
		taskUUID, _, err := selectors.ResolveTask(database, src)
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
	machineJSON := cpJSON || (!isStdoutTTY(cmd.OutOrStdout()) && !cpNDJSON && !cpPorcelain && !cpOne && !cpZero)
	op := &bulk.Operation{
		Jobs:            cpJobs,
		ContinueOnError: cpContinueOnError,
		ShowProgress:    isStdoutTTY(cmd.OutOrStdout()) && !cpJSON && !cpNDJSON && !cpPorcelain,
	}

	results := []copyResult{}
	result := op.Execute(sourceTasks, func(taskUUID string) error {
		res, err := copyTaskWithAttribution(database, cfg.AttachDir, attr, taskUUID, destUUID)
		if err == nil && res != nil {
			results = append(results, *res)
		}
		return err
	})

	// Output results
	if machineJSON {
		return writeJSONOutput(cmd.OutOrStdout(), outputSelection{}, results)
	}
	if cpNDJSON {
		return writeNDJSONOutput(cmd.OutOrStdout(), results)
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
	// Use shared resolver for container resolution (handles friendly IDs, UUIDs, and paths)
	uuid, _, err := selectors.ResolveContainer(database, dest)
	if err != nil {
		return "", err
	}
	return uuid, nil
}

func showCopyPlan(cmd *cobra.Command, database *db.DB, sourceTasks []string, destUUID string) error {
	var totalAttachments int
	var totalFiles int64
	if !isStdoutTTY(cmd.OutOrStdout()) {
		entries := make([]map[string]interface{}, 0, len(sourceTasks))
		for _, taskUUID := range sourceTasks {
			var sourceID, slug string
			if err := database.QueryRow("SELECT id, slug FROM tasks WHERE uuid = ?", taskUUID).Scan(&sourceID, &slug); err != nil {
				continue
			}
			var destPath string
			_ = database.QueryRow("SELECT path FROM container_paths WHERE uuid = ?", destUUID).Scan(&destPath)
			entry := map[string]interface{}{
				"source_id":   sourceID,
				"source_uuid": taskUUID,
				"dest_path":   destPath + "/" + slug,
				"would_copy":  true,
			}
			entries = append(entries, entry)
		}
		return writeJSONOutput(cmd.OutOrStdout(), outputSelection{}, map[string]interface{}{
			"dry_run": true,
			"copies":  entries,
		})
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Would copy %d task(s):\n\n", len(sourceTasks))

	for _, taskUUID := range sourceTasks {
		var sourceID, slug string
		err := database.QueryRow("SELECT id, slug FROM tasks WHERE uuid = ?", taskUUID).Scan(&sourceID, &slug)
		if err != nil {
			continue
		}

		var destPath string
		_ = database.QueryRow("SELECT path FROM container_paths WHERE uuid = ?", destUUID).Scan(&destPath)

		fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s) → %s/%s (new)\n", sourceID, slug, destPath, slug)

		// Count attachments
		if !cpShallow {
			var count int
			var size sql.NullInt64
			_ = database.QueryRow("SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM attachments WHERE task_uuid = ?", taskUUID).Scan(&count, &size)
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
	return copyTaskWithAttribution(database, attachDir, attributionFromLegacyActor(actorUUID), sourceUUID, destUUID)
}

func copyTaskWithAttribution(database *db.DB, attachDir string, attr attribution.Attribution, sourceUUID, destUUID string) (*copyResult, error) {
	tx, err := database.Begin()
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()

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
	var description, specification, labels *string
	var startAt, dueAt *string

	err = tx.QueryRow(`
		SELECT id, slug, title, state, priority, description, specification, labels, start_at, due_at
		FROM tasks WHERE uuid = ?
	`, sourceUUID).Scan(&sourceID, &slug, &title, &state, &priority, &description, &specification, &labels, &startAt, &dueAt)
	if err != nil {
		return nil, err
	}

	// Check for existing task with same slug in destination
	var existingUUID string
	_ = tx.QueryRow("SELECT uuid FROM tasks WHERE project_uuid = ? AND slug = ?", destUUID, slug).Scan(&existingUUID)

	if existingUUID != "" && !cpOverwrite {
		return nil, fmt.Errorf("task with slug '%s' already exists in destination container", slug)
	}

	var newUUID, newID string

	if cpOverwrite && existingUUID != "" {
		// Update existing task
		_, err = tx.Exec(`
			UPDATE tasks SET title = ?, state = ?, priority = ?, description = ?, specification = ?, labels = ?,
				start_at = ?, due_at = ?, updated_at = CURRENT_TIMESTAMP,
				updated_by_actor_uuid = ?, updated_by_principal_ref = ?, updated_by_scope_ref = ?, etag = etag + 1
			WHERE uuid = ?
		`, title, state, priority, description, specification, labels, startAt, dueAt,
			legacyActorBind(attr), attr.PrincipalRef, scopeBind(attr), existingUUID)
		if err != nil {
			return nil, err
		}

		newUUID = existingUUID
		_ = tx.QueryRow("SELECT id FROM tasks WHERE uuid = ?", newUUID).Scan(&newID)
	} else {
		// Insert new task (omit id and uuid to let triggers generate them)
		result, err := tx.Exec(`
			INSERT INTO tasks (slug, title, project_uuid, state, priority, description, specification, labels,
				start_at, due_at, created_by_actor_uuid, updated_by_actor_uuid,
				created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref, updated_by_scope_ref)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, slug, title, destUUID, state, priority, description, specification, labels, startAt, dueAt,
			legacyActorBind(attr), legacyActorBind(attr),
			attr.PrincipalRef, attr.PrincipalRef, scopeBind(attr), scopeBind(attr))
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
		attachmentCount, err = copyAttachments(tx, attachDir, attr, sourceUUID, newUUID)
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

	eventMeta, err := eventWriter.LogEventReturning(tx, &domain.Event{
		ActorUUID:    attr.LegacyActorUUID,
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &newUUID,
		EventType:    "task.copied",
		Payload:      &payloadStr,
	})
	if err != nil {
		return nil, err
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	webhooks.DispatchTaskEvent(database, newUUID, webhooks.EventContext{
		Metadata:   eventMeta,
		Event:      "created",
		ActorUUID:  attr.LegacyActorUUID,
		Via:        "cli",
		Transition: &webhooks.Transition{From: nil, To: &state},
		Changed:    []string{"source_uuid"},
		Changes: map[string]webhooks.Change{
			"source_uuid": {From: nil, To: sourceUUID},
		},
	})

	// Get destination path
	var destPath string
	_ = database.QueryRow("SELECT path FROM task_paths WHERE uuid = ?", newUUID).Scan(&destPath)

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

func copyAttachments(tx *sql.Tx, attachDir string, attr attribution.Attribution, sourceTaskUUID, destTaskUUID string) (int, error) {
	rows, err := tx.Query(`
		SELECT uuid, filename, relative_path, mime_type, size_bytes, checksum
		FROM attachments WHERE task_uuid = ?
	`, sourceTaskUUID)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

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
			defer func() { _ = sourceFile.Close() }()

			destFile, err := os.Create(destPath)
			if err != nil {
				return count, err
			}
			defer func() { _ = destFile.Close() }()

			if _, err := io.Copy(destFile, sourceFile); err != nil {
				return count, err
			}
		}
		// If not copying files, just insert metadata with new relative_path

		// Insert attachment metadata
		_, err = tx.Exec(`
			INSERT INTO attachments (
				id, task_uuid, filename, relative_path, mime_type, size_bytes, checksum,
				created_by_actor_uuid, created_by_principal_ref, created_by_scope_ref
			)
			VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, destTaskUUID, filename, newRelativePath, mimeType, sizeBytes, checksum,
			legacyActorBind(attr), attr.PrincipalRef, scopeBind(attr))
		if err != nil {
			return count, err
		}

		count++
	}

	return count, rows.Err()
}
