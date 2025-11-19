package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var actorsCmd = &cobra.Command{
	Use:   "actors",
	Short: "Manage actors (users and agents)",
	Long:  `Commands for listing and managing actors in the system.`,
}

var actorsLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all actors",
	Long:  `Lists all actors (users and agents) in the system.`,
	RunE:  runActorsList,
}

var actorAddCmd = &cobra.Command{
	Use:   "add <slug>",
	Short: "Create a new actor",
	Long:  `Creates a new actor with the given slug. The slug will be normalized to lowercase [a-z0-9-].`,
	Args:  cobra.ExactArgs(1),
	RunE:  runActorAdd,
}

var (
	actorsLsJSON      bool
	actorsLsNDJSON    bool
	actorsLsPorcelain bool
	actorAddName      string
	actorAddRole      string
)

func init() {
	rootCmd.AddCommand(actorsCmd)
	actorsCmd.AddCommand(actorsLsCmd)
	actorsCmd.AddCommand(actorAddCmd)

	// actors ls flags
	actorsLsCmd.Flags().BoolVar(&actorsLsJSON, "json", false, "Output as JSON")
	actorsLsCmd.Flags().BoolVar(&actorsLsNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	actorsLsCmd.Flags().BoolVar(&actorsLsPorcelain, "porcelain", false, "Machine-readable output")

	// actor add flags
	actorAddCmd.Flags().StringVar(&actorAddName, "name", "", "Display name for the actor")
	actorAddCmd.Flags().StringVar(&actorAddRole, "role", "human", "Actor role (human, agent, system)")
}

func runActorsList(cmd *cobra.Command, args []string) error {
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

	// List actors
	resolver := actors.NewResolver(database.DB)
	actorList, err := resolver.List()
	if err != nil {
		return fmt.Errorf("failed to list actors: %w", err)
	}

	// Render output
	if actorsLsJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if !actorsLsPorcelain {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(actorList)
	}

	if actorsLsNDJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, actor := range actorList {
			if err := encoder.Encode(actor); err != nil {
				return err
			}
		}
		return nil
	}

	// Table output
	headers := []string{"ID", "Slug", "Display Name", "Role"}
	var rows [][]string
	for _, actor := range actorList {
		displayName := ""
		if actor.DisplayName != nil {
			displayName = *actor.DisplayName
		}
		rows = append(rows, []string{
			actor.ID,
			actor.Slug,
			displayName,
			actor.Role,
		})
	}

	r := render.NewRenderer(cmd.OutOrStdout(), render.Options{
		Format:    render.FormatTable,
		Porcelain: actorsLsPorcelain,
	})

	return r.RenderTable(headers, rows)
}

func runActorAdd(cmd *cobra.Command, args []string) error {
	slug := args[0]

	// Normalize slug
	normalizedSlug, err := paths.NormalizeSlug(slug)
	if err != nil {
		return fmt.Errorf("invalid slug: %w", err)
	}

	// Validate role
	if actorAddRole != "human" && actorAddRole != "agent" && actorAddRole != "system" {
		return fmt.Errorf("invalid role: must be one of: human, agent, system")
	}

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

	// Create actor
	resolver := actors.NewResolver(database.DB)
	actor, err := resolver.Create(normalizedSlug, actorAddName, actorAddRole)
	if err != nil {
		return fmt.Errorf("failed to create actor: %w", err)
	}

	// Output
	fmt.Fprintf(cmd.OutOrStdout(), "Created actor %s (%s)\n", actor.Slug, actor.ID)

	return nil
}
