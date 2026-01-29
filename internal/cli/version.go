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
				// M0 - Core
				"init", "whoami", "actors", "actor",
				"mkdir", "touch", "ls", "tree", "stat", "ids", "resolve",
				"cat", "set", "mv", "rm", "restore",
				"version", "completion", "config",

				// M1 - Editing & History
				"edit", "apply", "log", "watch", "diff", "find",

				// M2 - Attachments & Operations
				"attach", "cp", "doctor",
			},
			"supported_formats": []string{
				"json", "ndjson", "yaml", "tsv", "table", "porcelain",
			},
			"supported_flags": map[string][]string{
				"output":     []string{"--json", "--ndjson", "--yaml", "--tsv", "--porcelain", "-1", "-0"},
				"filtering":  []string{"--state", "--priority", "--labels", "--since", "--until"},
				"sorting":    []string{"--sort"},
				"pagination": []string{"--limit", "--cursor"},
				"operations": []string{"--dry-run", "--yes", "--if-match", "--continue-on-error"},
				"bulk":       []string{"--jobs", "--batch-size"},
			},
			"capabilities": map[string]bool{
				"etag_concurrency":  true,
				"actor_attribution": true,
				"event_log":         true,
				"attachments":       true,
				"pagination":        true,
				"bulk_operations":   true,
				"glob_patterns":     true,
				"stdin_input":       true,
				"three_way_merge":   true,
			},
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(output)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "wrkq version %s\n", Version)
	fmt.Fprintf(cmd.OutOrStdout(), "  commit: %s\n", GitCommit)
	fmt.Fprintf(cmd.OutOrStdout(), "  built:  %s\n", BuildDate)
	fmt.Fprintf(cmd.OutOrStdout(), "  machine interface: v%d\n", 1)

	return nil
}
