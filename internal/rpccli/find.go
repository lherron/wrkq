package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

// newFindCmd mirrors `wrkq find [PATH...]` via wrkq.task.findListView (the
// server-owned compatibility list projection that reproduces legacy recursive/
// filtered task+container search, cursor.Apply + limit+1 + sort-validation +
// BuildNextCursor over the filtered set, and the mixed-type in-memory merge-sort).
//
// Implemented surface (byte-proven against legacy): all metadata filters, path
// prefix/glob matching, --type p|t, --limit, --cursor, all accepted --sort
// values, --reverse, --json/--ndjson/--porcelain/non-TTY rendering, --print0,
// and table/human/yaml/tsv rendering (the typed render paths decode the byte-
// proven findListView projection back into the legacy findResult struct so
// internal/render output is byte-identical, mirroring the ls ungate). The only
// remaining divergence is the deliberate one legacy itself enforces: --output raw
// is unsupported (byte-identical error).
func newFindCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, pretty, reverse, ackPending, print0 bool
	var limit int
	var cursorTok, typeFilter, sort string
	var slugGlob, state, dueBefore, dueAfter, kind, assignee, claimedBy, claimedNode, parentTask, requestedBy, assignedProject, causedBy, campaign string
	cmd := &cobra.Command{
		Use:   "find [PATH...]",
		Short: "Search for tasks and containers",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, stable, err := resolveFindMode(cmd, asJSON, ndjson, porcelain, pretty)
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
			var scoped []string
			if campaign == "" || len(args) > 0 {
				scoped = sc.paths(args, true)
			}

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
			if claimedBy != "" {
				params["claimedBy"] = claimedBy
			}
			if claimedNode != "" {
				params["claimedNode"] = claimedNode
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
			if causedBy != "" {
				params["causedBy"] = causedBy
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
			if campaign != "" {
				params["campaign"] = campaign
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

			// --print0 takes precedence over the output mode (legacy ordering):
			// NUL-joined Path values with a trailing NUL, nothing when empty.
			if print0 {
				results, derr := decodeFindResults(res.Items)
				if derr != nil {
					return derr
				}
				var paths []string
				for _, r := range results {
					paths = append(paths, r.Path)
				}
				if len(paths) > 0 {
					if _, werr := fmt.Fprint(out, strings.Join(paths, "\x00")); werr != nil {
						return werr
					}
					if _, werr := fmt.Fprint(out, "\x00"); werr != nil {
						return werr
					}
				}
				return nil
			}

			switch mode {
			case "json":
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
			case "ndjson":
				// One compact object per line; empty → no output.
				for _, it := range res.Items {
					if _, werr := out.Write(append([]byte(it), '\n')); werr != nil {
						return werr
					}
				}
				return nil
			}

			// Typed rendering paths (yaml / tsv / table / human) decode the compat
			// projection back into the legacy findResult struct so internal/render
			// output is byte-identical (yaml.v3 keys off the Go field names).
			results, derr := decodeFindResults(res.Items)
			if derr != nil {
				return derr
			}
			switch mode {
			case "yaml":
				return render.NewRenderer(out, render.Options{Format: render.FormatYAML}).RenderYAML(results)
			case "tsv":
				headers, rowsData := findTableData(results)
				return render.NewRenderer(out, render.Options{Format: render.FormatTSV}).RenderTSV(headers, rowsData)
			default: // table / human
				headers, rowsData := findTableData(results)
				return render.NewRenderer(out, render.Options{Format: render.FormatTable, Porcelain: stable}).RenderTable(headers, rowsData)
			}
		},
	}
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter by type: t (task), p (project/container)")
	cmd.Flags().StringVar(&slugGlob, "slug-glob", "", "Filter by slug glob pattern (e.g. 'login-*')")
	cmd.Flags().StringVar(&state, "state", "", "Filter by state (or 'all' for everything)")
	cmd.Flags().StringVar(&dueBefore, "due-before", "", "Filter tasks due before date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&dueAfter, "due-after", "", "Filter tasks due after date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by task kind: task, subtask, spike, bug, chore")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Filter by assignee principal ref or bare agent slug")
	cmd.Flags().StringVar(&claimedBy, "claimed-by", "", "Filter by claim holder principal ref")
	cmd.Flags().StringVar(&claimedNode, "claimed-node", "", "Filter by server-derived claim node")
	cmd.Flags().StringVar(&parentTask, "parent-task", "", "Filter subtasks of a specific parent task (ID or path)")
	cmd.Flags().StringVar(&requestedBy, "requested-by", "", "Filter by requester project ID")
	cmd.Flags().StringVar(&assignedProject, "assigned-project", "", "Filter by assignee project ID")
	cmd.Flags().StringVar(&causedBy, "caused-by", "", "Filter tasks whose caused_by lineage includes this task ID (e.g. T-00012)")
	cmd.Flags().BoolVar(&ackPending, "ack-pending", false, "Filter for ack-pending tasks (completed/cancelled, not yet acknowledged)")
	cmd.Flags().StringVar(&campaign, "campaign", "", "Find resident and enrolled members of a campaign")
	cmd.Flags().IntVar(&limit, "limit", 0, "Limit number of results")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor")
	cmd.Flags().StringVar(&sort, "sort", "", "Sort by field: updated_at, created_at, id, path")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "Reverse sort order")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Stable machine-readable output")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force human-readable table output even when not a TTY")
	cmd.Flags().BoolVarP(&print0, "print0", "0", false, "NUL-separated output")
	return cmd
}

// resolveFindMode reproduces legacy resolveOutputMode's decision for find's
// allowed surface (Allow: table,human,json,ndjson,yaml,tsv; DefaultTTY: table;
// raw NOT allowed → byte-identical parity error). human maps to table (legacy
// has no human render branch for find → table fall-through).
func resolveFindMode(cmd *cobra.Command, asJSON, ndjson, porcelain, pretty bool) (mode string, stable bool, err error) {
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
	if pretty {
		return "table", false, nil
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
		case "table", "yaml", "tsv":
			return m, false, nil
		case "human":
			// Legacy has no outputModeHuman render branch for find → it falls
			// through to the table renderer. Map human → table to match.
			return "table", false, nil
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	// Bare default: TTY → table, non-TTY list shape → ndjson (legacy convention).
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "table", false, nil
	}
	return "ndjson", false, nil
}

// findResult mirrors the legacy internal/cli findResult shape EXACTLY (field
// order + json tags) so yaml.v3 (which keys off the untagged Go field names) and
// the table/tsv row construction render byte-identically to legacy. The mirror
// decodes the byte-proven findListView projection into this type.
type findResult struct {
	Type                 string   `json:"type"` // "task" or "container"
	UUID                 string   `json:"uuid"`
	ID                   string   `json:"id"`
	Slug                 string   `json:"slug"`
	Title                string   `json:"title"`
	Path                 string   `json:"path"`
	Specification        string   `json:"specification,omitempty"`
	State                *string  `json:"state,omitempty"`
	Priority             *int     `json:"priority,omitempty"`
	Kind                 *string  `json:"kind,omitempty"`
	Assignee             *string  `json:"assignee,omitempty"`
	AssigneePrincipalRef *string  `json:"assignee_principal_ref,omitempty"`
	ClaimedBy            *string  `json:"claimed_by,omitempty"`
	ClaimedScope         *string  `json:"claimed_scope,omitempty"`
	ClaimedNode          *string  `json:"claimed_node,omitempty"`
	ClaimedAt            *string  `json:"claimed_at,omitempty"`
	ClaimGeneration      int64    `json:"claim_generation,omitempty"`
	ParentTaskID         *string  `json:"parent_task_id,omitempty"`
	RequestedByProjectID *string  `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string  `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string  `json:"acknowledged_at,omitempty"`
	Resolution           *string  `json:"resolution,omitempty"`
	DueAt                *string  `json:"due_at,omitempty"`
	CausedBy             []string `json:"caused_by,omitempty"`
	CreatedAt            string   `json:"created_at"`
	UpdatedAt            string   `json:"updated_at"`
	ETag                 int64    `json:"etag"`
	Membership           string   `json:"membership,omitempty"`
}

// decodeFindResults unmarshals the server projection items into the legacy
// struct. A nil/empty item slice yields a nil slice (so yaml of nothing matches).
func decodeFindResults(items []json.RawMessage) ([]findResult, error) {
	var results []findResult
	for _, it := range items {
		var r findResult
		if err := json.Unmarshal(it, &r); err != nil {
			return nil, err
		}
		results = append(results, r)
	}
	return results, nil
}

// findTableData reproduces legacy runFind's table/tsv row construction: the
// fixed header set and one row per result with a blank state for containers.
func findTableData(results []findResult) ([]string, [][]string) {
	headers := []string{"Type", "ID", "Path", "Title", "State", "UpdatedAt"}
	rowsData := make([][]string, 0, len(results))
	for _, result := range results {
		state := ""
		if result.State != nil {
			state = *result.State
		}
		rowsData = append(rowsData, []string{result.Type, result.ID, result.Path, result.Title, state, result.UpdatedAt})
	}
	return headers, rowsData
}
