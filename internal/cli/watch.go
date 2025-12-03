package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

var watchCmd = &cobra.Command{
	Use:   "watch [PATH...]",
	Short: "Stream change events from the event log",
	Long: `Stream change events from the event log in real-time.

Examples:
  wrkq watch                     # Watch all events
  wrkq watch --since 100         # Watch from event ID 100
  wrkq watch --ndjson            # Output as NDJSON
  wrkq watch portal/**           # Watch events under portal (future)
`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runWatch),
}

var (
	watchSince  int64
	watchNDJSON bool
	watchFollow bool
)

func init() {
	rootCmd.AddCommand(watchCmd)

	watchCmd.Flags().Int64Var(&watchSince, "since", 0, "Start from event ID (0 = all events)")
	watchCmd.Flags().BoolVar(&watchNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	watchCmd.Flags().BoolVarP(&watchFollow, "follow", "f", true, "Follow new events (default true)")
}

func runWatch(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Watch events
	return watchEvents(database, watchSince, watchNDJSON, watchFollow)
}

type watchEvent struct {
	ID           int64   `json:"id"`
	Timestamp    string  `json:"timestamp"`
	ActorUUID    *string `json:"actor_uuid,omitempty"`
	ActorSlug    *string `json:"actor_slug,omitempty"`
	ActorID      *string `json:"actor_id,omitempty"`
	ResourceType string  `json:"resource_type"`
	ResourceUUID *string `json:"resource_uuid,omitempty"`
	ResourceID   *string `json:"resource_id,omitempty"`
	EventType    string  `json:"event_type"`
	ETag         *int64  `json:"etag,omitempty"`
	Payload      *string `json:"payload,omitempty"`
}

func watchEvents(database *db.DB, sinceID int64, ndjson bool, follow bool) error {
	currentID := sinceID
	encoder := json.NewEncoder(os.Stdout)

	for {
		// Query new events
		query := `
			SELECT e.id, e.timestamp, e.actor_uuid, e.resource_type, e.resource_uuid, e.event_type, e.etag, e.payload,
			       a.slug as actor_slug, a.id as actor_id,
			       CASE e.resource_type
			           WHEN 'task' THEN (SELECT id FROM tasks WHERE uuid = e.resource_uuid)
			           WHEN 'container' THEN (SELECT id FROM containers WHERE uuid = e.resource_uuid)
			           WHEN 'actor' THEN (SELECT id FROM actors WHERE uuid = e.resource_uuid)
			           ELSE NULL
			       END as resource_id
			FROM event_log e
			LEFT JOIN actors a ON a.uuid = e.actor_uuid
			WHERE e.id > ?
			ORDER BY e.id ASC
		`

		rows, err := database.Query(query, currentID)
		if err != nil {
			return fmt.Errorf("query failed: %w", err)
		}

		hasEvents := false
		for rows.Next() {
			var e watchEvent
			var actorSlug, actorID, resourceID sql.NullString

			err := rows.Scan(
				&e.ID,
				&e.Timestamp,
				&e.ActorUUID,
				&e.ResourceType,
				&e.ResourceUUID,
				&e.EventType,
				&e.ETag,
				&e.Payload,
				&actorSlug,
				&actorID,
				&resourceID,
			)
			if err != nil {
				rows.Close()
				return fmt.Errorf("scan failed: %w", err)
			}

			if actorSlug.Valid {
				e.ActorSlug = &actorSlug.String
			}
			if actorID.Valid {
				e.ActorID = &actorID.String
			}
			if resourceID.Valid {
				e.ResourceID = &resourceID.String
			}

			// Output event
			if ndjson {
				if err := encoder.Encode(e); err != nil {
					rows.Close()
					return fmt.Errorf("encode failed: %w", err)
				}
			} else {
				printWatchEvent(e)
			}

			currentID = e.ID
			hasEvents = true
		}
		rows.Close()

		if err := rows.Err(); err != nil {
			return fmt.Errorf("rows error: %w", err)
		}

		// If not following or no more events initially, exit
		if !follow && !hasEvents {
			break
		}

		if !follow {
			break
		}

		// Wait before polling again
		time.Sleep(1 * time.Second)
	}

	return nil
}

func printWatchEvent(e watchEvent) {
	// Human-readable format
	timestamp := e.Timestamp

	actor := "system"
	if e.ActorSlug != nil {
		actorDisplay := *e.ActorSlug
		if e.ActorID != nil {
			actorDisplay += fmt.Sprintf(" (%s)", *e.ActorID)
		}
		actor = actorDisplay
	}

	resource := e.ResourceType
	if e.ResourceID != nil {
		resource += fmt.Sprintf(" %s", *e.ResourceID)
	} else if e.ResourceUUID != nil {
		resource += fmt.Sprintf(" %s", (*e.ResourceUUID)[:8])
	}

	fmt.Printf("[%s] %s: %s by %s\n", timestamp, resource, e.EventType, actor)

	// Print payload if present
	if e.Payload != nil && *e.Payload != "" {
		fmt.Printf("  %s\n", formatPayloadOneLine(*e.Payload))
	}
}

func formatPayloadOneLine(payload string) string {
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(payload), &data); err != nil {
		return payload
	}

	var parts []string
	for key, value := range data {
		parts = append(parts, fmt.Sprintf("%s=%v", key, value))
	}

	return fmt.Sprintf("{%s}", joinStrings(parts, ", "))
}

func joinStrings(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}

	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}

	return result
}
