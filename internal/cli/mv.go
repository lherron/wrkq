package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var mvCmd = &cobra.Command{
	Use:   "mv <src>... <dst>",
	Short: "Move or rename tasks and containers",
	Long: `Move or rename tasks and containers.

Rules:
- Multiple sources -> DST must be an existing container; sources move into DST.
- Single source:
  - If DST path resolves to existing container: move into container.
  - If DST does not exist: treat final segment as new slug (rename).
  - If DST is an existing task: error unless --overwrite-task.`,
	Args: cobra.MinimumNArgs(2),
	RunE: appctx.WithApp(appctx.WithActor(), runMv),
}

var (
	mvType          string
	mvIfMatch       int64
	mvDryRun        bool
	mvYes           bool
	mvNullglob      bool
	mvOverwriteTask bool
)

func init() {
	rootCmd.AddCommand(mvCmd)
	mvCmd.Flags().StringVar(&mvType, "type", "", "Force type: t (task), p (project/container)")
	mvCmd.Flags().Int64Var(&mvIfMatch, "if-match", 0, "Only move if etag matches")
	mvCmd.Flags().BoolVar(&mvDryRun, "dry-run", false, "Show what would be moved without applying")
	mvCmd.Flags().BoolVar(&mvYes, "yes", false, "Skip confirmation prompts")
	mvCmd.Flags().BoolVar(&mvNullglob, "nullglob", false, "Zero matches is a no-op instead of error")
	mvCmd.Flags().BoolVar(&mvOverwriteTask, "overwrite-task", false, "Allow overwriting existing tasks")
}

func runMv(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	// Split sources and destination
	sources := args[:len(args)-1]
	dst := args[len(args)-1]

	// If multiple sources, destination must be an existing container
	if len(sources) > 1 {
		dstContainerUUID, _, err := selectors.ResolveContainer(database, dst)
		if err != nil {
			return fmt.Errorf("destination must be an existing container for multiple sources: %w", err)
		}

		for _, src := range sources {
			if err := moveToContainer(cmd, database, actorUUID, src, dstContainerUUID, dst); err != nil {
				return err
			}
		}
		return nil
	}

	// Single source - could be move or rename
	src := sources[0]

	// Try to resolve destination as container first
	dstContainerUUID, _, dstErr := selectors.ResolveContainer(database, dst)
	if dstErr == nil {
		// Destination is an existing container - move into it
		return moveToContainer(cmd, database, actorUUID, src, dstContainerUUID, dst)
	}

	// Destination doesn't exist as container - could be rename
	// Check if source is a task or container
	srcTaskUUID, _, taskErr := selectors.ResolveTask(database, src)
	if taskErr == nil {
		// Source is a task - rename or move to new parent
		return renameOrMoveTask(cmd, database, actorUUID, srcTaskUUID, src, dst)
	}

	srcContainerUUID, _, containerErr := selectors.ResolveContainer(database, src)
	if containerErr == nil {
		// Source is a container - rename or move to new parent
		return renameOrMoveContainer(cmd, database, actorUUID, srcContainerUUID, src, dst)
	}

	return fmt.Errorf("source not found: %s", src)
}

func moveToContainer(cmd *cobra.Command, database *db.DB, actorUUID, src, dstContainerUUID, dstPath string) error {
	// Try as task first
	srcTaskUUID, srcPath, taskErr := selectors.ResolveTask(database, src)
	if taskErr == nil {
		if mvDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "Would move task %s -> %s\n", srcPath, dstPath)
			return nil
		}

		// Move task to destination container
		tx, err := database.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		// Check etag if --if-match was provided
		if mvIfMatch > 0 {
			var currentETag int64
			err = tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", srcTaskUUID).Scan(&currentETag)
			if err != nil {
				return fmt.Errorf("failed to get current etag: %w", err)
			}
			if currentETag != mvIfMatch {
				return &domain.ETagMismatchError{Expected: mvIfMatch, Actual: currentETag}
			}
		}

		// Update task's project_uuid
		_, err = tx.Exec(`
			UPDATE tasks
			SET project_uuid = ?,
			    etag = etag + 1,
			    updated_by_actor_uuid = ?
			WHERE uuid = ?
		`, dstContainerUUID, actorUUID, srcTaskUUID)
		if err != nil {
			return fmt.Errorf("failed to move task: %w", err)
		}

		// Log event
		eventWriter := events.NewWriter(database.DB)
		payload := fmt.Sprintf(`{"from":"%s","to":"%s","to_container_uuid":"%s"}`, srcPath, dstPath, dstContainerUUID)
		var newETag int64
		tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", srcTaskUUID).Scan(&newETag)

		if err := eventWriter.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &srcTaskUUID,
			EventType:    "task.moved",
			ETag:         &newETag,
			Payload:      &payload,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Moved task: %s -> %s\n", srcPath, dstPath)
		return nil
	}

	// Try as container
	srcContainerUUID, _, containerErr := selectors.ResolveContainer(database, src)
	if containerErr == nil {
		if mvDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "Would move container %s -> %s\n", src, dstPath)
			return nil
		}

		// Move container to destination container
		tx, err := database.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		// Check etag if --if-match was provided
		if mvIfMatch > 0 {
			var currentETag int64
			err = tx.QueryRow("SELECT etag FROM containers WHERE uuid = ?", srcContainerUUID).Scan(&currentETag)
			if err != nil {
				return fmt.Errorf("failed to get current etag: %w", err)
			}
			if currentETag != mvIfMatch {
				return &domain.ETagMismatchError{Expected: mvIfMatch, Actual: currentETag}
			}
		}

		// Update container's parent_uuid
		_, err = tx.Exec(`
			UPDATE containers
			SET parent_uuid = ?,
			    etag = etag + 1,
			    updated_by_actor_uuid = ?
			WHERE uuid = ?
		`, dstContainerUUID, actorUUID, srcContainerUUID)
		if err != nil {
			return fmt.Errorf("failed to move container: %w", err)
		}

		// Log event
		eventWriter := events.NewWriter(database.DB)
		payload := fmt.Sprintf(`{"from":"%s","to":"%s","to_container_uuid":"%s"}`, src, dstPath, dstContainerUUID)
		var newETag int64
		tx.QueryRow("SELECT etag FROM containers WHERE uuid = ?", srcContainerUUID).Scan(&newETag)

		if err := eventWriter.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "container",
			ResourceUUID: &srcContainerUUID,
			EventType:    "container.moved",
			ETag:         &newETag,
			Payload:      &payload,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Moved container: %s -> %s\n", src, dstPath)
		return nil
	}

	return fmt.Errorf("source not found: %s", src)
}

func renameOrMoveTask(cmd *cobra.Command, database *db.DB, actorUUID, srcTaskUUID, srcPath, dstPath string) error {
	// Parse destination path
	dstSegments := paths.SplitPath(dstPath)
	if len(dstSegments) == 0 {
		return fmt.Errorf("invalid destination path: %s", dstPath)
	}

	// Determine if we're moving to a new parent or just renaming
	var newParentUUID *string
	var newSlug string

	if len(dstSegments) > 1 {
		// Navigate to parent container
		for i, segment := range dstSegments[:len(dstSegments)-1] {
			slug, err := paths.NormalizeSlug(segment)
			if err != nil {
				return fmt.Errorf("invalid slug %q: %w", segment, err)
			}

			query := `SELECT uuid FROM containers WHERE slug = ? AND `
			args := []interface{}{slug}
			if newParentUUID == nil {
				query += `parent_uuid IS NULL`
			} else {
				query += `parent_uuid = ?`
				args = append(args, *newParentUUID)
			}

			var uuid string
			err = database.QueryRow(query, args...).Scan(&uuid)
			if err != nil {
				return fmt.Errorf("destination container not found: %s", paths.JoinPath(dstSegments[:i+1]...))
			}
			newParentUUID = &uuid
		}
		newSlug = dstSegments[len(dstSegments)-1]
	} else {
		// Just renaming within the same parent
		// Get current parent
		err := database.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", srcTaskUUID).Scan(&newParentUUID)
		if err != nil {
			return fmt.Errorf("failed to get current parent: %w", err)
		}
		newSlug = dstSegments[0]
	}

	// Normalize new slug
	normalizedSlug, err := paths.NormalizeSlug(newSlug)
	if err != nil {
		return fmt.Errorf("invalid destination slug %q: %w", newSlug, err)
	}

	// Check if destination already exists
	var existingTaskUUID string
	err = database.QueryRow(`
		SELECT uuid FROM tasks WHERE slug = ? AND project_uuid = ?
	`, normalizedSlug, *newParentUUID).Scan(&existingTaskUUID)

	if err == nil && existingTaskUUID != srcTaskUUID {
		if !mvOverwriteTask {
			return fmt.Errorf("destination task already exists: %s (use --overwrite-task to replace)", dstPath)
		}
		// Delete existing task
		if mvDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "Would overwrite task at %s\n", dstPath)
		} else {
			_, err := database.Exec("DELETE FROM tasks WHERE uuid = ?", existingTaskUUID)
			if err != nil {
				return fmt.Errorf("failed to delete existing task: %w", err)
			}
		}
	}

	if mvDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Would rename/move task %s -> %s\n", srcPath, dstPath)
		return nil
	}

	// Perform the move/rename
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Check etag if --if-match was provided
	if mvIfMatch > 0 {
		var currentETag int64
		err = tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", srcTaskUUID).Scan(&currentETag)
		if err != nil {
			return fmt.Errorf("failed to get current etag: %w", err)
		}
		if currentETag != mvIfMatch {
			return &domain.ETagMismatchError{Expected: mvIfMatch, Actual: currentETag}
		}
	}

	_, err = tx.Exec(`
		UPDATE tasks
		SET slug = ?,
		    project_uuid = ?,
		    etag = etag + 1,
		    updated_by_actor_uuid = ?
		WHERE uuid = ?
	`, normalizedSlug, *newParentUUID, actorUUID, srcTaskUUID)
	if err != nil {
		return fmt.Errorf("failed to rename/move task: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	changes := map[string]interface{}{
		"from":     srcPath,
		"to":       dstPath,
		"new_slug": normalizedSlug,
	}
	changesJSON, _ := json.Marshal(changes)
	payload := string(changesJSON)
	var newETag int64
	tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", srcTaskUUID).Scan(&newETag)

	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &srcTaskUUID,
		EventType:    "task.moved",
		ETag:         &newETag,
		Payload:      &payload,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Moved/renamed task: %s -> %s\n", srcPath, dstPath)
	return nil
}

func renameOrMoveContainer(cmd *cobra.Command, database *db.DB, actorUUID, srcContainerUUID, srcPath, dstPath string) error {
	// Parse destination path
	dstSegments := paths.SplitPath(dstPath)
	if len(dstSegments) == 0 {
		return fmt.Errorf("invalid destination path: %s", dstPath)
	}

	// Determine if we're moving to a new parent or just renaming
	var newParentUUID *string
	var newSlug string

	if len(dstSegments) > 1 {
		// Navigate to parent container
		for i, segment := range dstSegments[:len(dstSegments)-1] {
			slug, err := paths.NormalizeSlug(segment)
			if err != nil {
				return fmt.Errorf("invalid slug %q: %w", segment, err)
			}

			query := `SELECT uuid FROM containers WHERE slug = ? AND `
			args := []interface{}{slug}
			if newParentUUID == nil {
				query += `parent_uuid IS NULL`
			} else {
				query += `parent_uuid = ?`
				args = append(args, *newParentUUID)
			}

			var uuid string
			err = database.QueryRow(query, args...).Scan(&uuid)
			if err != nil {
				return fmt.Errorf("destination container not found: %s", paths.JoinPath(dstSegments[:i+1]...))
			}
			newParentUUID = &uuid
		}
		newSlug = dstSegments[len(dstSegments)-1]
	} else {
		// Just renaming within the same parent
		// Get current parent (may be NULL for root containers)
		var parentUUID *string
		err := database.QueryRow("SELECT parent_uuid FROM containers WHERE uuid = ?", srcContainerUUID).Scan(&parentUUID)
		if err != nil && !strings.Contains(err.Error(), "null") {
			return fmt.Errorf("failed to get current parent: %w", err)
		}
		newParentUUID = parentUUID
		newSlug = dstSegments[0]
	}

	// Normalize new slug
	normalizedSlug, err := paths.NormalizeSlug(newSlug)
	if err != nil {
		return fmt.Errorf("invalid destination slug %q: %w", newSlug, err)
	}

	// Check if destination already exists
	query := `SELECT uuid FROM containers WHERE slug = ? AND `
	args := []interface{}{normalizedSlug}
	if newParentUUID == nil {
		query += `parent_uuid IS NULL`
	} else {
		query += `parent_uuid = ?`
		args = append(args, *newParentUUID)
	}

	var existingContainerUUID string
	err = database.QueryRow(query, args...).Scan(&existingContainerUUID)

	if err == nil && existingContainerUUID != srcContainerUUID {
		return fmt.Errorf("destination container already exists: %s", dstPath)
	}

	if mvDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Would rename/move container %s -> %s\n", srcPath, dstPath)
		return nil
	}

	// Perform the move/rename
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Check etag if --if-match was provided
	if mvIfMatch > 0 {
		var currentETag int64
		err = tx.QueryRow("SELECT etag FROM containers WHERE uuid = ?", srcContainerUUID).Scan(&currentETag)
		if err != nil {
			return fmt.Errorf("failed to get current etag: %w", err)
		}
		if currentETag != mvIfMatch {
			return &domain.ETagMismatchError{Expected: mvIfMatch, Actual: currentETag}
		}
	}

	_, err = tx.Exec(`
		UPDATE containers
		SET slug = ?,
		    parent_uuid = ?,
		    etag = etag + 1,
		    updated_by_actor_uuid = ?
		WHERE uuid = ?
	`, normalizedSlug, newParentUUID, actorUUID, srcContainerUUID)
	if err != nil {
		return fmt.Errorf("failed to rename/move container: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	changes := map[string]interface{}{
		"from":     srcPath,
		"to":       dstPath,
		"new_slug": normalizedSlug,
	}
	changesJSON, _ := json.Marshal(changes)
	payload := string(changesJSON)
	var newETag int64
	tx.QueryRow("SELECT etag FROM containers WHERE uuid = ?", srcContainerUUID).Scan(&newETag)

	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "container",
		ResourceUUID: &srcContainerUUID,
		EventType:    "container.moved",
		ETag:         &newETag,
		Payload:      &payload,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Moved/renamed container: %s -> %s\n", srcPath, dstPath)
	return nil
}
