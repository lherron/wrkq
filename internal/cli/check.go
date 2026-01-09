package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Run pre-flight checks on tasks",
	Long: `Run pre-flight checks on tasks before starting work.

Use these commands to verify a task is ready for triage or implementation.

Examples:
  wrkq check blocked T-00001     # Check if task is blocked by incomplete dependencies
  wrkq check blocked T-00001 --json  # Output blockers as JSON`,
}

var checkBlockedCmd = &cobra.Command{
	Use:   "blocked <task>",
	Short: "Check if a task is blocked by incomplete dependencies",
	Long: `Check if a task has incomplete blocking dependencies.

A task is considered blocked if there are tasks with 'blocks' relations pointing
to it that are not yet completed (states: open, in_progress, blocked, draft).

Exit codes:
  0 - Task is not blocked (safe to proceed)
  1 - Task is blocked (lists blocking tasks)

Examples:
  wrkq check blocked T-00001           # Check and show blockers
  wrkq check blocked T-00001 --json    # Output blockers as JSON
  wrkq check blocked T-00001 --quiet   # Exit code only, no output`,
	Args: cobra.ExactArgs(1),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runCheckBlocked),
}

var (
	checkBlockedJSON   bool
	checkBlockedQuiet  bool
)

func init() {
	rootCmd.AddCommand(checkCmd)
	checkCmd.AddCommand(checkBlockedCmd)

	checkBlockedCmd.Flags().BoolVar(&checkBlockedJSON, "json", false, "Output blockers as JSON")
	checkBlockedCmd.Flags().BoolVar(&checkBlockedQuiet, "quiet", false, "Suppress output, exit code only")
}

// BlockedResult represents the result of a blocked check for JSON output
type BlockedResult struct {
	TaskID    string         `json:"task_id"`
	TaskUUID  string         `json:"task_uuid"`
	IsBlocked bool           `json:"is_blocked"`
	Blockers  []BlockerEntry `json:"blockers"`
}

// BlockerEntry represents a single blocking task
type BlockerEntry struct {
	ID    string `json:"id"`
	UUID  string `json:"uuid"`
	Slug  string `json:"slug"`
	Title string `json:"title"`
	State string `json:"state"`
}

func runCheckBlocked(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	taskRef := args[0]
	taskRef = applyProjectRootToSelector(app.Config, taskRef, false)

	// Resolve task
	taskUUID, taskID, err := selectors.ResolveTask(database, taskRef)
	if err != nil {
		return fmt.Errorf("failed to resolve task: %w", err)
	}

	// Get blockers using the store
	s := store.New(database)
	blockers, err := s.Tasks.BlockedBy(taskUUID)
	if err != nil {
		return fmt.Errorf("failed to check blockers: %w", err)
	}

	isBlocked := len(blockers) > 0

	// Quiet mode: exit code only
	if checkBlockedQuiet {
		if isBlocked {
			return fmt.Errorf("task is blocked")
		}
		return nil
	}

	// JSON output
	if checkBlockedJSON {
		result := BlockedResult{
			TaskID:    taskID,
			TaskUUID:  taskUUID,
			IsBlocked: isBlocked,
			Blockers:  make([]BlockerEntry, len(blockers)),
		}
		for i, b := range blockers {
			result.Blockers[i] = BlockerEntry{
				ID:    b.ID,
				UUID:  b.UUID,
				Slug:  b.Slug,
				Title: b.Title,
				State: b.State,
			}
		}

		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(result); err != nil {
			return err
		}

		if isBlocked {
			return fmt.Errorf("task is blocked by %d incomplete task(s)", len(blockers))
		}
		return nil
	}

	// Human-readable output
	if isBlocked {
		fmt.Fprintf(cmd.OutOrStderr(), "Error: Task %s is blocked by %d incomplete task(s):\n", taskID, len(blockers))
		for _, b := range blockers {
			fmt.Fprintf(cmd.OutOrStderr(), "  - %s: %s (state: %s)\n", b.ID, b.Title, b.State)
		}
		return fmt.Errorf("task %s is blocked", taskID)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Task %s is not blocked\n", taskID)
	return nil
}
