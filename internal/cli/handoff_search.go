package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

const handoffSearchExample = "wrkq handoff search quartz --scope cody@wrkq --status all"

var handoffSearchPorcelain bool

func init() {
	handoffSearchCmd.Flags().BoolVar(&handoffSearchPorcelain, "porcelain", false,
		"Emit next_cursor=<token> on stderr (mirrors `wrkq handoff list --porcelain`)")
}

type handoffSearchOutput struct {
	Handoffs    []handoffJSON      `json:"handoffs"`
	NextCursor  *string            `json:"next_cursor"`
	Truncated   bool               `json:"truncated"`
	Diagnostics []scope.Diagnostic `json:"diagnostics,omitempty"`
}

func runHandoffSearch(cmd *cobra.Command, args []string) error {
	defer resetHandoffSearchFlags()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	mode, modeErr := resolveHandoffSearchOutputMode(stdout)
	if modeErr != nil {
		return writeHandoffSearchError(stderr, mode, 1, "validation_error", modeErr.Error(), nil, "")
	}

	query, queryErr := readHandoffSearchQuery(args)
	if queryErr != nil {
		return writeHandoffSearchError(stderr, mode, 1, "validation_error", queryErr.Error(), nil, handoffSearchExample)
	}

	resolved, diags, resolveErr := scope.Resolve(strings.TrimSpace(handoffSearchScope))
	if resolveErr != nil {
		return writeHandoffSearchError(stderr, mode, 2, "scope_unresolvable", resolveErr.Error(), diags, handoffSearchExample)
	}
	if resolved.ProjectID == "" {
		return writeHandoffSearchError(stderr, mode, 2, "scope_unresolvable",
			"handoff search requires an agent/project scope; pass --scope cody@wrkq or set ASP_SCOPE_REF=agent:cody:project:wrkq",
			diags, handoffSearchExample)
	}

	limit, limitErr := resolveHandoffListLimit(handoffSearchLimit, stderr)
	if limitErr != nil {
		return writeHandoffSearchError(stderr, mode, 1, "validation_error", limitErr.Error(), diags, "")
	}

	status, statusErr := normalizeHandoffListStatus(handoffSearchStatus)
	if statusErr != nil {
		return writeHandoffSearchError(stderr, mode, 1, "invalid_status", statusErr.Error(), diags,
			"wrkq handoff search quartz --status pending|acknowledged|all")
	}

	database, err := openHandoffCreateDB(cmd)
	if err != nil {
		return writeHandoffSearchError(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}
	defer func() { _ = database.Close() }()

	handoffs, nextCursor, err := store.SearchHandoffs(cmd.Context(), database, store.SearchHandoffsOpts{
		Query:    query,
		ScopeRef: resolved.CanonicalRef,
		Status:   status,
		Limit:    limit,
		Cursor:   strings.TrimSpace(handoffSearchCursor),
	})
	if err != nil {
		return writeHandoffSearchError(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}

	out := handoffSearchOutput{
		Handoffs:    make([]handoffJSON, 0, len(handoffs)),
		Diagnostics: diags,
		Truncated:   nextCursor != "",
	}
	for _, h := range handoffs {
		out.Handoffs = append(out.Handoffs, toHandoffJSON(h))
	}
	if nextCursor != "" {
		nc := nextCursor
		out.NextCursor = &nc
	}

	if handoffSearchPorcelain && nextCursor != "" {
		fmt.Fprintf(stderr, "next_cursor=%s\n", nextCursor)
	}

	if err := writeHandoffSearchOutput(stdout, stderr, mode, query, resolved.CanonicalRef, out); err != nil {
		return exitError(1, err)
	}
	return nil
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

func resolveHandoffSearchOutputMode(stdout io.Writer) (handoffOutputMode, error) {
	selected := 0
	if handoffSearchJSON {
		selected++
	}
	if handoffSearchNDJSON {
		selected++
	}
	if handoffSearchHuman {
		selected++
	}
	if selected > 1 {
		return handoffOutputJSON, fmt.Errorf("choose only one output mode: --json, --ndjson, or --human")
	}
	switch {
	case handoffSearchJSON:
		return handoffOutputJSON, nil
	case handoffSearchNDJSON:
		return handoffOutputNDJSON, nil
	case handoffSearchHuman:
		return handoffOutputHuman, nil
	case isStdoutTTY(stdout):
		return handoffOutputHuman, nil
	default:
		return handoffOutputJSON, nil
	}
}

func writeHandoffSearchOutput(stdout, stderr io.Writer, mode handoffOutputMode, query, scopeRef string, out handoffSearchOutput) error {
	switch mode {
	case handoffOutputHuman:
		for _, d := range out.Diagnostics {
			fmt.Fprintf(stderr, "[%s] %s: %s\n", d.Level, d.Code, d.Message)
		}
		if len(out.Handoffs) == 0 {
			fmt.Fprintf(stdout, "No handoffs match %q in %s.\n", query, scopeRef)
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tScope\tStatus\tCreated\tTitle")
		for _, h := range out.Handoffs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				h.ID, h.ScopeRef, h.Status,
				h.CreatedAt.Format(time.RFC3339),
				truncateHandoffTitle(h.Title, 60),
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if out.NextCursor != nil {
			fmt.Fprintf(stdout, "(%d shown, more available - use --cursor %s)\n", len(out.Handoffs), *out.NextCursor)
		}
		return nil
	case handoffOutputNDJSON:
		encoder := json.NewEncoder(stdout)
		for _, h := range out.Handoffs {
			if err := encoder.Encode(h); err != nil {
				return err
			}
		}
		footer := map[string]interface{}{
			"cursor":    out.NextCursor,
			"truncated": out.Truncated,
		}
		return encoder.Encode(footer)
	default:
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(out)
	}
}

func writeHandoffSearchError(stderr io.Writer, mode handoffOutputMode, exitCode int, code, message string, diags []scope.Diagnostic, example string) error {
	errOut := handoffErrorOutput{
		Error: structuredCLIError{
			Code:    code,
			Message: message,
			Example: example,
		},
		Diagnostics: diags,
	}
	if mode == handoffOutputHuman {
		fmt.Fprintf(stderr, "Error: %s\n", message)
		if example != "" {
			fmt.Fprintf(stderr, "Example: %s\n", example)
		}
		for _, d := range diags {
			fmt.Fprintf(stderr, "[%s] %s: %s\n", d.Level, d.Code, d.Message)
		}
	} else {
		encoder := json.NewEncoder(stderr)
		if mode == handoffOutputJSON {
			encoder.SetIndent("", "  ")
		}
		_ = encoder.Encode(errOut)
	}
	return exitErrorReported(exitCode, fmt.Errorf("%s: %s", code, message))
}

func resetHandoffSearchFlags() {
	handoffSearchScope = ""
	handoffSearchStatus = "pending"
	handoffSearchLimit = 0
	handoffSearchCursor = ""
	handoffSearchJSON = false
	handoffSearchNDJSON = false
	handoffSearchHuman = false
	handoffSearchPorcelain = false
}
