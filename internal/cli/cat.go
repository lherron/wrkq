package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

var catCmd = &cobra.Command{
	Use:     "cat <path|id>...",
	Aliases: []string{"show"},
	Short:   "Show task detail",
	Long: `Shows one or more tasks. On a TTY, cat prints markdown with YAML front matter.
When stdout is not a TTY, cat defaults to JSON. Use --output raw to force markdown.
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
	catPretty          bool
)

func init() {
	rootCmd.AddCommand(catCmd)
	catCmd.Flags().BoolVar(&catNoFrontmatter, "no-frontmatter", false, "Print body only without front matter")
	catCmd.Flags().BoolVar(&catExcludeComments, "exclude-comments", false, "Exclude comments from output")
	catCmd.Flags().BoolVar(&catJSON, "json", false, "Output as JSON")
	catCmd.Flags().BoolVar(&catNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	catCmd.Flags().BoolVar(&catPorcelain, "porcelain", false, "Machine-readable output")
	catCmd.Flags().BoolVar(&catPretty, "pretty", false, "Force the styled task card even when not a TTY")
}

func valueOrEmpty(value sql.NullString) string {
	if !value.Valid {
		return ""
	}
	return value.String
}

func scopeRefToHandle(scopeRef string) string {
	parsed, err := scope.ParseScopeRef(scopeRef)
	if err != nil {
		return scopeRef
	}
	return scope.FormatScopeHandle(parsed)
}

func runCat(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	sel, err := resolveOutputMode(cmd, app.Config, outputShapeContent, outputResolveOptions{
		Allow:         []outputMode{outputModeRaw, outputModeJSON, outputModeNDJSON},
		DefaultTTY:    outputModeRaw,
		DefaultNonTTY: outputModeJSON,
	})
	if err != nil {
		return err
	}
	if cmd.Flag("json") == nil {
		switch {
		case catJSON && catNDJSON:
			return fmt.Errorf("choose only one output mode")
		case catJSON:
			sel = outputSelection{Mode: outputModeJSON, Stable: catPorcelain}
		case catNDJSON:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: catPorcelain}
		case catPorcelain:
			sel = outputSelection{Mode: outputModeRaw, Stable: true}
		}
	}

	// Define structs for JSON output
	type Comment struct {
		ID           string `json:"id"`
		CreatedAt    string `json:"created_at"`
		Body         string `json:"body"`
		PrincipalRef string `json:"principal_ref,omitempty"`
		ActorSlug    string `json:"actor_slug,omitempty"`
		ActorRole    string `json:"actor_role,omitempty"`
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
		ID                    string          `json:"id"`
		UUID                  string          `json:"uuid"`
		Path                  string          `json:"path"`
		ArtifactDir           string          `json:"artifact_dir"`
		ProjectID             string          `json:"project_id"`
		ProjectUUID           string          `json:"project_uuid"`
		RequestedByProjectID  *string         `json:"requested_by_project_id,omitempty"`
		AssignedProjectID     *string         `json:"assigned_project_id,omitempty"`
		Slug                  string          `json:"slug"`
		Title                 string          `json:"title"`
		State                 string          `json:"state"`
		Priority              int             `json:"priority"`
		Kind                  string          `json:"kind"`
		ParentTaskID          *string         `json:"parent_task_id,omitempty"`
		ParentTaskUUID        *string         `json:"parent_task_uuid,omitempty"`
		AssigneeSlug          *string         `json:"assignee,omitempty"`
		AssigneeUUID          *string         `json:"assignee_uuid,omitempty"`
		AssigneePrincipalRef  *string         `json:"assignee_principal_ref,omitempty"`
		StartAt               *string         `json:"start_at,omitempty"`
		DueAt                 *string         `json:"due_at,omitempty"`
		Labels                *string         `json:"labels,omitempty"`
		Meta                  json.RawMessage `json:"meta"`
		Description           string          `json:"description"`
		Specification         string          `json:"specification"`
		AcknowledgedAt        *string         `json:"acknowledged_at,omitempty"`
		Resolution            *string         `json:"resolution,omitempty"`
		Etag                  int64           `json:"etag"`
		CreatedAt             string          `json:"created_at"`
		UpdatedAt             string          `json:"updated_at"`
		CompletedAt           *string         `json:"completed_at,omitempty"`
		ArchivedAt            *string         `json:"archived_at,omitempty"`
		CreatedBy             string          `json:"created_by"`
		CreatedByPrincipalRef string          `json:"created_by_principal_ref,omitempty"`
		CreatedByActor        string          `json:"created_by_actor,omitempty"`
		CreatedByScopeRef     *string         `json:"created_by_scope_ref,omitempty"`
		UpdatedBy             string          `json:"updated_by"`
		UpdatedByPrincipalRef string          `json:"updated_by_principal_ref,omitempty"`
		CausedBy              []string        `json:"caused_by"`
		BlockedBy             []BlockerInfo   `json:"blocked_by,omitempty"`
		Comments              []Comment       `json:"comments,omitempty"`
		Relations             []Relation      `json:"relations,omitempty"`
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
		var id, slug, title, state, description, specification, kind string
		var priority int
		var startAt, dueAt, labels, meta, completedAt, archivedAt *string
		var requestedBy, assignedProject, acknowledgedAt, resolution *string
		var parentTaskUUID, assigneeActorUUID, assigneePrincipalRef *string
		var createdAt, updatedAt string
		var etag int64
		var projectUUID string
		var createdByUUID, updatedByUUID, createdByPrincipalRef, updatedByPrincipalRef sql.NullString
		var createdByScopeRef *string

		err = database.QueryRow(`
			SELECT id, slug, title, project_uuid, requested_by_project_id, assigned_project_id,
			       state, priority,
			       kind, parent_task_uuid, assignee_actor_uuid, assignee_principal_ref,
			       start_at, due_at, labels, meta, description, specification, etag,
			       created_at, updated_at, completed_at, archived_at,
			       acknowledged_at, resolution,
			       created_by_actor_uuid, updated_by_actor_uuid,
			       created_by_principal_ref, updated_by_principal_ref, created_by_scope_ref
			FROM tasks WHERE uuid = ?
		`, taskUUID).Scan(
			&id, &slug, &title, &projectUUID, &requestedBy, &assignedProject, &state, &priority,
			&kind, &parentTaskUUID, &assigneeActorUUID, &assigneePrincipalRef,
			&startAt, &dueAt, &labels, &meta, &description, &specification, &etag,
			&createdAt, &updatedAt, &completedAt, &archivedAt,
			&acknowledgedAt, &resolution,
			&createdByUUID, &updatedByUUID, &createdByPrincipalRef, &updatedByPrincipalRef, &createdByScopeRef,
		)
		if err != nil {
			return fmt.Errorf("failed to get task: %w", err)
		}

		// Get actor slugs
		var createdBySlug, updatedBySlug string
		if createdByUUID.Valid {
			_ = database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", createdByUUID.String).Scan(&createdBySlug)
		}
		if updatedByUUID.Valid {
			_ = database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", updatedByUUID.String).Scan(&updatedBySlug)
		}

		// Get project info
		var projectID string
		_ = database.QueryRow("SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

		// Get task path from v_task_paths view
		var taskPath string
		_ = database.QueryRow("SELECT path FROM v_task_paths WHERE uuid = ?", taskUUID).Scan(&taskPath)

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
		if assigneePrincipalRef != nil {
			display := attribution.PrincipalHandle(*assigneePrincipalRef)
			assigneeSlug = &display
		}
		createdBy := createdBySlug
		if createdByPrincipalRef.Valid {
			createdBy = attribution.PrincipalHandle(createdByPrincipalRef.String)
		}
		updatedBy := updatedBySlug
		if updatedByPrincipalRef.Valid {
			updatedBy = attribution.PrincipalHandle(updatedByPrincipalRef.String)
		}

		metaValue := "{}"
		if meta != nil && *meta != "" && json.Valid([]byte(*meta)) {
			metaValue = *meta
		}
		task := Task{
			ID:                    id,
			UUID:                  taskUUID,
			Path:                  taskPath,
			ArtifactDir:           taskArtifactDir(id),
			ProjectID:             projectID,
			ProjectUUID:           projectUUID,
			RequestedByProjectID:  requestedBy,
			AssignedProjectID:     assignedProject,
			Slug:                  slug,
			Title:                 title,
			State:                 state,
			Priority:              priority,
			Kind:                  kind,
			ParentTaskID:          parentTaskID,
			ParentTaskUUID:        parentTaskUUID,
			AssigneeSlug:          assigneeSlug,
			AssigneeUUID:          assigneeActorUUID,
			AssigneePrincipalRef:  assigneePrincipalRef,
			StartAt:               startAt,
			DueAt:                 dueAt,
			Labels:                labels,
			Meta:                  json.RawMessage(metaValue),
			Description:           description,
			Specification:         specification,
			AcknowledgedAt:        acknowledgedAt,
			Resolution:            resolution,
			Etag:                  etag,
			CreatedAt:             createdAt,
			UpdatedAt:             updatedAt,
			CompletedAt:           completedAt,
			ArchivedAt:            archivedAt,
			CreatedBy:             createdBy,
			CreatedByPrincipalRef: valueOrEmpty(createdByPrincipalRef),
			CreatedByActor:        createdBySlug,
			CreatedByScopeRef:     createdByScopeRef,
			UpdatedBy:             updatedBy,
			UpdatedByPrincipalRef: valueOrEmpty(updatedByPrincipalRef),
		}

		// Load caused_by causal lineage (ordered friendly IDs). Always a non-nil
		// slice so the JSON projection carries `caused_by: []` when empty rather
		// than omitting the key.
		causedByIDs, err := store.CausedByIDs(database, taskUUID)
		if err != nil {
			return err
		}
		task.CausedBy = causedByIDs

		// Include comments by default (unless excluded)
		if !catExcludeComments {
			// Query non-deleted comments for this task
			rows, err := database.Query(`
				SELECT c.id, c.created_at, c.body, c.created_by_principal_ref,
				       a.slug as actor_slug, a.role as actor_role
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
				var principalRef, actorSlug, actorRole sql.NullString
				if err := rows.Scan(&comment.ID, &comment.CreatedAt, &comment.Body, &principalRef, &actorSlug, &actorRole); err != nil {
					_ = rows.Close()
					return fmt.Errorf("failed to scan comment: %w", err)
				}
				if principalRef.Valid {
					comment.PrincipalRef = principalRef.String
					comment.ActorSlug = attribution.PrincipalHandle(principalRef.String)
				}
				if actorSlug.Valid && comment.ActorSlug == "" {
					comment.ActorSlug = actorSlug.String
				}
				if actorRole.Valid {
					comment.ActorRole = actorRole.String
				}
				comments = append(comments, comment)
			}
			_ = rows.Close()

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
			       COALESCE(r.created_by_principal_ref, a.id, '') AS created_by_id
			FROM task_relations r
			JOIN tasks t ON r.to_task_uuid = t.uuid
			LEFT JOIN actors a ON r.created_by_actor_uuid = a.uuid
			WHERE r.from_task_uuid = ?
			ORDER BY r.kind, t.id
		`, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to query outgoing relations: %w", err)
		}

		for outgoingRows.Next() {
			var rel Relation
			if err := outgoingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				_ = outgoingRows.Close()
				return fmt.Errorf("failed to scan relation: %w", err)
			}
			rel.Direction = "outgoing"
			relations = append(relations, rel)
		}
		_ = outgoingRows.Close()

		// Get incoming relations (other tasks -> this task)
		incomingRows, err := database.Query(`
			SELECT r.kind, r.created_at,
			       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
			       COALESCE(r.created_by_principal_ref, a.id, '') AS created_by_id
			FROM task_relations r
			JOIN tasks t ON r.from_task_uuid = t.uuid
			LEFT JOIN actors a ON r.created_by_actor_uuid = a.uuid
			WHERE r.to_task_uuid = ?
			ORDER BY r.kind, t.id
		`, taskUUID)
		if err != nil {
			return fmt.Errorf("failed to query incoming relations: %w", err)
		}

		for incomingRows.Next() {
			var rel Relation
			if err := incomingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				_ = incomingRows.Close()
				return fmt.Errorf("failed to scan relation: %w", err)
			}
			rel.Direction = "incoming"
			relations = append(relations, rel)
		}
		_ = incomingRows.Close()

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

		// The styled card renders on an interactive TTY by default, or whenever
		// --pretty forces it (which also overrides an explicit machine mode and
		// the non-TTY JSON default). Color still follows style.ColorEnabled, so a
		// non-TTY --pretty card is plain text — deterministic and byte-comparable.
		styled := catPretty || (sel.Mode == outputModeRaw && !sel.Stable && style.ColorEnabled)
		if !styled && (sel.Mode == outputModeJSON || sel.Mode == outputModeNDJSON) {
			tasks = append(tasks, task)
		} else if styled {
			// Pipes without --pretty (and --porcelain) fall through to the
			// byte-stable raw markdown branch below.
			if taskCount > 0 {
				fmt.Fprintln(cmd.OutOrStdout())
			}
			taskCount++
			var styledComments []style.StyledComment
			if !catExcludeComments {
				for _, c := range task.Comments {
					styledComments = append(styledComments, style.StyledComment{
						ID:        c.ID,
						CreatedAt: c.CreatedAt,
						Actor:     c.ActorSlug,
						Role:      c.ActorRole,
						Body:      c.Body,
					})
				}
			}
			style.RenderStyledTask(cmd.OutOrStdout(), style.StyledTask{
				ID:            task.ID,
				Path:          task.Path,
				Title:         task.Title,
				State:         task.State,
				Priority:      task.Priority,
				Assignee:      task.AssigneeSlug,
				Labels:        task.Labels,
				DueAt:         task.DueAt,
				UpdatedAt:     task.UpdatedAt,
				BlockedCount:  len(task.BlockedBy),
				Description:   task.Description,
				Specification: task.Specification,
				NoFrontmatter: catNoFrontmatter,
			}, styledComments)
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
				fmt.Fprintf(cmd.OutOrStdout(), "artifact_dir: %s\n", task.ArtifactDir)
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
				if task.AssigneePrincipalRef != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "assignee_principal_ref: %s\n", *task.AssigneePrincipalRef)
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
				if len(task.CausedBy) > 0 {
					fmt.Fprintf(cmd.OutOrStdout(), "caused_by: [%s]\n", strings.Join(task.CausedBy, ", "))
				}
				fmt.Fprintf(cmd.OutOrStdout(), "meta: %s\n", metaValue)
				if task.Specification != "" {
					fmt.Fprintln(cmd.OutOrStdout(), "specification: |")
					for _, line := range strings.Split(task.Specification, "\n") {
						fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", line)
					}
				}
				if task.AcknowledgedAt != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "acknowledged_at: %s\n", *task.AcknowledgedAt)
				}
				if task.Resolution != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "resolution: %s\n", *task.Resolution)
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
				if task.CreatedByPrincipalRef != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "created_by_principal_ref: %s\n", task.CreatedByPrincipalRef)
				}
				if task.CreatedByScopeRef != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "created_by_scope_ref: %s\n", *task.CreatedByScopeRef)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "updated_by: %s\n", task.UpdatedBy)
				if task.UpdatedByPrincipalRef != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "updated_by_principal_ref: %s\n", task.UpdatedByPrincipalRef)
				}
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

	// Output JSON if requested. --pretty forces the styled card above and leaves
	// tasks empty, so it must not fall through to a trailing `null`.
	if !catPretty && sel.Mode == outputModeJSON {
		return writeJSONOutput(cmd.OutOrStdout(), sel, tasks)
	}

	if !catPretty && sel.Mode == outputModeNDJSON {
		return writeNDJSONOutput(cmd.OutOrStdout(), tasks)
	}

	return nil
}
