package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var statCmd = &cobra.Command{
	Use:   "stat <path|id>...",
	Short: "Print metadata for tasks or containers",
	Long:  `Displays metadata (machine-friendly) for one or more tasks or containers.`,
	Args:  cobra.MinimumNArgs(1),
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runStat),
}

var (
	statJSON bool
	statNul  bool
)

func init() {
	rootCmd.AddCommand(statCmd)
	statCmd.Flags().BoolVar(&statJSON, "json", false, "Output as JSON")
	statCmd.Flags().BoolVarP(&statNul, "nul", "0", false, "NUL-separated output")
}

func runStat(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	for i, arg := range args {
		args[i] = applyProjectRootToSelector(app.Config, arg, false)
	}

	type Metadata struct {
		Type     string                 `json:"type"`
		UUID     string                 `json:"uuid"`
		ID       string                 `json:"id"`
		Slug     string                 `json:"slug"`
		Title    string                 `json:"title,omitempty"`
		State    string                 `json:"state,omitempty"`
		Priority int                    `json:"priority,omitempty"`
		ETag     int64                  `json:"etag"`
		Extra    map[string]interface{} `json:"extra,omitempty"`
	}

	results := []Metadata{}

	for _, arg := range args {
		// Try to resolve as task first
		taskUUID, _, err := selectors.ResolveTask(database, arg)
		if err == nil {
			// It's a task
			var id, slug, title, state string
			var priority int
			var etag int64

			err := database.QueryRow(`
				SELECT id, slug, title, state, priority, etag
				FROM tasks WHERE uuid = ?
			`, taskUUID).Scan(&id, &slug, &title, &state, &priority, &etag)
			if err != nil {
				return fmt.Errorf("failed to get task metadata: %w", err)
			}

			results = append(results, Metadata{
				Type:     "task",
				UUID:     taskUUID,
				ID:       id,
				Slug:     slug,
				Title:    title,
				State:    state,
				Priority: priority,
				ETag:     etag,
			})
			continue
		}

		// Try as container
		containerUUID, _, err := selectors.ResolveContainer(database, arg)
		if err != nil {
			return fmt.Errorf("path not found: %s", arg)
		}

		var id, slug string
		var title *string
		var etag int64

		err = database.QueryRow(`
			SELECT id, slug, title, etag
			FROM containers WHERE uuid = ?
		`, containerUUID).Scan(&id, &slug, &title, &etag)
		if err != nil {
			return fmt.Errorf("failed to get container metadata: %w", err)
		}

		titleStr := ""
		if title != nil {
			titleStr = *title
		}

		results = append(results, Metadata{
			Type:  "container",
			UUID:  containerUUID,
			ID:    id,
			Slug:  slug,
			Title: titleStr,
			ETag:  etag,
		})
	}

	// Output
	if statJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(results)
	}

	// Simple text output
	for _, meta := range results {
		fmt.Fprintf(cmd.OutOrStdout(), "type: %s\n", meta.Type)
		fmt.Fprintf(cmd.OutOrStdout(), "uuid: %s\n", meta.UUID)
		fmt.Fprintf(cmd.OutOrStdout(), "id: %s\n", meta.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "slug: %s\n", meta.Slug)
		if meta.Title != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "title: %s\n", meta.Title)
		}
		if meta.State != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "state: %s\n", meta.State)
		}
		if meta.Priority > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "priority: %d\n", meta.Priority)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "etag: %d\n", meta.ETag)
		fmt.Fprintln(cmd.OutOrStdout())
	}

	return nil
}
