package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
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

Supports all flags from 'wrkq set' to set initial values during creation:
- state, priority, title, description, labels, due-at, start-at

Examples:
  wrkq touch myproject/feature/task-name -t "Task Title"
  wrkq touch myproject/feature/task-name -d "Task description"
  wrkq touch myproject/feature/task-name -t "Title" -d @description.md
  wrkq touch inbox/new-task --state in_progress --priority 1
  wrkq touch inbox/bug-fix --labels '["bug","urgent"]' --due-at 2025-12-01
  echo "Description from stdin" | wrkq touch inbox/new-task -d -`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runTouch),
}

var (
	touchTitle       string
	touchDescription string
	touchState       string
	touchPriority    int
	touchLabels      string
	touchDueAt       string
	touchStartAt     string
)

func init() {
	rootCmd.AddCommand(touchCmd)
	touchCmd.Flags().StringVarP(&touchTitle, "title", "t", "", "Title for the task (defaults to slug)")
	touchCmd.Flags().StringVarP(&touchDescription, "description", "d", "", "Description for the task (use @file.md for file or - for stdin)")
	touchCmd.Flags().StringVar(&touchState, "state", "open", "Initial task state (open, in_progress, completed, blocked, cancelled)")
	touchCmd.Flags().IntVar(&touchPriority, "priority", 3, "Initial task priority (1-4)")
	touchCmd.Flags().StringVar(&touchLabels, "labels", "", "Initial task labels (JSON array)")
	touchCmd.Flags().StringVar(&touchDueAt, "due-at", "", "Initial task due date")
	touchCmd.Flags().StringVar(&touchStartAt, "start-at", "", "Initial task start date")
}

func runTouch(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	// Validate state
	if touchState != "" {
		if err := domain.ValidateState(touchState); err != nil {
			return err
		}
	}

	// Validate priority
	if touchPriority > 0 {
		if err := domain.ValidatePriority(touchPriority); err != nil {
			return err
		}
	}

	// Validate labels if provided
	if touchLabels != "" {
		var labels []string
		if err := json.Unmarshal([]byte(touchLabels), &labels); err != nil {
			return fmt.Errorf("invalid labels JSON: %w", err)
		}
	}

	// Process description if provided
	var description string
	if touchDescription != "" {
		var err error
		description, err = readDescriptionValue(touchDescription)
		if err != nil {
			return fmt.Errorf("failed to read description: %w", err)
		}
	}

	// Create task parameters
	params := &createTaskParams{
		title:       touchTitle,
		description: description,
		state:       touchState,
		priority:    touchPriority,
		labels:      touchLabels,
		dueAt:       touchDueAt,
		startAt:     touchStartAt,
	}

	// Create each task
	for _, path := range args {
		if err := createTask(database, actorUUID, path, params); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created task: %s\n", path)
	}

	return nil
}

type createTaskParams struct {
	title       string
	description string
	state       string
	priority    int
	labels      string
	dueAt       string
	startAt     string
}

func createTask(database *db.DB, actorUUID, path string, params *createTaskParams) error {
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
	title := params.title
	if title == "" {
		title = normalizedSlug
	}

	// Default state to "open" if not provided
	state := params.state
	if state == "" {
		state = "open"
	}

	// Default priority to 3 if not provided
	priority := params.priority
	if priority == 0 {
		priority = 3
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
			return fmt.Errorf("no root container found (create a project first with 'wrkq mkdir')")
		}
	}

	// Create task
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Build INSERT statement with optional fields
	query := `
		INSERT INTO tasks (
			id, slug, title, description, project_uuid, state, priority,
			labels, due_at, start_at, created_by_actor_uuid, updated_by_actor_uuid
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	result, err := tx.Exec(query,
		"",                    // id (auto-generated)
		normalizedSlug,        // slug
		title,                 // title
		params.description,    // description
		projectUUID,           // project_uuid
		state,                 // state
		priority,              // priority
		params.labels,         // labels (can be empty string or JSON)
		params.dueAt,          // due_at (can be empty string)
		params.startAt,        // start_at (can be empty string)
		actorUUID,             // created_by_actor_uuid
		actorUUID,             // updated_by_actor_uuid
	)
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
	payload := fmt.Sprintf(`{"slug":"%s","title":"%s","state":"%s","priority":%d}`, normalizedSlug, title, state, priority)
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
