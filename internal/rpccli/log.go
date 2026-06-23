package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// logEvent mirrors the legacy internal/cli logEvent shape EXACTLY (field order +
// json tags + pointer/omitempty). The mirror decodes the server's compat history
// rows into this type so JSON/NDJSON re-encoding and the oneline/detailed/--patch
// renderers go through the SAME byte path as legacy. payload stays a raw STRING;
// --patch decodes it CLIENT-side (no extra RPC, no server-side patch projection).
type logEvent struct {
	ID           int64     `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	ActorUUID    *string   `json:"actor_uuid,omitempty"`
	ActorSlug    *string   `json:"actor_slug,omitempty"`
	ActorID      *string   `json:"actor_id,omitempty"`
	PrincipalRef *string   `json:"principal_ref,omitempty"`
	ScopeRef     *string   `json:"scope_ref,omitempty"`
	ResourceType string    `json:"resource_type"`
	ResourceUUID string    `json:"resource_uuid"`
	EventType    string    `json:"event_type"`
	ETag         *int64    `json:"etag,omitempty"`
	Payload      *string   `json:"payload,omitempty"`
}

// newLogCmd mirrors `wrkq log <PATHSPEC|ID>` via wrkq.history.listView (the
// server-owned compatibility history read model over the generic event_log table —
// distinct from wrkf.event.query, which reads workflow_events). The server owns
// resource resolution (task/container/actor by friendly ID T-*/P-*/A- OR UUID),
// since/until filtering, and cursor.Apply + limit+1 + BuildNextCursor over
// event_log.id DESC. The CLI owns presentation (json/ndjson/oneline/detailed/patch).
//
// Implemented surface (byte-proven against legacy): --json, NDJSON (non-TTY
// default + --porcelain), --oneline, the detailed default, and --patch (over
// stable single-key payloads — multi-key payload nondeterminism is shared with
// legacy, see the parity fixtures). --since / --until, --limit, --cursor, and the
// porcelain next_cursor→stderr contract all match legacy.
func newLogCmd() *cobra.Command {
	var since, until, cursorTok string
	var oneline, patch, asJSON, porcelain bool
	var limit int
	cmd := &cobra.Command{
		Use:   "log <PATHSPEC|ID>",
		Short: "Show change history for a task or container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			// Legacy: applyProjectRootToSelector(args[0], defaultToRoot=false). The
			// scoper reproduces caller semantics; the server never reads project-root.
			target := sc.selector(args[0], false)

			params := map[string]any{"target": target}
			if since != "" {
				params["since"] = since
			}
			if until != "" {
				params["until"] = until
			}
			// Legacy always passes the flag value (default 50, 0 = unlimited). Send it
			// verbatim so the server applies the identical limit+1 / hasMore logic.
			params["limit"] = limit
			if cursorTok != "" {
				params["cursor"] = cursorTok
			}

			raw, err := tr.Call(cmd.Context(), "wrkq.history.listView", params)
			if err != nil {
				if re, ok := err.(*Error); ok {
					// Legacy surfaces resolve/query/cursor errors raw (the server
					// already includes the "failed to ...:" wrapping), without a
					// domain-code prefix.
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

			// Legacy prints next_cursor to stderr (porcelain only) BEFORE rendering
			// rows to stdout; match that ordering.
			if porcelain && res.NextCursor != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", res.NextCursor)
			}

			events, derr := decodeLogEvents(res.Items)
			if derr != nil {
				return derr
			}

			out := cmd.OutOrStdout()
			if asJSON {
				return renderLogEventsJSON(out, events)
			}
			if porcelain || (!isStdoutTTY(out) && !oneline && !patch) {
				return renderLogEventsNDJSON(out, events)
			}
			if oneline {
				return renderLogEventsOneline(out, events)
			}
			return renderLogEventsDetailed(out, events, patch)
		},
	}
	cmd.Flags().StringVar(&since, "since", "", "Show changes since date/time (YYYY-MM-DD or RFC3339)")
	cmd.Flags().StringVar(&until, "until", "", "Show changes until date/time (YYYY-MM-DD or RFC3339)")
	cmd.Flags().BoolVar(&oneline, "oneline", false, "Compact one-line format")
	cmd.Flags().BoolVar(&patch, "patch", false, "Show detailed payload changes")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().IntVar(&limit, "limit", 50, "Limit number of events (0 = unlimited)")
	cmd.Flags().StringVar(&cursorTok, "cursor", "", "Pagination cursor from previous page")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output with cursor on stderr")
	return cmd
}

// decodeLogEvents unmarshals the server projection rows into the legacy struct. A
// nil/empty item slice yields a nil slice so `--json` of an empty history encodes
// as `null` (legacy `var events []logEvent`), NOT `[]`.
func decodeLogEvents(items []json.RawMessage) ([]logEvent, error) {
	var events []logEvent
	for _, it := range items {
		var e logEvent
		if err := json.Unmarshal(it, &e); err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, nil
}

func renderLogEventsJSON(w io.Writer, events []logEvent) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(events)
}

func renderLogEventsNDJSON(w io.Writer, events []logEvent) error {
	encoder := json.NewEncoder(w)
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func renderLogEventsOneline(w io.Writer, events []logEvent) error {
	for _, e := range events {
		actor := "system"
		if e.PrincipalRef != nil {
			actor = *e.PrincipalRef
		} else if e.ActorSlug != nil {
			actor = *e.ActorSlug
		}
		timestamp := e.Timestamp.Format("2006-01-02 15:04")
		fmt.Fprintf(w, "%s  %s  %s  by %s\n", timestamp, e.EventType, formatLogEventSummary(e), actor)
	}
	return nil
}

func renderLogEventsDetailed(w io.Writer, events []logEvent, showPatch bool) error {
	for i, e := range events {
		if i > 0 {
			fmt.Fprintln(w)
		}
		fmt.Fprintf(w, "\033[33mEvent %d\033[0m - %s\n", e.ID, e.EventType)
		fmt.Fprintf(w, "  Timestamp:  %s\n", e.Timestamp.Format(time.RFC3339))

		if e.PrincipalRef != nil {
			fmt.Fprintf(w, "  Principal:  %s\n", *e.PrincipalRef)
			if e.ScopeRef != nil {
				fmt.Fprintf(w, "  Scope:      %s\n", *e.ScopeRef)
			}
		}
		if e.ActorSlug != nil && e.ActorID != nil {
			fmt.Fprintf(w, "  Actor:      %s (%s)\n", *e.ActorSlug, *e.ActorID)
		} else if e.PrincipalRef == nil {
			fmt.Fprintf(w, "  Actor:      system\n")
		}

		if e.ETag != nil {
			fmt.Fprintf(w, "  ETag:       %d\n", *e.ETag)
		}

		fmt.Fprintf(w, "  Summary:    %s\n", formatLogEventSummary(e))

		if showPatch && e.Payload != nil {
			fmt.Fprintln(w, "  Changes:")
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(*e.Payload), &payload); err == nil {
				for key, value := range payload {
					fmt.Fprintf(w, "    %s: %v\n", key, value)
				}
			} else {
				fmt.Fprintf(w, "    %s\n", *e.Payload)
			}
		}
	}
	return nil
}

func formatLogEventSummary(e logEvent) string {
	if e.Payload == nil {
		return "(no details)"
	}
	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(*e.Payload), &payload); err != nil {
		return "(invalid payload)"
	}
	var parts []string
	for key, value := range payload {
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}
	if len(parts) == 0 {
		return "(no changes)"
	}
	summary := strings.Join(parts, ", ")
	if len(summary) > 60 {
		return summary[:57] + "..."
	}
	return summary
}
