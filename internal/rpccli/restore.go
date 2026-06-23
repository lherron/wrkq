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
// Container targets are HARD-GATED with a clean non-zero error: there is no
// wrkq.container.restore method, so container restore cannot be byte-proven on
// this seam (mirrors the rm container hard-gate; container restore fans out with
// the container archive/restore slice).
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
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)
	ctx := cmd.Context()

	// Caller-side scoping (legacy: applyProjectRootToSelector(arg, false) +
	// applyProjectRootToPath(--to, false)). The server never reads project-root.
	arg = sc.selector(arg, false)
	to := f.to
	if to != "" {
		to = sc.path(to, false)
	}

	// Resolve the target as a task (legacy ResolveTask wins), capturing id/uuid for
	// the output. A non-task ref falls back to container — which is hard-gated here.
	show, terr := tr.Call(ctx, "wrkq.task.show", map[string]string{"task": arg})
	if terr != nil {
		if !isNotFound(terr) {
			return errors.New(rpcMessage(terr))
		}
		// Not a task: is it a container? (legacy restores containers; this seam
		// has no wrkq.container.restore — hard-gate, no silent degradation.)
		if _, cerr := tr.Call(ctx, "wrkq.container.show", map[string]string{"path": arg}); cerr == nil {
			return fmt.Errorf("restore: container targets are not yet supported in wrkq-rpccli (gated: container restore mode pending; got %s)", arg)
		}
		// Neither: legacy emits "not found: <arg>".
		return fmt.Errorf("not found: %s", arg)
	}
	var t struct {
		ID   string `json:"id"`
		UUID string `json:"uuid"`
	}
	if uerr := json.Unmarshal(show, &t); uerr != nil {
		return uerr
	}

	// Build the extended, explicit-intent restore params; the server applies the
	// whole semantic op atomically.
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
		params["comment"] = f.comment
	}
	if f.ifMatch != 0 {
		params["ifMatch"] = f.ifMatch
	}

	restored, rerr := tr.Call(ctx, "wrkq.task.restore", params)
	if rerr != nil {
		return errors.New(rpcMessage(rerr))
	}
	// Echo the canonical target state the server applied (legacy echoes the parsed
	// state, default "open"). Read it back from the returned DTO so a normalized
	// state alias renders identically to legacy.
	targetState := "open"
	if f.state != "" {
		var rt struct {
			State string `json:"state"`
		}
		if uerr := json.Unmarshal(restored, &rt); uerr == nil && rt.State != "" {
			targetState = rt.State
		}
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
