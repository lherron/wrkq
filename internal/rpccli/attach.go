package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newAttachCmd mirrors `wrkq attach`. `ls` is RPC-backed via the server-owned
// wrkq.attachment.listView compat list projection (cursor-paginated, DB-only).
// put/get/rm (filesystem) are pending.
func newAttachCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "attach", Short: "Manage task attachments"}
	cmd.AddCommand(newAttachLsCmd())
	for _, sub := range []mirroredCommand{
		{use: "put <task> <file|-|>"},
		{use: "get <attachment-id>"},
		{use: "rm <attachment-id>..."},
	} {
		cmd.AddCommand(newStubCmd(sub))
	}
	return cmd
}

func newAttachLsCmd() *cobra.Command {
	var asJSON, ndjson, porcelain bool
	var limit int
	var cursorTok string
	cmd := &cobra.Command{
		Use:   "ls <task>",
		Short: "List attachments for a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !asJSON && !ndjson && !porcelain && isStdoutTTY(cmd.OutOrStdout()) {
				return fmt.Errorf("attach ls: only --json / --ndjson / --porcelain / non-TTY output is implemented so far")
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			// Legacy: applyProjectRootToSelector(taskRef, false).
			params := map[string]any{"task": sc.selector(args[0], false)}
			if limit > 0 {
				params["limit"] = limit
			}
			if cursorTok != "" {
				params["cursor"] = cursorTok
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.attachment.listView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					return fmt.Errorf("%s", re.Message)
				}
				return err
			}
			var res struct {
				Items      []json.RawMessage `json:"items"`
				NextCursor string            `json:"next_cursor"`
			}
			if err := json.Unmarshal(raw, &res); err != nil {
				return err
			}
			// Legacy prints next_cursor to stderr (porcelain only) BEFORE rows.
			if porcelain && res.NextCursor != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", res.NextCursor)
			}
			out := cmd.OutOrStdout()
			if asJSON {
				data, err := json.MarshalIndent(res.Items, "", "  ")
				if err != nil {
					return err
				}
				if _, err := out.Write(append(data, '\n')); err != nil {
					return err
				}
			} else {
				for _, it := range res.Items {
					if _, err := out.Write(append([]byte(it), '\n')); err != nil {
						return err
					}
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as NDJSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor")
	return cmd
}
