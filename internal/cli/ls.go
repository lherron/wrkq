package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sort"
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
	Long:    `Lists direct child containers and tasks at the specified paths. Child containers include recursive task rollups.`,
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
	lsSort          string
	lsReverse       bool
)

type lsEntry struct {
	Type                 string  `json:"type"`
	ID                   string  `json:"id"`
	Slug                 string  `json:"slug"`
	Title                string  `json:"title,omitempty"`
	Path                 string  `json:"path"`
	CreatedAt            string  `json:"created_at"`
	UpdatedAt            string  `json:"updated_at"`
	State                string  `json:"state,omitempty"`
	Kind                 string  `json:"kind,omitempty"`
	TaskCount            *int    `json:"task_count,omitempty"`
	ActiveTaskCount      *int    `json:"active_task_count,omitempty"`
	RequestedByProjectID *string `json:"requested_by_project_id,omitempty"`
	AssignedProjectID    *string `json:"assigned_project_id,omitempty"`
	AcknowledgedAt       *string `json:"acknowledged_at,omitempty"`
	Resolution           *string `json:"resolution,omitempty"`
	CPProjectID          *string `json:"cp_project_id,omitempty"`
	CPWorkItemID         *string `json:"cp_work_item_id,omitempty"`
	CPRunID              *string `json:"cp_run_id,omitempty"`
	SessionID            *string `json:"session_id,omitempty"`
	RunStatus            *string `json:"run_status,omitempty"`
}

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
	lsCmd.Flags().StringVar(&lsSort, "sort", "slug", "Sort by field: slug, updated_at, created_at, id")
	lsCmd.Flags().BoolVar(&lsReverse, "reverse", false, "Reverse sort order")
}

func runLs(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	paths := applyProjectRootToPaths(app.Config, args, true)
	if len(paths) == 0 {
		paths = []string{""}
	}

	sortField, sortDescending, err := normalizeLsSort(lsSort, lsReverse)
	if err != nil {
		return err
	}

	// Build cursor pagination
	pag, err := cursor.Apply(lsCursor, cursor.ApplyOptions{
		SortFields: []string{sortField},
		Descending: []bool{sortDescending},
		IDField:    "id",
		Limit:      lsLimit,
	})
	if err != nil {
		return err
	}

	var entries []lsEntry
	var hasMore bool

	for _, path := range paths {
		if path == "" {
			// List root containers with SQL-based pagination
			if lsType == "" || lsType == "p" {
				query := `
					SELECT uuid, id, slug, title, kind, created_at, updated_at
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
				defer func() { _ = rows.Close() }()

				for rows.Next() {
					var uuid, id, slug, kind, createdAt, updatedAt string
					var title *string
					if err := rows.Scan(&uuid, &id, &slug, &title, &kind, &createdAt, &updatedAt); err != nil {
						return fmt.Errorf("failed to scan row: %w", err)
					}

					titleStr := slug
					if title != nil && *title != "" {
						titleStr = *title
					}

					taskCount, activeTaskCount, err := containerRollupCounts(database, uuid)
					if err != nil {
						return err
					}

					entries = append(entries, lsEntry{
						Type:            "container",
						ID:              id,
						Slug:            slug,
						Title:           titleStr,
						Path:            slug,
						CreatedAt:       createdAt,
						UpdatedAt:       updatedAt,
						Kind:            kind,
						TaskCount:       &taskCount,
						ActiveTaskCount: &activeTaskCount,
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
			var slug, title, createdAt, updatedAt, state, kind string
			var requestedBy, assignedProject, acknowledgedAt, resolution *string
			var cpProjectID, cpWorkItemID, cpRunID, cpSessionID, runStatus *string
			err = database.QueryRow(`
				SELECT slug, title, created_at, updated_at, state, kind, requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
				       cp_project_id, cp_work_item_id, cp_run_id, cp_session_id, run_status
				FROM tasks WHERE uuid = ?
			`, taskUUID).Scan(&slug, &title, &createdAt, &updatedAt, &state, &kind, &requestedBy, &assignedProject, &acknowledgedAt, &resolution,
				&cpProjectID, &cpWorkItemID, &cpRunID, &cpSessionID, &runStatus)
			if err != nil {
				return fmt.Errorf("failed to get task: %w", err)
			}

			entries = append(entries, lsEntry{
				Type:                 "task",
				ID:                   taskID,
				Slug:                 slug,
				Title:                title,
				Path:                 path,
				CreatedAt:            createdAt,
				UpdatedAt:            updatedAt,
				State:                state,
				Kind:                 kind,
				RequestedByProjectID: requestedBy,
				AssignedProjectID:    assignedProject,
				AcknowledgedAt:       acknowledgedAt,
				Resolution:           resolution,
				CPProjectID:          cpProjectID,
				CPWorkItemID:         cpWorkItemID,
				CPRunID:              cpRunID,
				SessionID:            cpSessionID,
				RunStatus:            runStatus,
			})
		}

		// If we found a container, list its children with SQL-based pagination
		if foundContainer {
			// List child containers
			if lsType == "" || lsType == "p" {
				query := `
					SELECT uuid, id, slug, title, kind, created_at, updated_at
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
					var uuid, id, slug, kind, createdAt, updatedAt string
					var title *string
					if err := rows.Scan(&uuid, &id, &slug, &title, &kind, &createdAt, &updatedAt); err != nil {
						_ = rows.Close()
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

					taskCount, activeTaskCount, err := containerRollupCounts(database, uuid)
					if err != nil {
						_ = rows.Close()
						return err
					}

					entries = append(entries, lsEntry{
						Type:            "container",
						ID:              id,
						Slug:            slug,
						Title:           titleStr,
						Path:            childPath,
						CreatedAt:       createdAt,
						UpdatedAt:       updatedAt,
						Kind:            kind,
						TaskCount:       &taskCount,
						ActiveTaskCount: &activeTaskCount,
					})
				}
				_ = rows.Close()
			}

			// List tasks
			if lsType == "" || lsType == "t" {
				query := `
					SELECT id, slug, title, created_at, updated_at, state, kind,
					       requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
					       cp_project_id, cp_work_item_id, cp_run_id, cp_session_id, run_status
					FROM tasks
					WHERE project_uuid = ?
				`
				queryArgs := []interface{}{containerUUID}

				// Show only draft and open by default
				if !lsIncludeHidden {
					query += ` AND state IN ('draft', 'open')`
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
					var id, slug, title, createdAt, updatedAt, state, kind string
					var requestedBy, assignedProject, acknowledgedAt, resolution *string
					var cpProjectID, cpWorkItemID, cpRunID, cpSessionID, runStatus *string
					if err := rows.Scan(&id, &slug, &title, &createdAt, &updatedAt, &state, &kind, &requestedBy, &assignedProject, &acknowledgedAt, &resolution,
						&cpProjectID, &cpWorkItemID, &cpRunID, &cpSessionID, &runStatus); err != nil {
						_ = rows.Close()
						return fmt.Errorf("failed to scan row: %w", err)
					}

					taskPath := path
					if taskPath != "" {
						taskPath += "/"
					}
					taskPath += slug

					entries = append(entries, lsEntry{
						Type:                 "task",
						ID:                   id,
						Slug:                 slug,
						Title:                title,
						Path:                 taskPath,
						CreatedAt:            createdAt,
						UpdatedAt:            updatedAt,
						State:                state,
						Kind:                 kind,
						RequestedByProjectID: requestedBy,
						AssignedProjectID:    assignedProject,
						AcknowledgedAt:       acknowledgedAt,
						Resolution:           resolution,
						CPProjectID:          cpProjectID,
						CPWorkItemID:         cpWorkItemID,
						CPRunID:              cpRunID,
						SessionID:            cpSessionID,
						RunStatus:            runStatus,
					})
				}
				_ = rows.Close()
			}
		}
	}

	sortLsEntries(entries, sortField, sortDescending)

	// Check if there are more results (we requested limit+1)
	if lsLimit > 0 && len(entries) > lsLimit {
		hasMore = true
		entries = entries[:lsLimit]
	}

	// Generate next cursor if there are more results
	var nextCursorStr string
	if hasMore && len(entries) > 0 {
		lastEntry := entries[len(entries)-1]
		lastValue := lsEntrySortValue(lastEntry, sortField)
		nextCursorStr, _ = cursor.BuildNextCursor(
			[]string{sortField},
			[]interface{}{lastValue},
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
	headers := []string{"Type", "ID", "Slug", "Title", "State", "Kind", "Tasks", "CreatedAt", "UpdatedAt"}
	var rowsData [][]string
	for _, entry := range entries {
		typeStr := "container"
		slug := entry.Slug
		title := entry.Title
		tasks := ""
		if entry.Type == "task" {
			typeStr = "task"
		} else {
			slug += "/"
			tasks = formatContainerRollup(entry)
			if entry.Kind != "" && tasks != "" {
				title = fmt.Sprintf("[%s] %s", entry.Kind, tasks)
			} else if title == entry.Slug {
				title = ""
			}
		}

		rowsData = append(rowsData, []string{
			typeStr,
			entry.ID,
			slug,
			title,
			entry.State,
			entry.Kind,
			tasks,
			entry.CreatedAt,
			entry.UpdatedAt,
		})
	}

	r := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: lsPorcelain,
	})

	return r.RenderTable(headers, rowsData)
}

func containerRollupCounts(database interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}, containerUUID string) (int, int, error) {
	var taskCount, activeTaskCount int
	err := database.QueryRow(`
		WITH RECURSIVE descendants(uuid) AS (
			SELECT uuid FROM containers WHERE uuid = ?
			UNION ALL
			SELECT c.uuid
			  FROM containers c
			  JOIN descendants d ON c.parent_uuid = d.uuid
		)
		SELECT
			COUNT(t.uuid),
			COALESCE(SUM(CASE
				WHEN t.state IN ('draft', 'open', 'in_progress', 'blocked')
				 AND t.archived_at IS NULL
				 AND t.deleted_at IS NULL
				THEN 1 ELSE 0 END), 0)
		  FROM descendants d
		  LEFT JOIN tasks t ON t.project_uuid = d.uuid
	`, containerUUID).Scan(&taskCount, &activeTaskCount)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to count container rollup tasks: %w", err)
	}
	return taskCount, activeTaskCount, nil
}

func formatContainerRollup(entry lsEntry) string {
	if entry.TaskCount == nil || entry.ActiveTaskCount == nil {
		return ""
	}
	taskLabel := "tasks"
	if *entry.TaskCount == 1 {
		taskLabel = "task"
	}
	return fmt.Sprintf("%d %s (%d active)", *entry.TaskCount, taskLabel, *entry.ActiveTaskCount)
}

func normalizeLsSort(field string, reverse bool) (string, bool, error) {
	field = strings.TrimSpace(field)
	if field == "" {
		field = "slug"
	}

	switch field {
	case "slug", "updated_at", "created_at", "id":
	default:
		return "", false, fmt.Errorf("invalid --sort %q: choose slug, updated_at, created_at, or id", field)
	}

	return field, reverse, nil
}

func sortLsEntries(entries []lsEntry, field string, descending bool) {
	sort.SliceStable(entries, func(i, j int) bool {
		left := lsEntrySortValue(entries[i], field)
		right := lsEntrySortValue(entries[j], field)
		if left == right {
			if descending {
				return entries[i].ID > entries[j].ID
			}
			return entries[i].ID < entries[j].ID
		}
		if descending {
			return left > right
		}
		return left < right
	})
}

func lsEntrySortValue(entry lsEntry, field string) string {
	switch field {
	case "id":
		return entry.ID
	case "created_at":
		return entry.CreatedAt
	case "updated_at":
		return entry.UpdatedAt
	default:
		return entry.Slug
	}
}
