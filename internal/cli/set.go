package cli

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/bulk"
	"github.com/lherron/wrkq/internal/cli/appctx"
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
Supported fields: state, priority, title, slug, labels, meta, due_at, start_at, description, specification, kind, parent_task, assignee, requested_by, assigned_project, resolution, cp_project_id, cp_work_item_id, cp_run_id, session_id, run_status

Reparenting:
  --parent-task (or --parent-id) accepts a task ID or path. The task is set
  to kind=subtask unless --kind is also given. Pass an empty value to clear
  the parent (reverts kind to task unless --kind is given). Parent and child
  must be in the same container, a task cannot be its own parent, and subtasks
  cannot themselves have subtasks (max depth 1).

Description can be set from:
  - String: --description "text"
  - File: --description @file.md
  - Stdin: --description - (or use -d flag)

Specification can be set from:
  - String: --specification "text"
  - File: --specification @file.md
  - Stdin: --specification -

Examples:
  wrkq set T-00001 --state in_progress
  wrkq set T-00001 --description "New description"
  wrkq set T-00001 --description @notes.md
  wrkq set T-00001 -d "New description"
  echo "New description" | wrkq set T-00001 -d -
  wrkq set T-00001 --state in_progress --priority 1 --title "New Title"
  wrkq set T-00001 --kind bug
  wrkq set T-00001 --parent-task T-00002
  wrkq set T-00001 --parent-id ""
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
	setSpecification   string
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
	setParentTask      string
	setParentID        string
	setAssignee        string
	setRequestedBy     string
	setAssignedProject string
	setResolution      string
	setCPProjectID     string
	setCPWorkItemID    string
	setCPRunID         string
	setSessionID       string
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
	setCmd.Flags().StringVar(&setSpecification, "specification", "", "Update task specification (use @file.md for file or - for stdin)")
	setCmd.Flags().StringVar(&setState, "state", "", "Update task state (idea, draft, open, in_progress, completed, blocked, cancelled, archived, deleted)")
	setCmd.Flags().IntVar(&setPriority, "priority", 0, "Update task priority (1-4)")
	setCmd.Flags().StringVar(&setTitle, "title", "", "Update task title")
	setCmd.Flags().StringVar(&setSlug, "slug", "", "Update task slug")
	setCmd.Flags().StringVar(&setLabels, "labels", "", "Update task labels (JSON array)")
	setCmd.Flags().StringVar(&setMeta, "meta", "", "Update task metadata (JSON object or null)")
	setCmd.Flags().StringVar(&setMetaFile, "meta-file", "", "Load task metadata from file (JSON object or null)")
	setCmd.Flags().StringVar(&setDueAt, "due-at", "", "Update task due date")
	setCmd.Flags().StringVar(&setStartAt, "start-at", "", "Update task start date")
	setCmd.Flags().StringVar(&setKind, "kind", "", "Update task kind (task, subtask, spike, bug, chore)")
	setCmd.Flags().StringVar(&setParentTask, "parent-task", "", "Set parent task ID or path (empty to clear); sets kind=subtask unless --kind given")
	setCmd.Flags().StringVar(&setParentID, "parent-id", "", "Alias for --parent-task")
	setCmd.Flags().StringVar(&setAssignee, "assignee", "", "Update task assignee (principal ref or bare agent slug)")
	setCmd.Flags().StringVar(&setRequestedBy, "requested-by", "", "Update requester project ID")
	setCmd.Flags().StringVar(&setAssignedProject, "assigned-project", "", "Update assignee project ID")
	setCmd.Flags().StringVar(&setResolution, "resolution", "", "Update task resolution (done, wont_do, duplicate, needs_info)")
	setCmd.Flags().StringVar(&setCPProjectID, "cp-project-id", "", "Update CP project ID (async run linkage)")
	setCmd.Flags().StringVar(&setCPWorkItemID, "cp-work-item-id", "", "Update CP work item ID (multi-agent coordination)")
	setCmd.Flags().StringVar(&setCPRunID, "cp-run-id", "", "Update CP run ID (async run linkage)")
	setCmd.Flags().StringVar(&setSessionID, "session-id", "", "Update session ID (async run linkage)")
	setCmd.Flags().StringVar(&setRunStatus, "run-status", "", "Update async run status (queued, running, completed, failed, cancelled, timed_out)")
}

func runSet(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	attr := app.Attribution()

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
	fields, err := buildFieldsFromFlags(app, cmd)
	if err != nil {
		return err
	}

	if len(fields) == 0 {
		return fmt.Errorf("no updates specified")
	}

	// Dry run handling
	if setDryRun {
		if !isStdoutTTY(cmd.OutOrStdout()) {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(map[string]interface{}{
				"dry_run": true,
				"targets": taskRefs,
				"fields":  fields,
			})
		}
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
		ShowProgress:    isStdoutTTY(cmd.OutOrStdout()),
	}

	result := op.Execute(taskRefs, func(ref string) error {
		taskUUID, _, err := selectors.ResolveTask(database, ref)
		if err != nil {
			return err
		}

		_, err = s.Tasks.UpdateFieldsWithAttribution(attr, taskUUID, fields, setIfMatch)
		return err
	})

	if !isStdoutTTY(cmd.OutOrStdout()) {
		errorsOut := make([]map[string]string, len(result.Errors))
		for i, itemErr := range result.Errors {
			errorsOut[i] = map[string]string{
				"item":  itemErr.Item,
				"error": itemErr.Error.Error(),
			}
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(map[string]interface{}{
			"total":     result.TotalItems,
			"succeeded": result.Succeeded,
			"failed":    result.Failed,
			"errors":    errorsOut,
		}); err != nil {
			return err
		}
	} else {
		// Print summary
		result.PrintSummary(cmd.OutOrStdout())
	}

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

func buildFieldsFromFlags(app *appctx.App, cmd *cobra.Command) (map[string]interface{}, error) {
	database := app.DB
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
	// Handle specification
	if setSpecification != "" {
		specValue, err := readDescriptionValue(setSpecification)
		if err != nil {
			return nil, fmt.Errorf("failed to read specification: %w", err)
		}
		fields["specification"] = specValue
	}

	// Handle kind
	if setKind != "" {
		if err := domain.ValidateTaskKind(setKind); err != nil {
			return nil, err
		}
		fields["kind"] = setKind
	}

	// Handle parent task (re-parenting). --parent-id is an alias for --parent-task.
	parentChanged := false
	parentVal := ""
	switch {
	case cmd.Flags().Changed("parent-task") && cmd.Flags().Changed("parent-id"):
		return nil, fmt.Errorf("specify only one of --parent-task or --parent-id")
	case cmd.Flags().Changed("parent-task"):
		parentChanged = true
		parentVal = setParentTask
	case cmd.Flags().Changed("parent-id"):
		parentChanged = true
		parentVal = setParentID
	}
	if parentChanged {
		if strings.TrimSpace(parentVal) == "" {
			// Clear parent; revert to a top-level task unless caller set kind explicitly.
			fields["parent_task_uuid"] = nil
			if setKind == "" {
				fields["kind"] = "task"
			}
		} else {
			parentRef := applyProjectRootToSelector(app.Config, parentVal, false)
			parentUUID, _, err := selectors.ResolveTask(database, parentRef)
			if err != nil {
				return nil, fmt.Errorf("failed to resolve parent task: %w", err)
			}
			fields["parent_task_uuid"] = parentUUID
			if setKind == "" {
				fields["kind"] = "subtask"
			}
		}
	}

	// Handle assignee
	if setAssignee != "" {
		principalRef, err := attribution.NormalizeCompat(setAssignee)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve assignee: %w", err)
		}
		fields["assignee_principal_ref"] = principalRef
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

	// Handle CP work item ID
	if setCPWorkItemID != "" {
		fields["cp_work_item_id"] = setCPWorkItemID
	}

	// Handle CP run ID
	if setCPRunID != "" {
		fields["cp_run_id"] = setCPRunID
	}

	// Handle session ID (stored internally as cp_session_id)
	if setSessionID != "" {
		fields["cp_session_id"] = setSessionID
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
