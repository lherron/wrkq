package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/spf13/cobra"
)

var setCmd = &cobra.Command{
	Use:   "set <path|id>... key=value [key=value...]",
	Short: "Mutate task fields",
	Long: `Updates one or more task fields quickly.
Supported keys: state, priority, title, slug, labels, due_at, start_at`,
	Args: cobra.MinimumNArgs(2),
	RunE: runSet,
}

var (
	setIfMatch          int64
	setDryRun           bool
	setJobs             int
	setContinueOnError  bool
	setBatchSize        int
	setOrdered          bool
)

func init() {
	rootCmd.AddCommand(setCmd)
	setCmd.Flags().Int64Var(&setIfMatch, "if-match", 0, "Only update if etag matches")
	setCmd.Flags().BoolVar(&setDryRun, "dry-run", false, "Show what would be changed without applying")
	setCmd.Flags().IntVarP(&setJobs, "jobs", "j", 1, "Number of parallel workers (0 = auto-detect CPU count)")
	setCmd.Flags().BoolVar(&setContinueOnError, "continue-on-error", false, "Continue processing on errors")
	setCmd.Flags().IntVar(&setBatchSize, "batch-size", 1, "Group operations into batches (not yet implemented)")
	setCmd.Flags().BoolVar(&setOrdered, "ordered", false, "Preserve input order (disables parallelism)")
}

func runSet(cmd *cobra.Command, args []string) error {
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
		return fmt.Errorf("no actor configured (set WRKQ_ACTOR, WRKQ_ACTOR_ID, or use --as flag)")
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

	// Separate task paths/IDs from key=value pairs
	var taskRefs []string
	var updates []string
	for _, arg := range args {
		if strings.Contains(arg, "=") {
			updates = append(updates, arg)
		} else {
			taskRefs = append(taskRefs, arg)
		}
	}

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
	if len(updates) == 0 {
		return fmt.Errorf("no updates specified")
	}

	// Parse updates
	fields, err := parseSetUpdates(updates)
	if err != nil {
		return err
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
		taskUUID, _, err := resolveTask(database, ref)
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

func parseSetUpdates(updates []string) (map[string]interface{}, error) {
	fields := make(map[string]interface{})

	for _, update := range updates {
		parts := strings.SplitN(update, "=", 2)
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid update: %s", update)
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Unquote if quoted
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			value = value[1 : len(value)-1]
		}

		switch key {
		case "state":
			if err := domain.ValidateState(value); err != nil {
				return nil, err
			}
			fields["state"] = value
		case "priority":
			p, err := strconv.Atoi(value)
			if err != nil {
				return nil, fmt.Errorf("invalid priority: %s", value)
			}
			if err := domain.ValidatePriority(p); err != nil {
				return nil, err
			}
			fields["priority"] = p
		case "title":
			fields["title"] = value
		case "slug":
			normalized, err := paths.NormalizeSlug(value)
			if err != nil {
				return nil, fmt.Errorf("invalid slug: %w", err)
			}
			fields["slug"] = normalized
		case "labels":
			// Parse as JSON array
			var labels []string
			if err := json.Unmarshal([]byte(value), &labels); err != nil {
				return nil, fmt.Errorf("invalid labels JSON: %w", err)
			}
			fields["labels"] = value
		case "due_at", "start_at":
			fields[key] = value
		default:
			return nil, fmt.Errorf("unsupported field: %s", key)
		}
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
