package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// newRelationCmd mirrors `wrkq relation`. add/rm are RPC-backed via
// wrkq.relation.add/.remove, composing wrkq.task.show to resolve each endpoint's
// id+uuid for the legacy output. ls is pending the list-view ruling.
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
			// JSON (indented array) and NDJSON/non-TTY default are proven; the
			// TTY table is pending.
			if !asJSON && !ndjson && !porcelain && isStdoutTTY(cmd.OutOrStdout()) {
				return fmt.Errorf("relation ls: only --json / --ndjson / non-TTY output is implemented in wrkq-rpccli so far")
			}
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
			var items []json.RawMessage
			if err := json.Unmarshal(raw, &items); err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if asJSON {
				data, err := json.MarshalIndent(items, "", "  ")
				if err != nil {
					return err
				}
				_, err = out.Write(append(data, '\n'))
				return err
			}
			// NDJSON: one compact relation per line.
			for _, it := range items {
				if _, err := out.Write(append([]byte(it), '\n')); err != nil {
					return err
				}
			}
			return nil
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
