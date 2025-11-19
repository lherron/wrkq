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

var restoreCmd = &cobra.Command{
	Use:   "restore <path|id>...",
	Short: "Unarchive archived tasks and containers",
	Long:  `Restores archived tasks and containers by clearing their archived_at field.`,
	Args:  cobra.MinimumNArgs(1),
	RunE:  runRestore,
}

func init() {
	rootCmd.AddCommand(restoreCmd)
}

func runRestore(cmd *cobra.Command, args []string) error {
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
		// Try to find as task (including archived)
		var taskUUID string
		if arg[0] == 'T' && arg[1] == '-' {
			database.QueryRow("SELECT uuid FROM tasks WHERE id = ?", arg).Scan(&taskUUID)
		}

		if taskUUID != "" {
			if err := restoreTask(database, actorUUID, taskUUID); err != nil {
				return fmt.Errorf("failed to restore task %s: %w", arg, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restored task: %s\n", arg)
			continue
		}

		// Try as container
		var containerUUID string
		if arg[0] == 'P' && arg[1] == '-' {
			database.QueryRow("SELECT uuid FROM containers WHERE id = ?", arg).Scan(&containerUUID)
		}

		if containerUUID != "" {
			if err := restoreContainer(database, actorUUID, containerUUID); err != nil {
				return fmt.Errorf("failed to restore container %s: %w", arg, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Restored container: %s\n", arg)
			continue
		}

		return fmt.Errorf("not found or not archived: %s", arg)
	}

	return nil
}

func restoreTask(database *db.DB, actorUUID, taskUUID string) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE tasks
		SET state = 'open',
		    archived_at = NULL,
		    updated_by_actor_uuid = ?
		WHERE uuid = ?
	`, actorUUID, taskUUID)
	if err != nil {
		return fmt.Errorf("failed to restore task: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payload := `{"action":"restored"}`
	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.updated",
		Payload:      &payload,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	return tx.Commit()
}

func restoreContainer(database *db.DB, actorUUID, containerUUID string) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	_, err = tx.Exec(`
		UPDATE containers
		SET archived_at = NULL,
		    updated_by_actor_uuid = ?
		WHERE uuid = ?
	`, actorUUID, containerUUID)
	if err != nil {
		return fmt.Errorf("failed to restore container: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payload := `{"action":"restored"}`
	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "container",
		ResourceUUID: &containerUUID,
		EventType:    "container.updated",
		Payload:      &payload,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	return tx.Commit()
}
