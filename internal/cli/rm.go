package cli

import (
	"fmt"

	"github.com/lherron/todo/internal/actors"
	"github.com/lherron/todo/internal/config"
	"github.com/lherron/todo/internal/db"
	"github.com/lherron/todo/internal/domain"
	"github.com/lherron/todo/internal/events"
	"github.com/spf13/cobra"
)

var rmCmd = &cobra.Command{
	Use:   "rm <path|id>...",
	Short: "Archive or delete tasks and containers",
	Long: `Archives (soft delete) or permanently deletes tasks and containers.
By default, performs soft delete (sets archived_at). Use --purge for hard delete.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runRm,
}

var (
	rmRecursive bool
	rmForce     bool
	rmYes       bool
	rmDryRun    bool
	rmPurge     bool
)

func init() {
	rootCmd.AddCommand(rmCmd)
	rmCmd.Flags().BoolVarP(&rmRecursive, "recursive", "r", false, "Remove containers recursively")
	rmCmd.Flags().BoolVarP(&rmForce, "force", "f", false, "Force removal")
	rmCmd.Flags().BoolVar(&rmYes, "yes", false, "Skip confirmation prompts")
	rmCmd.Flags().BoolVar(&rmDryRun, "dry-run", false, "Show what would be removed")
	rmCmd.Flags().BoolVar(&rmPurge, "purge", false, "Hard delete (remove from database)")
}

func runRm(cmd *cobra.Command, args []string) error {
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

	// Process each argument
	for _, arg := range args {
		// Try as task first
		taskUUID, _, err := resolveTask(database, arg)
		if err == nil {
			if rmDryRun {
				fmt.Fprintf(cmd.OutOrStdout(), "Would remove task: %s\n", arg)
				continue
			}

			if err := removeTask(database, actorUUID, taskUUID, rmPurge); err != nil {
				return fmt.Errorf("failed to remove task %s: %w", arg, err)
			}

			if rmPurge {
				fmt.Fprintf(cmd.OutOrStdout(), "Purged task: %s\n", arg)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "Archived task: %s\n", arg)
			}
			continue
		}

		// Try as container
		containerUUID, err := resolveContainer(database, arg)
		if err != nil {
			return fmt.Errorf("path not found: %s", arg)
		}

		if rmDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "Would remove container: %s\n", arg)
			continue
		}

		if err := removeContainer(database, actorUUID, containerUUID, rmPurge, rmRecursive); err != nil {
			return fmt.Errorf("failed to remove container %s: %w", arg, err)
		}

		if rmPurge {
			fmt.Fprintf(cmd.OutOrStdout(), "Purged container: %s\n", arg)
		} else {
			fmt.Fprintf(cmd.OutOrStdout(), "Archived container: %s\n", arg)
		}
	}

	return nil
}

func removeTask(database *db.DB, actorUUID, taskUUID string, purge bool) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	if purge {
		// Hard delete
		_, err = tx.Exec("DELETE FROM tasks WHERE uuid = ?", taskUUID)
		if err != nil {
			return fmt.Errorf("failed to delete task: %w", err)
		}

		// Log event
		eventWriter := events.NewWriter(database.DB)
		if err := eventWriter.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "task",
			ResourceUUID: &taskUUID,
			EventType:    "task.deleted",
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}
	} else {
		// Soft delete (archive)
		_, err = tx.Exec(`
			UPDATE tasks
			SET state = 'archived',
			    archived_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_actor_uuid = ?
			WHERE uuid = ?
		`, actorUUID, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to archive task: %w", err)
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
			return fmt.Errorf("failed to log event: %w", err)
		}
	}

	return tx.Commit()
}

func removeContainer(database *db.DB, actorUUID, containerUUID string, purge bool, recursive bool) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Check if container has children
	var childCount int
	err = tx.QueryRow(`
		SELECT COUNT(*) FROM containers WHERE parent_uuid = ?
		UNION ALL
		SELECT COUNT(*) FROM tasks WHERE project_uuid = ?
	`, containerUUID, containerUUID).Scan(&childCount)

	if childCount > 0 && !recursive {
		return fmt.Errorf("container has children (use -r to remove recursively)")
	}

	if purge {
		// Hard delete (cascade will handle children)
		_, err = tx.Exec("DELETE FROM containers WHERE uuid = ?", containerUUID)
		if err != nil {
			return fmt.Errorf("failed to delete container: %w", err)
		}

		// Log event
		eventWriter := events.NewWriter(database.DB)
		if err := eventWriter.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "container",
			ResourceUUID: &containerUUID,
			EventType:    "container.deleted",
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}
	} else {
		// Soft delete (archive)
		_, err = tx.Exec(`
			UPDATE containers
			SET archived_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
			    updated_by_actor_uuid = ?
			WHERE uuid = ?
		`, actorUUID, containerUUID)
		if err != nil {
			return fmt.Errorf("failed to archive container: %w", err)
		}

		// Log event
		eventWriter := events.NewWriter(database.DB)
		payload := `{"action":"archived"}`
		if err := eventWriter.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "container",
			ResourceUUID: &containerUUID,
			EventType:    "container.updated",
			Payload:      &payload,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}
	}

	return tx.Commit()
}
