package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/webhooks"
	"github.com/spf13/cobra"
)

var commentAddCmd = &cobra.Command{
	Use:   "add <task> [comment-text]",
	Short: "Add a comment to a task",
	Long: `Add a new comment to a task.
Comment text can come from:
  - The -m/--message flag
  - A positional argument (comment text)
  - A file path (use -f/--file)
  - stdin (use '-')

Comments are immutable and attributed to the current principal.`,
	Args: cobra.RangeArgs(1, 2),
	RunE: appctx.WithApp(appctx.WithActor(), runCommentAdd),
}

var (
	commentAddMessage string
	commentAddFile    string
	commentAddMeta    string
	commentAddIfMatch int64
	commentAddDryRun  bool
	commentAddAsActor string
)

func init() {
	commentCmd.AddCommand(commentAddCmd)

	commentAddCmd.Flags().StringVarP(&commentAddMessage, "message", "m", "", "Comment text")
	commentAddCmd.Flags().StringVarP(&commentAddFile, "file", "f", "", "Read comment from file")
	commentAddCmd.Flags().StringVar(&commentAddMeta, "meta", "", "JSON metadata for agents/tools")
	commentAddCmd.Flags().Int64Var(&commentAddIfMatch, "if-match", 0, "Only add if task etag matches (0 = skip check)")
	commentAddCmd.Flags().BoolVar(&commentAddDryRun, "dry-run", false, "Preview without writing")
	commentAddCmd.Flags().StringVar(&commentAddAsActor, "as", "", "Actor slug or ID")
}

func runCommentAdd(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	attr := app.Attribution()
	claims := &stdinClaims{}

	// Reset flag values after execution to prevent test contamination
	defer func() {
		commentAddMessage = ""
		commentAddFile = ""
		commentAddMeta = ""
		commentAddIfMatch = 0
		commentAddDryRun = false
		commentAddAsActor = ""
	}()

	// Remove t: prefix if present
	taskRef := strings.TrimPrefix(args[0], "t:")
	taskRef = applyProjectRootToSelector(app.Config, taskRef, false)

	// Resolve task
	taskUUID, taskID, err := selectors.ResolveTask(database, taskRef)
	if err != nil {
		return err
	}

	// Get comment body - check for conflicting sources
	// Read flag values directly from command to avoid stale package-level values
	message, _ := cmd.Flags().GetString("message")
	file, _ := cmd.Flags().GetString("file")

	sourceCount := 0
	if message != "" {
		sourceCount++
	}
	if file != "" {
		sourceCount++
	}
	if len(args) == 2 {
		sourceCount++
	}
	if sourceCount > 1 {
		return fmt.Errorf("only one comment source allowed: use -m, -f, positional argument, or stdin ('-')")
	}

	// Update package variables with fresh values
	commentAddMessage = message
	commentAddFile = file

	var body string
	if commentAddMessage != "" {
		// Use -m flag
		body, err = readTextValue(commentAddMessage, "-m/--message", cmd.InOrStdin(), claims)
		if err != nil {
			return err
		}
	} else if commentAddFile != "" {
		// Read from file specified by -f flag
		data, err := readFileValue(commentAddFile, "--file", cmd.InOrStdin(), claims)
		if err != nil {
			return err
		}
		body = string(data)
	} else if len(args) == 2 {
		// Second positional argument
		source := args[1]
		body, err = readTextValue(source, "comment text", cmd.InOrStdin(), claims)
		if err != nil {
			return err
		}
	} else {
		return fmt.Errorf("comment body required: use -m, -f, provide comment text, or use stdin with '-'")
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

	if commentAddDryRun {
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeJSONOutput(cmd.OutOrStdout(), outputSelection{}, map[string]interface{}{
				"dry_run":       true,
				"task_id":       taskID,
				"task_uuid":     taskUUID,
				"principal_ref": attr.PrincipalRef,
				"body":          body,
				"meta":          metaStr,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "[DRY RUN] Would add comment to task %s:\n", taskID)
		fmt.Fprintf(cmd.OutOrStdout(), "  Principal: %s\n", attr.PrincipalRef)
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
	defer func() { _ = tx.Rollback() }()

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

	// Get next comment ID by calculating from MAX(id)+1
	// This is self-healing: even if comment_sequences gets out of sync (e.g., from
	// restore or snapshot import), we'll generate the correct next ID.
	var nextSeq int
	err = tx.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) + 1 FROM comments").Scan(&nextSeq)
	if err != nil {
		return fmt.Errorf("failed to calculate next comment ID: %w", err)
	}

	// Update sequence table to stay in sync (for consistency, though we don't rely on it)
	_, err = tx.Exec("UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'", nextSeq)
	if err != nil {
		return fmt.Errorf("failed to update comment sequence: %w", err)
	}

	// Generate IDs
	commentUUID := uuid.New().String()
	commentID := id.FormatComment(nextSeq)

	// Insert comment
	_, err = tx.Exec(`
		INSERT INTO comments (
			uuid, id, task_uuid, created_by_principal_ref,
			created_by_scope_ref, body, meta, etag
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)
	`, commentUUID, commentID, taskUUID,
		attr.PrincipalRef, scopeBind(attr), body, metaStr)
	if err != nil {
		return fmt.Errorf("failed to insert comment: %w", err)
	}

	// Fetch the created comment for event logging
	var comment domain.Comment
	var createdAtStr string
	var createdByPrincipal, createdByScope sql.NullString
	err = tx.QueryRow(`
		SELECT uuid, id, task_uuid, created_by_principal_ref, created_by_scope_ref,
		       body, meta, etag, created_at
		FROM comments WHERE uuid = ?
	`, commentUUID).Scan(
		&comment.UUID, &comment.ID, &comment.TaskUUID,
		&createdByPrincipal, &createdByScope,
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
	if createdByPrincipal.Valid {
		comment.CreatedByPrincipalRef = createdByPrincipal.String
	}
	if createdByScope.Valid {
		comment.CreatedByScopeRef = createdByScope.String
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payload, err := json.Marshal(map[string]interface{}{
		"task_id":       comment.TaskUUID,
		"comment_id":    comment.ID,
		"principal_ref": attr.PrincipalRef,
	})
	if err != nil {
		return fmt.Errorf("failed to marshal event payload: %w", err)
	}
	payloadStr := string(payload)
	eventMeta, err := eventWriter.LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "comment",
		ResourceUUID: &comment.UUID,
		EventType:    "comment.created",
		ETag:         &comment.ETag,
		Payload:      &payloadStr,
	})
	if err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	webhooks.DispatchTaskEvent(database, taskUUID, webhooks.EventContext{
		Metadata:     eventMeta,
		Event:        "comment_added",
		PrincipalRef: attr.PrincipalRef,
		Via:          "cli",
		Transition:   nil,
		Changed:      []string{"comments"},
		Changes: map[string]webhooks.Change{
			"comments": {From: nil, To: commentID},
		},
	})

	// Output success
	output := map[string]interface{}{
		"id":            commentID,
		"uuid":          commentUUID,
		"task_id":       taskID,
		"principal_ref": attr.PrincipalRef,
		"created_at":    comment.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
		"etag":          comment.ETag,
	}

	// Check for --json flag from parent command or direct
	jsonFlag := false
	if cmd.Flag("json") != nil {
		jsonFlag, _ = cmd.Flags().GetBool("json")
	}

	if jsonFlag || !isStdoutTTY(cmd.OutOrStdout()) {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(output); err != nil {
			return err
		}
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
		"2006-01-02 15:04:05", // SQLite datetime() format
	}

	for _, format := range formats {
		t, err := time.Parse(format, s)
		if err == nil {
			return t, nil
		}
	}

	return time.Time{}, fmt.Errorf("unable to parse timestamp: %s", s)
}
