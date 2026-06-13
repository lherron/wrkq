package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/spf13/cobra"
)

var whoamiCmd = &cobra.Command{
	Use:   "whoami",
	Short: "Print the current principal",
	Long:  `Displays the resolved external principal and runtime scope based on configuration and environment.`,
	RunE:  appctx.WithApp(appctx.WithActor(), runWhoami),
}

var whoamiJSON bool

func init() {
	rootCmd.AddCommand(whoamiCmd)
	whoamiCmd.Flags().BoolVar(&whoamiJSON, "json", false, "Output as JSON")
}

func runWhoami(app *appctx.App, cmd *cobra.Command, args []string) error {
	cfg := app.Config
	attr := app.Attribution()

	if whoamiJSON {
		output := map[string]interface{}{
			"principal_ref": attr.PrincipalRef,
			"scope_ref":     attr.ScopeRef,
			"db_path":       cfg.DBPath,
		}
		if attr.LegacyActorUUID != nil {
			output["legacy_actor_uuid"] = *attr.LegacyActorUUID
		}
		if attr.LegacyActorID != "" {
			output["legacy_actor_id"] = attr.LegacyActorID
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Principal: %s\n", attr.PrincipalRef)
	if attr.ScopeRef != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Scope:     %s\n", attr.ScopeRef)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "DB:      %s\n", cfg.DBPath)
	if attr.LegacyActorUUID != nil || attr.LegacyActorID != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Legacy actor cache:")
		if attr.LegacyActorID != "" {
			fmt.Fprintf(cmd.OutOrStdout(), " %s", attr.LegacyActorID)
		}
		if attr.LegacyActorUUID != nil {
			fmt.Fprintf(cmd.OutOrStdout(), " %s", *attr.LegacyActorUUID)
		}
		fmt.Fprintln(cmd.OutOrStdout())
	}

	return nil
}
