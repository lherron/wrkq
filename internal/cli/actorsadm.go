package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

var actorsAdmCmd = &cobra.Command{
	Use:   "actors",
	Short: "Manage actors (users and agents)",
	Long:  `Administrative commands for listing and managing actors in the system. These operations should not be exposed to agents.`,
}

var actorsAdmLsCmd = &cobra.Command{
	Use:   "ls",
	Short: "List all actors",
	Long:  `Lists all actors (users and agents) in the system.`,
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runActorsAdmList),
}

var actorAdmAddCmd = &cobra.Command{
	Use:   "add <slug>",
	Short: "Create a new actor",
	Long:  `Creates a new actor with the given slug. The slug will be normalized to lowercase [a-z0-9-].`,
	Args:  cobra.ExactArgs(1),
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runActorAdmAdd),
}

var (
	actorsAdmLsJSON      bool
	actorsAdmLsNDJSON    bool
	actorsAdmLsPorcelain bool
	actorAdmAddName      string
	actorAdmAddRole      string
)

func init() {
	rootAdmCmd.AddCommand(actorsAdmCmd)
	actorsAdmCmd.AddCommand(actorsAdmLsCmd)
	actorsAdmCmd.AddCommand(actorAdmAddCmd)

	// actors ls flags
	actorsAdmLsCmd.Flags().BoolVar(&actorsAdmLsJSON, "json", false, "Output as JSON")
	actorsAdmLsCmd.Flags().BoolVar(&actorsAdmLsNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	actorsAdmLsCmd.Flags().BoolVar(&actorsAdmLsPorcelain, "porcelain", false, "Machine-readable output")

	// actor add flags
	actorAdmAddCmd.Flags().StringVar(&actorAdmAddName, "name", "", "Display name for the actor")
	actorAdmAddCmd.Flags().StringVar(&actorAdmAddRole, "role", "human", "Actor role (human, agent, system)")
}

func runActorsAdmList(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	// List actors
	resolver := actors.NewResolver(database.DB)
	actorList, err := resolver.List()
	if err != nil {
		return fmt.Errorf("failed to list actors: %w", err)
	}

	// Render output
	if actorsAdmLsJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if !actorsAdmLsPorcelain {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(actorList)
	}

	if actorsAdmLsNDJSON {
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
		Porcelain: actorsAdmLsPorcelain,
	})

	return r.RenderTable(headers, rows)
}

func runActorAdmAdd(app *appctx.App, cmd *cobra.Command, args []string) error {
	slug := args[0]
	database := app.DB

	// Normalize slug
	normalizedSlug, err := paths.NormalizeSlug(slug)
	if err != nil {
		return fmt.Errorf("invalid slug: %w", err)
	}

	// Validate role
	if actorAdmAddRole != "human" && actorAdmAddRole != "agent" && actorAdmAddRole != "system" {
		return fmt.Errorf("invalid role: must be one of: human, agent, system")
	}

	// Create actor
	resolver := actors.NewResolver(database.DB)
	actor, err := resolver.Create(normalizedSlug, actorAdmAddName, actorAdmAddRole)
	if err != nil {
		return fmt.Errorf("failed to create actor: %w", err)
	}

	// Output
	fmt.Fprintf(cmd.OutOrStdout(), "Created actor %s (%s)\n", actor.Slug, actor.ID)

	return nil
}
