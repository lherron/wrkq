package cli

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/id"
	"github.com/spf13/cobra"
)

var commentRmCmd = &cobra.Command{
	Use:   "rm <comment-id|c:token>...",
	Short: "Remove comment(s)",
	Long: `Remove one or more comments.
By default, performs a soft-delete (sets deleted_at and deleted_by_actor_uuid).
Use --purge for hard delete (removes from database entirely).

Use c:<token> for typed comment selector (c:C-00012, c:uuid, etc).`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runCommentRm),
}

var (
	commentRmYes     bool
	commentRmDryRun  bool
	commentRmPurge   bool
	commentRmIfMatch int64
)

func init() {
	commentCmd.AddCommand(commentRmCmd)

	commentRmCmd.Flags().BoolVar(&commentRmYes, "yes", false, "Skip confirmation")
	commentRmCmd.Flags().BoolVar(&commentRmDryRun, "dry-run", false, "Preview without deleting")
	commentRmCmd.Flags().BoolVar(&commentRmPurge, "purge", false, "Hard delete (remove from database)")
	commentRmCmd.Flags().Int64Var(&commentRmIfMatch, "if-match", 0, "Only delete if comment etag matches (0 = skip check)")
}

func runCommentRm(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	for _, commentRef := range args {
		// Remove c: prefix if present
		ref := commentRef
		if strings.HasPrefix(ref, "c:") {
			ref = ref[2:]
		}

		// Resolve comment
		var commentUUID, commentID, taskUUID, taskID, body string
		var etag int64

		isUUID := id.IsUUID(ref)
		isFriendlyID := id.IsFriendlyID(ref)

		query := `
			SELECT c.uuid, c.id, c.task_uuid, c.etag, c.body, t.id as task_id
			FROM comments c
			LEFT JOIN tasks t ON c.task_uuid = t.uuid
		`

		var queryErr error
		if isUUID {
			query += " WHERE c.uuid = ?"
			queryErr = database.QueryRow(query, ref).Scan(&commentUUID, &commentID, &taskUUID, &etag, &body, &taskID)
		} else if isFriendlyID {
			query += " WHERE c.id = ?"
			queryErr = database.QueryRow(query, ref).Scan(&commentUUID, &commentID, &taskUUID, &etag, &body, &taskID)
		} else {
			return fmt.Errorf("invalid comment reference: %s (expected friendly ID like C-00001 or UUID)", commentRef)
		}

		if queryErr == sql.ErrNoRows {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: comment not found: %s\n", commentRef)
			continue
		}
		if queryErr != nil {
			return fmt.Errorf("failed to resolve comment %s: %w", commentRef, queryErr)
		}

		// Check etag if requested
		if commentRmIfMatch > 0 && etag != commentRmIfMatch {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: etag mismatch for %s (current: %d, expected: %d), skipping\n",
				commentID, etag, commentRmIfMatch)
			continue
		}

		// Show preview for dry-run or confirmation
		bodyPreview := strings.ReplaceAll(body, "\n", " ")
		if len(bodyPreview) > 60 {
			bodyPreview = bodyPreview[:57] + "..."
		}

		if commentRmDryRun {
			action := "soft-delete"
			if commentRmPurge {
				action = "purge"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "[DRY RUN] Would %s comment %s (task %s): %s\n",
				action, commentID, taskID, bodyPreview)
			continue
		}

		// Confirm deletion
		if !commentRmYes {
			action := "soft-delete"
			if commentRmPurge {
				action = "purge"
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s comment %s (task %s)? [y/N]: ",
				strings.Title(action), commentID, taskID)
			var response string
			fmt.Scanln(&response)
			if response != "y" && response != "Y" {
				continue
			}
		}

		// Begin transaction
		tx, err := database.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		eventWriter := events.NewWriter(database.DB)

		if commentRmPurge {
			// Hard delete
			_, err = tx.Exec("DELETE FROM comments WHERE uuid = ?", commentUUID)
			if err != nil {
				return fmt.Errorf("failed to purge comment %s: %w", commentID, err)
			}

			// Log purge event
			if err := eventWriter.LogCommentPurged(tx, actorUUID, commentUUID, commentID, taskUUID); err != nil {
				return fmt.Errorf("failed to log purge event: %w", err)
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit transaction: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Purged: %s\n", commentID)
		} else {
			// Soft delete
			_, err = tx.Exec(`
				UPDATE comments
				SET deleted_at = datetime('now'),
				    deleted_by_actor_uuid = ?,
				    etag = etag + 1
				WHERE uuid = ?
			`, actorUUID, commentUUID)
			if err != nil {
				return fmt.Errorf("failed to soft-delete comment %s: %w", commentID, err)
			}

			// Fetch updated comment for event logging
			var deletedAt sql.NullString
			var newEtag int64
			err = tx.QueryRow(`
				SELECT deleted_at, etag FROM comments WHERE uuid = ?
			`, commentUUID).Scan(&deletedAt, &newEtag)
			if err != nil {
				return fmt.Errorf("failed to fetch updated comment: %w", err)
			}

			// Create comment struct for event logging
			comment := struct {
				UUID     string
				ID       string
				TaskUUID string
				ETag     int64
			}{
				UUID:     commentUUID,
				ID:       commentID,
				TaskUUID: taskUUID,
				ETag:     newEtag,
			}

			// Log delete event
			commentDomain := &struct {
				UUID     string
				ID       string
				TaskUUID string
				ETag     int64
			}{
				UUID:     comment.UUID,
				ID:       comment.ID,
				TaskUUID: comment.TaskUUID,
				ETag:     comment.ETag,
			}

			// We need to call LogCommentDeleted with a proper domain.Comment
			// Let me create a minimal helper or just inline the event logging
			payload := fmt.Sprintf(`{"task_id":"%s","comment_id":"%s","deleted_by_actor_id":"%s","soft_delete":true}`,
				taskUUID, commentID, actorUUID)
			eventPayload := payload
			_, err = tx.Exec(`
				INSERT INTO event_log (actor_uuid, resource_type, resource_uuid, event_type, etag, payload)
				VALUES (?, 'comment', ?, 'comment.deleted', ?, ?)
			`, actorUUID, commentUUID, commentDomain.ETag, eventPayload)
			if err != nil {
				return fmt.Errorf("failed to log delete event: %w", err)
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit transaction: %w", err)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "Deleted: %s\n", commentID)
		}
	}

	return nil
}
