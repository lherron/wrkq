package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

// newCommentCmd mirrors `wrkq comment`. Only `add` is implemented so far (via
// wrkq.comment.add); ls/cat/rm are pending (ls needs the list-view projection
// ruling, see docs/rpc-cli-migration.md).
func newCommentCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "comment", Short: "Manage task comments"}
	cmd.AddCommand(newCommentAddCmd())
	cmd.AddCommand(newCommentCatCmd())
	cmd.AddCommand(newCommentLsCmd())
	cmd.AddCommand(newStubCmd(mirroredCommand{use: "rm <comment-id|c:token>..."}))
	return cmd
}

func newCommentLsCmd() *cobra.Command {
	var asJSON, ndjson, yaml, tsv, porcelain, includeDeleted, reverse bool
	var limit int
	var cursorTok, sort string
	cmd := &cobra.Command{
		Use:   "ls <task>...",
		Short: "List comments for a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) != 1 {
				return fmt.Errorf("comment ls: only single-task listing is implemented in wrkq-rpccli so far")
			}
			if reverse {
				return fmt.Errorf("comment ls: --reverse not yet implemented in wrkq-rpccli (hard-gated pending parity)")
			}
			if yaml || tsv {
				return fmt.Errorf("comment ls: yaml/tsv output not yet implemented in wrkq-rpccli")
			}
			// Mode: --json (indented array), --ndjson/--porcelain/non-TTY (NDJSON);
			// porcelain also routes next_cursor to stderr. TTY table pending.
			if !asJSON && !ndjson && !porcelain && isStdoutTTY(cmd.OutOrStdout()) {
				return fmt.Errorf("comment ls: only --json / --ndjson / --porcelain / non-TTY output is implemented so far")
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			// Legacy: applyProjectRootToSelector(taskRef, false); the t: prefix is
			// stripped after scoping (scoping preserves the prefix).
			params := map[string]any{"task": strings.TrimPrefix(sc.selector(args[0], false), "t:")}
			if limit > 0 {
				params["limit"] = limit
			}
			if cursorTok != "" {
				params["cursor"] = cursorTok
			}
			if includeDeleted {
				params["includeDeleted"] = true
			}
			if sort != "" {
				params["sort"] = sort
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.comment.listView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					if re.DomainID == "WRKQ_NOT_FOUND" {
						return fmt.Errorf("failed to resolve task %s: task not found: %s", args[0], args[0])
					}
					// Legacy surfaces the raw error (e.g. malformed cursor) without
					// a domain-code prefix.
					return errors.New(re.Message)
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
			// Legacy prints next_cursor to stderr (porcelain/Stable only) BEFORE
			// rendering rows to stdout; match that ordering.
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
	cmd.Flags().BoolVar(&yaml, "yaml", false, "Output as YAML")
	cmd.Flags().BoolVar(&tsv, "tsv", false, "Output as TSV")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&includeDeleted, "include-deleted", false, "Include soft-deleted comments")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results (0 = no limit)")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor")
	cmd.Flags().StringVar(&sort, "sort", "", "Sort field")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "Reverse sort order (hard-gated: not yet implemented)")
	return cmd
}

func newCommentCatCmd() *cobra.Command {
	var asJSON, ndjson, raw bool
	cmd := &cobra.Command{
		Use:   "cat <comment-id|c:token>...",
		Short: "Print comment details",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// Legacy comment cat defaults to JSON when non-TTY; only the JSON path is
			// parity-proven.
			jsonMode := asJSON || (!isStdoutTTY(cmd.OutOrStdout()) && !raw && !ndjson)
			if !jsonMode {
				return fmt.Errorf("comment cat: only --json / non-TTY JSON output is implemented in wrkq-rpccli so far")
			}
			// comment cat takes comment IDs / c: tokens, not paths — no project-root
			// scoping applies (the scoper is intentionally unused here).
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			objs := make([]json.RawMessage, 0, len(args))
			for _, ref := range args {
				ref = strings.TrimPrefix(ref, "c:")
				out, err := tr.Call(cmd.Context(), "wrkq.comment.catView", map[string]string{"comment": ref})
				if err != nil {
					if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
						return fmt.Errorf("comment not found: %s", ref)
					}
					return err
				}
				objs = append(objs, out)
			}
			data, err := json.MarshalIndent(objs, "", "  ")
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&raw, "raw", false, "Print raw comment bodies")
	return cmd
}

func newCommentAddCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "add <task> [comment-text]",
		Short: "Add a comment to a task",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCommentAdd(cmd, args)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func runCommentAdd(cmd *cobra.Command, args []string) error {
	task := args[0]
	var body string
	if len(args) > 1 {
		body = args[1]
	} else {
		b, err := io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return err
		}
		body = string(b)
	}

	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	// Legacy: applyProjectRootToSelector(taskRef, false).
	task = sc.selector(task, false)
	params := map[string]any{"task": task, "body": body}
	if actor != "" {
		params["actor"] = actor
	}
	raw, err := tr.Call(cmd.Context(), "wrkq.comment.add", params)
	if err != nil {
		if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
			return fmt.Errorf("task not found: %s", task)
		}
		return err
	}
	var dto struct {
		ID                    string `json:"id"`
		UUID                  string `json:"uuid"`
		Task                  string `json:"task"`
		ETag                  int64  `json:"etag"`
		CreatedAt             string `json:"createdAt"`
		CreatedByPrincipalRef string `json:"createdByPrincipalRef"`
	}
	if err := json.Unmarshal(raw, &dto); err != nil {
		return err
	}

	output := map[string]interface{}{
		"id":            dto.ID,
		"uuid":          dto.UUID,
		"task_id":       dto.Task,
		"principal_ref": dto.CreatedByPrincipalRef,
		"created_at":    dto.CreatedAt,
		"etag":          dto.ETag,
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return encodeJSONIndent(cmd, output)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Comment created: %s\n", dto.ID)
	return nil
}
