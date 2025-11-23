package cli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/spf13/cobra"
)

var touchCmd = &cobra.Command{
	Use:   "touch <path>...",
	Short: "Create tasks",
	Long: `Creates one or more tasks at the specified paths.
The last segment of each path becomes the task slug (normalized to lowercase [a-z0-9-]).

Examples:
  wrkq touch myproject/feature/task-name -t "Task Title"
  wrkq touch myproject/feature/task-name -d "Task description"
  wrkq touch myproject/feature/task-name -t "Title" -d @description.md
  echo "Description from stdin" | wrkq touch inbox/new-task -d -`,
	Args: cobra.MinimumNArgs(1),
	RunE: runTouch,
}

var (
	touchTitle       string
	touchDescription string
)

func init() {
	rootCmd.AddCommand(touchCmd)
	touchCmd.Flags().StringVarP(&touchTitle, "title", "t", "", "Title for the task (defaults to slug)")
	touchCmd.Flags().StringVarP(&touchDescription, "description", "d", "", "Description for the task (use @file.md for file or - for stdin)")
}

func runTouch(cmd *cobra.Command, args []string) error {
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

	// Process description if provided
	var description string
	if touchDescription != "" {
		description, err = readDescriptionValue(touchDescription)
		if err != nil {
			return fmt.Errorf("failed to read description: %w", err)
		}
	}

	// Create each task
	for _, path := range args {
		if err := createTask(database, actorUUID, path, touchTitle, description); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created task: %s\n", path)
	}

	return nil
}

func createTask(database *db.DB, actorUUID, path, title, description string) error {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return fmt.Errorf("invalid path: %s", path)
	}

	// Last segment is the task slug
	taskSlug := segments[len(segments)-1]
	normalizedSlug, err := paths.NormalizeSlug(taskSlug)
	if err != nil {
		return fmt.Errorf("invalid task slug %q: %w", taskSlug, err)
	}

	// Default title to slug if not provided
	if title == "" {
		title = normalizedSlug
	}

	// Find parent container
	var projectUUID string
	if len(segments) > 1 {
		// Navigate to parent container
		var parentUUID *string
		for i, segment := range segments[:len(segments)-1] {
			slug, err := paths.NormalizeSlug(segment)
			if err != nil {
				return fmt.Errorf("invalid slug %q: %w", segment, err)
			}

			query := `SELECT uuid FROM containers WHERE slug = ? AND `
			args := []interface{}{slug}
			if parentUUID == nil {
				query += `parent_uuid IS NULL`
			} else {
				query += `parent_uuid = ?`
				args = append(args, *parentUUID)
			}

			var uuid string
			err = database.QueryRow(query, args...).Scan(&uuid)
			if err != nil {
				return fmt.Errorf("parent container not found: %s", paths.JoinPath(segments[:i+1]...))
			}
			parentUUID = &uuid
		}
		projectUUID = *parentUUID
	} else {
		// Task at root - find "inbox" or any root container
		err := database.QueryRow(`SELECT uuid FROM containers WHERE parent_uuid IS NULL LIMIT 1`).Scan(&projectUUID)
		if err != nil {
			return fmt.Errorf("no root container found (create a project first with 'todo mkdir')")
		}
	}

	// Create task
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO tasks (id, slug, title, description, project_uuid, state, priority, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('', ?, ?, ?, ?, 'open', 3, ?, ?)
	`, normalizedSlug, title, description, projectUUID, actorUUID, actorUUID)
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	// Get the UUID of the created task
	rowID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert ID: %w", err)
	}

	var uuid string
	err = tx.QueryRow("SELECT uuid FROM tasks WHERE rowid = ?", rowID).Scan(&uuid)
	if err != nil {
		return fmt.Errorf("failed to get task UUID: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payload := fmt.Sprintf(`{"slug":"%s","title":"%s","state":"open"}`, normalizedSlug, title)
	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &uuid,
		EventType:    "task.created",
		Payload:      &payload,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	return tx.Commit()
}
