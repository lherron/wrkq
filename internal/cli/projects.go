package cli

import (
	"fmt"
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
		Type  string  `json:"type"`
		ID    string  `json:"id"`
		Slug  string  `json:"slug"`
		Title string  `json:"title,omitempty"`
		Path  string  `json:"path"`
		Root  *string `json:"root"`
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

	// Query all top-level project containers (direct children of the root)
	query := `
		SELECT uuid, id, slug, title, root
		FROM containers
		WHERE parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')
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
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uuid, id, slug string
		var title, root *string
		if err := rows.Scan(&uuid, &id, &slug, &title, &root); err != nil {
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
			Root:  root,
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

	sel, err := resolveOutputMode(cmd, app.Config, outputShapeList, outputResolveOptions{
		Allow:      []outputMode{outputModeTable, outputModeHuman, outputModeJSON, outputModeNDJSON, outputModeYAML, outputModeTSV},
		DefaultTTY: outputModeTable,
	})
	if err != nil {
		return err
	}
	if cmd.Flag("json") == nil {
		switch {
		case projectsJSON && projectsNDJSON:
			return fmt.Errorf("choose only one output mode")
		case projectsJSON:
			sel = outputSelection{Mode: outputModeJSON, Stable: projectsPorcelain}
		case projectsNDJSON:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: projectsPorcelain}
		case projectsPorcelain:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: true}
		default:
			sel = outputSelection{Mode: outputModeTable}
		}
	}

	if sel.Stable && nextCursorStr != "" {
		fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", nextCursorStr)
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

	headers := []string{"ID", "Slug", "Title"}
	var rowsData [][]string
	for _, project := range projects {
		rowsData = append(rowsData, []string{
			project.ID,
			project.Slug,
			project.Title,
		})
	}

	switch sel.Mode {
	case outputModeJSON:
		return writeJSONOutput(cmd.OutOrStdout(), sel, projects)
	case outputModeNDJSON:
		return writeNDJSONOutput(cmd.OutOrStdout(), projects)
	case outputModeYAML:
		return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatYAML}).RenderYAML(projects)
	case outputModeTSV:
		return render.NewRenderer(cmd.OutOrStdout(), render.Options{Format: render.FormatTSV}).RenderTSV(headers, rowsData)
	}

	r := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: sel.Stable,
	})

	return r.RenderTable(headers, rowsData)
}
