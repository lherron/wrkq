package cli

import (
	"fmt"

	"github.com/lherron/todo/internal/actors"
	"github.com/lherron/todo/internal/config"
	"github.com/lherron/todo/internal/db"
	"github.com/lherron/todo/internal/domain"
	"github.com/lherron/todo/internal/events"
	"github.com/lherron/todo/internal/paths"
	"github.com/spf13/cobra"
)

var mkdirCmd = &cobra.Command{
	Use:   "mkdir <path>...",
	Short: "Create projects or subprojects",
	Long: `Creates one or more projects or subprojects (containers).
The last segment of each path is treated as a container slug and normalized to lowercase [a-z0-9-].`,
	Args: cobra.MinimumNArgs(1),
	RunE: runMkdir,
}

var (
	mkdirParents bool
)

func init() {
	rootCmd.AddCommand(mkdirCmd)
	mkdirCmd.Flags().BoolVarP(&mkdirParents, "parents", "p", false, "Create parent containers as needed")
}

func runMkdir(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Get actor from --as flag or config
	actorIdentifier := cmd.Flag("as").Value.String()
	if actorIdentifier == "" {
		actorIdentifier = cfg.GetActorID()
	}
	if actorIdentifier == "" {
		return fmt.Errorf("no actor configured (set TODO_ACTOR, TODO_ACTOR_ID, or use --as flag)")
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Resolve actor
	resolver := actors.NewResolver(database.DB)
	actorUUID, err := resolver.Resolve(actorIdentifier)
	if err != nil {
		return fmt.Errorf("failed to resolve actor: %w", err)
	}

	// Create each path
	for _, path := range args {
		if err := createContainer(database, actorUUID, path, mkdirParents); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Created: %s\n", path)
	}

	return nil
}

func createContainer(database *db.DB, actorUUID, path string, createParents bool) error {
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

			// Check if container exists
			var existingUUID string
			query := `SELECT uuid FROM containers WHERE slug = ? AND `
			args := []interface{}{slug}
			if parentUUID == nil {
				query += `parent_uuid IS NULL`
			} else {
				query += `parent_uuid = ?`
				args = append(args, *parentUUID)
			}

			err = database.QueryRow(query, args...).Scan(&existingUUID)
			if err == nil {
				// Container already exists
				parentUUID = &existingUUID
				continue
			}

			// Create container
			tx, err := database.Begin()
			if err != nil {
				return fmt.Errorf("failed to begin transaction: %w", err)
			}
			defer tx.Rollback()

			result, err := tx.Exec(`
				INSERT INTO containers (id, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid)
				VALUES ('', ?, ?, ?, ?, ?)
			`, slug, slug, parentUUID, actorUUID, actorUUID)
			if err != nil {
				return fmt.Errorf("failed to create container %q: %w", slug, err)
			}

			// Get the UUID of the created container
			rowID, err := result.LastInsertId()
			if err != nil {
				return fmt.Errorf("failed to get last insert ID: %w", err)
			}

			var uuid string
			err = tx.QueryRow("SELECT uuid FROM containers WHERE rowid = ?", rowID).Scan(&uuid)
			if err != nil {
				return fmt.Errorf("failed to get container UUID: %w", err)
			}

			// Log event
			eventWriter := events.NewWriter(database.DB)
			payload := fmt.Sprintf(`{"slug":"%s","parent_path":"%s"}`, slug, path)
			if err := eventWriter.LogEvent(tx, &domain.Event{
				ActorUUID:    &actorUUID,
				ResourceType: "container",
				ResourceUUID: &uuid,
				EventType:    "container.created",
				Payload:      &payload,
			}); err != nil {
				return fmt.Errorf("failed to log event: %w", err)
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit transaction: %w", err)
			}

			parentUUID = &uuid
		}
		return nil
	}

	// Without -p flag, only create the last segment if parent exists
	if len(segments) > 1 {
		// Find parent
		var parentUUID *string
		for i, segment := range segments[:len(segments)-1] {
			slug, err := paths.NormalizeSlug(segment)
			if err != nil {
				return fmt.Errorf("invalid slug %q: %w", segment, err)
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
				return fmt.Errorf("parent container not found: %s (use -p to create parents)", paths.JoinPath(segments[:i+1]...))
			}
			parentUUID = &uuid
		}

		// Create final segment
		slug, err := paths.NormalizeSlug(segments[len(segments)-1])
		if err != nil {
			return fmt.Errorf("invalid slug %q: %w", segments[len(segments)-1], err)
		}

		return createSingleContainer(database, actorUUID, slug, parentUUID)
	}

	// Single segment, create at root
	slug, err := paths.NormalizeSlug(segments[0])
	if err != nil {
		return fmt.Errorf("invalid slug %q: %w", segments[0], err)
	}

	return createSingleContainer(database, actorUUID, slug, nil)
}

func createSingleContainer(database *db.DB, actorUUID, slug string, parentUUID *string) error {
	tx, err := database.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.Exec(`
		INSERT INTO containers (id, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES ('', ?, ?, ?, ?, ?)
	`, slug, slug, parentUUID, actorUUID, actorUUID)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Get the UUID
	rowID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("failed to get last insert ID: %w", err)
	}

	var uuid string
	err = tx.QueryRow("SELECT uuid FROM containers WHERE rowid = ?", rowID).Scan(&uuid)
	if err != nil {
		return fmt.Errorf("failed to get container UUID: %w", err)
	}

	// Log event
	eventWriter := events.NewWriter(database.DB)
	payload := fmt.Sprintf(`{"slug":"%s"}`, slug)
	payloadStr := payload
	if err := eventWriter.LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "container",
		ResourceUUID: &uuid,
		EventType:    "container.created",
		Payload:      &payloadStr,
	}); err != nil {
		return fmt.Errorf("failed to log event: %w", err)
	}

	return tx.Commit()
}
