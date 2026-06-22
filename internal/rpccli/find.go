package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newFindCmd mirrors `wrkq find [PATH...]` via wrkq.task.findListView (the
// server-owned compatibility list projection that reproduces legacy recursive/
// filtered task+container search, cursor.Apply + limit+1 + sort-validation +
// BuildNextCursor over the filtered set, and the mixed-type in-memory merge-sort).
//
// Implemented surface (byte-proven against legacy): all metadata filters, path
// prefix/glob matching, --type p|t, --limit, --cursor, all accepted --sort
// values, --reverse, --json/--ndjson/--porcelain/non-TTY rendering. Everything
// the legacy command can additionally do is HARD-GATED with a clear error so
// there is never a silent degradation: --print0 and table/human/yaml/tsv
// rendering.
func newFindCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, reverse, ackPending, print0 bool
	var limit int
	var cursorTok, typeFilter, sort string
	var slugGlob, state, dueBefore, dueAfter, kind, assignee, parentTask, requestedBy, assignedProject string
	cmd := &cobra.Command{
		Use:   "find [PATH...]",
		Short: "Search for tasks and containers",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if print0 {
				return fmt.Errorf("find: --print0 not yet implemented in wrkq-rpccli (hard-gated pending parity)")
			}
			mode, stable, err := resolveFindMode(cmd, asJSON, ndjson, porcelain)
			if err != nil {
				return err
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: applyProjectRootToPaths(args, defaultToRoot=true). The scoper
			// reproduces caller semantics; the server never reads project-root.
			scoped := sc.paths(args, true)

			params := map[string]any{}
			if len(scoped) > 0 {
				params["paths"] = scoped
			}
			if typeFilter != "" {
				params["type"] = typeFilter
			}
			if slugGlob != "" {
				params["slugGlob"] = slugGlob
			}
			if state != "" {
				params["state"] = state
			}
			if dueBefore != "" {
				params["dueBefore"] = dueBefore
			}
			if dueAfter != "" {
				params["dueAfter"] = dueAfter
			}
			if kind != "" {
				params["kind"] = kind
			}
			if assignee != "" {
				params["assignee"] = assignee
			}
			if parentTask != "" {
				// Legacy applies project-root scoping to the parent-task selector.
				params["parentTask"] = sc.selector(parentTask, false)
			}
			if requestedBy != "" {
				params["requestedBy"] = requestedBy
			}
			if assignedProject != "" {
				params["assignedProject"] = assignedProject
			}
			if ackPending {
				params["ackPending"] = true
			}
			if limit > 0 {
				params["limit"] = limit
			}
			if cursorTok != "" {
				params["cursor"] = cursorTok
			}
			if sort != "" {
				params["sort"] = sort
			}
			if reverse {
				params["reverse"] = true
			}

			raw, err := tr.Call(cmd.Context(), "wrkq.task.findListView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					// Legacy surfaces store/cursor/validation/not-found errors raw,
					// without a domain-code prefix.
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

			// Legacy prints next_cursor to stderr (Stable/porcelain only) BEFORE
			// rendering rows to stdout; match that ordering.
			if stable && res.NextCursor != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", res.NextCursor)
			}

			out := cmd.OutOrStdout()
			if mode == "json" {
				// Legacy: json.Encoder with SetIndent unless Stable (then compact).
				// find initializes results as []findResult{} → empty renders as `[]`
				// (NOT null). The server mirrors this with an initialized slice.
				var data []byte
				if stable {
					data, err = json.Marshal(res.Items)
				} else {
					data, err = json.MarshalIndent(res.Items, "", "  ")
				}
				if err != nil {
					return err
				}
				_, err = out.Write(append(data, '\n'))
				return err
			}
			// NDJSON: one compact object per line; empty → no output.
			for _, it := range res.Items {
				if _, err := out.Write(append([]byte(it), '\n')); err != nil {
					return err
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter by type: t (task), p (project/container)")
	cmd.Flags().StringVar(&slugGlob, "slug-glob", "", "Filter by slug glob pattern (e.g. 'login-*')")
	cmd.Flags().StringVar(&state, "state", "", "Filter by state (or 'all' for everything)")
	cmd.Flags().StringVar(&dueBefore, "due-before", "", "Filter tasks due before date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&dueAfter, "due-after", "", "Filter tasks due after date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by task kind: task, subtask, spike, bug, chore")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Filter by assignee principal ref or bare agent slug")
	cmd.Flags().StringVar(&parentTask, "parent-task", "", "Filter subtasks of a specific parent task (ID or path)")
	cmd.Flags().StringVar(&requestedBy, "requested-by", "", "Filter by requester project ID")
	cmd.Flags().StringVar(&assignedProject, "assigned-project", "", "Filter by assignee project ID")
	cmd.Flags().BoolVar(&ackPending, "ack-pending", false, "Filter for ack-pending tasks (completed/cancelled, not yet acknowledged)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit number of results")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor")
	cmd.Flags().StringVar(&sort, "sort", "", "Sort by field: updated_at, created_at, id, path")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "Reverse sort order")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Stable machine-readable output")
	cmd.Flags().BoolVarP(&print0, "print0", "0", false, "NUL-separated output (hard-gated: not yet implemented)")
	return cmd
}

// resolveFindMode reproduces legacy resolveOutputMode's decision for find's
// allowed surface (Allow: table,human,json,ndjson,yaml,tsv; DefaultTTY: table;
// raw NOT allowed → parity error). Modes the mirror does not yet implement
// (table/human/yaml/tsv and the TTY default table) are hard-gated as errors so
// parity is never silently degraded.
func resolveFindMode(cmd *cobra.Command, asJSON, ndjson, porcelain bool) (mode string, stable bool, err error) {
	count := 0
	var explicit string
	if asJSON {
		explicit = "json"
		count++
	}
	if ndjson {
		explicit = "ndjson"
		count++
	}
	if count > 1 {
		return "", false, fmt.Errorf("choose only one output mode")
	}
	stable = porcelain
	if explicit != "" {
		return explicit, stable, nil
	}
	if stable { // bare --porcelain → canonical machine mode for a list = ndjson
		return "ndjson", true, nil
	}
	if outF := cmd.Flag("output"); outF != nil && outF.Changed {
		m := strings.ToLower(strings.TrimSpace(outF.Value.String()))
		switch m {
		case "json":
			return "json", false, nil
		case "ndjson":
			return "ndjson", false, nil
		case "porcelain":
			return "ndjson", true, nil
		case "raw":
			// Legacy excludes raw from find's allowed output set, so it errors with
			// this exact message. Emit it verbatim → byte-parity (not a gate).
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		case "table", "human", "yaml", "tsv":
			// Legacy RENDERS these; the mirror cannot yet, so hard-gate with a
			// mirror-specific error (no silent degradation). Not a parity case.
			return "", false, fmt.Errorf("find: %s output not yet implemented in wrkq-rpccli (hard-gated pending parity)", m)
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "", false, fmt.Errorf("find: table output not yet implemented in wrkq-rpccli (use --json / --ndjson / --porcelain)")
	}
	return "ndjson", false, nil
}
