package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// newLsCmd mirrors `wrkq ls` via wrkq.task.lsView (the server-owned compatibility
// list projection that reproduces legacy mixed task/container listing, rollup
// counts, in-memory merge-sort, and cursor pagination over the merged set).
//
// Implemented surface (byte-proven against legacy): single path (or top-level),
// --json/--ndjson/--porcelain/non-TTY rendering, --type p|t, --all, --limit,
// --cursor, all accepted --sort values, --reverse. Everything the legacy command
// can additionally do is HARD-GATED with a clear error so there is never a silent
// degradation to a narrower behavior: multi-path, --recursive, --one, --nul, and
// table/human/yaml/tsv rendering.
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
			if recursive {
				return fmt.Errorf("ls: --recursive not yet implemented in wrkq-rpccli (hard-gated pending parity)")
			}
			if one || nul {
				return fmt.Errorf("ls: --one/--nul not yet implemented in wrkq-rpccli (hard-gated pending parity)")
			}
			if len(args) > 1 {
				return fmt.Errorf("ls: multi-path listing not yet implemented in wrkq-rpccli (hard-gated pending parity)")
			}
			mode, stable, err := resolveLsMode(cmd, asJSON, ndjson, porcelain)
			if err != nil {
				return err
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: applyProjectRootToPaths(args, defaultToRoot=true) → empty args
			// resolve to the project root (or "" with no root); single path is
			// root-prefixed. Multi-path is gated above.
			scoped := sc.paths(args, true)
			path := ""
			if len(scoped) == 1 {
				path = scoped[0]
			}

			params := map[string]any{"path": path}
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
			if mode == "json" {
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
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVarP(&recursive, "recursive", "R", false, "List recursively (hard-gated: not yet implemented)")
	cmd.Flags().StringVar(&typeFilter, "type", "", "Filter by type (p=project, t=task)")
	cmd.Flags().BoolVarP(&one, "one", "1", false, "One entry per line (hard-gated: not yet implemented)")
	cmd.Flags().BoolVarP(&nul, "nul", "0", false, "NUL-separated output (hard-gated: not yet implemented)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results to return (0 = no limit)")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor from previous page")
	cmd.Flags().BoolVarP(&all, "all", "a", false, "Include archived and deleted items")
	cmd.Flags().StringVar(&sort, "sort", "slug", "Sort by field: slug, updated_at, created_at, id")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "Reverse sort order")
	return cmd
}

// resolveLsMode reproduces legacy resolveOutputMode's decision for ls's allowed
// surface, returning the rendering mode ("json"|"ndjson") and whether it is the
// Stable (porcelain) variant. Modes the mirror does not yet implement
// (table/human/yaml/tsv and the TTY default table) are hard-gated as errors so
// parity is never silently degraded.
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
		case "raw":
			// Legacy excludes raw from ls's allowed output set, so it errors with
			// this exact message. Emit it verbatim → byte-parity (not a gate).
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		case "table", "human", "yaml", "tsv":
			// Legacy RENDERS these; the mirror cannot yet, so hard-gate with a
			// mirror-specific error (no silent degradation). Not a parity case.
			return "", false, fmt.Errorf("ls: %s output not yet implemented in wrkq-rpccli (hard-gated pending parity)", m)
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "", false, fmt.Errorf("ls: table output not yet implemented in wrkq-rpccli (use --json / --ndjson / --porcelain)")
	}
	return "ndjson", false, nil
}
