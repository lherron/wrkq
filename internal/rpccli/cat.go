package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/spf13/cobra"
)

// newCatCmd mirrors `wrkq cat`. RPC-backed via the server-owned wrkq.task.catView
// compatibility projection (one consistent snapshot per task), re-rendered into
// legacy `cat` output. JSON (non-TTY default + --json) is byte-parity proven;
// ndjson/porcelain/raw modes are not yet implemented (cat is `partial`, not fully
// migrated — see docs/rpc-cli-migration.md).
func newCatCmd() *cobra.Command {
	var noFrontmatter, excludeComments, asJSON, ndjson, porcelain bool
	cmd := &cobra.Command{
		Use:     "cat <path|id>...",
		Aliases: []string{"show"},
		Short:   "Print one or more tasks",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCat(cmd, args, excludeComments, asJSON, ndjson, porcelain)
		},
	}
	cmd.Flags().BoolVar(&noFrontmatter, "no-frontmatter", false, "Print body only without front matter")
	cmd.Flags().BoolVar(&excludeComments, "exclude-comments", false, "Exclude comments from output")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	return cmd
}

func runCat(cmd *cobra.Command, args []string, excludeComments, asJSON, ndjson, porcelain bool) error {
	// Legacy cat defaults to JSON when non-TTY. Only the JSON path is parity-proven.
	jsonMode := asJSON || (!isStdoutTTY(cmd.OutOrStdout()) && !ndjson && !porcelain)
	if !jsonMode {
		return fmt.Errorf("cat: only --json / non-TTY JSON output is implemented in wrkq-rpccli so far (ndjson/porcelain/raw pending)")
	}

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

	includeComments := !excludeComments
	objs := make([]json.RawMessage, 0, len(args))
	for _, ref := range args {
		// Legacy: applyProjectRootToSelector(arg, false) before resolving the task.
		sref := sc.selector(ref, false)
		raw, err := tr.Call(cmd.Context(), "wrkq.task.catView",
			map[string]any{"task": sref, "includeComments": includeComments})
		if err != nil {
			if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
				return fmt.Errorf("task not found: %s", sref)
			}
			return err
		}
		objs = append(objs, raw)
	}

	// Legacy renders the per-task objects as one indented JSON array.
	out, err := json.MarshalIndent(objs, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	_, err = cmd.OutOrStdout().Write(out)
	return err
}
