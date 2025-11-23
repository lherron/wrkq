package cli

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/render"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:   "rm <path|id>...",
	Short: "Archive or delete tasks and containers",
	Long: `Archives (soft delete) or permanently deletes tasks and containers.

By default, performs soft delete (sets archived_at). Use --purge for hard delete.

WARNING: --purge permanently deletes tasks and attachments. This CANNOT be undone!`,
	Args: cobra.MinimumNArgs(1),
	RunE: runRm,
}

var (
	rmRecursive       bool
	rmForce           bool
	rmYes             bool
	rmDryRun          bool
	rmPurge           bool
	rmNullglob        bool
	rmJobs            int
	rmContinueOnError bool
	rmJSON            bool
	rmNDJSON          bool
	rmPorcelain       bool
)

type rmResult struct {
	ID                string `json:"id"`
	UUID              string `json:"uuid"`
	Slug              string `json:"slug"`
	Path              string `json:"path"`
	Purged            bool   `json:"purged"`
	AttachmentsDeleted int    `json:"attachments_deleted,omitempty"`
	BytesFreed        int64  `json:"bytes_freed,omitempty"`
}

func init() {
	rootCmd.AddCommand(rmCmd)
	rmCmd.Flags().BoolVarP(&rmRecursive, "recursive", "r", false, "Remove containers recursively")
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Force removal")
	rmCmd.Flags().BoolVar(&rmYes, "yes", false, "Skip confirmation prompts")
	rmCmd.Flags().BoolVar(&rmDryRun, "dry-run", false, "Show what would be removed")
	rmCmd.Flags().BoolVar(&rmPurge, "purge", false, "Permanently delete (CANNOT BE UNDONE)")
	rmCmd.Flags().BoolVar(&rmNullglob, "nullglob", false, "Zero matches is not an error")
	rmCmd.Flags().IntVarP(&rmJobs, "jobs", "j", 1, "Parallel workers")
	rmCmd.Flags().BoolVar(&rmContinueOnError, "continue-on-error", false, "Continue on errors")
	rmCmd.Flags().BoolVar(&rmJSON, "json", false, "Output JSON")
	rmCmd.Flags().BoolVar(&rmNDJSON, "ndjson", false, "Output NDJSON")
	rmCmd.Flags().BoolVar(&rmPorcelain, "porcelain", false, "Machine-readable output")
}

func runRm(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	actorIdentifier := cmd.Flag("as").Value.String()
	if actorIdentifier == "" {
		actorIdentifier = cfg.GetActorID()
	}
	if actorIdentifier == "" {
		return fmt.Errorf("no actor configured")
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	resolver := actors.NewResolver(database.DB)
	actorUUID, err := resolver.Resolve(actorIdentifier)
	if err != nil {
		return fmt.Errorf("failed to resolve actor: %w", err)
	}

	// Handle stdin
	if len(args) == 1 && args[0] == "-" {
		scanner := bufio.NewScanner(cmd.InOrStdin())
		args = nil
		for scanner.Scan() {
			line := scanner.Text()
			if line != "" {
				args = append(args, line)
			}
		}
		if err := scanner.Err(); err != nil {
			return fmt.Errorf("failed to read stdin: %w", err)
		}
	}

	// Resolve all tasks
	var taskUUIDs []string
	for _, arg := range args {
		taskUUID, _, err := selectors.ResolveTask(database, arg)
		if err != nil {
			if !rmNullglob {
				return fmt.Errorf("task not found: %s", arg)
			}
			continue
		}
		taskUUIDs = append(taskUUIDs, taskUUID)
	}

	if len(taskUUIDs) == 0 {
		if !rmNullglob {
			return fmt.Errorf("no tasks found to remove")
		}
		return nil
	}

	// Dry run with details
	if rmDryRun {
		return showRemovalPlan(cmd, database, taskUUIDs)
	}

	// Confirmation for purge operations
	if rmPurge && !rmYes {
		if err := confirmPurge(cmd, database, taskUUIDs); err != nil {
			return err
		}
	}

	// Execute removal
	op := &bulk.Operation{
		Jobs:            rmJobs,
		ContinueOnError: rmContinueOnError,
		ShowProgress:    !rmJSON && !rmNDJSON && !rmPorcelain,
	}

	var results []rmResult
	result := op.Execute(taskUUIDs, func(taskUUID string) error {
		res, err := removeTask(database, cfg.AttachDir, actorUUID, taskUUID)
		if err == nil && res != nil {
			results = append(results, *res)
		}
		return err
	})

	// Output results
	if rmJSON {
		return render.RenderJSON(results, false)
	}
	if rmNDJSON {
		items := make([]interface{}, len(results))
		for i, r := range results {
			items[i] = r
		}
		return render.RenderNDJSON(items)
	}
	if rmPorcelain {
		for _, r := range results {
			fmt.Fprintf(cmd.OutOrStdout(), "%s\n", r.ID)
		}
		return nil
	}

	// Human output
	for _, r := range results {
		if r.Purged {
			if r.AttachmentsDeleted > 0 {
				fmt.Fprintf(cmd.OutOrStdout(), "✓ %s permanently deleted (%d attachments, %.1f MB)\n",
					r.ID, r.AttachmentsDeleted, float64(r.BytesFreed)/(1024*1024))
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "✓ %s permanently deleted\n", r.ID)
			}
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "✓ %s archived\n", r.ID)
		}
	}

	if result.Failed > 0 {
		if rmContinueOnError {
			os.Exit(5) // Partial success
		}
		os.Exit(1)
	}

	return nil
}

func showRemovalPlan(cmd *cobra.Command, database *db.DB, taskUUIDs []string) error {
	var totalAttachments int
	var totalBytes int64

	if rmPurge {
		fmt.Fprintf(cmd.OutOrStdout(), "Would permanently delete %d task(s):\n\n", len(taskUUIDs))
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Would archive %d task(s):\n\n", len(taskUUIDs))
	}

	for _, taskUUID := range taskUUIDs {
		var id, slug, title, state string
		var priority int
		err := database.QueryRow(`
			SELECT t.id, t.slug, COALESCE(t.title, ''), t.state, t.priority
			FROM tasks t
			WHERE t.uuid = ?
		`, taskUUID).Scan(&id, &slug, &title, &state, &priority)

		if err != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  Error reading task %s: %v\n", taskUUID, err)
			continue
		}

		displayPath := slug

		fmt.Fprintf(cmd.OutOrStdout(), "  %s (%s)\n", id, displayPath)
		if title != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "    Title: %s\n", title)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "    State: %s, Priority: %d\n", state, priority)

		if rmPurge {
			var count int
			var size sql.NullInt64
			database.QueryRow("SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM attachments WHERE task_uuid = ?", taskUUID).Scan(&count, &size)
			if count > 0 {
				totalAttachments += count
				if size.Valid {
					totalBytes += size.Int64
				}
				fmt.Fprintf(cmd.OutOrStdout(), "    Attachments: %d file(s) (%.1f MB)\n", count, float64(size.Int64)/(1024*1024))
			}
		}
		fmt.Fprintln(cmd.OutOrStdout())
	}

	if rmPurge && totalAttachments > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "Total: %d attachments (%.1f MB)\n\n", totalAttachments, float64(totalBytes)/(1024*1024))
		fmt.Fprintf(cmd.OutOrStdout(), "WARNING: This action CANNOT be undone!\n")
	}

	return nil
}

func confirmPurge(cmd *cobra.Command, database *db.DB, taskUUIDs []string) error {
	var totalAttachments int
	var totalBytes int64

	for _, taskUUID := range taskUUIDs {
		var count int
		var size sql.NullInt64
		database.QueryRow("SELECT COUNT(*), COALESCE(SUM(size_bytes), 0) FROM attachments WHERE task_uuid = ?", taskUUID).Scan(&count, &size)
		totalAttachments += count
		if size.Valid {
			totalBytes += size.Int64
		}
	}

	fmt.Fprintf(cmd.ErrOrStderr(), "\nWARNING: This will permanently delete:\n")
	fmt.Fprintf(cmd.ErrOrStderr(), "  - %d task(s)\n", len(taskUUIDs))
	if totalAttachments > 0 {
		fmt.Fprintf(cmd.ErrOrStderr(), "  - %d attachments (%.1f MB)\n", totalAttachments, float64(totalBytes)/(1024*1024))
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "\nThis action CANNOT be undone.\n\n")
	fmt.Fprintf(cmd.ErrOrStderr(), "Type 'yes' to confirm: ")

	reader := bufio.NewReader(cmd.InOrStdin())
	response, _ := reader.ReadString('\n')
	if strings.TrimSpace(strings.ToLower(response)) != "yes" {
		return fmt.Errorf("aborted")
	}

	return nil
}

func removeTask(database *db.DB, attachDir, actorUUID, taskUUID string) (*rmResult, error) {
	tx, err := database.Begin()
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Get task info
	var id, slug string
	err = tx.QueryRow(`
		SELECT t.id, t.slug
		FROM tasks t
		WHERE t.uuid = ?
	`, taskUUID).Scan(&id, &slug)
	if err != nil {
		return nil, fmt.Errorf("task not found: %w", err)
	}

	displayPath := slug

	result := &rmResult{
		ID:     id,
		UUID:   taskUUID,
		Slug:   slug,
		Path:   displayPath,
		Purged: rmPurge,
	}

	if rmPurge {
		// Delete attachment files first
		rows, err := tx.Query("SELECT relative_path, size_bytes FROM attachments WHERE task_uuid = ?", taskUUID)
		if err == nil {
			defer rows.Close()
			for rows.Next() {
				var relativePath string
				var sizeBytes int64
				if err := rows.Scan(&relativePath, &sizeBytes); err != nil {
					continue
				}

				// Delete file
				filePath := filepath.Join(attachDir, relativePath)
				if err := os.Remove(filePath); err != nil && !os.IsNotExist(err) {
					// Log warning but continue
					fmt.Fprintf(os.Stderr, "Warning: failed to delete file %s: %v\n", filePath, err)
				}

				result.AttachmentsDeleted++
				result.BytesFreed += sizeBytes
			}
		}

		// Delete task directory
		taskDir := filepath.Join(attachDir, "tasks", taskUUID)
		os.RemoveAll(taskDir) // Ignore errors, directory might not exist

		// Log event before deletion
		eventWriter := events.NewWriter(database.DB)
		payloadData := map[string]interface{}{
			"slug":  slug,
			"purged_by": actorUUID,
		}
		if result.AttachmentsDeleted > 0 {
			payloadData["attachment_count"] = result.AttachmentsDeleted
			payloadData["bytes_freed"] = result.BytesFreed
		}
		payloadJSON, _ := json.Marshal(payloadData)
		payloadStr := string(payloadJSON)

		if err := eventWriter.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.purged",
			Payload:      &payloadStr,
		}); err != nil {
			return nil, fmt.Errorf("failed to log event: %w", err)
		}

		// Hard delete (CASCADE will delete attachments metadata)
		_, err = tx.Exec("DELETE FROM tasks WHERE uuid = ?", taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to delete task: %w", err)
		}
	} else {
		// Soft delete (archive)
		_, err = tx.Exec(`
			UPDATE tasks
			SET state = 'archived',
			    archived_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_actor_uuid = ?,
			    etag = etag + 1
			WHERE uuid = ?
		`, actorUUID, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to archive task: %w", err)
		}

		// Log event
		eventWriter := events.NewWriter(database.DB)
		payload := `{"action":"archived"}`
		if err := eventWriter.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.updated",
			Payload:      &payload,
		}); err != nil {
			return nil, fmt.Errorf("failed to log event: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return result, nil
}

