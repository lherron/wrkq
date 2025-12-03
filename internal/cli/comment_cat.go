package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/id"
	"github.com/spf13/cobra"
)

var commentCatCmd = &cobra.Command{
	Use:   "cat <comment-id|c:token>...",
	Short: "Show comment(s)",
	Long: `Show one or more comments.
By default, prints a header line (ID, timestamp, actor, task) and body for each comment.
Use c:<token> for typed comment selector (c:C-00012, c:uuid, etc).`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runCommentCat),
}

var (
	commentCatJSON   bool
	commentCatNDJSON bool
	commentCatRaw    bool
)

func init() {
	commentCmd.AddCommand(commentCatCmd)

	commentCatCmd.Flags().BoolVar(&commentCatJSON, "json", false, "Output as JSON")
	commentCatCmd.Flags().BoolVar(&commentCatNDJSON, "ndjson", false, "Output as NDJSON")
	commentCatCmd.Flags().BoolVar(&commentCatRaw, "raw", false, "Body only, separated by ---")
}

func runCommentCat(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	var comments []map[string]interface{}

	for _, commentRef := range args {
		// Remove c: prefix if present
		ref := commentRef
		if strings.HasPrefix(ref, "c:") {
			ref = ref[2:]
		}

		// Resolve comment (friendly ID or UUID)
		var commentUUID string
		var commentID string
		var taskUUID string
		var actorUUID string
		var body string
		var meta sql.NullString
		var etag int64
		var createdAt string
		var updatedAt, deletedAt sql.NullString
		var actorSlug, actorRole, taskID string

		// Determine if ref is UUID, friendly ID, or path
		isUUID := id.IsUUID(ref)
		isFriendlyID := id.IsFriendlyID(ref)

		query := `
			SELECT c.uuid, c.id, c.task_uuid, c.actor_uuid, c.body, c.meta, c.etag,
			       c.created_at, c.updated_at, c.deleted_at,
			       a.slug as actor_slug, a.role as actor_role,
			       t.id as task_id
			FROM comments c
			LEFT JOIN actors a ON c.actor_uuid = a.uuid
			LEFT JOIN tasks t ON c.task_uuid = t.uuid
		`

		var queryErr error
		if isUUID {
			query += " WHERE c.uuid = ?"
			queryErr = database.QueryRow(query, ref).Scan(
				&commentUUID, &commentID, &taskUUID, &actorUUID, &body, &meta, &etag,
				&createdAt, &updatedAt, &deletedAt, &actorSlug, &actorRole, &taskID,
			)
		} else if isFriendlyID {
			query += " WHERE c.id = ?"
			queryErr = database.QueryRow(query, ref).Scan(
				&commentUUID, &commentID, &taskUUID, &actorUUID, &body, &meta, &etag,
				&createdAt, &updatedAt, &deletedAt, &actorSlug, &actorRole, &taskID,
			)
		} else {
			return fmt.Errorf("invalid comment reference: %s (expected friendly ID like C-00001 or UUID)", commentRef)
		}

		if queryErr == sql.ErrNoRows {
			return fmt.Errorf("comment not found: %s", commentRef)
		}
		if queryErr != nil {
			return fmt.Errorf("failed to resolve comment %s: %w", commentRef, queryErr)
		}

		comment := map[string]interface{}{
			"uuid":        commentUUID,
			"id":          commentID,
			"task_uuid":   taskUUID,
			"task_id":     taskID,
			"actor_uuid":  actorUUID,
			"actor_slug":  actorSlug,
			"actor_role":  actorRole,
			"body":        body,
			"etag":        etag,
			"created_at":  createdAt,
		}

		if meta.Valid && meta.String != "" {
			comment["meta"] = meta.String
		}
		if updatedAt.Valid {
			comment["updated_at"] = updatedAt.String
		}
		if deletedAt.Valid {
			comment["deleted_at"] = deletedAt.String
		}

		comments = append(comments, comment)
	}

	// Output
	if commentCatJSON {
		data, err := json.MarshalIndent(comments, "", "  ")
		if err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
	}

	if commentCatNDJSON {
		for _, comment := range comments {
			data, err := json.Marshal(comment)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(data))
		}
		return nil
	}

	// Human-readable output
	for i, comment := range comments {
		if i > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "---")
		}

		if commentCatRaw {
			// Body only
			fmt.Fprintln(cmd.OutOrStdout(), comment["body"].(string))
		} else {
			// Header + body
			fmt.Fprintf(cmd.OutOrStdout(), "[%s] [%s] %s (%s) - Task: %s\n",
				comment["id"].(string),
				comment["created_at"].(string),
				comment["actor_slug"].(string),
				comment["actor_role"].(string),
				comment["task_id"].(string),
			)
			fmt.Fprintln(cmd.OutOrStdout())
			fmt.Fprintln(cmd.OutOrStdout(), comment["body"].(string))
		}
	}

	return nil
}
