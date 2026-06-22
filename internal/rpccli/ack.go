package rpccli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/spf13/cobra"
)

// newAckCmd mirrors `wrkq ack`. It is RPC-backed via client-local composition:
// per ref it reads the task (wrkq.task.show) to classify already-acknowledged
// skips and to surface not-found, then performs the durable mutation through
// wrkq.task.acknowledge (the server remains authoritative for the force/terminal
// gate, attribution, and etag). Output and error wording match the legacy CLI
// byte-for-byte (proven by TestParity).
func newAckCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "ack <task|id>...",
		Short: "Acknowledge completed tasks",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAck(cmd, args, force)
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Allow ack on non-completed tasks")
	return cmd
}

func runAck(cmd *cobra.Command, args []string, force bool) error {
	h, err := bootstrap.Open(dbOverride(cmd))
	if err != nil {
		return err
	}
	sc, err := newScoper(cmd, h)
	if err != nil {
		_ = h.Close()
		return err
	}
	tr, err := NewInProcess(h)
	if err != nil {
		_ = h.Close()
		return err
	}
	defer func() { _ = tr.Close() }()

	actor := ""
	if f := cmd.Flag("as"); f != nil {
		actor = f.Value.String()
	}

	total, acked, skipped := len(args), 0, 0
	for _, ref := range args {
		// Legacy: applyProjectRootToSelector(arg, false) — scoped value drives both
		// the show/acknowledge calls and the error wording.
		ref = sc.selector(ref, false)
		show, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": ref})
		if err != nil {
			return ackResolveError(ref, err)
		}
		var t struct {
			AcknowledgedAt string `json:"acknowledgedAt"`
		}
		if err := json.Unmarshal(show, &t); err != nil {
			return err
		}
		if strings.TrimSpace(t.AcknowledgedAt) != "" {
			skipped++
			continue
		}
		params := map[string]any{"task": ref, "force": force}
		if actor != "" {
			params["actor"] = actor
		}
		if _, err := tr.Call(cmd.Context(), "wrkq.task.acknowledge", params); err != nil {
			return ackMutateError(ref, err)
		}
		acked++
	}
	return writeAckCounts(cmd, total, acked, skipped)
}

// ackResolveError reformats a not-found from the show step into the legacy CLI
// wording ("task not found: <ref>").
func ackResolveError(ref string, err error) error {
	if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
		return fmt.Errorf("task not found: %s", ref)
	}
	return err
}

// ackMutateError reformats the server's terminal-state validation error into the
// legacy CLI wording ("cannot ack <ref>: state is <state> (requires completed or
// cancelled)"), pulling the state out of the error data payload.
func ackMutateError(ref string, err error) error {
	if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_VALIDATION" {
		var d struct {
			State string `json:"state"`
		}
		if len(re.Data) > 0 && json.Unmarshal(re.Data, &d) == nil && d.State != "" {
			return fmt.Errorf("cannot ack %s: state is %s (requires completed or cancelled)", ref, d.State)
		}
	}
	return err
}

// writeAckCounts renders the acknowledge counts identically to legacy `wrkq ack`:
// indented JSON when non-TTY, human lines when TTY.
func writeAckCounts(cmd *cobra.Command, total, acked, skipped int) error {
	out := cmd.OutOrStdout()
	if !isStdoutTTY(out) {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]int{"total": total, "acknowledged": acked, "skipped": skipped})
	}
	if acked > 0 {
		fmt.Fprintf(out, "Acknowledged %d task(s)\n", acked)
	}
	if skipped > 0 {
		fmt.Fprintf(out, "Skipped %d task(s) already acknowledged\n", skipped)
	}
	if acked == 0 && skipped == 0 {
		fmt.Fprintln(out, "No tasks acknowledged")
	}
	return nil
}

// isStdoutTTY mirrors internal/cli.isStdoutTTY so output-mode selection matches.
func isStdoutTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
