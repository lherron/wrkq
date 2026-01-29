package cli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

var versionAdmCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Long:  `Displays version, commit, and build date information for wrkqadm.`,
	RunE:  runVersionAdm,
}

var versionAdmJSON bool

func init() {
	rootAdmCmd.AddCommand(versionAdmCmd)
	versionAdmCmd.Flags().BoolVar(&versionAdmJSON, "json", false, "Output as JSON")
}

func runVersionAdm(cmd *cobra.Command, args []string) error {
	if versionAdmJSON {
		output := map[string]interface{}{
			"binary":                    "wrkqadm",
			"version":                   Version,
			"commit":                    GitCommit,
			"build_date":                BuildDate,
			"machine_interface_version": 1,
			"supported_commands": []string{
				// Admin commands
				"init",
				"actors", "actor",
				"doctor", "config",
				"db",
				"bundle",
				"attach",
				"version", "completion",
			},
			"supported_formats": []string{
				"json", "ndjson", "yaml", "tsv", "table", "porcelain",
			},
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "wrkqadm version %s\n", Version)
	fmt.Fprintf(cmd.OutOrStdout(), "  commit: %s\n", GitCommit)
	fmt.Fprintf(cmd.OutOrStdout(), "  built:  %s\n", BuildDate)
	fmt.Fprintf(cmd.OutOrStdout(), "  machine interface: v%d\n", 1)

	return nil
}
