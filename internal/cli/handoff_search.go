package cli

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/search"
	"github.com/lherron/wrkq/internal/search/indexer"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

const handoffSearchExample = "wrkq handoff search quartz --scope cody@wrkq --status all"

var handoffSearchPorcelain bool

func init() {
	handoffSearchCmd.Flags().BoolVar(&handoffSearchPorcelain, "porcelain", false,
		"Emit next_cursor=<token> on stderr (mirrors 'wrkq handoff list --porcelain')")
}

type handoffSearchOutput struct {
	Handoffs    []handoffJSON      `json:"handoffs"`
	NextCursor  *string            `json:"next_cursor"`
	Truncated   bool               `json:"truncated"`
	Diagnostics []scope.Diagnostic `json:"diagnostics,omitempty"`
}

type handoffSearchCursorPayload struct {
	Offset int `json:"offset"`
}

func runHandoffSearch(cmd *cobra.Command, args []string) error {
	defer resetHandoffSearchFlags()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	sel, modeErr := resolveHandoffSelection(cmd, outputShapeList, true)
	mode := handoffModeFromSelection(sel)
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

	offset, offsetErr := decodeHandoffSearchCursor(strings.TrimSpace(handoffSearchCursor))
	if offsetErr != nil {
		return writeHandoffSearchError(stderr, mode, 1, "validation_error", offsetErr.Error(), diags, "")
	}

	database, err := openHandoffCreateDB(cmd)
	if err != nil {
		return writeHandoffSearchError(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}
	defer func() { _ = database.Close() }()

	cfg, err := config.Load()
	if err != nil {
		return writeHandoffSearchError(stderr, mode, 1, "runtime_error", fmt.Errorf("failed to load config: %w", err).Error(), diags, "")
	}
	if dbFlag := cmd.Flag("db"); dbFlag != nil {
		if dbPath := dbFlag.Value.String(); dbPath != "" {
			cfg.DBPath = dbPath
		}
	}

	idx, err := openSearchIndex(cfg)
	if err != nil {
		return writeHandoffSearchError(stderr, mode, 1, "search_unavailable",
			fmt.Errorf("search index unavailable: %w (try `wrkq index rebuild`)", err).Error(),
			diags, "wrkq index rebuild")
	}
	defer func() { _ = idx.Close() }()

	embedder := denseEmbedderFromConfig(cfg)

	ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
	defer cancel()

	ix := indexer.New(database, idx, embedder)
	ix.BatchSize = cfg.Search.IndexBatchSize
	if err := ix.IndexPending(ctx); err != nil {
		fmt.Fprintf(stderr, "warning: search index update failed: %v (proceeding with stale index)\n", err)
	}

	svc := search.NewService(database, idx, embedder)

	resp, err := svc.Search(ctx, search.Options{
		Query:        query,
		ResourceType: "handoff",
		ScopeRef:     resolved.CanonicalRef,
		Status:       status,
		Offset:       offset,
		Limit:        limit,
	})
	if err != nil {
		return writeHandoffSearchError(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}

	if resp.Stale {
		fmt.Fprintf(stderr, "warning: search index is stale by %d event(s); run `wrkq index rebuild` for fresh results\n",
			resp.Status.StaleEventCount)
	}

	out := handoffSearchOutput{
		Handoffs:    make([]handoffJSON, 0, len(resp.Results)),
		Diagnostics: diags,
	}
	for _, result := range resp.Results {
		handoff, getErr := store.GetHandoff(ctx, database, result.ResourceUUID)
		if getErr != nil {
			fmt.Fprintf(stderr, "warning: failed to hydrate handoff %s: %v\n", result.ResourceID, getErr)
			continue
		}
		out.Handoffs = append(out.Handoffs, toHandoffJSON(handoff))
	}

	nextOffset := resp.Offset + len(resp.Results)
	if nextOffset < resp.TotalMatches {
		token, encErr := encodeHandoffSearchCursor(nextOffset)
		if encErr == nil {
			out.NextCursor = &token
			out.Truncated = true
		}
	}

	if sel.Stable && out.NextCursor != nil {
		fmt.Fprintf(stderr, "next_cursor=%s\n", *out.NextCursor)
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

func decodeHandoffSearchCursor(token string) (int, error) {
	if token == "" {
		return 0, nil
	}
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return 0, fmt.Errorf("invalid --cursor: %w", err)
	}
	var c handoffSearchCursorPayload
	if err := json.Unmarshal(raw, &c); err != nil {
		return 0, fmt.Errorf("invalid --cursor: %w", err)
	}
	if c.Offset < 0 {
		return 0, fmt.Errorf("invalid --cursor: offset must be non-negative")
	}
	return c.Offset, nil
}

func encodeHandoffSearchCursor(offset int) (string, error) {
	raw, err := json.Marshal(handoffSearchCursorPayload{Offset: offset})
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(raw), nil
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
			"type":        "wrkq.pagination",
			"next_cursor": out.NextCursor,
			"truncated":   out.Truncated,
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
