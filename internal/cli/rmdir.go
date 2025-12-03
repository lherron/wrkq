package cli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var rmdirCmd = &cobra.Command{
	Use:   "rmdir <path|id>...",
	Short: "Remove empty containers",
	Long: `Remove empty containers (projects or subprojects).

By default, only removes empty containers (no tasks or child containers).
Use --force to remove non-empty containers (recursively deletes all contents).

WARNING: --force permanently deletes containers, tasks, and attachments. This CANNOT be undone!`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runRmdir),
}

var (
	rmdirForce bool
	rmdirYes   bool
)

func init() {
	rootCmd.AddCommand(rmdirCmd)
	rmdirCmd.Flags().BoolVarP(&rmdirForce, "force", "f", false, "Force removal of non-empty containers")
	rmdirCmd.Flags().BoolVar(&rmdirYes, "yes", false, "Skip confirmation prompts")
}

func runRmdir(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	// Process each path
	for _, path := range args {
		if err := removeContainer(cmd, database, actorUUID, path); err != nil {
			return err
		}
	}

	return nil
}

func removeContainer(cmd *cobra.Command, database *db.DB, actorUUID, path string) error {
	// Resolve container path or ID to UUID
	containerUUID, id, err := selectors.ResolveContainer(database, path)
	if err != nil {
		return fmt.Errorf("container not found: %s", path)
	}

	// Get container info
	var slug, title string
	var taskCount, childCount int

	err = database.QueryRow(`
		SELECT c.slug, c.title
		FROM containers c
		WHERE c.uuid = ?
	`, containerUUID).Scan(&slug, &title)
	if err != nil {
		return fmt.Errorf("container not found: %s", path)
	}

	// Count tasks in this container
	err = database.QueryRow(`
		SELECT COUNT(*)
		FROM tasks
		WHERE project_uuid = ? AND archived_at IS NULL
	`, containerUUID).Scan(&taskCount)
	if err != nil {
		return fmt.Errorf("failed to count tasks: %w", err)
	}

	// Count child containers
	err = database.QueryRow(`
		SELECT COUNT(*)
		FROM containers
		WHERE parent_uuid = ? AND archived_at IS NULL
	`, containerUUID).Scan(&childCount)
	if err != nil {
		return fmt.Errorf("failed to count child containers: %w", err)
	}

	// Check if container is empty
	if !rmdirForce && (taskCount > 0 || childCount > 0) {
		return fmt.Errorf("container not empty: %s (contains %d task(s) and %d child container(s)). Use --force to remove anyway", path, taskCount, childCount)
	}

	// Confirm if force is used and container is not empty
	if rmdirForce && !rmdirYes && (taskCount > 0 || childCount > 0) {
		fmt.Fprintf(cmd.ErrOrStderr(), "\nWARNING: This will permanently delete:\n")
		fmt.Fprintf(cmd.ErrOrStderr(), "  - Container: %s (%s)\n", id, path)
		if taskCount > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %d task(s)\n", taskCount)
		}
		if childCount > 0 {
			fmt.Fprintf(cmd.ErrOrStderr(), "  - %d child container(s) (and all their contents)\n", childCount)
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "\nThis action CANNOT be undone.\n\n")
		fmt.Fprintf(cmd.ErrOrStderr(), "Are you sure? (yes/no): ")

		var response string
		fmt.Fscanln(cmd.InOrStdin(), &response)
		if response != "yes" {
			return fmt.Errorf("aborted")
		}
	}

	// Delete the container
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// If force is set, delete tasks and child containers first
	if rmdirForce {
		// Delete all tasks in this container
		_, err = tx.Exec("DELETE FROM tasks WHERE project_uuid = ?", containerUUID)
		if err != nil {
			return fmt.Errorf("failed to delete tasks: %w", err)
		}

		// Recursively delete child containers (CASCADE will handle their tasks)
		_, err = tx.Exec("DELETE FROM containers WHERE parent_uuid = ?", containerUUID)
		if err != nil {
			return fmt.Errorf("failed to delete child containers: %w", err)
		}
	}

	// Log event before deletion
	eventWriter := events.NewWriter(database.DB)
	payload := fmt.Sprintf(`{"slug":"%s","path":"%s","force":%t}`, slug, path, rmdirForce)
	payloadStr := payload
	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "container",
		ResourceUUID: &containerUUID,
		EventType:    "container.deleted",
		Payload:      &payloadStr,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	// Delete the container
	_, err = tx.Exec("DELETE FROM containers WHERE uuid = ?", containerUUID)
	if err != nil {
		return fmt.Errorf("failed to delete container: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "✓ Removed: %s (%s)\n", id, path)
	return nil
}
