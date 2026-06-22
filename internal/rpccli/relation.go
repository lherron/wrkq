package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

// newRelationCmd mirrors `wrkq relation`. add/rm are RPC-backed via
// wrkq.relation.add/.remove, composing wrkq.task.show to resolve each endpoint's
// id+uuid for the legacy output. ls is RPC-backed via wrkq.relation.listView and
// renders --json / NDJSON / --porcelain / non-TTY default plus the legacy TTY table.
func newRelationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "relation", Short: "Manage task relations"}
	cmd.AddCommand(newRelationMutateCmd("add", "wrkq.relation.add", "created", "Created"))
	cmd.AddCommand(newRelationMutateCmd("rm", "wrkq.relation.remove", "removed", "Removed"))
	cmd.AddCommand(newRelationLsCmd())
	return cmd
}

func newRelationLsCmd() *cobra.Command {
	var asJSON, ndjson, porcelain bool
	cmd := &cobra.Command{
		Use:   "ls <task>",
		Short: "List relations for a task",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			// Legacy: applyProjectRootToSelector(taskRef, false).
			sref := sc.selector(args[0], false)
			raw, err := tr.Call(cmd.Context(), "wrkq.relation.listView", map[string]string{"task": sref})
			if err != nil {
				if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
					return fmt.Errorf("failed to resolve task: task not found: %s", sref)
				}
				return err
			}
			out := cmd.OutOrStdout()

			// --json: indented array of the raw relation objects (empty → null,
			// matching the server's nil-slice projection).
			if asJSON {
				var items []json.RawMessage
				if err := json.Unmarshal(raw, &items); err != nil {
					return err
				}
				data, err := json.MarshalIndent(items, "", "  ")
				if err != nil {
					return err
				}
				_, err = out.Write(append(data, '\n'))
				return err
			}

			// NDJSON / --porcelain / non-TTY default: one compact relation per line.
			if ndjson || porcelain || !isStdoutTTY(out) {
				var items []json.RawMessage
				if err := json.Unmarshal(raw, &items); err != nil {
					return err
				}
				for _, it := range items {
					if _, err := out.Write(append([]byte(it), '\n')); err != nil {
						return err
					}
				}
				return nil
			}

			// TTY default: legacy table (internal/cli/relation.go). Columns
			// Direction/Kind/Task ID/Slug/Title; empty → "No relations found".
			// porcelain is always false here (porcelain routes to NDJSON above),
			// so the renderer emits the padded human table, never tab-separated.
			var rels []struct {
				Direction string `json:"direction"`
				Kind      string `json:"kind"`
				TaskID    string `json:"task_id"`
				TaskSlug  string `json:"task_slug"`
				TaskTitle string `json:"task_title"`
			}
			if err := json.Unmarshal(raw, &rels); err != nil {
				return err
			}
			if len(rels) == 0 {
				fmt.Fprintln(out, "No relations found")
				return nil
			}
			headers := []string{"Direction", "Kind", "Task ID", "Slug", "Title"}
			rowsData := make([][]string, 0, len(rels))
			for _, rel := range rels {
				rowsData = append(rowsData, []string{
					rel.Direction, rel.Kind, rel.TaskID, rel.TaskSlug, rel.TaskTitle,
				})
			}
			r := render.NewRenderer(out, render.Options{Format: render.FormatTable})
			return r.RenderTable(headers, rowsData)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	return cmd
}

func newRelationMutateCmd(use, method, flagKey, verb string) *cobra.Command {
	return &cobra.Command{
		Use:   use + " <from-task> <kind> <to-task>",
		Short: verb + " a relation between tasks",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runRelationMutate(cmd, args, method, flagKey, verb)
		},
	}
}

func runRelationMutate(cmd *cobra.Command, args []string, method, flagKey, verb string) error {
	from, kind, to := args[0], args[1], args[2]

	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor := actorFlag(cmd)

	// Legacy: applyProjectRootToSelector(false) for both endpoints.
	from = sc.selector(from, false)
	to = sc.selector(to, false)

	fromID, fromUUID, err := relTaskIDUUID(cmd, tr, from)
	if err != nil {
		return err
	}
	toID, toUUID, err := relTaskIDUUID(cmd, tr, to)
	if err != nil {
		return err
	}

	params := map[string]any{"fromTask": from, "kind": kind, "toTask": to}
	if actor != "" {
		params["actor"] = actor
	}
	if _, err := tr.Call(cmd.Context(), method, params); err != nil {
		if re, ok := err.(*Error); ok {
			return fmt.Errorf("%s", re.Message)
		}
		return err
	}

	out := map[string]interface{}{
		"from_task_id":   fromID,
		"from_task_uuid": fromUUID,
		"kind":           kind,
		"to_task_id":     toID,
		"to_task_uuid":   toUUID,
		flagKey:          true,
	}
	if !isStdoutTTY(cmd.OutOrStdout()) {
		return encodeJSONIndent(cmd, out)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s relation: %s %s %s\n", verb, fromID, kind, toID)
	return nil
}

func relTaskIDUUID(cmd *cobra.Command, tr Transport, ref string) (string, string, error) {
	raw, err := tr.Call(cmd.Context(), "wrkq.task.show", map[string]string{"task": ref})
	if err != nil {
		if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
			return "", "", fmt.Errorf("task not found: %s", ref)
		}
		return "", "", err
	}
	var t struct {
		ID   string `json:"id"`
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(raw, &t); err != nil {
		return "", "", err
	}
	return t.ID, t.UUID, nil
}
