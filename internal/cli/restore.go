package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/webhooks"
	"github.com/spf13/cobra"
)

var restoreCmd = &cobra.Command{
	Use:   "restore <path|id|uuid>",
	Short: "Restore deleted or archived tasks",
	Long: `Restores a deleted or archived task to active state.

The task must be in 'deleted' or 'archived' state. By default, restores to 'open' state.
Subtasks are cascade-restored when their parent is restored.

Examples:
  wrkq restore T-00042                       # Restore by ID
  wrkq restore inbox/my-task                 # Restore by path
  wrkq restore T-00042 --state in_progress   # Restore to specific state
  wrkq restore T-00042 --comment "Restored"  # Add comment on restore
  wrkq restore T-00042 --to inbox/new-loc    # Move and restore
`,
	Args: cobra.ExactArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runRestore),
}

var (
	restoreTo          string
	restoreTitle       string
	restoreDescription string
	restoreState       string
	restorePriority    int
	restoreLabels      string
	restoreAssignee    string
	restoreIfMatch     int64
	restoreComment     string
)

func init() {
	rootCmd.AddCommand(restoreCmd)

	restoreCmd.Flags().StringVar(&restoreTo, "to", "", "Restore to different container/slug (path)")
	restoreCmd.Flags().StringVar(&restoreTitle, "title", "", "Update title on restore")
	restoreCmd.Flags().StringVar(&restoreDescription, "description", "", "Update description on restore")
	restoreCmd.Flags().StringVar(&restoreState, "state", "", "Restore to specific state (default: open)")
	restoreCmd.Flags().IntVar(&restorePriority, "priority", 0, "Update priority on restore (1-4)")
	restoreCmd.Flags().StringVar(&restoreLabels, "labels", "", "Update labels on restore (JSON array)")
	restoreCmd.Flags().StringVar(&restoreAssignee, "assignee", "", "Update assignee on restore")
	restoreCmd.Flags().Int64Var(&restoreIfMatch, "if-match", 0, "Conditional restore (etag)")
	restoreCmd.Flags().StringVar(&restoreComment, "comment", "", "Add comment explaining restoration")
}

func runRestore(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	attr := app.Attribution()
	claims := &stdinClaims{}
	arg := args[0]
	arg = applyProjectRootToSelector(app.Config, arg, false)
	if restoreTo != "" {
		restoreTo = applyProjectRootToPath(app.Config, restoreTo, false)
	}

	// Validate state if provided
	targetState := "open"
	if restoreState != "" {
		state, err := domain.ParseState(restoreState)
		if err != nil {
			return err
		}
		if state == domain.StateArchived || state == domain.StateDeleted {
			return fmt.Errorf("cannot restore to %s state", restoreState)
		}
		targetState = string(state)
	}

	// Validate priority if provided
	if restorePriority != 0 {
		if err := domain.ValidatePriority(restorePriority); err != nil {
			return err
		}
	}

	// Validate labels if provided
	if restoreLabels != "" {
		var labels []string
		if err := json.Unmarshal([]byte(restoreLabels), &labels); err != nil {
			return fmt.Errorf("invalid labels JSON: %w", err)
		}
	}

	// Resolve assignee if provided
	var assigneePrincipalRef *string
	if restoreAssignee != "" {
		principalRef, err := attribution.NormalizeCompat(restoreAssignee)
		if err != nil {
			return fmt.Errorf("failed to resolve assignee: %w", err)
		}
		assigneePrincipalRef = &principalRef
	}

	// Try to resolve as task first
	taskUUID, taskID, err := selectors.ResolveTask(database, arg)
	if err != nil {
		// Try as container
		containerUUID, _, containerErr := selectors.ResolveContainer(database, arg)
		if containerErr != nil {
			return fmt.Errorf("not found: %s", arg)
		}

		// Restore container
		if err := restoreContainer(database, attr, containerUUID); err != nil {
			return fmt.Errorf("failed to restore container: %w", err)
		}
		if !isStdoutTTY(cmd.OutOrStdout()) {
			return writeJSONOutput(cmd.OutOrStdout(), outputSelection{}, map[string]interface{}{
				"type":     "container",
				"selector": arg,
				"uuid":     containerUUID,
				"restored": true,
			})
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Restored container: %s\n", arg)
		return nil
	}

	// Check task state - must be archived or deleted
	var currentState string
	var currentETag int64
	err = database.QueryRow("SELECT state, etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentState, &currentETag)
	if err != nil {
		return fmt.Errorf("failed to get task state: %w", err)
	}

	if currentState != "archived" && currentState != "deleted" {
		return fmt.Errorf("task is not deleted or archived (current state: %s)", currentState)
	}

	// Check etag if --if-match provided
	if restoreIfMatch != 0 && restoreIfMatch != currentETag {
		return fmt.Errorf("etag mismatch: expected %d, got %d", restoreIfMatch, currentETag)
	}

	// Handle --to flag (move and restore)
	var newProjectUUID *string
	var newSlug *string
	if restoreTo != "" {
		parentUUID, slug, _, err := selectors.ResolveParentContainer(database, restoreTo)
		if err != nil {
			return fmt.Errorf("failed to resolve destination: %w", err)
		}
		newProjectUUID = parentUUID
		newSlug = &slug

		// Check for slug conflict at destination
		var existingUUID string
		err = database.QueryRow(`
			SELECT uuid FROM tasks WHERE project_uuid = ? AND slug = ? AND uuid != ?
		`, *parentUUID, slug, taskUUID).Scan(&existingUUID)
		if err == nil {
			return fmt.Errorf("slug conflict: task with slug '%s' already exists at destination", slug)
		} else if err != sql.ErrNoRows {
			return fmt.Errorf("failed to check for conflicts: %w", err)
		}
	}

	// Restore the task with updates
	restoreCommentValue := restoreComment
	if restoreComment != "" {
		var commentErr error
		restoreCommentValue, commentErr = readTextValue(restoreComment, "--comment", cmd.InOrStdin(), claims)
		if commentErr != nil {
			return commentErr
		}
	}
	opts := restoreTaskOptions{
		taskUUID:             taskUUID,
		attr:                 attr,
		targetState:          targetState,
		newProjectUUID:       newProjectUUID,
		newSlug:              newSlug,
		newTitle:             restoreTitle,
		newDescription:       restoreDescription,
		newPriority:          restorePriority,
		newLabels:            restoreLabels,
		assigneePrincipalRef: assigneePrincipalRef,
		comment:              restoreCommentValue,
	}

	webhookCtx, err := restoreTaskWithOptions(database, opts)
	if err != nil {
		return fmt.Errorf("failed to restore task: %w", err)
	}

	webhooks.DispatchTaskEvent(database, taskUUID, webhookCtx)

	// Cascade restore subtasks
	if err := cascadeRestoreSubtasks(database, attr, taskUUID, targetState); err != nil {
		return fmt.Errorf("failed to restore subtasks: %w", err)
	}

	if !isStdoutTTY(cmd.OutOrStdout()) {
		return writeJSONOutput(cmd.OutOrStdout(), outputSelection{}, map[string]interface{}{
			"type":          "task",
			"id":            taskID,
			"uuid":          taskUUID,
			"restored":      true,
			"state":         targetState,
			"moved":         newProjectUUID != nil || newSlug != nil,
			"comment_added": restoreCommentValue != "",
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Restored task: %s\n", taskID)
	return nil
}

type restoreTaskOptions struct {
	taskUUID             string
	attr                 attribution.Attribution
	targetState          string
	newProjectUUID       *string
	newSlug              *string
	newTitle             string
	newDescription       string
	newPriority          int
	newLabels            string
	assigneePrincipalRef *string
	comment              string
}

func restoreTaskWithOptions(database *db.DB, opts restoreTaskOptions) (webhooks.EventContext, error) {
	tx, err := database.Begin()
	if err != nil {
		return webhooks.EventContext{}, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var currentState string
	if err := tx.QueryRow("SELECT state FROM tasks WHERE uuid = ?", opts.taskUUID).Scan(&currentState); err != nil {
		return webhooks.EventContext{}, fmt.Errorf("failed to load current task state: %w", err)
	}

	// Build dynamic UPDATE query
	query := `UPDATE tasks SET state = ?, archived_at = NULL, deleted_at = NULL,
		deleted_by_principal_ref = NULL, deleted_by_scope_ref = NULL,
		updated_by_principal_ref = ?, updated_by_scope_ref = ?`
	args := []interface{}{opts.targetState, opts.attr.PrincipalRef, scopeBind(opts.attr)}
	fields := map[string]interface{}{
		"state":       opts.targetState,
		"archived_at": nil,
		"deleted_at":  nil,
	}

	if opts.newProjectUUID != nil {
		query += `, project_uuid = ?`
		args = append(args, *opts.newProjectUUID)
		fields["project_uuid"] = *opts.newProjectUUID
	}
	if opts.newSlug != nil {
		query += `, slug = ?`
		args = append(args, *opts.newSlug)
		fields["slug"] = *opts.newSlug
	}
	if opts.newTitle != "" {
		query += `, title = ?`
		args = append(args, opts.newTitle)
		fields["title"] = opts.newTitle
	}
	if opts.newDescription != "" {
		query += `, description = ?`
		args = append(args, opts.newDescription)
		fields["description"] = map[string]interface{}{"length": len(opts.newDescription)}
	}
	if opts.newPriority != 0 {
		query += `, priority = ?`
		args = append(args, opts.newPriority)
		fields["priority"] = opts.newPriority
	}
	if opts.newLabels != "" {
		query += `, labels = ?`
		args = append(args, opts.newLabels)
		fields["labels"] = opts.newLabels
	}
	if opts.assigneePrincipalRef != nil {
		query += `, assignee_principal_ref = ?`
		args = append(args, *opts.assigneePrincipalRef)
		fields["assignee_principal_ref"] = *opts.assigneePrincipalRef
	}

	query += ` WHERE uuid = ?`
	args = append(args, opts.taskUUID)

	_, err = tx.Exec(query, args...)
	if err != nil {
		return webhooks.EventContext{}, fmt.Errorf("failed to restore task: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payload := map[string]interface{}{
		"action":       "restored",
		"target_state": opts.targetState,
	}
	if opts.newProjectUUID != nil {
		payload["moved_to"] = *opts.newProjectUUID
	}
	payloadJSON, _ := json.Marshal(payload)
	payloadStr := string(payloadJSON)

	eventMeta, err := eventWriter.LogEventReturning(tx, &domain.Event{
		PrincipalRef: opts.attr.PrincipalRef,
		ScopeRef:     opts.attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &opts.taskUUID,
		EventType:    "task.restored",
		Payload:      &payloadStr,
	})
	if err != nil {
		return webhooks.EventContext{}, fmt.Errorf("failed to log event: %w", err)
	}

	// Add comment if provided
	if opts.comment != "" {
		// Get next comment ID by calculating from MAX(id)+1
		var nextSeq int64
		err := tx.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) + 1 FROM comments").Scan(&nextSeq)
		if err != nil {
			return webhooks.EventContext{}, fmt.Errorf("failed to get comment sequence: %w", err)
		}

		// Update sequence table to stay in sync
		_, err = tx.Exec("UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'", nextSeq)
		if err != nil {
			return webhooks.EventContext{}, fmt.Errorf("failed to update comment sequence: %w", err)
		}

		commentUUID := uuid.New().String()
		commentID := id.FormatComment(int(nextSeq))

		_, err = tx.Exec(`
			INSERT INTO comments (
				uuid, id, task_uuid, created_by_principal_ref,
				created_by_scope_ref, body, etag
			)
			VALUES (?, ?, ?, ?, ?, ?, 1)
		`, commentUUID, commentID, opts.taskUUID,
			opts.attr.PrincipalRef, scopeBind(opts.attr), opts.comment)
		if err != nil {
			return webhooks.EventContext{}, fmt.Errorf("failed to add comment: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return webhooks.EventContext{}, err
	}
	return webhooks.EventContext{
		Metadata:     eventMeta,
		Event:        "updated",
		PrincipalRef: opts.attr.PrincipalRef,
		Via:          "cli",
		Transition:   &webhooks.Transition{From: &currentState, To: &opts.targetState},
		Changed:      sortedMapKeys(fields),
		Changes:      mapChanges(fields, map[string]interface{}{"state": currentState}),
	}, nil
}

func cascadeRestoreSubtasks(database *db.DB, attr attribution.Attribution, parentTaskUUID, targetState string) error {
	// Find resident subtasks that are archived or deleted. Cross-project parent
	// edges are backlinks, not containment, so restore must not mutate them via
	// the parent.
	rows, err := database.Query(`
		SELECT c.uuid
		FROM tasks c
		JOIN tasks p ON p.uuid = c.parent_task_uuid
		WHERE c.parent_task_uuid = ?
		  AND c.project_uuid = p.project_uuid
		  AND c.state IN ('archived', 'deleted')
	`, parentTaskUUID)
	if err != nil {
		return fmt.Errorf("failed to query subtasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var subtaskUUIDs []string
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return fmt.Errorf("failed to scan subtask: %w", err)
		}
		subtaskUUIDs = append(subtaskUUIDs, uuid)
	}

	// Restore each subtask
	for _, subtaskUUID := range subtaskUUIDs {
		opts := restoreTaskOptions{
			taskUUID:    subtaskUUID,
			attr:        attr,
			targetState: targetState,
		}
		webhookCtx, err := restoreTaskWithOptions(database, opts)
		if err != nil {
			return fmt.Errorf("failed to restore subtask %s: %w", subtaskUUID, err)
		}

		webhooks.DispatchTaskEvent(database, subtaskUUID, webhookCtx)

		// Recursively restore nested subtasks
		if err := cascadeRestoreSubtasks(database, attr, subtaskUUID, targetState); err != nil {
			return err
		}
	}

	return nil
}

func restoreContainer(database *db.DB, attr attribution.Attribution, containerUUID string) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	_, err = tx.Exec(`
		UPDATE containers
		SET archived_at = NULL,
		    updated_by_principal_ref = ?,
		    updated_by_scope_ref = ?
		WHERE uuid = ?
	`, attr.PrincipalRef, scopeBind(attr), containerUUID)
	if err != nil {
		return fmt.Errorf("failed to restore container: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payload := `{"action":"restored"}`
	if err := eventWriter.LogEvent(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "container",
		ResourceUUID: &containerUUID,
		EventType:    "container.restored",
		Payload:      &payload,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	return tx.Commit()
}
