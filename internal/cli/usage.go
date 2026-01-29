package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// wrkqUsageContent and agentUsageContent are embedded in initadm.go

var usageCmd = &cobra.Command{
	Use:     "usage",
	Aliases: []string{"info"},
	Short:   "Display wrkq usage documentation",
	Long:    `Displays the embedded WRKQ-USAGE.md documentation for agents and users.`,
	RunE:    runUsage,
}

var agentInfoCmd = &cobra.Command{
	Use:   "agent-info",
	Short: "Display condensed wrkq quick reference for agents",
	Long:  `Displays a condensed quick reference of essential wrkq commands for coding agents.`,
	RunE:  runAgentInfo,
}

var usageJSON bool
var agentInfoJSON bool

func init() {
	rootCmd.AddCommand(usageCmd)
	usageCmd.Flags().BoolVar(&usageJSON, "json", false, "Output as JSON")

	rootCmd.AddCommand(agentInfoCmd)
	agentInfoCmd.Flags().BoolVar(&agentInfoJSON, "json", false, "Output as JSON")
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

func runAgentInfo(cmd *cobra.Command, args []string) error {
	if agentInfoJSON {
		output := map[string]interface{}{
			"content": agentUsageContent,
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	fmt.Fprint(cmd.OutOrStdout(), agentUsageContent)
	return nil
}
