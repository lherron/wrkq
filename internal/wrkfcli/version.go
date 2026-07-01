package wrkfcli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/spf13/cobra"
)

// Version information (set by build flags via -ldflags -X).
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func versionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  `Displays version, commit, and build date information.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON || flagJSON {
				output := map[string]interface{}{
					"version":          Version,
					"commit":           GitCommit,
					"build_date":       BuildDate,
					"protocol_version": workrpc.ProtocolVersion,
				}
				encoder := json.NewEncoder(cmd.OutOrStdout())
				encoder.SetIndent("", "  ")
				return encoder.Encode(output)
			}

			fmt.Fprintf(cmd.OutOrStdout(), "wrkf version %s\n", Version)
			fmt.Fprintf(cmd.OutOrStdout(), "  commit:   %s\n", GitCommit)
			fmt.Fprintf(cmd.OutOrStdout(), "  built:    %s\n", BuildDate)
			fmt.Fprintf(cmd.OutOrStdout(), "  protocol: %s\n", workrpc.ProtocolVersion)
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}
