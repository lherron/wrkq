package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/id"
	"github.com/spf13/cobra"
)

var commentAddCmd = &cobra.Command{
	Use:   "add <task> [file|-]",
	Short: "Add a comment to a task",
	Long: `Add a new comment to a task.
Comment text can come from:
  - The -m/--message flag
  - A file path
  - stdin (use '-')

Comments are immutable and attributed to the current actor.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: runCommentAdd,
}

var (
	commentAddMessage  string
	commentAddMeta     string
	commentAddIfMatch  int64
	commentAddDryRun   bool
	commentAddAsActor  string
)

func init() {
	commentCmd.AddCommand(commentAddCmd)

	commentAddCmd.Flags().StringVarP(&commentAddMessage, "message", "m", "", "Comment text")
	commentAddCmd.Flags().StringVar(&commentAddMeta, "meta", "", "JSON metadata for agents/tools")
	commentAddCmd.Flags().Int64Var(&commentAddIfMatch, "if-match", 0, "Only add if task etag matches (0 = skip check)")
	commentAddCmd.Flags().BoolVar(&commentAddDryRun, "dry-run", false, "Preview without writing")
	commentAddCmd.Flags().StringVar(&commentAddAsActor, "as", "", "Actor slug or ID")
}

func runCommentAdd(cmd *cobra.Command, args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Remove t: prefix if present
	taskRef := args[0]
	if strings.HasPrefix(taskRef, "t:") {
		taskRef = taskRef[2:]
	}

	// Resolve task
	taskUUID, taskID, err := resolveTask(database, taskRef)
	if err != nil {
		return err
	}

	// Get comment body
	var body string
	if commentAddMessage != "" {
		body = commentAddMessage
	} else if len(args) == 2 {
		// Read from file or stdin
		source := args[1]
		if source == "-" {
			// Read from stdin
			data, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("failed to read from stdin: %w", err)
			}
			body = string(data)
		} else {
			// Read from file
			data, err := os.ReadFile(source)
			if err != nil {
				return fmt.Errorf("failed to read file %s: %w", source, err)
			}
			body = string(data)
		}
	} else {
		return fmt.Errorf("comment body required: use -m, provide a file, or use stdin with '-'")
	}

	// Validate body
	body = strings.TrimSpace(body)
	if body == "" {
		return fmt.Errorf("comment body cannot be empty")
	}

	// Validate meta if provided
	var metaStr *string
	if commentAddMeta != "" {
		// Validate JSON
		var metaObj map[string]interface{}
		if err := json.Unmarshal([]byte(commentAddMeta), &metaObj); err != nil {
			return fmt.Errorf("invalid JSON for --meta: %w", err)
		}
		metaStr = &commentAddMeta
	}

	// Resolve current actor
	actorUUID, actorSlug, err := resolveCurrentActor(database, cfg, cmd)
	if err != nil {
		return fmt.Errorf("failed to resolve actor: %w", err)
	}

	if commentAddDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "[DRY RUN] Would add comment to task %s:\n", taskID)
		fmt.Fprintf(cmd.OutOrStdout(), "  Actor: %s\n", actorSlug)
		fmt.Fprintf(cmd.OutOrStdout(), "  Body: %s\n", body)
		if metaStr != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "  Meta: %s\n", *metaStr)
		}
		return nil
	}

	// Begin transaction
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Check task etag if requested
	if commentAddIfMatch > 0 {
		var currentEtag int64
		err := tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentEtag)
		if err != nil {
			return fmt.Errorf("failed to read task etag: %w", err)
		}
		if currentEtag != commentAddIfMatch {
			return fmt.Errorf("etag mismatch: task has etag %d, expected %d", currentEtag, commentAddIfMatch)
		}
	}

	// Get next comment sequence number
	var nextSeq int
	err = tx.QueryRow("SELECT value FROM comment_sequences WHERE name = 'next_comment'").Scan(&nextSeq)
	if err != nil {
		return fmt.Errorf("failed to get comment sequence: %w", err)
	}
	nextSeq++

	// Update sequence
	_, err = tx.Exec("UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'", nextSeq)
	if err != nil {
		return fmt.Errorf("failed to update comment sequence: %w", err)
	}

	// Generate IDs
	commentUUID := uuid.New().String()
	commentID := id.FormatComment(nextSeq)

	// Insert comment
	_, err = tx.Exec(`
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, meta, etag)
		VALUES (?, ?, ?, ?, ?, ?, 1)
	`, commentUUID, commentID, taskUUID, actorUUID, body, metaStr)
	if err != nil {
		return fmt.Errorf("failed to insert comment: %w", err)
	}

	// Fetch the created comment for event logging
	var comment domain.Comment
	var createdAtStr string
	err = tx.QueryRow(`
		SELECT uuid, id, task_uuid, actor_uuid, body, meta, etag, created_at
		FROM comments WHERE uuid = ?
	`, commentUUID).Scan(
		&comment.UUID, &comment.ID, &comment.TaskUUID, &comment.ActorUUID,
		&comment.Body, &comment.Meta, &comment.ETag, &createdAtStr,
	)
	if err != nil {
		return fmt.Errorf("failed to fetch created comment: %w", err)
	}

	// Parse created_at timestamp
	comment.CreatedAt, err = parseTimestamp(createdAtStr)
	if err != nil {
		return fmt.Errorf("failed to parse created_at: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	if err := eventWriter.LogCommentCreated(tx, actorUUID, &comment); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Output success
	output := map[string]interface{}{
		"id":         commentID,
		"uuid":       commentUUID,
		"task_id":    taskID,
		"actor_slug": actorSlug,
		"created_at": comment.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"etag":       comment.ETag,
	}

	// Check for --json flag from parent command or direct
	jsonFlag := false
	if cmd.Flag("json") != nil {
		jsonFlag, _ = cmd.Flags().GetBool("json")
	}

	if jsonFlag {
		data, err := json.MarshalIndent(output, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Comment created: %s\n", commentID)
	}

	return nil
}

// parseTimestamp parses a timestamp string in various formats
func parseTimestamp(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",  // SQLite datetime() format
	}

	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", s)
}
