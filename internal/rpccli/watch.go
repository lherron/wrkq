package rpccli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
)

// watch mirrors `wrkq watch [PATH...]` over wrkq.history.tailView (T-05116), a
// SIBLING of wrkq.history.listView in the `history` namespace. tailView is the
// bounded ASCENDING raw event_log read model: the SERVER owns the cursored page
// (`id > cursor`, hard limit), actor slug/id + resource_id hydration, and the
// high-water cursor; the CLIENT (here) repeats it for --follow, prints the
// deprecation warning caller-side, and renders human/NDJSON locally. This is the
// shared bounded-polling-as-RPC-v1-streaming arch record (with monitor): NO server
// push/subscribe in v1; the loop lives caller-side. `monitor watch --raw`
// delegates to the SAME watchTailLoop (NDJSON, follow=true).

const watchPollInterval = 1 * time.Second

// watchEvent mirrors the legacy internal/cli watchEvent shape EXACTLY (field order
// + json tags + pointer/omitempty). Decoding the server's WrkqWatchEvent into this
// type routes NDJSON re-encoding + the human renderer through the SAME byte path as
// legacy. It INCLUDES resource_id and uses a STRING timestamp (distinct from
// logEvent). The mirror NEVER reuses logEvent for the raw tail.
type watchEvent struct {
	ID           int64   `json:"id"`
	Timestamp    string  `json:"timestamp"`
	PrincipalRef *string `json:"principal_ref,omitempty"`
	ScopeRef     *string `json:"scope_ref,omitempty"`
	ResourceType string  `json:"resource_type"`
	ResourceUUID *string `json:"resource_uuid,omitempty"`
	ResourceID   *string `json:"resource_id,omitempty"`
	EventType    string  `json:"event_type"`
	ETag         *int64  `json:"etag,omitempty"`
	Payload      *string `json:"payload,omitempty"`
}

func newWatchCmd() *cobra.Command {
	var since int64
	var ndjson bool
	var follow bool
	cmd := &cobra.Command{
		Use:   "watch [PATH...]",
		Short: "Stream change events from the event log",
		Long: `Stream change events from the event log in real-time.

Examples:
  wrkq watch                     # Watch all events
  wrkq watch --since 100         # Watch from event ID 100
  wrkq watch --ndjson            # Output as NDJSON
  wrkq watch portal/**           # Watch events under portal (future)
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			out := cmd.OutOrStdout()
			// Legacy prints the deprecation warning caller-side, BEFORE streaming.
			fmt.Fprintln(cmd.ErrOrStderr(), "wrkq watch is deprecated; use wrkq monitor watch --raw")
			// Legacy: ndjson = watchNDJSON || !isStdoutTTY(stdout). Non-TTY defaults
			// to NDJSON; a TTY without --ndjson renders the human format.
			ndjsonMode := ndjson || !isStdoutTTY(out)
			return watchTailLoop(cmd.Context(), tr, out, since, ndjsonMode, follow)
		},
	}
	cmd.Flags().Int64Var(&since, "since", 0, "Start from event ID (0 = all events)")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVarP(&follow, "follow", "f", true, "Follow new events (default true)")
	return cmd
}

// watchTailLoop reproduces legacy watchEvents: it polls wrkq.history.tailView for
// the next bounded ASCENDING page from the monotonic cursor, advances the cursor by
// the server's high-water, renders each row (NDJSON or human), and (when following)
// sleeps watchPollInterval between polls. Without --follow it drains the current
// backlog and returns. Shared by `wrkq watch` and `monitor watch --raw`.
func watchTailLoop(ctx context.Context, tr Transport, out io.Writer, sinceID int64, ndjson bool, follow bool) error {
	currentID := sinceID
	encoder := json.NewEncoder(out)

	for {
		raw, err := tr.Call(ctx, "wrkq.history.tailView", map[string]any{"cursor": currentID})
		if err != nil {
			return monitorStripError(err)
		}
		var res struct {
			Items     []watchEvent `json:"items"`
			HighWater int64        `json:"high_water"`
		}
		if jerr := json.Unmarshal(raw, &res); jerr != nil {
			return jerr
		}

		hasEvents := false
		for _, e := range res.Items {
			if ndjson {
				if encErr := encoder.Encode(e); encErr != nil {
					return fmt.Errorf("encode failed: %w", encErr)
				}
			} else {
				printWatchEvent(out, e)
			}
			currentID = e.ID
			hasEvents = true
		}
		if res.HighWater > currentID {
			currentID = res.HighWater
		}

		if !follow {
			break
		}
		_ = hasEvents
		time.Sleep(watchPollInterval)
	}
	return nil
}

// printWatchEvent reproduces legacy printWatchEvent's human format byte-for-byte.
func printWatchEvent(stdout io.Writer, e watchEvent) {
	timestamp := e.Timestamp

	actor := "system"
	if e.PrincipalRef != nil {
		actor = *e.PrincipalRef
	}

	resource := e.ResourceType
	if e.ResourceID != nil {
		resource += fmt.Sprintf(" %s", *e.ResourceID)
	} else if e.ResourceUUID != nil {
		resource += fmt.Sprintf(" %s", (*e.ResourceUUID)[:8])
	}

	fmt.Fprintf(stdout, "[%s] %s: %s by %s\n", timestamp, resource, e.EventType, actor)

	if e.Payload != nil && *e.Payload != "" {
		fmt.Fprintf(stdout, "  %s\n", formatWatchPayloadOneLine(*e.Payload))
	}
}

func formatWatchPayloadOneLine(payload string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return payload
	}
	var parts []string
	for key, value := range data {
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}
	return fmt.Sprintf("{%s}", joinWatchStrings(parts, ", "))
}

func joinWatchStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
