package rpccli

// handoff_list.go — the paged handoff reads, `wrkq handoff list` and
// `wrkq handoff search`. Both resolve the caller's agent/project scope, share
// the status/limit/cursor flags, and render one page in the same three output
// modes; they differ only in the RPC method and its page shape.

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"

	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

const (
	handoffListDefaultLimit = 50
	handoffListMaxLimit     = 500
)

const handoffSearchExample = "wrkq handoff search quartz --scope cody@wrkq --status all"

// handoffPageFlags are the flags list and search share.
type handoffPageFlags struct {
	scopeOverride, status, cursor    string
	limit                            int
	asJSON, ndjson, human, porcelain bool
}

func addHandoffPageFlags(cmd *cobra.Command, f *handoffPageFlags, porcelainMirrors string) {
	cmd.Flags().StringVar(&f.scopeOverride, "scope", "", "Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	cmd.Flags().StringVar(&f.status, "status", "pending", "Status filter: pending, acknowledged, or all (default: pending)")
	cmd.Flags().IntVar(&f.limit, "limit", 0, "Maximum number of results (0 = server default)")
	cmd.Flags().StringVar(&f.cursor, "cursor", "", "Pagination cursor from a previous page")
	cmd.Flags().BoolVar(&f.asJSON, "json", false, "Force JSON output")
	cmd.Flags().BoolVar(&f.ndjson, "ndjson", false, "Force NDJSON output")
	cmd.Flags().BoolVar(&f.human, "human", false, "Force human-readable output")
	cmd.Flags().BoolVar(&f.porcelain, "porcelain", false, "Emit next_cursor=<token> on stderr (mirrors '"+porcelainMirrors+" --porcelain')")
}

type handoffListOutput struct {
	Handoffs    []handoffJSON      `json:"handoffs"`
	NextCursor  *string            `json:"next_cursor"`
	Truncated   bool               `json:"truncated"`
	Diagnostics []scope.Diagnostic `json:"diagnostics,omitempty"`
}

func newHandoffListCmd() *cobra.Command {
	var f handoffPageFlags
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List handoffs for the resolved agent/project scope",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHandoffList(cmd, f)
		},
	}
	addHandoffPageFlags(cmd, &f, "wrkq comment ls")
	return cmd
}

func newHandoffSearchCmd() *cobra.Command {
	var f handoffPageFlags
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search handoffs by title, body, scope, or status",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHandoffSearch(cmd, args, f)
		},
	}
	addHandoffPageFlags(cmd, &f, "wrkq handoff list")
	return cmd
}

func runHandoffList(cmd *cobra.Command, f handoffPageFlags) error {
	stderr := cmd.ErrOrStderr()
	mode := handoffMode(cmd, f.asJSON, f.ndjson, f.human, handoffOutputNDJSON)

	req, err := prepareHandoffPage(stderr, mode, f, "list", handoffScopeExample,
		"wrkq handoff list --status pending|acknowledged|all")
	if err != nil {
		return err
	}
	raw, err := req.call(cmd, "wrkq.handoff.listView", func(rerr error) error {
		return writeHandoffListErr(stderr, mode, 1, "runtime_error", rpcMessage(rerr), req.diags, "")
	})
	if err != nil {
		return err
	}

	var page struct {
		Items      []handoffJSON `json:"items"`
		NextCursor string        `json:"nextCursor"`
	}
	if uerr := json.Unmarshal(raw, &page); uerr != nil {
		return uerr
	}
	out := handoffListOutput{
		Handoffs:    append([]handoffJSON{}, page.Items...),
		Diagnostics: req.diags,
		Truncated:   page.NextCursor != "",
	}
	if page.NextCursor != "" {
		nc := page.NextCursor
		out.NextCursor = &nc
	}
	echoHandoffNextCursor(stderr, f, mode, out.NextCursor)
	return writeHandoffPage(cmd, mode, out,
		fmt.Sprintf("No %s handoffs for %s.", req.status, req.scopeRef), "—")
}

func runHandoffSearch(cmd *cobra.Command, args []string, f handoffPageFlags) error {
	stderr := cmd.ErrOrStderr()
	mode := handoffMode(cmd, f.asJSON, f.ndjson, f.human, handoffOutputNDJSON)

	query, queryErr := readHandoffSearchQuery(args)
	if queryErr != nil {
		return writeHandoffListErr(stderr, mode, 1, "validation_error", queryErr.Error(), nil, handoffSearchExample)
	}
	req, err := prepareHandoffPage(stderr, mode, f, "search", handoffSearchExample,
		"wrkq handoff search quartz --status pending|acknowledged|all")
	if err != nil {
		return err
	}
	req.params["query"] = query
	raw, err := req.call(cmd, "wrkq.handoff.searchView", func(rerr error) error {
		re, ok := rerr.(*Error)
		if ok && re.DomainID == "WRKQ_VALIDATION" && strings.Contains(re.Message, "search is disabled") {
			msg := "search index unavailable: search is disabled (try `wrkq index rebuild`)"
			return writeHandoffListErr(stderr, mode, 1, "search_unavailable", msg, req.diags, "wrkq index rebuild")
		}
		return writeHandoffListErr(stderr, mode, 1, "runtime_error", rpcMessage(rerr), req.diags, "")
	})
	if err != nil {
		return err
	}

	var page struct {
		Handoffs        []handoffJSON `json:"handoffs"`
		NextCursor      *string       `json:"next_cursor"`
		Truncated       bool          `json:"truncated"`
		Stale           bool          `json:"stale,omitempty"`
		StaleEventCount int64         `json:"stale_event_count,omitempty"`
		IndexWarning    string        `json:"index_warning,omitempty"`
	}
	if uerr := json.Unmarshal(raw, &page); uerr != nil {
		return uerr
	}
	if page.IndexWarning != "" {
		fmt.Fprintf(stderr, "warning: %s\n", page.IndexWarning)
	}
	if page.Stale {
		fmt.Fprintf(stderr, "warning: search index is stale by %d event(s); run `wrkq index rebuild` for fresh results\n",
			page.StaleEventCount)
	}
	echoHandoffNextCursor(stderr, f, mode, page.NextCursor)

	out := handoffListOutput{
		Handoffs:    append([]handoffJSON{}, page.Handoffs...),
		NextCursor:  page.NextCursor,
		Truncated:   page.Truncated,
		Diagnostics: req.diags,
	}
	return writeHandoffPage(cmd, mode, out,
		fmt.Sprintf("No handoffs match %q in %s.", query, req.scopeRef), "-")
}

// handoffPageRequest is a validated list/search call: the caller's resolved
// scope and the shared scopeRef/status/limit/cursor params.
type handoffPageRequest struct {
	stderr   io.Writer
	mode     handoffOutputMode
	scopeRef string
	status   string
	diags    []scope.Diagnostic
	params   map[string]any
}

// prepareHandoffPage resolves the caller scope (which must name a project) and
// validates --limit and --status, writing the legacy structured error on
// failure. verb names the command in the scope error.
func prepareHandoffPage(stderr io.Writer, mode handoffOutputMode, f handoffPageFlags, verb, scopeExample, statusExample string) (handoffPageRequest, error) {
	resolved, diags, resolveErr := scope.Resolve(strings.TrimSpace(f.scopeOverride))
	if resolveErr != nil {
		return handoffPageRequest{}, writeHandoffListErr(stderr, mode, 2, "scope_unresolvable", resolveErr.Error(), diags, scopeExample)
	}
	if resolved.ProjectID == "" {
		return handoffPageRequest{}, writeHandoffListErr(stderr, mode, 2, "scope_unresolvable",
			"handoff "+verb+" requires an agent/project scope; pass --scope cody@wrkq or set ASP_SCOPE_REF=agent:cody:project:wrkq",
			diags, scopeExample)
	}
	limit, limitErr := resolveHandoffListLimit(f.limit, stderr)
	if limitErr != nil {
		return handoffPageRequest{}, writeHandoffListErr(stderr, mode, 1, "validation_error", limitErr.Error(), diags, "")
	}
	status, statusErr := normalizeHandoffListStatus(f.status)
	if statusErr != nil {
		return handoffPageRequest{}, writeHandoffListErr(stderr, mode, 1, "invalid_status", statusErr.Error(), diags, statusExample)
	}
	params := map[string]any{
		"scopeRef": resolved.CanonicalRef,
		"status":   status,
		"limit":    limit,
	}
	if c := strings.TrimSpace(f.cursor); c != "" {
		params["cursor"] = c
	}
	return handoffPageRequest{
		stderr: stderr, mode: mode, scopeRef: resolved.CanonicalRef, status: status, diags: diags, params: params,
	}, nil
}

// call opens the transport and sends method; an RPC failure is reported
// through onRPCError.
func (r handoffPageRequest) call(cmd *cobra.Command, method string, onRPCError func(error) error) (json.RawMessage, error) {
	tr, closeFn, err := openHandoffMirror(cmd)
	if err != nil {
		return nil, writeHandoffListErr(r.stderr, r.mode, 1, "runtime_error", err.Error(), r.diags, "")
	}
	defer closeFn()
	raw, rerr := tr.Call(cmd.Context(), method, r.params)
	if rerr != nil {
		return nil, onRPCError(rerr)
	}
	return raw, nil
}

// echoHandoffNextCursor echoes next_cursor on stderr in porcelain or NDJSON
// mode, exactly as legacy comment ls / handoff list does.
func echoHandoffNextCursor(stderr io.Writer, f handoffPageFlags, mode handoffOutputMode, next *string) {
	if (f.porcelain || mode == handoffOutputNDJSON) && next != nil {
		fmt.Fprintf(stderr, "next_cursor=%s\n", *next)
	}
}

func resolveHandoffListLimit(requested int, stderr io.Writer) (int, error) {
	if requested < 0 {
		return 0, fmt.Errorf("--limit cannot be negative")
	}
	if requested == 0 {
		return handoffListDefaultLimit, nil
	}
	if requested > handoffListMaxLimit {
		fmt.Fprintf(stderr, "warning: --limit %d exceeds maximum %d; clamping to %d\n",
			requested, handoffListMaxLimit, handoffListMaxLimit)
		return handoffListMaxLimit, nil
	}
	return requested, nil
}

func normalizeHandoffListStatus(status string) (string, error) {
	switch strings.TrimSpace(status) {
	case "", "pending":
		return "pending", nil
	case "acknowledged":
		return "acknowledged", nil
	case "all":
		return "all", nil
	default:
		return "", fmt.Errorf("invalid --status %q: must be pending, acknowledged, or all", status)
	}
}

func readHandoffSearchQuery(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("query is required")
	}
	query := strings.TrimSpace(args[0])
	if query == "" {
		return "", fmt.Errorf("query cannot be empty")
	}
	return query, nil
}

// writeHandoffPage renders one list/search page. Human mode prints empty when
// the page has no rows; moreDash is the separator in the "more available"
// footer (list and search have always differed here).
func writeHandoffPage(cmd *cobra.Command, mode handoffOutputMode, out handoffListOutput, empty, moreDash string) error {
	stdout := cmd.OutOrStdout()
	switch mode {
	case handoffOutputHuman:
		writeHandoffDiagnostics(cmd.ErrOrStderr(), out.Diagnostics)
		if len(out.Handoffs) == 0 {
			fmt.Fprintln(stdout, empty)
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tScope\tStatus\tCreated\tTitle")
		for _, h := range out.Handoffs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				h.ID, h.ScopeRef, h.Status,
				style.FormatLocalTime(h.CreatedAt),
				truncateHandoffTitle(h.Title, 60),
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if out.NextCursor != nil {
			fmt.Fprintf(stdout, "(%d shown, more available %s use --cursor %s)\n", len(out.Handoffs), moreDash, *out.NextCursor)
		}
		return nil
	case handoffOutputNDJSON:
		enc := json.NewEncoder(stdout)
		for _, h := range out.Handoffs {
			if err := enc.Encode(h); err != nil {
				return err
			}
		}
		footer := map[string]interface{}{
			"type":        "wrkq.pagination",
			"next_cursor": out.NextCursor,
			"truncated":   out.Truncated,
		}
		return enc.Encode(footer)
	default:
		return writeHandoffIndentedJSON(stdout, out)
	}
}

func truncateHandoffTitle(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}
