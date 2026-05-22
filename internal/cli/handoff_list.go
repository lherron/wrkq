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

// Default and maximum bounds for `wrkq handoff list --limit`. Values above the
// cap are clamped down with a stderr diagnostic; zero/unset uses the default.
const (
	handoffListDefaultLimit = 50
	handoffListMaxLimit     = 500
)

// Extra flag for porcelain mode. The other --json/--ndjson/--human flags and
// scope/status/limit/cursor flags are declared in handoff.go alongside the
// command definition.
var handoffListPorcelain bool

func init() {
	handoffListCmd.Flags().BoolVar(&handoffListPorcelain, "porcelain", false,
		"Emit next_cursor=<token> on stderr (mirrors 'wrkq comment ls --porcelain')")
}

// handoffListOutput is the structured payload returned by JSON and NDJSON modes.
type handoffListOutput struct {
	Handoffs    []handoffJSON      `json:"handoffs"`
	NextCursor  *string            `json:"next_cursor"`
	Truncated   bool               `json:"truncated"`
	Diagnostics []scope.Diagnostic `json:"diagnostics,omitempty"`
}

func runHandoffList(cmd *cobra.Command, _ []string) error {
	defer resetHandoffListFlags()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	mode, modeErr := resolveHandoffListOutputMode(stdout)
	if modeErr != nil {
		return writeHandoffListError(stderr, mode, 1, "validation_error", modeErr.Error(), nil, "")
	}

	resolved, diags, resolveErr := scope.Resolve(strings.TrimSpace(handoffListScope))
	if resolveErr != nil {
		return writeHandoffListError(stderr, mode, 2, "scope_unresolvable", resolveErr.Error(), diags, handoffScopeExample)
	}
	if resolved.ProjectID == "" {
		return writeHandoffListError(stderr, mode, 2, "scope_unresolvable",
			"handoff list requires an agent/project scope; pass --scope cody@wrkq or set ASP_SCOPE_REF=agent:cody:project:wrkq",
			diags, handoffScopeExample)
	}

	limit, limitErr := resolveHandoffListLimit(handoffListLimit, stderr)
	if limitErr != nil {
		return writeHandoffListError(stderr, mode, 1, "validation_error", limitErr.Error(), diags, "")
	}

	status, statusErr := normalizeHandoffListStatus(handoffListStatus)
	if statusErr != nil {
		return writeHandoffListError(stderr, mode, 1, "invalid_status", statusErr.Error(), diags,
			"wrkq handoff list --status pending|acknowledged|all")
	}

	database, err := openHandoffCreateDB(cmd)
	if err != nil {
		return writeHandoffListError(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}
	defer func() { _ = database.Close() }()

	handoffs, nextCursor, err := store.ListHandoffs(cmd.Context(), database, store.ListHandoffsOpts{
		ScopeRef: resolved.CanonicalRef,
		Status:   status,
		Limit:    limit,
		Cursor:   strings.TrimSpace(handoffListCursor),
	})
	if err != nil {
		return writeHandoffListError(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}

	out := handoffListOutput{
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

	if handoffListPorcelain && nextCursor != "" {
		fmt.Fprintf(stderr, "next_cursor=%s\n", nextCursor)
	}

	if err := writeHandoffListOutput(stdout, stderr, mode, resolved.CanonicalRef, status, out); err != nil {
		return exitError(1, err)
	}
	return nil
}

// resolveHandoffListOutputMode mirrors the handoff create resolver: explicit
// flags win, otherwise TTY-aware (human on TTY, JSON when piped).
func resolveHandoffListOutputMode(stdout io.Writer) (handoffOutputMode, error) {
	selected := 0
	if handoffListJSON {
		selected++
	}
	if handoffListNDJSON {
		selected++
	}
	if handoffListHuman {
		selected++
	}
	if selected > 1 {
		return handoffOutputJSON, fmt.Errorf("choose only one output mode: --json, --ndjson, or --human")
	}
	switch {
	case handoffListJSON:
		return handoffOutputJSON, nil
	case handoffListNDJSON:
		return handoffOutputNDJSON, nil
	case handoffListHuman:
		return handoffOutputHuman, nil
	case isStdoutTTY(stdout):
		return handoffOutputHuman, nil
	default:
		return handoffOutputJSON, nil
	}
}

// resolveHandoffListLimit applies the default (50) and cap (500). Values above
// the cap are clamped down with a stderr diagnostic. Negative values are an
// error.
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

// normalizeHandoffListStatus validates the --status flag and returns the store
// status filter. We return literal "all" (instead of "") for the all case
// because store.ListHandoffs treats an empty Status as default-to-pending —
// the store-internal "all" sentinel collapses to no filter inside the store's
// own normalization helper. Empty CLI input defaults to "pending" per AC2.
func normalizeHandoffListStatus(status string) (string, error) {
	switch strings.TrimSpace(status) {
	case "", store.HandoffStatusPending:
		return store.HandoffStatusPending, nil
	case store.HandoffStatusAcknowledged:
		return store.HandoffStatusAcknowledged, nil
	case "all":
		return "all", nil
	default:
		return "", fmt.Errorf("invalid --status %q: must be pending, acknowledged, or all", status)
	}
}

func writeHandoffListOutput(stdout, stderr io.Writer, mode handoffOutputMode, scopeRef, status string, out handoffListOutput) error {
	switch mode {
	case handoffOutputHuman:
		for _, d := range out.Diagnostics {
			fmt.Fprintf(stderr, "[%s] %s: %s\n", d.Level, d.Code, d.Message)
		}
		if len(out.Handoffs) == 0 {
			label := "pending"
			switch status {
			case "all":
				label = "all"
			case store.HandoffStatusAcknowledged:
				label = "acknowledged"
			}
			fmt.Fprintf(stdout, "No %s handoffs for %s.\n", label, scopeRef)
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
			fmt.Fprintf(stdout, "(%d shown, more available — use --cursor %s)\n", len(out.Handoffs), *out.NextCursor)
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

func writeHandoffListError(stderr io.Writer, mode handoffOutputMode, exitCode int, code, message string, diags []scope.Diagnostic, example string) error {
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

func truncateHandoffTitle(s string, max int) string {
	if max <= 0 {
		return s
	}
	if len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

func resetHandoffListFlags() {
	handoffListScope = ""
	handoffListStatus = "pending"
	handoffListLimit = 0
	handoffListCursor = ""
	handoffListJSON = false
	handoffListNDJSON = false
	handoffListHuman = false
	handoffListPorcelain = false
}
