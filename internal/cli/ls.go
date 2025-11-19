package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var lsCmd = &cobra.Command{
	Use:   "ls [path...]",
	Short: "List containers and tasks",
	Long:  `Lists containers (projects/subprojects) and tasks at the specified paths.`,
	RunE:  runLs,
}

var (
	lsJSON      bool
	lsNDJSON    bool
	lsPorcelain bool
	lsRecursive bool
	lsType      string
	lsOne       bool
	lsNul       bool
	lsLimit     int
	lsCursor    string
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
}

func runLs(cmd *cobra.Command, args []string) error {
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

	// If no paths specified, list root
	if len(args) == 0 {
		args = []string{""}
	}

	type Entry struct {
		Type  string `json:"type"`
		ID    string `json:"id"`
		Slug  string `json:"slug"`
		Title string `json:"title,omitempty"`
		Path  string `json:"path"`
		State string `json:"state,omitempty"`
	}

	var entries []Entry

	for _, path := range args {
		if path == "" {
			// List root containers
			if lsType == "" || lsType == "p" {
				rows, err := database.Query(`
					SELECT uuid, id, slug, title
					FROM containers
					WHERE parent_uuid IS NULL
					ORDER BY slug
				`)
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

		// Resolve path to container or task
		segments := paths.SplitPath(path)
		if len(segments) == 0 {
			continue
		}

		// Find the container/task
		var parentUUID *string
		var foundContainer bool
		var containerUUID string

		for i, segment := range segments {
			slug, err := paths.NormalizeSlug(segment)
			if err != nil {
				return fmt.Errorf("invalid slug %q: %w", segment, err)
			}

			// Try to find as container
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
			if err == nil {
				foundContainer = true
				containerUUID = uuid
				parentUUID = &uuid
				continue
			}

			// If last segment and not found as container, try as task
			if i == len(segments)-1 {
				if parentUUID == nil {
					return fmt.Errorf("path not found: %s", path)
				}

				var taskUUID string
				err = database.QueryRow(`
					SELECT uuid FROM tasks WHERE slug = ? AND project_uuid = ?
				`, slug, *parentUUID).Scan(&taskUUID)
				if err != nil {
					return fmt.Errorf("path not found: %s", path)
				}

				// Found as task - list this single task
				var id, title, state string
				err = database.QueryRow(`
					SELECT id, slug, title, state FROM tasks WHERE uuid = ?
				`, taskUUID).Scan(&id, &slug, &title, &state)
				if err != nil {
					return fmt.Errorf("failed to get task: %w", err)
				}

				entries = append(entries, Entry{
					Type:  "task",
					ID:    id,
					Slug:  slug,
					Title: title,
					Path:  path,
					State: state,
				})

				foundContainer = false
				break
			}

			return fmt.Errorf("path not found: %s", paths.JoinPath(segments[:i+1]...))
		}

		// If we found a container, list its children
		if foundContainer {
			// List child containers
			if lsType == "" || lsType == "p" {
				rows, err := database.Query(`
					SELECT uuid, id, slug, title
					FROM containers
					WHERE parent_uuid = ?
					ORDER BY slug
				`, containerUUID)
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
				rows, err := database.Query(`
					SELECT id, slug, title, state
					FROM tasks
					WHERE project_uuid = ?
					ORDER BY slug
				`, containerUUID)
				if err != nil {
					return fmt.Errorf("failed to query tasks: %w", err)
				}

				for rows.Next() {
					var id, slug, title, state string
					if err := rows.Scan(&id, &slug, &title, &state); err != nil {
						rows.Close()
						return fmt.Errorf("failed to scan row: %w", err)
					}

					taskPath := path
					if taskPath != "" {
						taskPath += "/"
					}
					taskPath += slug

					entries = append(entries, Entry{
						Type:  "task",
						ID:    id,
						Slug:  slug,
						Title: title,
						Path:  taskPath,
						State: state,
					})
				}
				rows.Close()
			}
		}
	}

	// Apply pagination
	var currentCursor *cursor.Cursor
	if lsCursor != "" {
		c, err := cursor.Decode(lsCursor)
		if err != nil {
			return fmt.Errorf("invalid cursor: %w", err)
		}
		currentCursor = c

		// Filter entries based on cursor (slug-based pagination)
		// Since we sort by slug, we filter entries where slug > cursor.LastValues[0]
		var filtered []Entry
		for _, entry := range entries {
			if entry.Slug > currentCursor.LastValues[0].(string) ||
				(entry.Slug == currentCursor.LastValues[0].(string) && entry.ID > currentCursor.LastID) {
				filtered = append(filtered, entry)
			}
		}
		entries = filtered
	}

	// Apply limit and generate next cursor
	var nextCursor *cursor.Cursor
	if lsLimit > 0 && len(entries) > lsLimit {
		// Create cursor from the last entry we'll return
		lastEntry := entries[lsLimit-1]
		nextCursor, _ = cursor.NewCursor(
			[]string{"slug"},
			[]interface{}{lastEntry.Slug},
			lastEntry.ID,
		)
		entries = entries[:lsLimit]
	}

	// Output next_cursor to stderr in porcelain mode
	if lsPorcelain && nextCursor != nil {
		encoded, err := nextCursor.Encode()
		if err == nil {
			fmt.Fprintf(os.Stderr, "next_cursor=%s\n", encoded)
		}
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
	headers := []string{"Type", "ID", "Slug", "Title", "State"}
	var rows [][]string
	for _, entry := range entries {
		typeStr := "project"
		if entry.Type == "task" {
			typeStr = "task"
		}

		rows = append(rows, []string{
			typeStr,
			entry.ID,
			entry.Slug,
			entry.Title,
			entry.State,
		})
	}

	r := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: lsPorcelain,
	})

	return r.RenderTable(headers, rows)
}
