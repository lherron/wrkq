package rpccli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

// newSearchCmd mirrors `wrkq search <query> [PATH...]` via the server-owned
// wrkq.search.listView compatibility read model. The SERVER owns the entire
// search pipeline: opening + migrating the derived <db>.search.sqlite sidecar,
// the dense embedder, FTS5/vec/lexical candidate retrieval, RRF fusion, canonical
// filtering, freshness, sort + paging. The CLI owns ONLY project-root path scoping
// (before the call) + presentation. It NEVER opens the sidecar (importguard-proven).
//
// Implemented surface (byte-proven against legacy, non-TTY): the bare non-TTY
// default (NDJSON of results), --json (indented full response), --ndjson,
// --porcelain, --human/--pretty, path/assignee/state/kind/sort/limit filters,
// and --fresh staleness gate. --pretty forces the human renderer in non-TTY mode;
// color remains terminal-gated, so the forced output is deterministic.
func newSearchCmd() *cobra.Command {
	var state, kind, assignee, sort string
	var labels []string
	var limit, candidateLimit int
	var reverse, asJSON, ndjson, porcelain, human, pretty, explain, fresh bool
	cmd := &cobra.Command{
		Use:   "search <query> [PATH...]",
		Short: "Search task and comment text",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, stable, err := resolveSearchMode(cmd, asJSON, ndjson, porcelain, human, pretty)
			if err != nil {
				return err
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			query := args[0]
			// Legacy: applyProjectRootToPaths(args[1:], defaultToRoot=true).
			paths := sc.paths(args[1:], true)

			params := map[string]any{
				"query":          query,
				"sort":           sort,
				"limit":          limit,
				"candidateLimit": candidateLimit,
			}
			if len(paths) > 0 {
				params["paths"] = paths
			}
			if state != "" {
				params["state"] = state
			}
			if kind != "" {
				params["kind"] = kind
			}
			if len(labels) > 0 {
				params["labels"] = labels
			}
			if assignee != "" {
				params["assigneePrincipalRef"] = assignee
			}
			if reverse {
				params["reverse"] = true
			}
			if explain {
				params["explain"] = true
			}
			if fresh {
				params["fresh"] = true
			}

			raw, err := tr.Call(cmd.Context(), "wrkq.search.listView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					// Legacy surfaces search/index errors raw (the server message
					// already carries the legacy text), without a domain-code prefix.
					return errors.New(re.Message)
				}
				return err
			}

			var resp searchResponse
			if err := json.Unmarshal(raw, &resp); err != nil {
				return err
			}

			out := cmd.OutOrStdout()
			switch mode {
			case "json":
				return writeSearchJSON(out, raw, stable)
			case "human":
				if resp.Stale {
					fmt.Fprintln(cmd.ErrOrStderr(), style.Paint(style.ColStateStop, fmt.Sprintf("search index is stale by %d event(s)", respStatusStaleEventCount(resp.Status))))
				}
				renderSearchHuman(out, query, resp.Results)
				return nil
			default: // ndjson
				return writeSearchNDJSON(out, resp.Results)
			}
		},
	}
	cmd.Flags().StringVar(&state, "state", "", "Filter by state (default open, use all for non-deleted states)")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by task kind")
	cmd.Flags().StringArrayVar(&labels, "label", nil, "Filter by exact task label (repeatable; all must match)")
	cmd.Flags().StringVar(&assignee, "assignee", "", "Filter by assignee principal ref or bare agent slug")
	cmd.Flags().IntVar(&limit, "limit", 20, "Maximum number of task results")
	cmd.Flags().IntVar(&candidateLimit, "candidate-limit", 300, "Candidate chunks to retrieve before aggregation")
	cmd.Flags().StringVar(&sort, "sort", "relevance", "Sort by relevance, updated_at, or created_at")
	cmd.Flags().BoolVar(&reverse, "reverse", false, "Reverse sort order")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Stable machine-readable output")
	cmd.Flags().BoolVar(&human, "human", false, "Force human-readable output")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force human-readable output even when not a TTY")
	cmd.Flags().BoolVar(&explain, "explain", false, "Include ranking diagnostics in JSON output")
	cmd.Flags().BoolVar(&fresh, "fresh", false, "Fail if the search index is stale")
	return cmd
}

// resolveSearchMode reproduces legacy search's mode decision. search is
// outputShapeList over allowed {human, json, ndjson} with DefaultTTY=human, so the
// non-TTY default resolves to NDJSON. --json/--ndjson select directly; --porcelain
// is the canonical machine mode for a list (NDJSON, stable). --human and --pretty
// force the same human renderer legacy uses on a TTY.
func resolveSearchMode(cmd *cobra.Command, asJSON, ndjson, porcelain, human, pretty bool) (mode string, stable bool, err error) {
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
	if human {
		explicit = "human"
		count++
	}
	if count > 1 {
		return "", false, fmt.Errorf("choose only one output mode")
	}
	if pretty {
		return "human", porcelain, nil
	}
	if explicit != "" {
		if explicit == "human" {
			return "human", porcelain, nil
		}
		return explicit, false, nil
	}
	// Bare --porcelain (no --json/--ndjson/--output) → canonical machine mode for a
	// list = NDJSON, stable.
	outputChanged := false
	if outF := cmd.Flag("output"); outF != nil {
		outputChanged = outF.Changed
	}
	if porcelain && !outputChanged {
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
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		case "human":
			return "human", false, nil
		case "table", "yaml", "tsv":
			return "", false, fmt.Errorf("output mode %q is not supported for this command", m)
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", outF.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "human", false, nil
	}
	return "ndjson", false, nil
}

// ── wire DTO (mirrors internal/wrkqapi.WrkqSearchListView / WrkqSearchResult) ──

type searchResult struct {
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	ResourceUUID string         `json:"resource_uuid"`
	TaskID       string         `json:"task_id,omitempty"`
	TaskUUID     string         `json:"task_uuid,omitempty"`
	CommentID    *string        `json:"comment_id,omitempty"`
	ScopeRef     string         `json:"scope_ref,omitempty"`
	Status       string         `json:"status,omitempty"`
	Path         string         `json:"path"`
	Title        string         `json:"title"`
	State        string         `json:"state,omitempty"`
	Kind         string         `json:"kind,omitempty"`
	Snippet      string         `json:"snippet"`
	Score        float64        `json:"score"`
	CreatedAt    string         `json:"created_at"`
	UpdatedAt    string         `json:"updated_at"`
	Stale        bool           `json:"stale"`
	Explain      map[string]any `json:"explain,omitempty"`
}

type searchResponse struct {
	Query        string          `json:"query"`
	Stale        bool            `json:"stale"`
	Status       json.RawMessage `json:"status"`
	Results      []searchResult  `json:"results"`
	TotalMatches int             `json:"total_matches"`
	Offset       int             `json:"offset"`
}

// writeSearchJSON renders the full response. Legacy --json uses writeJSONOutput
// (json.Encoder, HTML-escaping, trailing newline; indented unless stable). The
// server already produced the response in legacy search.Response field order (with
// the nested status in indexdb.Status struct order), so re-emitting the SERVER's
// raw bytes — indented via json.Indent (non-stable) or compacted via json.Compact
// (stable/--porcelain) — preserves byte-parity. Decoding to `any` would
// re-alphabetize the keys and break struct-order parity, so we never do that here.
func writeSearchJSON(w io.Writer, raw json.RawMessage, stable bool) error {
	var buf bytes.Buffer
	if stable {
		if err := json.Compact(&buf, raw); err != nil {
			return err
		}
	} else if err := json.Indent(&buf, raw, "", "  "); err != nil {
		return err
	}
	buf.WriteByte('\n')
	_, err := w.Write(buf.Bytes())
	return err
}

// writeSearchNDJSON renders one compact JSON result object per line, mirroring
// legacy writeNDJSONOutput(resp.Results). Each result is encoded through the typed
// searchResult struct, whose explicit field order reproduces the legacy
// search.Result struct order (NOT alphabetical).
func writeSearchNDJSON(w io.Writer, results []searchResult) error {
	enc := json.NewEncoder(w)
	for i := range results {
		if err := enc.Encode(results[i]); err != nil {
			return err
		}
	}
	return nil
}

func respStatusStaleEventCount(raw json.RawMessage) int64 {
	var status struct {
		StaleEventCount int64 `json:"stale_event_count"`
	}
	_ = json.Unmarshal(raw, &status)
	return status.StaleEventCount
}

const (
	searchHumanHitColor      = "1;33"
	searchHumanTitleBudget   = 72
	searchHumanSnippetBudget = 150
)

func renderSearchHuman(w io.Writer, query string, results []searchResult) {
	if len(results) == 0 {
		fmt.Fprintln(w, style.Paint(style.ColDim, fmt.Sprintf("no matches for %q", query)))
		return
	}

	noun := "results"
	if len(results) == 1 {
		noun = "result"
	}
	fmt.Fprintf(w, "%s %s   %s\n\n",
		style.Paint(style.ColDim, "search"),
		query,
		style.Paint(style.ColDim, fmt.Sprintf("%d %s", len(results), noun)))

	idW, cidW := 0, 0
	for _, r := range results {
		if n := len(r.TaskID); n > idW {
			idW = n
		}
		if r.CommentID != nil {
			if n := len(*r.CommentID); n > cidW {
				cidW = n
			}
		}
	}

	gutter := idW + 2
	if cidW > 0 {
		gutter += cidW + 2
	}
	indent := strings.Repeat(" ", gutter)
	terms := strings.Fields(strings.ToLower(query))

	for _, r := range results {
		var line strings.Builder
		line.WriteString(style.Paint(style.ColDim, searchPadRight(r.TaskID, idW)))
		if cidW > 0 {
			cid := ""
			if r.CommentID != nil {
				cid = *r.CommentID
			}
			line.WriteString("  " + style.Paint(style.ColDim, searchPadRight(cid, cidW)))
		}
		title := searchFirstNonEmpty(r.Title, searchLastPathSegment(r.Path))
		line.WriteString("  " + searchTruncateRunes(title, searchHumanTitleBudget))
		if r.State != "" {
			line.WriteString("  " + style.Paint(style.StateColor(r.State), "<"+r.State+">"))
		}
		fmt.Fprintln(w, line.String())

		fmt.Fprintf(w, "%s%s\n", indent,
			style.Paint(style.ColDim, fmt.Sprintf("%s  ·  %.3f", r.Path, r.Score)))

		if snip := searchClipSnippet(r.Snippet, searchHumanSnippetBudget); snip != "" {
			fmt.Fprintf(w, "%s%s\n", indent, searchHighlightTerms(snip, terms))
		}
	}
}

func searchHighlightTerms(text string, terms []string) string {
	if !style.ColorEnabled || len(terms) == 0 {
		return style.Paint(style.ColDim, text)
	}
	lower := strings.ToLower(text)
	var spans [][2]int
	for _, t := range terms {
		if t == "" {
			continue
		}
		for from := 0; ; {
			i := strings.Index(lower[from:], t)
			if i < 0 {
				break
			}
			s := from + i
			spans = append(spans, [2]int{s, s + len(t)})
			from = s + len(t)
		}
	}
	if len(spans) == 0 {
		return style.Paint(style.ColDim, text)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })

	dim := "\033[" + style.ColDim + "m"
	hit := "\033[" + searchHumanHitColor + "m"
	const reset = "\033[0m"
	var b strings.Builder
	b.WriteString(dim)
	pos := 0
	for _, sp := range spans {
		if sp[0] < pos {
			continue
		}
		b.WriteString(text[pos:sp[0]])
		b.WriteString(reset + hit + text[sp[0]:sp[1]] + reset + dim)
		pos = sp[1]
	}
	b.WriteString(text[pos:])
	b.WriteString(reset)
	return b.String()
}

func searchPadRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func searchTruncateRunes(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

func searchClipSnippet(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if sp := strings.LastIndexByte(cut, ' '); sp > n/2 {
		cut = cut[:sp]
	}
	cut = strings.TrimRight(cut, " ,.;:—-")
	return cut + "…"
}

func searchFirstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func searchLastPathSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
