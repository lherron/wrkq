package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/render"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:     "ls [path...]",
	Aliases: []string{"list"},
	Short:   "List containers and tasks",
	Long:    `Lists containers (projects/subprojects) and tasks at the specified paths.`,
	RunE:    appctx.WithApp(appctx.DefaultOptions(), runLs),
}

var (
	lsJSON          bool
	lsNDJSON        bool
	lsPorcelain     bool
	lsRecursive     bool
	lsType          string
	lsOne           bool
	lsNul           bool
	lsLimit         int
	lsCursor        string
	lsIncludeHidden bool
)

func init() {
	rootCmd.AddCommand(lsCmd)

	lsCmd.Flags().BoolVar(&lsJSON, "json", false, "Output as JSON")
	lsCmd.Flags().BoolVar(&lsNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	lsCmd.Flags().BoolVar(&lsPorcelain, "porcelain", false, "Machine-readable output")
	lsCmd.Flags().BoolVarP(&lsRecursive, "recursive", "R", false, "List recursively")
	lsCmd.Flags().StringVar(&lsType, "type", "", "Filter by type (p=project, t=task)")
	lsCmd.Flags().BoolVarP(&lsOne, "one", "1", false, "One entry per line")
	lsCmd.Flags().BoolVarP(&lsNul, "nul", "0", false, "NUL-separated output")
	lsCmd.Flags().IntVar(&lsLimit, "limit", 0, "Maximum number of results to return (0 = no limit)")
	lsCmd.Flags().StringVar(&lsCursor, "cursor", "", "Pagination cursor from previous page")
	lsCmd.Flags().BoolVarP(&lsIncludeHidden, "all", "a", false, "Include archived and deleted items")
}

func runLs(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	paths := applyProjectRootToPaths(app.Config, args, true)
	if len(paths) == 0 {
		paths = []string{""}
	}

	type Entry struct {
		Type                 string  `json:"type"`
		ID                   string  `json:"id"`
		Slug                 string  `json:"slug"`
		Title                string  `json:"title,omitempty"`
		Path                 string  `json:"path"`
		State                string  `json:"state,omitempty"`
		Kind                 string  `json:"kind,omitempty"`
		RequestedByProjectID *string `json:"requested_by_project_id,omitempty"`
		AssignedProjectID    *string `json:"assigned_project_id,omitempty"`
		AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`
		Resolution           *string `json:"resolution,omitempty"`
		CPProjectID          *string `json:"cp_project_id,omitempty"`
		CPWorkItemID         *string `json:"cp_work_item_id,omitempty"`
		CPRunID              *string `json:"cp_run_id,omitempty"`
		CPSessionID          *string `json:"cp_session_id,omitempty"`
		SDKSessionID         *string `json:"sdk_session_id,omitempty"`
		RunStatus            *string `json:"run_status,omitempty"`
	}

	// Build cursor pagination
	pag, err := cursor.Apply(lsCursor, cursor.ApplyOptions{
		SortFields: []string{"slug"},
		Descending: []bool{false}, // ASC
		IDField:    "id",
		Limit:      lsLimit,
	})
	if err != nil {
		return err
	}

	var entries []Entry
	var hasMore bool

	for _, path := range paths {
		if path == "" {
			// List root containers with SQL-based pagination
			if lsType == "" || lsType == "p" {
				query := `
					SELECT uuid, id, slug, title
					FROM containers
					WHERE parent_uuid IS NULL
				`
				queryArgs := []interface{}{}

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
					return fmt.Errorf("failed to query containers: %w", err)
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

					entries = append(entries, Entry{
						Type:  "container",
						ID:    id,
						Slug:  slug,
						Title: titleStr,
						Path:  slug,
					})
				}
			}
			continue
		}

		// Try to resolve as container first using shared helper
		containerUUID, _, err := selectors.WalkContainerPath(database, path)
		foundContainer := err == nil

		// If not found as container, try as task
		if !foundContainer {
			taskUUID, taskID, taskErr := selectors.ResolveTaskByPath(database, path)
			if taskErr != nil {
				// Neither container nor task found
				return fmt.Errorf("path not found: %s", path)
			}

			// Found as task - list this single task (no pagination needed)
			var slug, title, state, kind string
			var requestedBy, assignedProject, acknowledgedAt, resolution *string
			var cpProjectID, cpWorkItemID, cpRunID, cpSessionID, sdkSessionID, runStatus *string
			err = database.QueryRow(`
				SELECT slug, title, state, kind, requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
				       cp_project_id, cp_work_item_id, cp_run_id, cp_session_id, sdk_session_id, run_status
				FROM tasks WHERE uuid = ?
			`, taskUUID).Scan(&slug, &title, &state, &kind, &requestedBy, &assignedProject, &acknowledgedAt, &resolution,
				&cpProjectID, &cpWorkItemID, &cpRunID, &cpSessionID, &sdkSessionID, &runStatus)
			if err != nil {
				return fmt.Errorf("failed to get task: %w", err)
			}

			entries = append(entries, Entry{
				Type:                 "task",
				ID:                   taskID,
				Slug:                 slug,
				Title:                title,
				Path:                 path,
				State:                state,
				Kind:                 kind,
				RequestedByProjectID: requestedBy,
				AssignedProjectID:    assignedProject,
				AcknowledgedAt:       acknowledgedAt,
				Resolution:           resolution,
				CPProjectID:          cpProjectID,
				CPWorkItemID:         cpWorkItemID,
				CPRunID:              cpRunID,
				CPSessionID:          cpSessionID,
				SDKSessionID:         sdkSessionID,
				RunStatus:            runStatus,
			})
		}

		// If we found a container, list its children with SQL-based pagination
		if foundContainer {
			// List child containers
			if lsType == "" || lsType == "p" {
				query := `
					SELECT uuid, id, slug, title
					FROM containers
					WHERE parent_uuid = ?
				`
				queryArgs := []interface{}{containerUUID}

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
					return fmt.Errorf("failed to query containers: %w", err)
				}

				for rows.Next() {
					var uuid, id, slug string
					var title *string
					if err := rows.Scan(&uuid, &id, &slug, &title); err != nil {
						rows.Close()
						return fmt.Errorf("failed to scan row: %w", err)
					}

					titleStr := slug
					if title != nil && *title != "" {
						titleStr = *title
					}

					childPath := path
					if childPath != "" {
						childPath += "/"
					}
					childPath += slug

					entries = append(entries, Entry{
						Type:  "container",
						ID:    id,
						Slug:  slug,
						Title: titleStr,
						Path:  childPath,
					})
				}
				rows.Close()
			}

			// List tasks
			if lsType == "" || lsType == "t" {
				query := `
					SELECT id, slug, title, state, kind,
					       requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
					       cp_project_id, cp_work_item_id, cp_run_id, cp_session_id, sdk_session_id, run_status
					FROM tasks
					WHERE project_uuid = ?
				`
				queryArgs := []interface{}{containerUUID}

				// Filter out archived and deleted by default
				if !lsIncludeHidden {
					query += ` AND state NOT IN ('archived', 'deleted')`
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
					return fmt.Errorf("failed to query tasks: %w", err)
				}

				for rows.Next() {
					var id, slug, title, state, kind string
					var requestedBy, assignedProject, acknowledgedAt, resolution *string
					var cpProjectID, cpWorkItemID, cpRunID, cpSessionID, sdkSessionID, runStatus *string
					if err := rows.Scan(&id, &slug, &title, &state, &kind, &requestedBy, &assignedProject, &acknowledgedAt, &resolution,
						&cpProjectID, &cpWorkItemID, &cpRunID, &cpSessionID, &sdkSessionID, &runStatus); err != nil {
						rows.Close()
						return fmt.Errorf("failed to scan row: %w", err)
					}

					taskPath := path
					if taskPath != "" {
						taskPath += "/"
					}
					taskPath += slug

					entries = append(entries, Entry{
						Type:                 "task",
						ID:                   id,
						Slug:                 slug,
						Title:                title,
						Path:                 taskPath,
						State:                state,
						Kind:                 kind,
						RequestedByProjectID: requestedBy,
						AssignedProjectID:    assignedProject,
						AcknowledgedAt:       acknowledgedAt,
						Resolution:           resolution,
						CPProjectID:          cpProjectID,
						CPWorkItemID:         cpWorkItemID,
						CPRunID:              cpRunID,
						CPSessionID:          cpSessionID,
						SDKSessionID:         sdkSessionID,
						RunStatus:            runStatus,
					})
				}
				rows.Close()
			}
		}
	}

	// Check if there are more results (we requested limit+1)
	if lsLimit > 0 && len(entries) > lsLimit {
		hasMore = true
		entries = entries[:lsLimit]
	}

	// Generate next cursor if there are more results
	var nextCursorStr string
	if hasMore && len(entries) > 0 {
		lastEntry := entries[len(entries)-1]
		nextCursorStr, _ = cursor.BuildNextCursor(
			[]string{"slug"},
			[]interface{}{lastEntry.Slug},
			lastEntry.ID,
		)
	}

	// Output next_cursor to stderr in porcelain mode
	if lsPorcelain && nextCursorStr != "" {
		fmt.Fprintf(os.Stderr, "next_cursor=%s\n", nextCursorStr)
	}

	// Render output
	if lsJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if !lsPorcelain {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(entries)
	}

	if lsNDJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, entry := range entries {
			if err := encoder.Encode(entry); err != nil {
				return err
			}
		}
		return nil
	}

	if lsOne || lsNul {
		var paths []string
		for _, entry := range entries {
			paths = append(paths, entry.Path)
		}
		delimiter := "\n"
		if lsNul {
			delimiter = "\x00"
		}
		fmt.Fprint(cmd.OutOrStdout(), strings.Join(paths, delimiter))
		if len(paths) > 0 && !lsNul {
			fmt.Fprintln(cmd.OutOrStdout())
		}
		return nil
	}

	// Table output
	headers := []string{"Type", "ID", "Slug", "Title", "State", "Kind"}
	var rowsData [][]string
	for _, entry := range entries {
		typeStr := "project"
		if entry.Type == "task" {
			typeStr = "task"
		}

		rowsData = append(rowsData, []string{
			typeStr,
			entry.ID,
			entry.Slug,
			entry.Title,
			entry.State,
			entry.Kind,
		})
	}

	r := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: lsPorcelain,
	})

	return r.RenderTable(headers, rowsData)
}
