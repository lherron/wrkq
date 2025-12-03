package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var catCmd = &cobra.Command{
	Use:   "cat <path|id>...",
	Short: "Print tasks as markdown",
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

	type Task struct {
		ID          string     `json:"id"`
		UUID        string     `json:"uuid"`
		ProjectID   string     `json:"project_id"`
		ProjectUUID string     `json:"project_uuid"`
		Slug        string     `json:"slug"`
		Title       string     `json:"title"`
		State       string     `json:"state"`
		Priority    int        `json:"priority"`
		StartAt     *string    `json:"start_at,omitempty"`
		DueAt       *string    `json:"due_at,omitempty"`
		Labels      *string    `json:"labels,omitempty"`
		Description string     `json:"description"`
		Etag        int64      `json:"etag"`
		CreatedAt   string     `json:"created_at"`
		UpdatedAt   string     `json:"updated_at"`
		CompletedAt *string    `json:"completed_at,omitempty"`
		ArchivedAt  *string    `json:"archived_at,omitempty"`
		CreatedBy   string     `json:"created_by"`
		UpdatedBy   string     `json:"updated_by"`
		Comments    []Comment  `json:"comments,omitempty"`
	}

	var tasks []Task
	taskCount := 0

	// Process each argument
	for _, arg := range args {
		taskUUID, _, err := selectors.ResolveTask(database, arg)
		if err != nil {
			return err
		}

		// Get task details
		var id, slug, title, state, description string
		var priority int
		var startAt, dueAt, labels, completedAt, archivedAt *string
		var createdAt, updatedAt string
		var etag int64
		var projectUUID, createdByUUID, updatedByUUID string

		err = database.QueryRow(`
			SELECT id, slug, title, project_uuid, state, priority,
			       start_at, due_at, labels, description, etag,
			       created_at, updated_at, completed_at, archived_at,
			       created_by_actor_uuid, updated_by_actor_uuid
			FROM tasks WHERE uuid = ?
		`, taskUUID).Scan(
			&id, &slug, &title, &projectUUID, &state, &priority,
			&startAt, &dueAt, &labels, &description, &etag,
			&createdAt, &updatedAt, &completedAt, &archivedAt,
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

		task := Task{
			ID:          id,
			UUID:        taskUUID,
			ProjectID:   projectID,
			ProjectUUID: projectUUID,
			Slug:        slug,
			Title:       title,
			State:       state,
			Priority:    priority,
			StartAt:     startAt,
			DueAt:       dueAt,
			Labels:      labels,
			Description: description,
			Etag:        etag,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			CompletedAt: completedAt,
			ArchivedAt:  archivedAt,
			CreatedBy:   createdBySlug,
			UpdatedBy:   updatedBySlug,
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
				fmt.Fprintf(cmd.OutOrStdout(), "project_id: %s\n", task.ProjectID)
				fmt.Fprintf(cmd.OutOrStdout(), "project_uuid: %s\n", task.ProjectUUID)
				fmt.Fprintf(cmd.OutOrStdout(), "slug: %s\n", task.Slug)
				fmt.Fprintf(cmd.OutOrStdout(), "title: %s\n", task.Title)
				fmt.Fprintf(cmd.OutOrStdout(), "state: %s\n", task.State)
				fmt.Fprintf(cmd.OutOrStdout(), "priority: %d\n", task.Priority)
				if task.StartAt != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "start_at: %s\n", *task.StartAt)
				}
				if task.DueAt != nil {
					fmt.Fprintf(cmd.OutOrStdout(), "due_at: %s\n", *task.DueAt)
				}
				if task.Labels != nil && *task.Labels != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "labels: %s\n", *task.Labels)
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

// UsageError represents a usage error (exit code 2)
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	return e.Message
}
