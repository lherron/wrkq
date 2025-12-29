package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
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
	touchKind        string
	touchParentTask  string
	touchAssignee    string
	touchLabels      string
	touchDueAt       string
	touchStartAt     string
)

func init() {
	rootCmd.AddCommand(touchCmd)
	touchCmd.Flags().StringVarP(&touchTitle, "title", "t", "", "Title for the task (defaults to slug)")
	touchCmd.Flags().StringVarP(&touchDescription, "description", "d", "", "Description for the task (use @file.md for file or - for stdin)")
	touchCmd.Flags().StringVar(&touchState, "state", "open", "Initial task state (draft, open, in_progress, completed, blocked, cancelled)")
	touchCmd.Flags().IntVar(&touchPriority, "priority", 3, "Initial task priority (1-4)")
	touchCmd.Flags().StringVar(&touchKind, "kind", "", "Task kind: task, subtask, spike, bug, chore (default: task)")
	touchCmd.Flags().StringVar(&touchParentTask, "parent-task", "", "Parent task ID or path (for subtasks)")
	touchCmd.Flags().StringVar(&touchAssignee, "assignee", "", "Assignee actor slug or ID")
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

	// Validate kind if provided
	if touchKind != "" {
		if err := domain.ValidateTaskKind(touchKind); err != nil {
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

	// Resolve parent task if provided
	var parentTaskUUID *string
	if touchParentTask != "" {
		uuid, _, err := selectors.ResolveTask(database, touchParentTask)
		if err != nil {
			return fmt.Errorf("failed to resolve parent task: %w", err)
		}
		parentTaskUUID = &uuid
	}

	// Resolve assignee if provided
	var assigneeActorUUID *string
	if touchAssignee != "" {
		resolver := actors.NewResolver(database.DB)
		uuid, err := resolver.Resolve(touchAssignee)
		if err != nil {
			return fmt.Errorf("failed to resolve assignee: %w", err)
		}
		assigneeActorUUID = &uuid
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

	// Create store
	s := store.New(database)

	// Create each task
	for _, path := range args {
		// Use shared resolver to get parent container and normalized slug
		parentUUID, normalizedSlug, _, err := selectors.ResolveParentContainer(database, path)
		if err != nil {
			return err
		}

		// Default title to slug if not provided
		title := touchTitle
		if title == "" {
			title = normalizedSlug
		}

		// Default state to "open" if not provided
		state := touchState
		if state == "" {
			state = "open"
		}

		// Default priority to 3 if not provided
		priority := touchPriority
		if priority == 0 {
			priority = 3
		}

		// Determine project UUID
		var projectUUID string
		if parentUUID != nil {
			projectUUID = *parentUUID
		} else {
			// Task at root - find "inbox" or any root container
			err := database.QueryRow(`SELECT uuid FROM containers WHERE parent_uuid IS NULL LIMIT 1`).Scan(&projectUUID)
			if err != nil {
				return fmt.Errorf("no root container found (create a project first with 'wrkq mkdir')")
			}
		}

		// Create the task using the store
		_, err = s.Tasks.Create(actorUUID, store.CreateParams{
			Slug:              normalizedSlug,
			Title:             title,
			Description:       description,
			ProjectUUID:       projectUUID,
			State:             state,
			Priority:          priority,
			Kind:              touchKind,
			ParentTaskUUID:    parentTaskUUID,
			AssigneeActorUUID: assigneeActorUUID,
			Labels:            touchLabels,
			DueAt:             touchDueAt,
			StartAt:           touchStartAt,
		})
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created task: %s\n", path)
	}

	return nil
}

