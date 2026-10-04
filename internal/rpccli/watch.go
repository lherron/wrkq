package rpccli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/spf13/cobra"
)

// The raw tail reads wrkq.history.tailView (T-05116), a SIBLING of
// wrkq.history.listView in the `history` namespace. tailView is the bounded
// ASCENDING raw event_log read model: the SERVER owns the cursored page
// (`id > cursor`, hard limit), actor slug/id + resource_id hydration, and the
// high-water cursor; the CLIENT (here) repeats it to follow and writes NDJSON.
// This is the shared bounded-polling-as-RPC-v1-streaming arch record (with
// monitor): NO server push/subscribe in v1; the loop lives caller-side.
// `monitor watch --raw` is its only command; `wrkq watch` is retired.

const watchPollInterval = 1 * time.Second

// watchEvent mirrors the legacy internal/cli watchEvent shape EXACTLY (field order
// + json tags + pointer/omitempty). Decoding the server's WrkqWatchEvent into this
// type routes NDJSON re-encoding + the human renderer through the SAME byte path as
// legacy. It INCLUDES resource_id and uses a STRING timestamp (distinct from
// logEvent). The mirror NEVER reuses logEvent for the raw tail.
type watchEvent struct {
	TaskID       string  `json:"task_id,omitempty"`
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

// retiredWatchPointer is the whole of `wrkq watch` since its retirement
// (T-10234, CLI standard §13): callers were migrated first, and the shim exits
// 2 with the replacement instead of streaming, for --help too.
const retiredWatchPointer = "wrkq watch was removed; use: wrkq monitor watch --raw [--since <event-id>] [--timeout <d>] [--stall-after <d>]"

func newWatchCmd() *cobra.Command {
	return &cobra.Command{
		Use:                "watch",
		Short:              "Removed: use wrkq monitor watch --raw",
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return exitError(clifunnel.ExitUsage, errors.New(retiredWatchPointer))
		},
	}
}

// watchTailLoop polls wrkq.history.tailView for the next bounded ASCENDING page
// from the monotonic cursor, advances the cursor by the server's high-water,
// writes each row as NDJSON, and sleeps watchPollInterval between polls. It
// backs `monitor watch --raw`, and returns when a clock expires: timeout bounds
// the whole follow, stallAfter the gap since the last event (0 = unbounded).
func watchTailLoop(ctx context.Context, tr Transport, out io.Writer, sinceID int64, timeout, stallAfter time.Duration) (monitorTerminalResult, error) {
	currentID := sinceID
	encoder := json.NewEncoder(out)
	started := time.Now()
	lastEvent := started

	for {
		raw, err := tr.Call(ctx, "wrkq.history.tailView", map[string]any{"cursor": currentID})
		if err != nil {
			return monitorResultError, monitorStripError(err)
		}
		var res struct {
			Items     []watchEvent `json:"items"`
			HighWater int64        `json:"high_water"`
		}
		if jerr := json.Unmarshal(raw, &res); jerr != nil {
			return monitorResultError, jerr
		}

		for _, e := range res.Items {
			if encErr := encoder.Encode(e); encErr != nil {
				return monitorResultError, fmt.Errorf("encode failed: %w", encErr)
			}
			currentID = e.ID
			lastEvent = time.Now()
		}
		if res.HighWater > currentID {
			currentID = res.HighWater
		}

		if timeout > 0 && time.Since(started) >= timeout {
			return monitorResultTimeout, nil
		}
		if stallAfter > 0 && time.Since(lastEvent) >= stallAfter {
			return monitorResultStall, nil
		}
		time.Sleep(min(watchPollInterval, clockSlack(started, lastEvent, timeout, stallAfter)))
	}
}

// clockSlack is the time left before the nearer clock expires, so a short
// --timeout is not overshot by a whole poll interval.
func clockSlack(started, lastEvent time.Time, timeout, stallAfter time.Duration) time.Duration {
	slack := watchPollInterval
	if timeout > 0 {
		slack = min(slack, time.Until(started.Add(timeout)))
	}
	if stallAfter > 0 {
		slack = min(slack, time.Until(lastEvent.Add(stallAfter)))
	}
	return max(slack, 10*time.Millisecond)
}
