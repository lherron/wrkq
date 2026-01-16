package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var catCmd = &cobra.Command{
	Use:     "cat <path|id>...",
	Aliases: []string{"show"},
	Short:   "Print tasks as markdown",
	Long: `Prints one or more tasks as markdown with YAML front matter.
Comments are included by default. Use --exclude-comments to omit them.
If the argument resolves to a container, exits with error code 2.`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runCat),
}

var (
	catNoFrontmatter   bool
	catExcludeComments bool
	catJSON            bool
	catNDJSON          bool
	catPorcelain       bool
)

func init() {
	rootCmd.AddCommand(catCmd)
	catCmd.Flags().BoolVar(&catNoFrontmatter, "no-frontmatter", false, "Print body only without front matter")
	catCmd.Flags().BoolVar(&catExcludeComments, "exclude-comments", false, "Exclude comments from output")
	catCmd.Flags().BoolVar(&catJSON, "json", false, "Output as JSON")
	catCmd.Flags().BoolVar(&catNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	catCmd.Flags().BoolVar(&catPorcelain, "porcelain", false, "Machine-readable output")
}

func runCat(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Define structs for JSON output
	type Comment struct {
		ID        string `json:"id"`
		CreatedAt string `json:"created_at"`
		Body      string `json:"body"`
		ActorSlug string `json:"actor_slug"`
		ActorRole string `json:"actor_role"`
	}

	type Relation struct {
		Direction   string `json:"direction"` // "outgoing" or "incoming"
		Kind        string `json:"kind"`      // "blocks", "relates_to", "duplicates"
		TaskID      string `json:"task_id"`
		TaskUUID    string `json:"task_uuid"`
		TaskSlug    string `json:"task_slug"`
		TaskTitle   string `json:"task_title"`
		CreatedAt   string `json:"created_at"`
		CreatedByID string `json:"created_by_id"`
	}

	// BlockerInfo represents an incomplete blocking task
	type BlockerInfo struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}

	type Task struct {
		ID                   string          `json:"id"`
		UUID                 string          `json:"uuid"`
		Path                 string          `json:"path"`
		ProjectID            string          `json:"project_id"`
		ProjectUUID          string          `json:"project_uuid"`
		RequestedByProjectID *string         `json:"requested_by_project_id,omitempty"`
		AssignedProjectID    *string         `json:"assigned_project_id,omitempty"`
		Slug                 string          `json:"slug"`
		Title                string          `json:"title"`
		State                string          `json:"state"`
		Priority             int             `json:"priority"`
		Kind                 string          `json:"kind"`
		ParentTaskID         *string         `json:"parent_task_id,omitempty"`
		ParentTaskUUID       *string         `json:"parent_task_uuid,omitempty"`
		AssigneeSlug         *string         `json:"assignee,omitempty"`
		AssigneeUUID         *string         `json:"assignee_uuid,omitempty"`
		StartAt              *string         `json:"start_at,omitempty"`
		DueAt                *string         `json:"due_at,omitempty"`
		Labels               *string         `json:"labels,omitempty"`
		Meta                 json.RawMessage `json:"meta"`
		Description          string          `json:"description"`
		AcknowledgedAt       *string         `json:"acknowledged_at,omitempty"`
		Resolution           *string         `json:"resolution,omitempty"`
		CPProjectID          *string         `json:"cp_project_id,omitempty"`
		CPWorkItemID         *string         `json:"cp_work_item_id,omitempty"`
		CPRunID              *string         `json:"cp_run_id,omitempty"`
		CPSessionID          *string         `json:"cp_session_id,omitempty"`
		SDKSessionID         *string         `json:"sdk_session_id,omitempty"`
		RunStatus            *string         `json:"run_status,omitempty"`
		Etag                 int64           `json:"etag"`
		CreatedAt            string          `json:"created_at"`
		UpdatedAt            string          `json:"updated_at"`
		CompletedAt          *string         `json:"completed_at,omitempty"`
		ArchivedAt           *string         `json:"archived_at,omitempty"`
		CreatedBy            string          `json:"created_by"`
		UpdatedBy            string          `json:"updated_by"`
		BlockedBy            []BlockerInfo   `json:"blocked_by,omitempty"`
		Comments             []Comment       `json:"comments,omitempty"`
		Relations            []Relation      `json:"relations,omitempty"`
	}

	var tasks []Task
	taskCount := 0

	// Process each argument
	for _, arg := range args {
		taskUUID, _, err := selectors.ResolveTask(database, applyProjectRootToSelector(app.Config, arg, false))
		if err != nil {
			return err
		}

		// Get task details
		var id, slug, title, state, description, kind string
		var priority int
		var startAt, dueAt, labels, meta, completedAt, archivedAt *string
		var requestedBy, assignedProject, acknowledgedAt, resolution *string
		var cpProjectID, cpWorkItemID, cpRunID, cpSessionID, sdkSessionID, runStatus *string
		var parentTaskUUID, assigneeActorUUID *string
		var createdAt, updatedAt string
		var etag int64
		var projectUUID, createdByUUID, updatedByUUID string

		err = database.QueryRow(`
			SELECT id, slug, title, project_uuid, requested_by_project_id, assigned_project_id,
			       state, priority,
			       kind, parent_task_uuid, assignee_actor_uuid,
			       start_at, due_at, labels, meta, description, etag,
			       created_at, updated_at, completed_at, archived_at,
			       acknowledged_at, resolution,
			       cp_project_id, cp_work_item_id, cp_run_id, cp_session_id, sdk_session_id, run_status,
			       created_by_actor_uuid, updated_by_actor_uuid
			FROM tasks WHERE uuid = ?
		`, taskUUID).Scan(
			&id, &slug, &title, &projectUUID, &requestedBy, &assignedProject, &state, &priority,
			&kind, &parentTaskUUID, &assigneeActorUUID,
			&startAt, &dueAt, &labels, &meta, &description, &etag,
			&createdAt, &updatedAt, &completedAt, &archivedAt,
			&acknowledgedAt, &resolution,
			&cpProjectID, &cpWorkItemID, &cpRunID, &cpSessionID, &sdkSessionID, &runStatus,
			&createdByUUID, &updatedByUUID,
		)
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}

		// Get actor slugs
		var createdBySlug, updatedBySlug string
		database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", createdByUUID).Scan(&createdBySlug)
		database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", updatedByUUID).Scan(&updatedBySlug)

		// Get project info
		var projectID string
		database.QueryRow("SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

		// Get task path from v_task_paths view
		var taskPath string
		database.QueryRow("SELECT path FROM v_task_paths WHERE uuid = ?", taskUUID).Scan(&taskPath)

		// Get parent task ID if parent exists
		var parentTaskID *string
		if parentTaskUUID != nil {
			var ptID string
			if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", *parentTaskUUID).Scan(&ptID); err == nil {
				parentTaskID = &ptID
			}
		}

		// Get assignee slug if assignee exists
		var assigneeSlug *string
		if assigneeActorUUID != nil {
			var aSlug string
			if err := database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", *assigneeActorUUID).Scan(&aSlug); err == nil {
				assigneeSlug = &aSlug
			}
		}

		metaValue := "{}"
		if meta != nil && *meta != "" && json.Valid([]byte(*meta)) {
			metaValue = *meta
		}
		task := Task{
			ID:                   id,
			UUID:                 taskUUID,
			Path:                 taskPath,
			ProjectID:            projectID,
			ProjectUUID:          projectUUID,
			RequestedByProjectID: requestedBy,
			AssignedProjectID:    assignedProject,
			Slug:                 slug,
			Title:                title,
			State:                state,
			Priority:             priority,
			Kind:                 kind,
			ParentTaskID:         parentTaskID,
			ParentTaskUUID:       parentTaskUUID,
			AssigneeSlug:         assigneeSlug,
			AssigneeUUID:         assigneeActorUUID,
			StartAt:              startAt,
			DueAt:                dueAt,
			Labels:               labels,
			Meta:                 json.RawMessage(metaValue),
			Description:          description,
			AcknowledgedAt:       acknowledgedAt,
			Resolution:           resolution,
			CPProjectID:          cpProjectID,
			CPWorkItemID:         cpWorkItemID,
			CPRunID:              cpRunID,
			CPSessionID:          cpSessionID,
			SDKSessionID:         sdkSessionID,
			RunStatus:            runStatus,
			Etag:                 etag,
			CreatedAt:            createdAt,
			UpdatedAt:            updatedAt,
			CompletedAt:          completedAt,
			ArchivedAt:           archivedAt,
			CreatedBy:            createdBySlug,
			UpdatedBy:            updatedBySlug,
		}

		// Include comments by default (unless excluded)
		if !catExcludeComments {
			// Query non-deleted comments for this task
			rows, err := database.Query(`
				SELECT c.id, c.created_at, c.body, a.slug as actor_slug, a.role as actor_role
				FROM comments c
				LEFT JOIN actors a ON c.actor_uuid = a.uuid
				WHERE c.task_uuid = ? AND c.deleted_at IS NULL
				ORDER BY c.created_at ASC
			`, taskUUID)
			if err != nil {
				return fmt.Errorf("failed to query comments: %w", err)
			}

			var comments []Comment
			for rows.Next() {
				var comment Comment
				if err := rows.Scan(&comment.ID, &comment.CreatedAt, &comment.Body, &comment.ActorSlug, &comment.ActorRole); err != nil {
					rows.Close()
					return fmt.Errorf("failed to scan comment: %w", err)
				}
				comments = append(comments, comment)
			}
			rows.Close()

			if err := rows.Err(); err != nil {
				return fmt.Errorf("error iterating comments: %w", err)
			}

			if len(comments) > 0 {
				task.Comments = comments
			}
		}

		// Query relations for this task
		var relations []Relation

		// Get outgoing relations (this task -> other tasks)
		outgoingRows, err := database.Query(`
			SELECT r.kind, r.created_at,
			       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
			       a.id AS created_by_id
			FROM task_relations r
			JOIN tasks t ON r.to_task_uuid = t.uuid
			JOIN actors a ON r.created_by_actor_uuid = a.uuid
			WHERE r.from_task_uuid = ?
			ORDER BY r.kind, t.id
		`, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to query outgoing relations: %w", err)
		}

		for outgoingRows.Next() {
			var rel Relation
			if err := outgoingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				outgoingRows.Close()
				return fmt.Errorf("failed to scan relation: %w", err)
			}
			rel.Direction = "outgoing"
			relations = append(relations, rel)
		}
		outgoingRows.Close()

		// Get incoming relations (other tasks -> this task)
		incomingRows, err := database.Query(`
			SELECT r.kind, r.created_at,
			       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
			       a.id AS created_by_id
			FROM task_relations r
			JOIN tasks t ON r.from_task_uuid = t.uuid
			JOIN actors a ON r.created_by_actor_uuid = a.uuid
			WHERE r.to_task_uuid = ?
			ORDER BY r.kind, t.id
		`, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to query incoming relations: %w", err)
		}

		for incomingRows.Next() {
			var rel Relation
			if err := incomingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				incomingRows.Close()
				return fmt.Errorf("failed to scan relation: %w", err)
			}
			rel.Direction = "incoming"
			relations = append(relations, rel)
		}
		incomingRows.Close()

		if len(relations) > 0 {
			task.Relations = relations
		}

		// Query incomplete blockers using the store's BlockedBy method
		s := store.New(database)
		blockers, err := s.Tasks.BlockedBy(taskUUID)
		if err != nil {
			return fmt.Errorf("failed to query blockers: %w", err)
		}
		if len(blockers) > 0 {
			blockerInfos := make([]BlockerInfo, len(blockers))
			for i, b := range blockers {
				blockerInfos[i] = BlockerInfo{
					ID:    b.ID,
					State: b.State,
				}
			}
			task.BlockedBy = blockerInfos
		}

		// For JSON output, collect tasks
		if catJSON || catNDJSON {
			tasks = append(tasks, task)
		} else {
			// Original markdown output
			if taskCount > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			taskCount++

			if !catNoFrontmatter {
				// Print YAML front matter
				fmt.Fprintln(cmd.OutOrStdout(), "---")
				fmt.Fprintf(cmd.OutOrStdout(), "id: %s\n", task.ID)
				fmt.Fprintf(cmd.OutOrStdout(), "uuid: %s\n", task.UUID)
				fmt.Fprintf(cmd.OutOrStdout(), "path: %s\n", task.Path)
				fmt.Fprintf(cmd.OutOrStdout(), "project_id: %s\n", task.ProjectID)
				fmt.Fprintf(cmd.OutOrStdout(), "project_uuid: %s\n", task.ProjectUUID)
				if task.RequestedByProjectID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "requested_by_project_id: %s\n", *task.RequestedByProjectID)
				}
				if task.AssignedProjectID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "assigned_project_id: %s\n", *task.AssignedProjectID)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "slug: %s\n", task.Slug)
				fmt.Fprintf(cmd.OutOrStdout(), "title: %s\n", task.Title)
				fmt.Fprintf(cmd.OutOrStdout(), "state: %s\n", task.State)
				fmt.Fprintf(cmd.OutOrStdout(), "priority: %d\n", task.Priority)
				fmt.Fprintf(cmd.OutOrStdout(), "kind: %s\n", task.Kind)
				if task.ParentTaskID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "parent_task_id: %s\n", *task.ParentTaskID)
				}
				if task.ParentTaskUUID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "parent_task_uuid: %s\n", *task.ParentTaskUUID)
				}
				if task.AssigneeSlug != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "assignee: %s\n", *task.AssigneeSlug)
				}
				if task.AssigneeUUID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "assignee_uuid: %s\n", *task.AssigneeUUID)
				}
				if task.StartAt != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "start_at: %s\n", *task.StartAt)
				}
				if task.DueAt != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "due_at: %s\n", *task.DueAt)
				}
				if task.Labels != nil && *task.Labels != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "labels: %s\n", *task.Labels)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "meta: %s\n", metaValue)
				if task.AcknowledgedAt != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "acknowledged_at: %s\n", *task.AcknowledgedAt)
				}
				if task.Resolution != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "resolution: %s\n", *task.Resolution)
				}
				if task.CPProjectID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "cp_project_id: %s\n", *task.CPProjectID)
				}
				if task.CPWorkItemID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "cp_work_item_id: %s\n", *task.CPWorkItemID)
				}
				if task.CPRunID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "cp_run_id: %s\n", *task.CPRunID)
				}
				if task.CPSessionID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "cp_session_id: %s\n", *task.CPSessionID)
				}
				if task.SDKSessionID != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "sdk_session_id: %s\n", *task.SDKSessionID)
				}
				if task.RunStatus != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "run_status: %s\n", *task.RunStatus)
				}
				if len(task.BlockedBy) > 0 {
					parts := make([]string, len(task.BlockedBy))
					for i, b := range task.BlockedBy {
						parts[i] = fmt.Sprintf("%s (%s)", b.ID, b.State)
					}
					fmt.Fprintf(cmd.OutOrStdout(), "blocked_by: [%s]\n", strings.Join(parts, ", "))
				}
				fmt.Fprintf(cmd.OutOrStdout(), "etag: %d\n", task.Etag)
				fmt.Fprintf(cmd.OutOrStdout(), "created_at: %s\n", task.CreatedAt)
				fmt.Fprintf(cmd.OutOrStdout(), "updated_at: %s\n", task.UpdatedAt)
				if task.CompletedAt != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "completed_at: %s\n", *task.CompletedAt)
				}
				if task.ArchivedAt != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "archived_at: %s\n", *task.ArchivedAt)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "created_by: %s\n", task.CreatedBy)
				fmt.Fprintf(cmd.OutOrStdout(), "updated_by: %s\n", task.UpdatedBy)
				fmt.Fprintln(cmd.OutOrStdout(), "---")
				fmt.Fprintln(cmd.OutOrStdout())
			}

			// Print description
			fmt.Fprintln(cmd.OutOrStdout(), task.Description)

			// Print comments unless excluded
			if !catExcludeComments && len(task.Comments) > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), "---")
				fmt.Fprintln(cmd.OutOrStdout())
				fmt.Fprintln(cmd.OutOrStdout(), "<!-- wrkq-comments: do not edit below -->")
				fmt.Fprintln(cmd.OutOrStdout())

				for _, comment := range task.Comments {
					// Print header line
					fmt.Fprintf(cmd.OutOrStdout(), "> [%s] [%s] %s (%s)\n",
						comment.ID, comment.CreatedAt, comment.ActorSlug, comment.ActorRole)

					// Print body lines with > prefix
					bodyLines := strings.Split(comment.Body, "\n")
					for _, line := range bodyLines {
						fmt.Fprintf(cmd.OutOrStdout(), "> %s\n", line)
					}
					fmt.Fprintln(cmd.OutOrStdout())
				}
			}
		}
	}

	// Output JSON if requested
	if catJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if !catPorcelain {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(tasks)
	}

	if catNDJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, task := range tasks {
			if err := encoder.Encode(task); err != nil {
				return err
			}
		}
		return nil
	}

	return nil
}
