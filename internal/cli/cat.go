package cli

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/spf13/cobra"
)

var catCmd = &cobra.Command{
	Use:   "cat <path|id>...",
	Short: "Print tasks as markdown",
	Long: `Prints one or more tasks as markdown with YAML front matter.
If the argument resolves to a container, exits with error code 2.`,
	Args: cobra.MinimumNArgs(1),
	RunE: runCat,
}

var (
	catNoFrontmatter bool
)

func init() {
	rootCmd.AddCommand(catCmd)
	catCmd.Flags().BoolVar(&catNoFrontmatter, "no-frontmatter", false, "Print body only without front matter")
}

func runCat(cmd *cobra.Command, args []string) error {
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

	// Process each argument
	for i, arg := range args {
		if i > 0 {
			fmt.Fprintln(cmd.OutOrStdout())
		}

		taskUUID, _, err := resolveTask(database, arg)
		if err != nil {
			return err
		}

		// Get task details
		var id, slug, title, state, body string
		var priority int
		var startAt, dueAt, labels, completedAt, archivedAt *string
		var createdAt, updatedAt string
		var etag int64
		var projectUUID, createdByUUID, updatedByUUID string

		err = database.QueryRow(`
			SELECT id, slug, title, project_uuid, state, priority,
			       start_at, due_at, labels, body, etag,
			       created_at, updated_at, completed_at, archived_at,
			       created_by_actor_uuid, updated_by_actor_uuid
			FROM tasks WHERE uuid = ?
		`, taskUUID).Scan(
			&id, &slug, &title, &projectUUID, &state, &priority,
			&startAt, &dueAt, &labels, &body, &etag,
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

		if !catNoFrontmatter {
			// Print YAML front matter
			fmt.Fprintln(cmd.OutOrStdout(), "---")
			fmt.Fprintf(cmd.OutOrStdout(), "id: %s\n", id)
			fmt.Fprintf(cmd.OutOrStdout(), "uuid: %s\n", taskUUID)
			fmt.Fprintf(cmd.OutOrStdout(), "project_id: %s\n", projectID)
			fmt.Fprintf(cmd.OutOrStdout(), "project_uuid: %s\n", projectUUID)
			fmt.Fprintf(cmd.OutOrStdout(), "slug: %s\n", slug)
			fmt.Fprintf(cmd.OutOrStdout(), "title: %s\n", title)
			fmt.Fprintf(cmd.OutOrStdout(), "state: %s\n", state)
			fmt.Fprintf(cmd.OutOrStdout(), "priority: %d\n", priority)
			if startAt != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "start_at: %s\n", *startAt)
			}
			if dueAt != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "due_at: %s\n", *dueAt)
			}
			if labels != nil && *labels != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "labels: %s\n", *labels)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "etag: %d\n", etag)
			fmt.Fprintf(cmd.OutOrStdout(), "created_at: %s\n", createdAt)
			fmt.Fprintf(cmd.OutOrStdout(), "updated_at: %s\n", updatedAt)
			if completedAt != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "completed_at: %s\n", *completedAt)
			}
			if archivedAt != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "archived_at: %s\n", *archivedAt)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created_by: %s\n", createdBySlug)
			fmt.Fprintf(cmd.OutOrStdout(), "updated_by: %s\n", updatedBySlug)
			fmt.Fprintln(cmd.OutOrStdout(), "---")
			fmt.Fprintln(cmd.OutOrStdout())
		}

		// Print body
		fmt.Fprintln(cmd.OutOrStdout(), body)
	}

	return nil
}

func resolveTask(database *db.DB, arg string) (string, string, error) {
	// Try as friendly ID
	if strings.HasPrefix(arg, "T-") {
		var uuid string
		err := database.QueryRow("SELECT uuid FROM tasks WHERE id = ?", arg).Scan(&uuid)
		if err == nil {
			return uuid, arg, nil
		}
	}

	// Try as UUID
	if len(arg) == 36 && strings.Count(arg, "-") == 4 {
		var uuid string
		err := database.QueryRow("SELECT uuid FROM tasks WHERE uuid = ?", arg).Scan(&uuid)
		if err == nil {
			return uuid, arg, nil
		}
	}

	// Try as path
	segments := paths.SplitPath(arg)
	if len(segments) == 0 {
		return "", "", fmt.Errorf("invalid path: %s", arg)
	}

	// Navigate to parent container
	var parentUUID *string
	for i, segment := range segments[:len(segments)-1] {
		slug, err := paths.NormalizeSlug(segment)
		if err != nil {
			return "", "", fmt.Errorf("invalid slug %q: %w", segment, err)
		}

		query := `SELECT uuid FROM containers WHERE slug = ? AND `
		args := []interface{}{slug}
		if parentUUID == nil {
			query += `parent_uuid IS NULL`
		} else {
			query += `parent_uuid = ?`
			args = append(args, *parentUUID)
		}

		var uuid string
		err = database.QueryRow(query, args...).Scan(&uuid)
		if err != nil {
			return "", "", fmt.Errorf("container not found: %s", paths.JoinPath(segments[:i+1]...))
		}
		parentUUID = &uuid
	}

	// Get final segment as task
	taskSlug := segments[len(segments)-1]
	normalizedSlug, err := paths.NormalizeSlug(taskSlug)
	if err != nil {
		return "", "", fmt.Errorf("invalid task slug %q: %w", taskSlug, err)
	}

	// Find task
	var taskUUID string
	if parentUUID == nil {
		// Try to find in any root container
		err = database.QueryRow(`
			SELECT uuid FROM tasks WHERE slug = ? AND project_uuid IN (
				SELECT uuid FROM containers WHERE parent_uuid IS NULL
			) LIMIT 1
		`, normalizedSlug).Scan(&taskUUID)
	} else {
		err = database.QueryRow(`
			SELECT uuid FROM tasks WHERE slug = ? AND project_uuid = ?
		`, normalizedSlug, *parentUUID).Scan(&taskUUID)
	}

	if err != nil {
		// Check if it's a container instead
		var containerUUID string
		query := `SELECT uuid FROM containers WHERE slug = ? AND `
		args := []interface{}{normalizedSlug}
		if parentUUID == nil {
			query += `parent_uuid IS NULL`
		} else {
			query += `parent_uuid = ?`
			args = append(args, *parentUUID)
		}

		err2 := database.QueryRow(query, args...).Scan(&containerUUID)
		if err2 == nil {
			// It's a container, not a task
			return "", "", &UsageError{Message: fmt.Sprintf("cat only supports tasks; got container `%s`", arg)}
		}

		return "", "", fmt.Errorf("task not found: %s", arg)
	}

	return taskUUID, arg, nil
}

// UsageError represents a usage error (exit code 2)
type UsageError struct {
	Message string
}

func (e *UsageError) Error() string {
	return e.Message
}
