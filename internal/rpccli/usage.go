package rpccli

import (
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

//go:embed embedded/WRKQ-USAGE.md
var wrkqUsageContent string

//go:embed embedded/AGENT-WRKQ-USAGE.md
var agentUsageContent string

func newUsageCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "usage",
		Aliases: []string{"info"},
		Short:   "Display wrkq usage documentation",
		Long:    `Displays the embedded WRKQ-USAGE.md documentation for agents and users.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return renderEmbeddedUsage(cmd, wrkqUsageContent, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func newAgentInfoCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "agent-info",
		Short: "Display condensed wrkq quick reference for agents",
		Long:  `Displays a condensed quick reference of essential wrkq commands for coding agents.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return renderEmbeddedUsage(cmd, agentUsageContent, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func renderEmbeddedUsage(cmd *cobra.Command, content string, asJSON bool) error {
	if asJSON || !isStdoutTTY(cmd.OutOrStdout()) {
		output := map[string]interface{}{"content": content}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}
	fmt.Fprint(cmd.OutOrStdout(), content)
	return nil
}
