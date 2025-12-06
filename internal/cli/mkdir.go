package cli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var mkdirCmd = &cobra.Command{
	Use:   "mkdir <path>...",
	Short: "Create projects or subprojects",
	Long: `Creates one or more projects or subprojects (containers).
The last segment of each path is treated as a container slug and normalized to lowercase [a-z0-9-].`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runMkdir),
}

var (
	mkdirParents bool
)

func init() {
	rootCmd.AddCommand(mkdirCmd)
	mkdirCmd.Flags().BoolVarP(&mkdirParents, "parents", "p", false, "Create parent containers as needed")
}

func runMkdir(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	// Create store
	s := store.New(database)

	// Create each path
	for _, path := range args {
		if err := createContainer(s, actorUUID, path, mkdirParents); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n", path)
	}

	return nil
}

func createContainer(s *store.Store, actorUUID, path string, createParents bool) error {
	segments := paths.SplitPath(path)
	if len(segments) == 0 {
		return fmt.Errorf("invalid path: %s", path)
	}

	// If parents flag is set, create all segments
	if createParents {
		var parentUUID *string
		for _, segment := range segments {
			// Normalize slug
			slug, err := paths.NormalizeSlug(segment)
			if err != nil {
				return fmt.Errorf("invalid slug %q: %w", segment, err)
			}

			// Check if container exists using shared helper
			if existingUUID, _, exists := selectors.LookupContainerSegment(s.DB(), slug, parentUUID); exists {
				parentUUID = &existingUUID
				continue
			}

			// Create container using store
			result, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{
				Slug:       slug,
				ParentUUID: parentUUID,
			})
			if err != nil {
				return fmt.Errorf("failed to create container %q: %w", slug, err)
			}

			parentUUID = &result.UUID
		}
		return nil
	}

	// Without -p flag, use ResolveParentContainer to find parent and get normalized slug
	parentUUID, slug, _, err := selectors.ResolveParentContainer(s.DB(), path)
	if err != nil {
		// Wrap error to suggest -p flag
		return fmt.Errorf("%w (use -p to create parents)", err)
	}

	// Create container using store
	_, err = s.Containers.Create(actorUUID, store.ContainerCreateParams{
		Slug:       slug,
		ParentUUID: parentUUID,
	})
	return err
}
