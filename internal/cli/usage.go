package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// wrkqUsageContent is embedded in initadm.go

var usageCmd = &cobra.Command{
	Use:     "usage",
	Aliases: []string{"info"},
	Short:   "Display wrkq usage documentation",
	Long:    `Displays the embedded WRKQ-USAGE.md documentation for agents and users.`,
	RunE:    runUsage,
}

var usageJSON bool

func init() {
	rootCmd.AddCommand(usageCmd)
	usageCmd.Flags().BoolVar(&usageJSON, "json", false, "Output as JSON")
}

func runUsage(cmd *cobra.Command, args []string) error {
	if usageJSON {
		output := map[string]interface{}{
			"content": wrkqUsageContent,
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	fmt.Fprint(cmd.OutOrStdout(), wrkqUsageContent)
	return nil
}
