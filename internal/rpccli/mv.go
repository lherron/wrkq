package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newMvCmd mirrors `wrkq mv` for the common case: moving a single task into an
// existing destination container (wrkq.task.move). Rename, container sources, and
// multi-source moves are not yet implemented and hard-error (tracked gaps).
func newMvCmd() *cobra.Command {
	var typ string
	var ifMatch int64
	var dryRun, yes, nullglob, overwriteTask bool
	cmd := &cobra.Command{
		Use:   "mv <src>... <dst>",
		Short: "Move or rename tasks and containers",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if dryRun {
				return fmt.Errorf("mv: --dry-run not yet supported in wrkq-rpccli")
			}
			return runMv(cmd, args)
		},
	}
	cmd.Flags().StringVar(&typ, "type", "", "Force type: t (task), p (project/container)")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Only move if etag matches")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Show what would be moved without applying")
	cmd.Flags().BoolVar(&yes, "yes", false, "Skip confirmation prompts")
	cmd.Flags().BoolVar(&nullglob, "nullglob", false, "Zero matches is a no-op instead of error")
	cmd.Flags().BoolVar(&overwriteTask, "overwrite-task", false, "Allow overwriting existing tasks")
	return cmd
}

func runMv(cmd *cobra.Command, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("mv: only single-source move into an existing container is implemented in wrkq-rpccli so far")
	}
	src, dst := args[0], args[1]

	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	// Legacy: applyProjectRootToSelector(false) for every source and the dst.
	src = sc.selector(src, false)
	dst = sc.selector(dst, false)

	// Resolve the source task and capture its pre-move path (the legacy output's
	// "source"). Non-task sources / rename are not implemented yet.
	show, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": src})
	if err != nil {
		if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
			return fmt.Errorf("source not found: %s", src)
		}
		return err
	}
	var t struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(show, &t); err != nil {
		return err
	}

	params := map[string]any{"task": src, "targetPath": dst}
	if actor != "" {
		params["actor"] = actor
	}
	if _, err := tr.Call(cmd.Context(), "wrkq.task.move", params); err != nil {
		if re, ok := err.(*Error); ok {
			return fmt.Errorf("%s", re.Message)
		}
		return err
	}

	if isStdoutTTY(cmd.OutOrStdout()) {
		fmt.Fprintf(cmd.OutOrStdout(), "Moved task: %s -> %s\n", t.ID, dst)
		return nil
	}
	return encodeJSONIndent(cmd, map[string]interface{}{
		"type": "task", "source": t.ID, "destination": dst, "moved": true,
	})
}
