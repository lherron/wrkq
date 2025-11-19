package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Print the current actor",
	Long:  `Displays information about the current actor (user or agent) based on configuration and environment.`,
	RunE:  runWhoami,
}

var whoamiJSON bool

func init() {
	rootCmd.AddCommand(whoamiCmd)
	whoamiCmd.Flags().BoolVar(&whoamiJSON, "json", false, "Output as JSON")
}

func runWhoami(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Get actor identifier from config/env
	actorIdentifier := cfg.GetActorID()
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

	actor, err := resolver.GetByUUID(actorUUID)
	if err != nil {
		return fmt.Errorf("failed to get actor: %w", err)
	}

	if whoamiJSON {
		output := map[string]interface{}{
			"actor":   actor,
			"db_path": cfg.DBPath,
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	// Human-readable output
	displayName := actor.Slug
	if actor.DisplayName != nil && *actor.DisplayName != "" {
		displayName = *actor.DisplayName
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Actor:   %s (%s)\n", displayName, actor.ID)
	fmt.Fprintf(cmd.OutOrStdout(), "Slug:    %s\n", actor.Slug)
	fmt.Fprintf(cmd.OutOrStdout(), "Role:    %s\n", actor.Role)
	fmt.Fprintf(cmd.OutOrStdout(), "DB:      %s\n", cfg.DBPath)

	return nil
}
