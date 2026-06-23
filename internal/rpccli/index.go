package rpccli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/spf13/cobra"
)

// newIndexCmd mirrors `wrkq index` and its lifecycle subcommands via the
// server-owned wrkq.index.* family. The SERVER owns the derived sidecar
// open/migrate, freshness/status, the dense embedder, and (for update/rebuild)
// kickstarting ONLY the server host's configured embedder via EnsureLlamaReady.
// The CLI owns ONLY presentation. It NEVER opens the sidecar or calls
// EnsureLlamaReady (importguard-proven).
//
// Implemented surface (byte-proven against legacy, non-TTY): index status
// (--json + non-TTY default JSON), index update / rebuild / vacuum / pause /
// resume (non-TTY JSON ack). The interactive TTY human renderers are hard-gated.
func newIndexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "index",
		Short: "Inspect and rebuild the local search index",
	}
	cmd.AddCommand(
		newIndexStatusCmd(),
		newIndexRebuildCmd(),
		newIndexUpdateCmd(),
		newIndexVacuumCmd(),
		newIndexPauseCmd(),
		newIndexResumeCmd(),
	)
	return cmd
}

func newIndexStatusCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show local search index status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			raw, err := indexCall(cmd, tr, "wrkq.index.status", nil)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			// Legacy: --json OR non-TTY → json.Encoder with indent. The TTY plain
			// key:value human form is hard-gated (never byte-tested; TTY-only).
			if asJSON || !isStdoutTTY(out) {
				return writeIndexJSON(out, raw)
			}
			return errors.New("index status: human output not yet implemented in wrkq-rpccli (use --json)")
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newIndexRebuildCmd() *cobra.Command {
	var foreground bool
	cmd := &cobra.Command{
		Use:   "rebuild",
		Short: "Rebuild the local search index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{}
			if foreground {
				params["foreground"] = true
			}
			return runIndexLifecycle(cmd, "wrkq.index.rebuild", params)
		},
	}
	cmd.Flags().BoolVar(&foreground, "foreground", false, "Run rebuild in the foreground")
	return cmd
}

func newIndexUpdateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "update",
		Short: "Index pending canonical changes",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIndexLifecycle(cmd, "wrkq.index.update", nil)
		},
	}
}

func newIndexVacuumCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "vacuum",
		Short: "Vacuum the local search index",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIndexLifecycle(cmd, "wrkq.index.vacuum", nil)
		},
	}
}

func newIndexPauseCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "pause",
		Short: "Pause background search indexing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIndexLifecycle(cmd, "wrkq.index.pause", nil)
		},
	}
}

func newIndexResumeCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "resume",
		Short: "Resume background search indexing",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runIndexLifecycle(cmd, "wrkq.index.resume", nil)
		},
	}
}

// runIndexLifecycle drives a lifecycle mutation and renders its map-shaped ack.
// Legacy non-TTY: writeJSONOutput(outputSelection{}, map) → json.Encoder, indented.
// The map keys are server-emitted in map-alphabetical order (Go marshals map keys
// sorted), and the mirror re-emits the raw bytes, so byte-parity holds. The TTY
// human one-liner ("rebuilt search index: ...") is hard-gated.
func runIndexLifecycle(cmd *cobra.Command, method string, params map[string]any) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	raw, err := indexCall(cmd, tr, method, params)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if !isStdoutTTY(out) {
		return writeIndexJSON(out, raw)
	}
	return errors.New(cmd.Name() + ": human output not yet implemented in wrkq-rpccli (pipe stdout for JSON)")
}

// indexCall sends one index-family RPC, stripping the domain-code prefix off
// errors so the mirror surfaces the legacy raw message (e.g. "search is disabled").
func indexCall(cmd *cobra.Command, tr Transport, method string, params map[string]any) (json.RawMessage, error) {
	if params == nil {
		params = map[string]any{}
	}
	raw, err := tr.Call(cmd.Context(), method, params)
	if err != nil {
		if re, ok := err.(*Error); ok {
			return nil, errors.New(re.Message)
		}
		return nil, err
	}
	return raw, nil
}

// writeIndexJSON re-emits the server's index result through legacy's json.Encoder
// byte path (indented, trailing newline). It indents the SERVER's raw bytes
// directly (json.Indent) rather than decode→re-encode, so the server's field order
// is preserved: the legacy indexdb.Status STRUCT order for the nested `status`
// block and the map-ALPHABETICAL order for the lifecycle ack keys (rebuilt/status,
// updated/status, vacuumed, status) both survive byte-for-byte. The server already
// HTML-escaped on marshal, which json.Indent preserves.
func writeIndexJSON(w io.Writer, raw json.RawMessage) error {
	var buf bytes.Buffer
	if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}
