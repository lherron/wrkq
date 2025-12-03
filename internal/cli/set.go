package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var setCmd = &cobra.Command{
	Use:   "set <path|id>... [flags]",
	Short: "Mutate task fields",
	Long: `Updates one or more task fields quickly.
Supported fields: state, priority, title, slug, labels, due_at, start_at, description

Description can be set from:
  - String: --description "text"
  - File: --description @file.md
  - Stdin: --description - (or use -d flag)

Examples:
  wrkq set T-00001 --state in_progress
  wrkq set T-00001 --description "New description"
  wrkq set T-00001 --description @notes.md
  wrkq set T-00001 -d "New description"
  echo "New description" | wrkq set T-00001 -d -
  wrkq set T-00001 --state in_progress --priority 1 --title "New Title"`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runSet),
}

var (
	setIfMatch          int64
	setDryRun           bool
	setJobs             int
	setContinueOnError  bool
	setBatchSize        int
	setOrdered          bool
	setDescription      string
	setState            string
	setPriority         int
	setTitle            string
	setSlug             string
	setLabels           string
	setDueAt            string
	setStartAt          string
)

func init() {
	rootCmd.AddCommand(setCmd)
	setCmd.Flags().Int64Var(&setIfMatch, "if-match", 0, "Only update if etag matches")
	setCmd.Flags().BoolVar(&setDryRun, "dry-run", false, "Show what would be changed without applying")
	setCmd.Flags().IntVarP(&setJobs, "jobs", "j", 1, "Number of parallel workers (0 = auto-detect CPU count)")
	setCmd.Flags().BoolVar(&setContinueOnError, "continue-on-error", false, "Continue processing on errors")
	setCmd.Flags().IntVar(&setBatchSize, "batch-size", 1, "Group operations into batches (not yet implemented)")
	setCmd.Flags().BoolVar(&setOrdered, "ordered", false, "Preserve input order (disables parallelism)")
	setCmd.Flags().StringVarP(&setDescription, "description", "d", "", "Update task description (use @file.md for file or - for stdin)")
	setCmd.Flags().StringVar(&setState, "state", "", "Update task state (open, in_progress, completed, blocked, cancelled)")
	setCmd.Flags().IntVar(&setPriority, "priority", 0, "Update task priority (1-4)")
	setCmd.Flags().StringVar(&setTitle, "title", "", "Update task title")
	setCmd.Flags().StringVar(&setSlug, "slug", "", "Update task slug")
	setCmd.Flags().StringVar(&setLabels, "labels", "", "Update task labels (JSON array)")
	setCmd.Flags().StringVar(&setDueAt, "due-at", "", "Update task due date")
	setCmd.Flags().StringVar(&setStartAt, "start-at", "", "Update task start date")
}

func runSet(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	// All args are task refs now (no more key=value parsing)
	taskRefs := args

	// Check for stdin input (single "-" as task ref)
	if len(taskRefs) == 1 && taskRefs[0] == "-" {
		stdinRefs, err := readLinesFromStdin(cmd.InOrStdin())
		if err != nil {
			return fmt.Errorf("failed to read from stdin: %w", err)
		}
		taskRefs = stdinRefs
	}

	if len(taskRefs) == 0 {
		return fmt.Errorf("no tasks specified")
	}

	// Build fields map from flags
	fields, err := buildFieldsFromFlags()
	if err != nil {
		return err
	}

	if len(fields) == 0 {
		return fmt.Errorf("no updates specified")
	}

	// Dry run handling
	if setDryRun {
		for _, ref := range taskRefs {
			fmt.Fprintf(cmd.OutOrStdout(), "Would update task %s: %+v\n", ref, fields)
		}
		return nil
	}

	// Execute bulk operation
	op := &bulk.Operation{
		Jobs:            setJobs,
		ContinueOnError: setContinueOnError,
		Ordered:         setOrdered,
		ShowProgress:    true,
	}

	result := op.Execute(taskRefs, func(ref string) error {
		taskUUID, _, err := selectors.ResolveTask(database, ref)
		if err != nil {
			return err
		}

		return updateTask(database, actorUUID, taskUUID, fields, setIfMatch)
	})

	// Print summary
	result.PrintSummary(cmd.OutOrStdout())

	// Exit with appropriate code
	os.Exit(result.ExitCode())
	return nil
}

func readLinesFromStdin(r io.Reader) ([]string, error) {
	var lines []string
	scanner := bufio.NewScanner(r)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			lines = append(lines, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return lines, nil
}

func buildFieldsFromFlags() (map[string]interface{}, error) {
	fields := make(map[string]interface{})

	// Handle state
	if setState != "" {
		if err := domain.ValidateState(setState); err != nil {
			return nil, err
		}
		fields["state"] = setState
	}

	// Handle priority
	if setPriority > 0 {
		if err := domain.ValidatePriority(setPriority); err != nil {
			return nil, err
		}
		fields["priority"] = setPriority
	}

	// Handle title
	if setTitle != "" {
		fields["title"] = setTitle
	}

	// Handle slug
	if setSlug != "" {
		normalized, err := paths.NormalizeSlug(setSlug)
		if err != nil {
			return nil, fmt.Errorf("invalid slug: %w", err)
		}
		fields["slug"] = normalized
	}

	// Handle labels
	if setLabels != "" {
		// Parse as JSON array
		var labels []string
		if err := json.Unmarshal([]byte(setLabels), &labels); err != nil {
			return nil, fmt.Errorf("invalid labels JSON: %w", err)
		}
		fields["labels"] = setLabels
	}

	// Handle due_at
	if setDueAt != "" {
		fields["due_at"] = setDueAt
	}

	// Handle start_at
	if setStartAt != "" {
		fields["start_at"] = setStartAt
	}

	// Handle description
	if setDescription != "" {
		descValue, err := readDescriptionValue(setDescription)
		if err != nil {
			return nil, fmt.Errorf("failed to read description: %w", err)
		}
		fields["description"] = descValue
	}

	return fields, nil
}

func updateTask(database *db.DB, actorUUID, taskUUID string, fields map[string]interface{}, ifMatch int64) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Get current etag
	var currentETag int64
	err = tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentETag)
	if err != nil {
		return fmt.Errorf("failed to get current etag: %w", err)
	}

	// Check etag if --if-match was provided
	if ifMatch > 0 && currentETag != ifMatch {
		return &domain.ETagMismatchError{Expected: ifMatch, Actual: currentETag}
	}

	// Build UPDATE query
	var setClauses []string
	var args []interface{}

	for key, value := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", key))
		args = append(args, value)
	}

	// Increment etag
	setClauses = append(setClauses, "etag = etag + 1")
	setClauses = append(setClauses, "updated_by_actor_uuid = ?")
	args = append(args, actorUUID)

	// Add WHERE clause
	args = append(args, taskUUID)

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
	_, err = tx.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to update task: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	changes, _ := json.Marshal(fields)
	changesStr := string(changes)
	newETag := currentETag + 1

	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.updated",
		ETag:         &newETag,
		Payload:      &changesStr,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	return tx.Commit()
}
