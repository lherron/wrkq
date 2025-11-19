package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var (
	// Version information (set by build flags)
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Displays version, commit, and build date information.`,
	RunE:  runVersion,
}

var versionJSON bool

func init() {
	rootCmd.AddCommand(versionCmd)
	versionCmd.Flags().BoolVar(&versionJSON, "json", false, "Output as JSON")
}

func runVersion(cmd *cobra.Command, args []string) error {
	if versionJSON {
		output := map[string]interface{}{
			"version":                   Version,
			"commit":                    GitCommit,
			"build_date":                BuildDate,
			"machine_interface_version": 1,
			"supported_commands": []string{
				"init", "whoami", "actors", "mkdir", "touch", "ls", "cat", "set", "version",
			},
			"supported_formats": []string{
				"json", "ndjson", "yaml", "tsv", "table",
			},
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "todo version %s\n", Version)
	fmt.Fprintf(cmd.OutOrStdout(), "  commit: %s\n", GitCommit)
	fmt.Fprintf(cmd.OutOrStdout(), "  built:  %s\n", BuildDate)

	return nil
}
