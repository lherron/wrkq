package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

// lsEntry mirrors the legacy internal/cli lsEntry shape EXACTLY (field order +
// json tags AND Go field names). The mirror unmarshals the server's compat
// projection into this type so table/human/yaml/tsv rendering goes through the
// SAME internal/render code path as legacy — yaml.v3 keys off the (untagged) Go
// field names, so the struct identity is load-bearing for byte parity.
type lsEntry struct {
	Type                 string  `json:"type"`
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	Title                string  `json:"title,omitempty"`
	Path                 string  `json:"path"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	State                string  `json:"state,omitempty"`
	Kind                 string  `json:"kind,omitempty"`
	TaskCount            *int    `json:"task_count,omitempty"`
	ActiveTaskCount      *int    `json:"active_task_count,omitempty"`
	RequestedByProjectID *string `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`
	Resolution           *string `json:"resolution,omitempty"`
	CPProjectID          *string `json:"cp_project_id,omitempty"`
	CPWorkItemID         *string `json:"cp_work_item_id,omitempty"`
	CPRunID              *string `json:"cp_run_id,omitempty"`
	SessionID            *string `json:"session_id,omitempty"`
	RunStatus            *string `json:"run_status,omitempty"`
}

// newLsCmd mirrors `wrkq ls` via wrkq.task.lsView (the server-owned compatibility
// list projection that reproduces legacy mixed task/container listing, rollup
// counts, in-memory merge-sort, and cursor pagination over the merged set — now
// including the multi-path merge).
//
// Implemented surface (byte-proven against legacy): single + multi path (or
// top-level), --json/--ndjson/--porcelain, table/human/yaml/tsv + the TTY default
// table, --one (-1) / --nul (-0), --type p|t, --all, --limit, --cursor, all
// accepted --sort values, --reverse, and --recursive (-R) which is a no-op in
// legacy (the rollups already recurse) so it is accepted-and-ignored to byte-match.
// Pinned divergences kept: --output raw is unsupported (legacy rejects it too);
// an unknown --type yields an empty set → `null`.
func newLsCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, recursive, one, nul, all, reverse bool
	var limit int
	var cursorTok, typeFilter, sort string
	cmd := &cobra.Command{
		Use:     "ls [path...]",
		Aliases: []string{"list"},
		Short:   "List containers and tasks",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			_ = recursive // legacy --recursive/-R is a no-op (rollups already recurse)

			mode, stable, err := resolveLsMode(cmd, asJSON, ndjson, porcelain)
			if err != nil {
				return err
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: applyProjectRootToPaths(args, defaultToRoot=true). Empty args
			// resolve to the project root (or [""] with no root); each path is
			// root-prefixed. The server owns the per-path query + combined merge.
			scoped := sc.paths(args, true)

			params := map[string]any{}
			if len(scoped) == 1 {
				params["path"] = scoped[0]
			} else if len(scoped) > 1 {
				params["paths"] = scoped
			} else {
				params["path"] = ""
			}
			if sort != "" {
				params["sort"] = sort
			}
			if reverse {
				params["reverse"] = true
			}
			if limit > 0 {
				params["limit"] = limit
			}
			if cursorTok != "" {
				params["cursor"] = cursorTok
			}
			if typeFilter != "" {
				params["type"] = typeFilter
			}
			if all {
				params["includeHidden"] = true
			}

			raw, err := tr.Call(cmd.Context(), "wrkq.task.lsView", params)
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

			// --one / --nul override rendering entirely: emit entry.Path per legacy.
			if one || nul {
				entries, derr := decodeLsEntries(res.Items)
				if derr != nil {
					return derr
				}
				var paths []string
				for _, e := range entries {
					paths = append(paths, e.Path)
				}
				delimiter := "\n"
				if nul {
					delimiter = "\x00"
				}
				fmt.Fprint(out, strings.Join(paths, delimiter))
				if len(paths) > 0 && !nul {
					fmt.Fprintln(out)
				}
				return nil
			}

			switch mode {
			case "json":
				// Legacy: json.Encoder with SetIndent unless Stable (then compact).
				// Empty entries is a nil slice → `null` (NOT `[]`); MarshalIndent and
				// Marshal of a nil []json.RawMessage both render `null`, matching.
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
					if _, err := out.Write(append([]byte(it), '\n')); err != nil {
						return err
					}
				}
				return nil
			}

			// Typed rendering paths (yaml / tsv / table / human) decode the compat
			// projection back into the legacy struct so render output is byte-identical.
			entries, derr := decodeLsEntries(res.Items)
			if derr != nil {
				return derr
			}

			switch mode {
			case "yaml":
				return render.NewRenderer(out, render.Options{Format: render.FormatYAML}).RenderYAML(entries)
			case "tsv":
				headers, rowsData := lsTableData(entries)
				return render.NewRenderer(out, render.Options{Format: render.FormatTSV}).RenderTSV(headers, rowsData)
			default: // table / human
				headers, rowsData := lsTableData(entries)
				return render.NewRenderer(out, render.Options{Format: render.FormatTable, Porcelain: stable}).RenderTable(headers, rowsData)
			}
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVarP(&recursive, "recursive", "R", false, "List recursively")
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter by type (p=project, t=task)")
	cmd.Flags().BoolVarP(&one, "one", "1", false, "One entry per line")
	cmd.Flags().BoolVarP(&nul, "nul", "0", false, "NUL-separated output")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results to return (0 = no limit)")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor from previous page")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include archived and deleted items")
	cmd.Flags().StringVar(&sort, "sort", "slug", "Sort by field: slug, updated_at, created_at, id")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "Reverse sort order")
	return cmd
}

// decodeLsEntries unmarshals the server projection items into the legacy struct.
// A nil/empty item slice yields a nil entries slice (so yaml of nothing matches).
func decodeLsEntries(items []json.RawMessage) ([]lsEntry, error) {
	var entries []lsEntry
	for _, it := range items {
		var e lsEntry
		if err := json.Unmarshal(it, &e); err != nil {
			return nil, err
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// lsTableData reproduces legacy runLs's table/tsv row construction, including the
// container slug "/" suffix, the rollup-count "Tasks" column, and the title rewrite
// ("[kind] <rollup>" for containers, blank when the title equals the slug).
func lsTableData(entries []lsEntry) ([]string, [][]string) {
	headers := []string{"Type", "ID", "Slug", "Title", "State", "Kind", "Tasks", "CreatedAt", "UpdatedAt"}
	var rowsData [][]string
	for _, entry := range entries {
		typeStr := "container"
		slug := entry.Slug
		title := entry.Title
		tasks := ""
		if entry.Type == "task" {
			typeStr = "task"
		} else {
			slug += "/"
			tasks = formatContainerRollup(entry)
			if entry.Kind != "" && tasks != "" {
				title = fmt.Sprintf("[%s] %s", entry.Kind, tasks)
			} else if title == entry.Slug {
				title = ""
			}
		}
		rowsData = append(rowsData, []string{
			typeStr, entry.ID, slug, title, entry.State, entry.Kind, tasks, entry.CreatedAt, entry.UpdatedAt,
		})
	}
	return headers, rowsData
}

func formatContainerRollup(entry lsEntry) string {
	if entry.TaskCount == nil || entry.ActiveTaskCount == nil {
		return ""
	}
	taskLabel := "tasks"
	if *entry.TaskCount == 1 {
		taskLabel = "task"
	}
	return fmt.Sprintf("%d %s (%d active)", *entry.TaskCount, taskLabel, *entry.ActiveTaskCount)
}

// resolveLsMode reproduces legacy resolveOutputMode's decision for ls's allowed
// surface (Allow = table/human/json/ndjson/yaml/tsv, DefaultTTY = table). It
// returns the rendering mode and whether it is the Stable (porcelain) variant.
// raw is the one mode legacy rejects for ls → byte-matched error (a pinned
// divergence, not a gate).
func resolveLsMode(cmd *cobra.Command, asJSON, ndjson, porcelain bool) (mode string, stable bool, err error) {
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
		case "table", "human":
			// Legacy renders BOTH through the table path (the runLs switch has no
			// human case → falls through to the table renderer).
			return "table", false, nil
		case "yaml":
			return "yaml", false, nil
		case "tsv":
			return "tsv", false, nil
		case "raw":
			// Legacy excludes raw from ls's allowed output set → this exact error.
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "table", false, nil
	}
	return "ndjson", false, nil
}
