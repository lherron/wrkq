package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newContainerCmd mirrors `wrkq container`. `cat` is RPC-backed via the
// server-owned wrkq.container.catView compat projection; `set` is pending.
func newContainerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "container", Short: "Manage containers"}
	cmd.AddCommand(newContainerCatCmd())
	cmd.AddCommand(newStubCmd(mirroredCommand{use: "set <container>"}))
	return cmd
}

func newContainerCatCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, noFrontmatter bool
	cmd := &cobra.Command{
		Use:     "cat <path|id>",
		Aliases: []string{"show"},
		Short:   "Print container details",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Legacy emits JSON for --json/--ndjson/--porcelain or non-TTY w/o
			// --no-frontmatter. Only the indented-JSON path is parity-proven.
			jsonMode := asJSON || (!isStdoutTTY(cmd.OutOrStdout()) && !ndjson && !porcelain && !noFrontmatter)
			if !jsonMode {
				return fmt.Errorf("container cat: only --json / non-TTY JSON output is implemented in wrkq-rpccli so far")
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			// Legacy: applyProjectRootToSelector(arg, false).
			sref := sc.selector(args[0], false)
			raw, err := tr.Call(cmd.Context(), "wrkq.container.catView", map[string]string{"container": sref})
			if err != nil {
				if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
					return fmt.Errorf("path not found: %s", sref)
				}
				return err
			}
			var buf []byte
			buf, err = indentRaw(raw)
			if err != nil {
				return err
			}
			_, err = cmd.OutOrStdout().Write(buf)
			return err
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&noFrontmatter, "no-frontmatter", false, "Print body only without front matter")
	return cmd
}

// indentRaw pretty-prints a raw JSON object with 2-space indent + trailing
// newline, matching json.NewEncoder(SetIndent("", "  ")).Encode.
func indentRaw(raw json.RawMessage) ([]byte, error) {
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}
