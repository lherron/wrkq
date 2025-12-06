package cli

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var mvCmd = &cobra.Command{
	Use:   "mv <src>... <dst>",
	Short: "Move or rename tasks and containers",
	Long: `Move or rename tasks and containers.

Rules:
- Multiple sources -> DST must be an existing container; sources move into DST.
- Single source:
  - If DST path resolves to existing container: move into container.
  - If DST does not exist: treat final segment as new slug (rename).
  - If DST is an existing task: error unless --overwrite-task.`,
	Args: cobra.MinimumNArgs(2),
	RunE: appctx.WithApp(appctx.WithActor(), runMv),
}

var (
	mvType          string
	mvIfMatch       int64
	mvDryRun        bool
	mvYes           bool
	mvNullglob      bool
	mvOverwriteTask bool
)

func init() {
	rootCmd.AddCommand(mvCmd)
	mvCmd.Flags().StringVar(&mvType, "type", "", "Force type: t (task), p (project/container)")
	mvCmd.Flags().Int64Var(&mvIfMatch, "if-match", 0, "Only move if etag matches")
	mvCmd.Flags().BoolVar(&mvDryRun, "dry-run", false, "Show what would be moved without applying")
	mvCmd.Flags().BoolVar(&mvYes, "yes", false, "Skip confirmation prompts")
	mvCmd.Flags().BoolVar(&mvNullglob, "nullglob", false, "Zero matches is a no-op instead of error")
	mvCmd.Flags().BoolVar(&mvOverwriteTask, "overwrite-task", false, "Allow overwriting existing tasks")
}

func runMv(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	// Create store
	s := store.New(database)

	// Split sources and destination
	sources := args[:len(args)-1]
	dst := args[len(args)-1]

	// If multiple sources, destination must be an existing container
	if len(sources) > 1 {
		dstContainerUUID, _, err := selectors.ResolveContainer(database, dst)
		if err != nil {
			return fmt.Errorf("destination must be an existing container for multiple sources: %w", err)
		}

		for _, src := range sources {
			if err := moveToContainer(cmd, s, actorUUID, src, dstContainerUUID, dst); err != nil {
				return err
			}
		}
		return nil
	}

	// Single source - could be move or rename
	src := sources[0]

	// Try to resolve destination as container first
	dstContainerUUID, _, dstErr := selectors.ResolveContainer(database, dst)
	if dstErr == nil {
		// Destination is an existing container - move into it
		return moveToContainer(cmd, s, actorUUID, src, dstContainerUUID, dst)
	}

	// Destination doesn't exist as container - could be rename
	// Check if source is a task or container
	srcTaskUUID, _, taskErr := selectors.ResolveTask(database, src)
	if taskErr == nil {
		// Source is a task - rename or move to new parent
		return renameOrMoveTask(cmd, s, actorUUID, srcTaskUUID, src, dst)
	}

	srcContainerUUID, _, containerErr := selectors.ResolveContainer(database, src)
	if containerErr == nil {
		// Source is a container - rename or move to new parent
		return renameOrMoveContainer(cmd, s, actorUUID, srcContainerUUID, src, dst)
	}

	return fmt.Errorf("source not found: %s", src)
}

func moveToContainer(cmd *cobra.Command, s *store.Store, actorUUID, src, dstContainerUUID, dstPath string) error {
	// Try as task first
	srcTaskUUID, srcPath, taskErr := selectors.ResolveTask(s.DB(), src)
	if taskErr == nil {
		if mvDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "Would move task %s -> %s\n", srcPath, dstPath)
			return nil
		}

		// Move task to destination container using store
		_, err := s.Tasks.Move(actorUUID, srcTaskUUID, dstContainerUUID, mvIfMatch)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Moved task: %s -> %s\n", srcPath, dstPath)
		return nil
	}

	// Try as container
	srcContainerUUID, _, containerErr := selectors.ResolveContainer(s.DB(), src)
	if containerErr == nil {
		if mvDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "Would move container %s -> %s\n", src, dstPath)
			return nil
		}

		// Move container to destination container using store
		_, err := s.Containers.Move(actorUUID, srcContainerUUID, &dstContainerUUID, mvIfMatch)
		if err != nil {
			return err
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Moved container: %s -> %s\n", src, dstPath)
		return nil
	}

	return fmt.Errorf("source not found: %s", src)
}

func renameOrMoveTask(cmd *cobra.Command, s *store.Store, actorUUID, srcTaskUUID, srcPath, dstPath string) error {
	database := s.DB()

	// Use shared resolver to determine destination parent and slug
	newParentUUID, normalizedSlug, _, err := selectors.ResolveParentContainer(database, dstPath)
	if err != nil {
		// If parent container not found, check if it's a single segment (rename in place)
		dstSegments := paths.SplitPath(dstPath)
		if len(dstSegments) == 1 {
			// Just renaming within the same parent - get current parent
			err := database.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", srcTaskUUID).Scan(&newParentUUID)
			if err != nil {
				return fmt.Errorf("failed to get current parent: %w", err)
			}
			normalizedSlug, err = paths.NormalizeSlug(dstSegments[0])
			if err != nil {
				return fmt.Errorf("invalid destination slug %q: %w", dstSegments[0], err)
			}
		} else {
			return err
		}
	}

	// Check if destination already exists
	var existingTaskUUID string
	err = database.QueryRow(`
		SELECT uuid FROM tasks WHERE slug = ? AND project_uuid = ?
	`, normalizedSlug, *newParentUUID).Scan(&existingTaskUUID)

	if err == nil && existingTaskUUID != srcTaskUUID {
		if !mvOverwriteTask {
			return fmt.Errorf("destination task already exists: %s (use --overwrite-task to replace)", dstPath)
		}
		// Delete existing task
		if mvDryRun {
			fmt.Fprintf(cmd.OutOrStdout(), "Would overwrite task at %s\n", dstPath)
		} else {
			_, err := s.Tasks.Purge(actorUUID, existingTaskUUID, 0)
			if err != nil {
				return fmt.Errorf("failed to delete existing task: %w", err)
			}
		}
	}

	if mvDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Would rename/move task %s -> %s\n", srcPath, dstPath)
		return nil
	}

	// Perform the move/rename using store's UpdateFields
	fields := map[string]interface{}{
		"slug":         normalizedSlug,
		"project_uuid": *newParentUUID,
	}
	_, err = s.Tasks.UpdateFields(actorUUID, srcTaskUUID, fields, mvIfMatch)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Moved/renamed task: %s -> %s\n", srcPath, dstPath)
	return nil
}

func renameOrMoveContainer(cmd *cobra.Command, s *store.Store, actorUUID, srcContainerUUID, srcPath, dstPath string) error {
	database := s.DB()

	// Use shared resolver to determine destination parent and slug
	newParentUUID, normalizedSlug, _, err := selectors.ResolveParentContainer(database, dstPath)
	if err != nil {
		// If parent container not found, check if it's a single segment (rename in place)
		dstSegments := paths.SplitPath(dstPath)
		if len(dstSegments) == 1 {
			// Just renaming within the same parent - get current parent (may be NULL for root containers)
			var parentUUID *string
			queryErr := database.QueryRow("SELECT parent_uuid FROM containers WHERE uuid = ?", srcContainerUUID).Scan(&parentUUID)
			if queryErr != nil && !strings.Contains(queryErr.Error(), "null") {
				return fmt.Errorf("failed to get current parent: %w", queryErr)
			}
			newParentUUID = parentUUID
			normalizedSlug, err = paths.NormalizeSlug(dstSegments[0])
			if err != nil {
				return fmt.Errorf("invalid destination slug %q: %w", dstSegments[0], err)
			}
		} else {
			return err
		}
	}

	// Check if destination already exists
	query := `SELECT uuid FROM containers WHERE slug = ? AND `
	args := []interface{}{normalizedSlug}
	if newParentUUID == nil {
		query += `parent_uuid IS NULL`
	} else {
		query += `parent_uuid = ?`
		args = append(args, *newParentUUID)
	}

	var existingContainerUUID string
	err = database.QueryRow(query, args...).Scan(&existingContainerUUID)

	if err == nil && existingContainerUUID != srcContainerUUID {
		return fmt.Errorf("destination container already exists: %s", dstPath)
	}

	if mvDryRun {
		fmt.Fprintf(cmd.OutOrStdout(), "Would rename/move container %s -> %s\n", srcPath, dstPath)
		return nil
	}

	// Perform the move/rename using store's UpdateFields
	fields := map[string]interface{}{
		"slug":        normalizedSlug,
		"parent_uuid": newParentUUID,
	}
	_, err = s.Containers.UpdateFields(actorUUID, srcContainerUUID, fields, mvIfMatch)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Moved/renamed container: %s -> %s\n", srcPath, dstPath)
	return nil
}
