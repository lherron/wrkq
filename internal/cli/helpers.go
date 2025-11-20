package cli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

// exitError returns an error that will cause the CLI to exit with the given code
func exitError(code int, err error) error {
	// For now, just return the error. We'll enhance this with proper exit codes later
	return err
}

// resolveCurrentActor resolves the current actor UUID and friendly ID
// from --as flag, environment variables, or config.
func resolveCurrentActor(database *db.DB, cfg *config.Config, cmd *cobra.Command) (uuid, friendlyID string, err error) {
	// Get actor from --as flag or config
	actorIdentifier := cmd.Flag("as").Value.String()
	if actorIdentifier == "" {
		actorIdentifier = cfg.GetActorID()
	}
	if actorIdentifier == "" {
		return "", "", fmt.Errorf("no actor configured (set TODO_ACTOR, TODO_ACTOR_ID, or use --as flag)")
	}

	// Resolve actor UUID
	resolver := actors.NewResolver(database.DB)
	actorUUID, err := resolver.Resolve(actorIdentifier)
	if err != nil {
		return "", "", fmt.Errorf("failed to resolve actor: %w", err)
	}

	// Get actor friendly ID
	var actorID string
	err = database.QueryRow("SELECT id FROM actors WHERE uuid = ?", actorUUID).Scan(&actorID)
	if err != nil {
		return "", "", fmt.Errorf("failed to get actor ID: %w", err)
	}

	return actorUUID, actorID, nil
}
