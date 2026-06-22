package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// newCheckCmd mirrors the `wrkq check` command group. Only the `check blocked`
// subcommand exists in legacy; it is RPC-backed via wrkq.task.blockedView (the
// server-owned compatibility projection that reproduces legacy BlockedResult and
// the store.Tasks.BlockedBy dependency query).
//
// Parity-proven surface (non-TTY): default/`--json` JSON output + exit-code
// semantics (blocked → exit 1 with both the JSON body on stdout and an
// Error line on stderr). `--quiet` is implemented (exit-code only). The TTY
// human-readable table is hard-gated (not yet implemented) so a narrower
// behavior can never masquerade as parity.
func newCheckCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Run pre-flight checks on tasks",
	}
	cmd.AddCommand(newCheckBlockedCmd())
	return cmd
}

func newCheckBlockedCmd() *cobra.Command {
	var asJSON, quiet bool
	cmd := &cobra.Command{
		Use:   "blocked <task>",
		Short: "Check if a task is blocked by incomplete dependencies",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: applyProjectRootToSelector(arg, false) before resolving.
			sref := sc.selector(args[0], false)
			raw, err := tr.Call(cmd.Context(), "wrkq.task.blockedView",
				map[string]any{"task": sref})
			if err != nil {
				if re, ok := err.(*Error); ok {
					// Legacy wraps the resolve failure as
					// "failed to resolve task: <err>"; the server surfaced the raw
					// resolve message, so re-wrap it identically here.
					return fmt.Errorf("failed to resolve task: %s", re.Message)
				}
				return err
			}
			var view struct {
				TaskID    string            `json:"task_id"`
				TaskUUID  string            `json:"task_uuid"`
				IsBlocked bool              `json:"is_blocked"`
				Blockers  []json.RawMessage `json:"blockers"`
			}
			if err := json.Unmarshal(raw, &view); err != nil {
				return err
			}

			// Quiet mode: exit code only, no output.
			if quiet {
				if view.IsBlocked {
					return errors.New("task is blocked")
				}
				return nil
			}

			// Legacy non-TTY (and --json) path: indented JSON to stdout, then an
			// error (exit 1) when blocked. The human-readable table path is the only
			// branch we cannot yet reproduce; hard-gate it on a real TTY.
			if !asJSON && isStdoutTTY(cmd.OutOrStdout()) {
				return errors.New("check blocked: human-readable output not yet implemented in wrkq-rpccli (use --json)")
			}

			enc := json.NewEncoder(cmd.OutOrStdout())
			enc.SetIndent("", "  ")
			if err := enc.Encode(raw); err != nil {
				return err
			}
			if view.IsBlocked {
				return fmt.Errorf("task is blocked by %d incomplete task(s)", len(view.Blockers))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output blockers as JSON")
	cmd.Flags().BoolVar(&quiet, "quiet", false, "Suppress output, exit code only")
	return cmd
}

// newCheckInboxCmd mirrors `wrkq check-inbox`. RPC-backed via wrkq.task.inboxView
// (the server-owned compatibility list projection reproducing the legacy open-
// inbox + ack-pending queries). The project-root inbox path and project id are
// scoped CALLER-side and passed as params; the view never reads project-root.
//
// Parity-proven surface (non-TTY): default ndjson, `--json` (indented array),
// `--ndjson`/`--porcelain` (ndjson). The TTY table is hard-gated.
func newCheckInboxCmd() *cobra.Command {
	var asJSON, ndjson, porcelain bool
	cmd := &cobra.Command{
		Use:   "check-inbox",
		Short: "List open tasks in the inbox",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: applyProjectRootToPath("inbox", false) for the inbox path and
			// normalizeProjectRoot(cfg) for the requested-by project id.
			inboxPath := sc.paths([]string{"inbox"}, false)
			scopedInbox := "inbox"
			if len(inboxPath) == 1 {
				scopedInbox = inboxPath[0]
			}
			params := map[string]any{"inboxPath": scopedInbox}
			if pid := sc.projectRoot(); pid != "" {
				params["projectId"] = pid
			}

			raw, err := tr.Call(cmd.Context(), "wrkq.task.inboxView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					return errors.New(re.Message)
				}
				return err
			}
			var view struct {
				Items []json.RawMessage `json:"items"`
			}
			if err := json.Unmarshal(raw, &view); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			// --json: indented JSON array of the row objects.
			if asJSON {
				enc := json.NewEncoder(out)
				enc.SetIndent("", "  ")
				return enc.Encode(view.Items)
			}
			// --ndjson / --porcelain / non-TTY default: one compact object per line.
			if ndjson || porcelain || !isStdoutTTY(out) {
				for _, it := range view.Items {
					if _, err := out.Write(append([]byte(it), '\n')); err != nil {
						return err
					}
				}
				return nil
			}
			return errors.New("check-inbox: table output not yet implemented in wrkq-rpccli (use --json / --ndjson / --porcelain)")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Stable machine-readable output")
	return cmd
}
