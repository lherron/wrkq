package cli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var renameContainerCmd = &cobra.Command{
	Use:   "rename-container <path|id> <new-slug>",
	Short: "Rename a container's slug and title",
	Long: `Rename a container by updating both its slug and title.

By default, the title is set to match the new slug. Use --title to specify
a custom title.

Examples:
  # Rename both slug and title to "new-project"
  wrkq rename-container old-project new-project

  # Rename with custom title
  wrkq rename-container P-00001 new-project --title "New Project Name"

  # Rename a top-level container in canonical DB
  WRKQ_PROJECT_ROOT="" wrkq rename-container rex rex-control-plane`,
	Args: cobra.ExactArgs(2),
	RunE: appctx.WithApp(appctx.WithActor(), runRenameContainer),
}

var (
	renameContainerTitle   string
	renameContainerIfMatch int64
	renameContainerDryRun  bool
)

func init() {
	rootCmd.AddCommand(renameContainerCmd)
	renameContainerCmd.Flags().StringVar(&renameContainerTitle, "title", "", "Custom title (defaults to new slug)")
	renameContainerCmd.Flags().Int64Var(&renameContainerIfMatch, "if-match", 0, "Only rename if etag matches")
	renameContainerCmd.Flags().BoolVar(&renameContainerDryRun, "dry-run", false, "Show what would change without applying")
}

func runRenameContainer(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	// Apply project root to the container selector
	containerSelector := applyProjectRootToSelector(app.Config, args[0], false)
	newSlug := args[1]

	// Normalize the new slug
	normalizedSlug, err := paths.NormalizeSlug(newSlug)
	if err != nil {
		return fmt.Errorf("invalid new slug %q: %w", newSlug, err)
	}

	// Resolve the container
	containerUUID, containerPath, err := selectors.ResolveContainer(database, containerSelector)
	if err != nil {
		return fmt.Errorf("container not found: %s", containerSelector)
	}

	// Determine the new title
	newTitle := normalizedSlug
	if renameContainerTitle != "" {
		newTitle = renameContainerTitle
	}

	// Get current container info for display
	s := store.New(database)
	container, err := s.Containers.GetByUUID(containerUUID)
	if err != nil {
		return fmt.Errorf("failed to get container: %w", err)
	}

	if renameContainerDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Would rename container:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  Path:  %s\n", containerPath)
		fmt.Fprintf(cmd.OutOrStdout(), "  Slug:  %s -> %s\n", container.Slug, normalizedSlug)
		oldTitle := container.Slug // fallback
		if container.Title != nil {
			oldTitle = *container.Title
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  Title: %s -> %s\n", oldTitle, newTitle)
		return nil
	}

	// Update the container
	fields := map[string]interface{}{
		"slug":  normalizedSlug,
		"title": newTitle,
	}
	_, err = s.Containers.UpdateFields(actorUUID, containerUUID, fields, renameContainerIfMatch)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Renamed container: %s -> %s\n", containerPath, normalizedSlug)
	return nil
}
