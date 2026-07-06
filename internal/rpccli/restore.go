package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// newRestoreCmd mirrors `wrkq restore` on the caller-owned-confirmation seam
// (architecture/records/invariants/wrkq.mutation.caller-owned-confirmation.yaml).
//
// restore is the rare mutation that NEVER prompts, so it carries no confirmation
// flow: the seam contribution is purely the non-interactive, server-side semantic
// op. Per the daedalus B-ruling (T-05100 hrcchat#10185 item 4) the whole legacy
// restore — move-on-restore (--to), field updates (--title/--description/
// --priority/--labels/--assignee), --comment, --state, cascade restore, the
// task.restored event payload, slug-conflict + etag precedence — runs as one
// atomic wrkq.task.restore call, NOT composed client-side (which would expose
// intermediate states + drift). The mirror owns ONLY the caller-side scoping of
// the task ref + the --to destination path and the legacy output rendering.
//
// Container targets use the narrow wrkq.container.restore method after the task
// restore method returns a genuine NOT_FOUND. The task call stays first so
// validation-before-resolution precedence for restore flags remains server-owned.
func newRestoreCmd() *cobra.Command {
	var (
		to          string
		title       string
		description string
		state       string
		priority    int
		labels      string
		assignee    string
		ifMatch     int64
		comment     string
	)
	cmd := &cobra.Command{
		Use:   "restore <path|id|uuid>",
		Short: "Restore deleted or archived tasks",
		Long: `Restores a deleted or archived task to active state.

The task must be in 'deleted' or 'archived' state. By default, restores to 'open' state.
Subtasks are cascade-restored when their parent is restored.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRestore(cmd, args[0], restoreFlags{
				to: to, title: title, description: description, state: state,
				priority: priority, labels: labels, assignee: assignee,
				ifMatch: ifMatch, comment: comment,
			})
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "Restore to different container/slug (path)")
	cmd.Flags().StringVar(&title, "title", "", "Update title on restore")
	cmd.Flags().StringVar(&description, "description", "", "Update description on restore")
	cmd.Flags().StringVar(&state, "state", "", "Restore to specific state (default: open)")
	cmd.Flags().IntVar(&priority, "priority", 0, "Update priority on restore (1-4)")
	cmd.Flags().StringVar(&labels, "labels", "", "Update labels on restore (JSON array)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Update assignee on restore")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Conditional restore (etag)")
	cmd.Flags().StringVar(&comment, "comment", "", "Add comment explaining restoration")
	return cmd
}

type restoreFlags struct {
	to          string
	title       string
	description string
	state       string
	priority    int
	labels      string
	assignee    string
	ifMatch     int64
	comment     string
}

func runRestore(cmd *cobra.Command, arg string, f restoreFlags) error {
	claims := &stdinClaims{}
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	ctx := cmd.Context()

	// Caller-side scoping (legacy: applyProjectRootToSelector(arg, false) +
	// applyProjectRootToPath(--to, false)). The server never reads project-root.
	arg = sc.selector(arg, false)
	to := f.to
	if to != "" {
		to = sc.path(to, false)
	}

	// Build the extended, explicit-intent restore params; the server applies the
	// whole semantic op atomically AND owns validation-before-lookup, so we call it
	// FIRST (no speculative task.show pre-resolve) to preserve the server's
	// validation-before-resolution precedence: a bad --state/--priority/--labels/
	// --assignee on a MISSING ref surfaces the VALIDATION error, not not-found.
	params := map[string]any{"task": arg}
	if actor != "" {
		params["actor"] = actor
	}
	if f.state != "" {
		params["state"] = f.state
	}
	if to != "" {
		params["toPath"] = to
	}
	if f.title != "" {
		params["title"] = f.title
	}
	if f.description != "" {
		params["description"] = f.description
	}
	if f.priority != 0 {
		params["priority"] = f.priority
	}
	if f.labels != "" {
		params["labels"] = f.labels
	}
	if f.assignee != "" {
		params["assignee"] = f.assignee
	}
	if f.comment != "" {
		comment, cerr := readTextValue(f.comment, "--comment", cmd.InOrStdin(), claims)
		if cerr != nil {
			return cerr
		}
		params["comment"] = comment
	}
	if f.ifMatch != 0 {
		params["ifMatch"] = f.ifMatch
	}

	restored, rerr := tr.Call(ctx, "wrkq.task.restore", params)
	if rerr != nil {
		// Only a genuine NOT_FOUND (ref didn't resolve to a task) falls through to
		// container restore / legacy not-found. Validation/conflict errors
		// (bad flag, etag/slug conflict) pass through unchanged — the server already
		// owns their precedence ahead of the task lookup.
		if isNotFound(rerr) {
			params := map[string]any{"container": arg}
			if actor != "" {
				params["actor"] = actor
			}
			raw, cerr := tr.Call(ctx, "wrkq.container.restore", params)
			if cerr == nil {
				var c struct {
					UUID string `json:"uuid"`
				}
				if uerr := json.Unmarshal(raw, &c); uerr != nil {
					return uerr
				}
				if !isStdoutTTY(cmd.OutOrStdout()) {
					return encodeJSONIndent(cmd, map[string]interface{}{
						"type":     "container",
						"selector": arg,
						"uuid":     c.UUID,
						"restored": true,
					})
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Restored container: %s\n", arg)
				return nil
			}
			if !isNotFound(cerr) {
				return errors.New(rpcMessage(cerr))
			}
			// Neither task nor container: legacy emits "not found: <arg>".
			return fmt.Errorf("not found: %s", arg)
		}
		return errors.New(rpcMessage(rerr))
	}

	// Use the RETURNED DTO for output (id/uuid + the canonical applied state); no
	// speculative pre-resolve. Legacy echoes the parsed state (default "open").
	var t struct {
		ID    string `json:"id"`
		UUID  string `json:"uuid"`
		State string `json:"state"`
	}
	if uerr := json.Unmarshal(restored, &t); uerr != nil {
		return uerr
	}
	targetState := "open"
	if f.state != "" && t.State != "" {
		targetState = t.State
	}

	out := cmd.OutOrStdout()
	if !isStdoutTTY(out) {
		return encodeJSONIndent(cmd, map[string]interface{}{
			"type":          "task",
			"id":            t.ID,
			"uuid":          t.UUID,
			"restored":      true,
			"state":         targetState,
			"moved":         to != "",
			"comment_added": f.comment != "",
		})
	}
	fmt.Fprintf(out, "Restored task: %s\n", t.ID)
	return nil
}
