package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var projectsCmd = &cobra.Command{
	Use:   "projects",
	Short: "List all top-level projects",
	Long: `Lists all top-level projects (containers with no parent) in the database.

This command always shows all projects regardless of WRKQ_PROJECT_ROOT,
making it easy to see what projects exist when working in a scoped context.

Examples:
  wrkq projects              # List all projects in table format
  wrkq projects --json       # Output as JSON
  wrkq projects -1           # One project slug per line
  wrkq projects -a           # Include archived projects`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runProjects),
}

var (
	projectsJSON            bool
	projectsNDJSON          bool
	projectsPorcelain       bool
	projectsOne             bool
	projectsNul             bool
	projectsLimit           int
	projectsCursor          string
	projectsIncludeArchived bool
)

func init() {
	rootCmd.AddCommand(projectsCmd)

	projectsCmd.Flags().BoolVar(&projectsJSON, "json", false, "Output as JSON")
	projectsCmd.Flags().BoolVar(&projectsNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	projectsCmd.Flags().BoolVar(&projectsPorcelain, "porcelain", false, "Machine-readable output")
	projectsCmd.Flags().BoolVarP(&projectsOne, "one", "1", false, "One entry per line")
	projectsCmd.Flags().BoolVarP(&projectsNul, "nul", "0", false, "NUL-separated output")
	projectsCmd.Flags().IntVar(&projectsLimit, "limit", 0, "Maximum number of results to return (0 = no limit)")
	projectsCmd.Flags().StringVar(&projectsCursor, "cursor", "", "Pagination cursor from previous page")
	projectsCmd.Flags().BoolVarP(&projectsIncludeArchived, "all", "a", false, "Include archived projects")
}

func runProjects(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// Note: We intentionally do NOT apply project root here.
	// This command always lists all top-level projects regardless of WRKQ_PROJECT_ROOT.

	type Project struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Slug  string `json:"slug"`
		Title string `json:"title,omitempty"`
		Path  string `json:"path"`
	}

	// Build cursor pagination
	pag, err := cursor.Apply(projectsCursor, cursor.ApplyOptions{
		SortFields: []string{"slug"},
		Descending: []bool{false}, // ASC
		IDField:    "id",
		Limit:      projectsLimit,
	})
	if err != nil {
		return err
	}

	var projects []Project
	var hasMore bool

	// Query all root containers (projects)
	query := `
		SELECT uuid, id, slug, title
		FROM containers
		WHERE parent_uuid IS NULL
	`
	queryArgs := []interface{}{}

	// Filter out archived by default
	if !projectsIncludeArchived {
		query += ` AND archived_at IS NULL`
	}

	// Add cursor WHERE clause if present
	if pag.WhereClause != "" {
		query += " AND " + pag.WhereClause
		queryArgs = append(queryArgs, pag.Params...)
	}

	// Add ORDER BY
	query += " " + pag.OrderByClause

	// Add LIMIT
	if pag.LimitClause != "" {
		query += " " + pag.LimitClause
		queryArgs = append(queryArgs, *pag.LimitParam)
	}

	rows, err := database.Query(query, queryArgs...)
	if err != nil {
		return fmt.Errorf("failed to query projects: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var uuid, id, slug string
		var title *string
		if err := rows.Scan(&uuid, &id, &slug, &title); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		titleStr := slug
		if title != nil && *title != "" {
			titleStr = *title
		}

		projects = append(projects, Project{
			Type:  "project",
			ID:    id,
			Slug:  slug,
			Title: titleStr,
			Path:  slug,
		})
	}

	// Check if there are more results (we requested limit+1)
	if projectsLimit > 0 && len(projects) > projectsLimit {
		hasMore = true
		projects = projects[:projectsLimit]
	}

	// Generate next cursor if there are more results
	var nextCursorStr string
	if hasMore && len(projects) > 0 {
		lastProject := projects[len(projects)-1]
		nextCursorStr, _ = cursor.BuildNextCursor(
			[]string{"slug"},
			[]interface{}{lastProject.Slug},
			lastProject.ID,
		)
	}

	// Output next_cursor to stderr in porcelain mode
	if projectsPorcelain && nextCursorStr != "" {
		fmt.Fprintf(os.Stderr, "next_cursor=%s\n", nextCursorStr)
	}

	// Render output
	if projectsJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if !projectsPorcelain {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(projects)
	}

	if projectsNDJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, project := range projects {
			if err := encoder.Encode(project); err != nil {
				return err
			}
		}
		return nil
	}

	if projectsOne || projectsNul {
		var slugs []string
		for _, project := range projects {
			slugs = append(slugs, project.Slug)
		}
		delimiter := "\n"
		if projectsNul {
			delimiter = "\x00"
		}
		fmt.Fprint(cmd.OutOrStdout(), strings.Join(slugs, delimiter))
		if len(slugs) > 0 && !projectsNul {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return nil
	}

	// Table output
	headers := []string{"ID", "Slug", "Title"}
	var rowsData [][]string
	for _, project := range projects {
		rowsData = append(rowsData, []string{
			project.ID,
			project.Slug,
			project.Title,
		})
	}

	r := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: projectsPorcelain,
	})

	return r.RenderTable(headers, rowsData)
}
