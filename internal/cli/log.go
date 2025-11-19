package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/id"
	"github.com/spf13/cobra"
)

var logCmd = &cobra.Command{
	Use:   "log <PATHSPEC|ID>",
	Short: "Show change history for a task or container",
	Long: `Show change history from the event log.

Examples:
  todo log T-00001                     # Show history for task
  todo log portal/auth/login-ux        # Show history by path
  todo log P-00001 --since 2025-11-01  # Show recent changes
  todo log T-00001 --oneline           # Compact format
`,
	Args: cobra.ExactArgs(1),
	RunE: runLog,
}

var (
	logSince    string
	logUntil    string
	logOneline  bool
	logPatch    bool
	logJSON     bool
	logLimit    int
)

func init() {
	rootCmd.AddCommand(logCmd)

	logCmd.Flags().StringVar(&logSince, "since", "", "Show changes since date/time (YYYY-MM-DD or RFC3339)")
	logCmd.Flags().StringVar(&logUntil, "until", "", "Show changes until date/time (YYYY-MM-DD or RFC3339)")
	logCmd.Flags().BoolVar(&logOneline, "oneline", false, "Compact one-line format")
	logCmd.Flags().BoolVar(&logPatch, "patch", false, "Show detailed payload changes")
	logCmd.Flags().BoolVar(&logJSON, "json", false, "Output as JSON")
	logCmd.Flags().IntVar(&logLimit, "limit", 50, "Limit number of events (0 = unlimited)")
}

func runLog(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Resolve target resource
	resourceUUID, resourceType, err := resolveResource(database, args[0])
	if err != nil {
		return fmt.Errorf("failed to resolve resource: %w", err)
	}

	// Query event log
	events, err := queryEventLog(database, resourceUUID, resourceType, logOptions{
		since: logSince,
		until: logUntil,
		limit: logLimit,
	})
	if err != nil {
		return fmt.Errorf("failed to query event log: %w", err)
	}

	// Render output
	if logJSON {
		return renderEventsJSON(events)
	}

	if logOneline {
		return renderEventsOneline(events)
	}

	return renderEventsDetailed(events, logPatch)
}

type logOptions struct {
	since string
	until string
	limit int
}

type logEvent struct {
	ID           int64     `json:"id"`
	Timestamp    time.Time `json:"timestamp"`
	ActorUUID    *string   `json:"actor_uuid,omitempty"`
	ActorSlug    *string   `json:"actor_slug,omitempty"`
	ActorID      *string   `json:"actor_id,omitempty"`
	ResourceType string    `json:"resource_type"`
	ResourceUUID string    `json:"resource_uuid"`
	EventType    string    `json:"event_type"`
	ETag         *int64    `json:"etag,omitempty"`
	Payload      *string   `json:"payload,omitempty"`
}

func resolveResource(database *db.DB, target string) (string, string, error) {
	// Try as friendly ID first
	if id.IsFriendlyID(target) {
		prefix := target[:1]
		switch prefix {
		case "T":
			// Task
			var uuid string
			err := database.QueryRow("SELECT uuid FROM tasks WHERE id = ?", target).Scan(&uuid)
			if err != nil {
				return "", "", fmt.Errorf("task not found: %s", target)
			}
			return uuid, "task", nil
		case "P":
			// Container
			var uuid string
			err := database.QueryRow("SELECT uuid FROM containers WHERE id = ?", target).Scan(&uuid)
			if err != nil {
				return "", "", fmt.Errorf("container not found: %s", target)
			}
			return uuid, "container", nil
		case "A":
			// Actor
			var uuid string
			err := database.QueryRow("SELECT uuid FROM actors WHERE id = ?", target).Scan(&uuid)
			if err != nil {
				return "", "", fmt.Errorf("actor not found: %s", target)
			}
			return uuid, "actor", nil
		default:
			return "", "", fmt.Errorf("unknown friendly ID prefix: %s", prefix)
		}
	}

	// Try as UUID
	if len(target) == 36 && strings.Count(target, "-") == 4 {
		// Check which table contains this UUID
		var count int
		err := database.QueryRow("SELECT COUNT(*) FROM tasks WHERE uuid = ?", target).Scan(&count)
		if err == nil && count > 0 {
			return target, "task", nil
		}

		err = database.QueryRow("SELECT COUNT(*) FROM containers WHERE uuid = ?", target).Scan(&count)
		if err == nil && count > 0 {
			return target, "container", nil
		}

		err = database.QueryRow("SELECT COUNT(*) FROM actors WHERE uuid = ?", target).Scan(&count)
		if err == nil && count > 0 {
			return target, "actor", nil
		}

		return "", "", fmt.Errorf("UUID not found: %s", target)
	}

	// Try as path (task or container)
	// TODO: Implement path resolution
	return "", "", fmt.Errorf("path resolution not yet implemented: %s", target)
}

func queryEventLog(database *db.DB, resourceUUID string, resourceType string, opts logOptions) ([]logEvent, error) {
	query := `
		SELECT e.id, e.timestamp, e.actor_uuid, e.resource_type, e.resource_uuid, e.event_type, e.etag, e.payload,
		       a.slug as actor_slug, a.id as actor_id
		FROM event_log e
		LEFT JOIN actors a ON a.uuid = e.actor_uuid
		WHERE e.resource_uuid = ? AND e.resource_type = ?
	`
	args := []interface{}{resourceUUID, resourceType}

	// Add time filters
	if opts.since != "" {
		sinceTime, err := parseTimeFilter(opts.since)
		if err != nil {
			return nil, fmt.Errorf("invalid --since value: %w", err)
		}
		query += " AND e.timestamp >= ?"
		args = append(args, sinceTime.Format(time.RFC3339))
	}

	if opts.until != "" {
		untilTime, err := parseTimeFilter(opts.until)
		if err != nil {
			return nil, fmt.Errorf("invalid --until value: %w", err)
		}
		query += " AND e.timestamp <= ?"
		args = append(args, untilTime.Format(time.RFC3339))
	}

	query += " ORDER BY e.id DESC"

	if opts.limit > 0 {
		query += " LIMIT ?"
		args = append(args, opts.limit)
	}

	rows, err := database.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	var events []logEvent
	for rows.Next() {
		var e logEvent
		var timestampStr string
		var actorSlug, actorID sql.NullString

		err := rows.Scan(
			&e.ID,
			&timestampStr,
			&e.ActorUUID,
			&e.ResourceType,
			&e.ResourceUUID,
			&e.EventType,
			&e.ETag,
			&e.Payload,
			&actorSlug,
			&actorID,
		)
		if err != nil {
			return nil, fmt.Errorf("scan failed: %w", err)
		}

		// Parse timestamp
		e.Timestamp, err = time.Parse(time.RFC3339, timestampStr)
		if err != nil {
			e.Timestamp, _ = time.Parse("2006-01-02T15:04:05Z", timestampStr)
		}

		if actorSlug.Valid {
			e.ActorSlug = &actorSlug.String
		}
		if actorID.Valid {
			e.ActorID = &actorID.String
		}

		events = append(events, e)
	}

	return events, rows.Err()
}

func parseTimeFilter(value string) (time.Time, error) {
	// Try RFC3339 first
	t, err := time.Parse(time.RFC3339, value)
	if err == nil {
		return t, nil
	}

	// Try date only (YYYY-MM-DD)
	t, err = time.Parse("2006-01-02", value)
	if err == nil {
		return t, nil
	}

	return time.Time{}, fmt.Errorf("invalid time format: %s (use YYYY-MM-DD or RFC3339)", value)
}

func renderEventsJSON(events []logEvent) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(events)
}

func renderEventsOneline(events []logEvent) error {
	for _, e := range events {
		actor := "system"
		if e.ActorSlug != nil {
			actor = *e.ActorSlug
		}

		timestamp := e.Timestamp.Format("2006-01-02 15:04")
		fmt.Printf("%s  %s  %s  by %s\n", timestamp, e.EventType, formatEventSummary(e), actor)
	}
	return nil
}

func renderEventsDetailed(events []logEvent, showPatch bool) error {
	for i, e := range events {
		if i > 0 {
			fmt.Println()
		}

		// Header
		fmt.Printf("\033[33mEvent %d\033[0m - %s\n", e.ID, e.EventType)
		fmt.Printf("  Timestamp:  %s\n", e.Timestamp.Format(time.RFC3339))

		if e.ActorSlug != nil && e.ActorID != nil {
			fmt.Printf("  Actor:      %s (%s)\n", *e.ActorSlug, *e.ActorID)
		} else {
			fmt.Printf("  Actor:      system\n")
		}

		if e.ETag != nil {
			fmt.Printf("  ETag:       %d\n", *e.ETag)
		}

		// Payload summary
		fmt.Printf("  Summary:    %s\n", formatEventSummary(e))

		// Detailed payload if requested
		if showPatch && e.Payload != nil {
			fmt.Println("  Changes:")
			var payload map[string]interface{}
			if err := json.Unmarshal([]byte(*e.Payload), &payload); err == nil {
				for key, value := range payload {
					fmt.Printf("    %s: %v\n", key, value)
				}
			} else {
				fmt.Printf("    %s\n", *e.Payload)
			}
		}
	}

	return nil
}

func formatEventSummary(e logEvent) string {
	if e.Payload == nil {
		return "(no details)"
	}

	var payload map[string]interface{}
	if err := json.Unmarshal([]byte(*e.Payload), &payload); err != nil {
		return "(invalid payload)"
	}

	// Try to extract meaningful summary
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
