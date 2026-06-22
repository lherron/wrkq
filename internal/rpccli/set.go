package rpccli

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// newSetCmd mirrors `wrkq set` for the common field updates that map to the
// wrkq.task.update patch (state/priority/title/description/specification/kind/
// labels/meta/assignee/due-at/start-at). Flags with no patch field (slug,
// parent-task, requested-by, cp-*, resolution, run-status, …) hard-error as gaps.
func newSetCmd() *cobra.Command {
	var description, specification, state, title, slug, labels, meta, kind, assignee, dueAt, startAt string
	var parentTask, parentID, requestedBy, assignedProject, resolution string
	var cpProjectID, cpWorkItemID, cpRunID, sessionID, runStatus, metaFile string
	var priority int
	var ifMatch int64
	var dryRun, continueOnError bool

	cmd := &cobra.Command{
		Use:     "set <path|id>... [flags]",
		Aliases: []string{"edit"},
		Short:   "Update task fields",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun {
				return fmt.Errorf("set: --dry-run not yet supported in wrkq-rpccli")
			}
			for name, val := range map[string]string{
				"slug": slug, "parent-task": parentTask, "parent-id": parentID,
				"requested-by": requestedBy, "assigned-project": assignedProject,
				"resolution": resolution, "cp-project-id": cpProjectID,
				"cp-work-item-id": cpWorkItemID, "cp-run-id": cpRunID,
				"session-id": sessionID, "run-status": runStatus, "meta-file": metaFile,
			} {
				if val != "" {
					return fmt.Errorf("set: --%s is not yet supported in wrkq-rpccli (RPC gap)", name)
				}
			}
			patch := map[string]any{}
			if state != "" {
				patch["state"] = state
			}
			if priority != 0 {
				patch["priority"] = priority
			}
			if title != "" {
				patch["title"] = title
			}
			if description != "" {
				patch["description"] = description
			}
			if specification != "" {
				patch["specification"] = specification
			}
			if kind != "" {
				patch["kind"] = kind
			}
			if assignee != "" {
				patch["assigneePrincipalRef"] = assignee
			}
			if dueAt != "" {
				patch["dueAt"] = dueAt
			}
			if startAt != "" {
				patch["startAt"] = startAt
			}
			if labels != "" {
				var lbls []string
				if err := json.Unmarshal([]byte(labels), &lbls); err != nil {
					return fmt.Errorf("invalid --labels JSON array: %w", err)
				}
				patch["labels"] = lbls
			}
			if meta != "" {
				var m map[string]any
				if err := json.Unmarshal([]byte(meta), &m); err != nil {
					return fmt.Errorf("invalid --meta JSON object: %w", err)
				}
				patch["meta"] = m
			}
			return runSet(cmd, args, patch)
		},
	}
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only update if etag matches")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be changed without applying")
	cmd.Flags().BoolVar(&continueOnError, "continue-on-error", false, "Continue processing on errors")
	cmd.Flags().StringVarP(&description, "description", "d", "", "Update task description")
	cmd.Flags().StringVar(&specification, "specification", "", "Update task specification")
	cmd.Flags().StringVar(&state, "state", "", "Update task state")
	cmd.Flags().IntVar(&priority, "priority", 0, "Update task priority (1-4)")
	cmd.Flags().StringVar(&title, "title", "", "Update task title")
	cmd.Flags().StringVar(&slug, "slug", "", "Update task slug")
	cmd.Flags().StringVar(&labels, "labels", "", "Update task labels (JSON array)")
	cmd.Flags().StringVar(&meta, "meta", "", "Update task metadata (JSON object or null)")
	cmd.Flags().StringVar(&metaFile, "meta-file", "", "Load task metadata from file")
	cmd.Flags().StringVar(&dueAt, "due-at", "", "Update task due date")
	cmd.Flags().StringVar(&startAt, "start-at", "", "Update task start date")
	cmd.Flags().StringVar(&kind, "kind", "", "Update task kind")
	cmd.Flags().StringVar(&parentTask, "parent-task", "", "Set parent task ID or path")
	cmd.Flags().StringVar(&parentID, "parent-id", "", "Alias for --parent-task")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Update task assignee")
	cmd.Flags().StringVar(&requestedBy, "requested-by", "", "Update requester project ID")
	cmd.Flags().StringVar(&assignedProject, "assigned-project", "", "Update assignee project ID")
	cmd.Flags().StringVar(&resolution, "resolution", "", "Update task resolution")
	cmd.Flags().StringVar(&cpProjectID, "cp-project-id", "", "Update CP project ID")
	cmd.Flags().StringVar(&cpWorkItemID, "cp-work-item-id", "", "Update CP work item ID")
	cmd.Flags().StringVar(&cpRunID, "cp-run-id", "", "Update CP run ID")
	cmd.Flags().StringVar(&sessionID, "session-id", "", "Update session ID")
	cmd.Flags().StringVar(&runStatus, "run-status", "", "Update async run status")
	return cmd
}

func runSet(cmd *cobra.Command, args []string, patch map[string]any) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	total, succeeded, failed := len(args), 0, 0
	errorsOut := []map[string]string{}
	for _, ref := range args {
		// Legacy: applyProjectRootToSelector(ref, false); the scoped value is the
		// task selector and the error-report item.
		ref = sc.selector(ref, false)
		params := map[string]any{"task": ref, "patch": patch}
		if actor != "" {
			params["actor"] = actor
		}
		if _, err := tr.Call(cmd.Context(), "wrkq.task.update", params); err != nil {
			failed++
			msg := err.Error()
			if re, ok := err.(*Error); ok {
				msg = re.Message
			}
			errorsOut = append(errorsOut, map[string]string{"item": ref, "error": msg})
			continue
		}
		succeeded++
	}

	if !isStdoutTTY(cmd.OutOrStdout()) {
		if err := encodeJSONIndent(cmd, map[string]interface{}{
			"total": total, "succeeded": succeeded, "failed": failed, "errors": errorsOut,
		}); err != nil {
			return err
		}
	}
	// Legacy set ends with os.Exit(result.ExitCode()); mirror the direct exit so
	// no extra "Error:" line is printed on partial failure.
	if failed > 0 {
		os.Exit(1)
	}
	return nil
}
