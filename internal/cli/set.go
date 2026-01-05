package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var setCmd = &cobra.Command{
	Use:     "set <path|id>... [flags]",
	Aliases: []string{"edit"},
	Short:   "Mutate task fields",
	Long: `Updates one or more task fields quickly.
Supported fields: state, priority, title, slug, labels, meta, due_at, start_at, description, kind, assignee, requested_by, assigned_project, resolution, cp_project_id, cp_run_id, cp_session_id, sdk_session_id, run_status

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
  wrkq set T-00001 --state in_progress --priority 1 --title "New Title"
  wrkq set T-00001 --kind bug
  wrkq set T-00001 --assignee agent-claude
  wrkq set T-00001 --cp-run-id run123 --run-status queued`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runSet),
}

var (
	setIfMatch         int64
	setDryRun          bool
	setJobs            int
	setContinueOnError bool
	setBatchSize       int
	setOrdered         bool
	setDescription     string
	setState           string
	setPriority        int
	setTitle           string
	setSlug            string
	setLabels          string
	setMeta            string
	setMetaFile        string
	setDueAt           string
	setStartAt         string
	setKind            string
	setAssignee        string
	setRequestedBy     string
	setAssignedProject string
	setResolution      string
	setCPProjectID     string
	setCPRunID         string
	setCPSessionID     string
	setSDKSessionID    string
	setRunStatus       string
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
	setCmd.Flags().StringVar(&setState, "state", "", "Update task state (draft, open, in_progress, completed, blocked, cancelled)")
	setCmd.Flags().IntVar(&setPriority, "priority", 0, "Update task priority (1-4)")
	setCmd.Flags().StringVar(&setTitle, "title", "", "Update task title")
	setCmd.Flags().StringVar(&setSlug, "slug", "", "Update task slug")
	setCmd.Flags().StringVar(&setLabels, "labels", "", "Update task labels (JSON array)")
	setCmd.Flags().StringVar(&setMeta, "meta", "", "Update task metadata (JSON object or null)")
	setCmd.Flags().StringVar(&setMetaFile, "meta-file", "", "Load task metadata from file (JSON object or null)")
	setCmd.Flags().StringVar(&setDueAt, "due-at", "", "Update task due date")
	setCmd.Flags().StringVar(&setStartAt, "start-at", "", "Update task start date")
	setCmd.Flags().StringVar(&setKind, "kind", "", "Update task kind (task, subtask, spike, bug, chore)")
	setCmd.Flags().StringVar(&setAssignee, "assignee", "", "Update task assignee (actor slug or ID)")
	setCmd.Flags().StringVar(&setRequestedBy, "requested-by", "", "Update requester project ID")
	setCmd.Flags().StringVar(&setAssignedProject, "assigned-project", "", "Update assignee project ID")
	setCmd.Flags().StringVar(&setResolution, "resolution", "", "Update task resolution (done, wont_do, duplicate, needs_info)")
	setCmd.Flags().StringVar(&setCPProjectID, "cp-project-id", "", "Update CP project ID (async run linkage)")
	setCmd.Flags().StringVar(&setCPRunID, "cp-run-id", "", "Update CP run ID (async run linkage)")
	setCmd.Flags().StringVar(&setCPSessionID, "cp-session-id", "", "Update CP session ID (async run linkage)")
	setCmd.Flags().StringVar(&setSDKSessionID, "sdk-session-id", "", "Update SDK session ID (async run linkage)")
	setCmd.Flags().StringVar(&setRunStatus, "run-status", "", "Update async run status (queued, running, completed, failed, cancelled, timed_out)")
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

	for i, ref := range taskRefs {
		taskRefs[i] = applyProjectRootToSelector(app.Config, ref, false)
	}

	// Build fields map from flags
	fields, err := buildFieldsFromFlags(database)
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

	// Create store
	s := store.New(database)

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

		_, err = s.Tasks.UpdateFields(actorUUID, taskUUID, fields, setIfMatch)
		return err
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

func buildFieldsFromFlags(database *db.DB) (map[string]interface{}, error) {
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

	// Handle meta
	metaSet, metaValue, err := readMetaValue(setMeta, setMetaFile)
	if err != nil {
		return nil, err
	}
	if metaSet {
		if metaValue == nil {
			fields["meta"] = nil
		} else {
			fields["meta"] = *metaValue
		}
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

	// Handle kind
	if setKind != "" {
		if err := domain.ValidateTaskKind(setKind); err != nil {
			return nil, err
		}
		fields["kind"] = setKind
	}

	// Handle assignee
	if setAssignee != "" {
		// db.DB embeds *sql.DB, so we can access it directly
		resolver := actors.NewResolver(database.DB)
		actorUUID, err := resolver.Resolve(setAssignee)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve assignee: %w", err)
		}
		fields["assignee_actor_uuid"] = actorUUID
	}

	// Handle requested_by_project_id
	if setRequestedBy != "" {
		fields["requested_by_project_id"] = setRequestedBy
	}

	// Handle assigned_project_id
	if setAssignedProject != "" {
		fields["assigned_project_id"] = setAssignedProject
	}

	// Handle resolution
	if setResolution != "" {
		if err := domain.ValidateResolution(setResolution); err != nil {
			return nil, err
		}
		fields["resolution"] = setResolution
	}

	// Handle CP project ID
	if setCPProjectID != "" {
		fields["cp_project_id"] = setCPProjectID
	}

	// Handle CP run ID
	if setCPRunID != "" {
		fields["cp_run_id"] = setCPRunID
	}

	// Handle CP session ID
	if setCPSessionID != "" {
		fields["cp_session_id"] = setCPSessionID
	}

	// Handle SDK session ID
	if setSDKSessionID != "" {
		fields["sdk_session_id"] = setSDKSessionID
	}

	// Handle run status
	if setRunStatus != "" {
		if err := domain.ValidateRunStatus(setRunStatus); err != nil {
			return nil, err
		}
		fields["run_status"] = setRunStatus
	}

	return fields, nil
}
