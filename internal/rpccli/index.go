package rpccli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
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
// Implemented surface (byte-proven against legacy): index status (--json +
// non-TTY default JSON + TTY key:value human), index update / rebuild / vacuum /
// pause / resume (non-TTY JSON ack + TTY one-liners).
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
			// Legacy: --json OR non-TTY → json.Encoder with indent.
			if asJSON || !isStdoutTTY(out) {
				return writeIndexJSON(out, raw)
			}
			var status rpcIndexStatus
			if err := json.Unmarshal(raw, &status); err != nil {
				return err
			}
			return writeIndexStatusHuman(out, status)
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
// sorted), and the mirror re-emits the raw bytes, so byte-parity holds.
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
	return writeIndexLifecycleHuman(cmd, raw)
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

type rpcIndexStatus struct {
	Path                 string  `json:"path"`
	Enabled              bool    `json:"enabled"`
	Status               string  `json:"status"`
	LastIndexedEventID   int64   `json:"last_indexed_event_id"`
	CanonicalMaxEventID  int64   `json:"canonical_max_event_id"`
	StaleEventCount      int64   `json:"stale_event_count"`
	DenseModelID         string  `json:"dense_model_id,omitempty"`
	DenseDimension       int     `json:"dense_dimension,omitempty"`
	DenseVectorCount     int64   `json:"dense_vector_count,omitempty"`
	LastError            *string `json:"last_error,omitempty"`
	SearchableChunkCount int64   `json:"searchable_chunk_count"`
}

func writeIndexStatusHuman(w io.Writer, status rpcIndexStatus) error {
	if _, err := fmt.Fprintf(w, "path: %s\n", status.Path); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "status: %s\n", status.Status); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "last_indexed_event_id: %d\n", status.LastIndexedEventID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "canonical_max_event_id: %d\n", status.CanonicalMaxEventID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "stale_event_count: %d\n", status.StaleEventCount); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "chunks: %d\n", status.SearchableChunkCount); err != nil {
		return err
	}
	if status.DenseModelID != "" {
		if _, err := fmt.Fprintf(w, "dense_model: %s\n", status.DenseModelID); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "dense_dimension: %d\n", status.DenseDimension); err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "dense_vectors: %d\n", status.DenseVectorCount); err != nil {
			return err
		}
	}
	if status.LastError != nil {
		if _, err := fmt.Fprintf(w, "last_error: %s\n", *status.LastError); err != nil {
			return err
		}
	}
	return nil
}

func writeIndexLifecycleHuman(cmd *cobra.Command, raw json.RawMessage) error {
	switch cmd.Name() {
	case "rebuild":
		var result struct {
			Status *rpcIndexStatus `json:"status"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		status := result.Status
		if status == nil {
			return errors.New("index rebuild response missing status")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "rebuilt search index: %d chunks, last event %d\n", status.SearchableChunkCount, status.LastIndexedEventID)
		if status.LastError != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "dense indexing warning: %s\n", *status.LastError)
		}
	case "update":
		var result struct {
			Status *rpcIndexStatus `json:"status"`
		}
		if err := json.Unmarshal(raw, &result); err != nil {
			return err
		}
		status := result.Status
		if status == nil {
			return errors.New("index update response missing status")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "updated search index: %d chunks, last event %d\n", status.SearchableChunkCount, status.LastIndexedEventID)
		if status.LastError != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "dense indexing warning: %s\n", *status.LastError)
		}
	case "vacuum":
		fmt.Fprintln(cmd.OutOrStdout(), "vacuumed search index")
	case "pause":
		fmt.Fprintln(cmd.OutOrStdout(), "paused search indexing")
	case "resume":
		fmt.Fprintln(cmd.OutOrStdout(), "resumed search indexing")
	default:
		return fmt.Errorf("unsupported index lifecycle command: %s", cmd.Name())
	}
	return nil
}
