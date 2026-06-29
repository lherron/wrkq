package rpccli

import (
	"encoding/json"
	"fmt"

	"github.com/spf13/cobra"
)

// Version information mirrors the legacy CLI version payload so the RPC-backed
// production entrypoint and the temporary parity mirror stay byte-compatible.
var (
	Version   = "dev"
	GitCommit = "unknown"
	BuildDate = "unknown"
)

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  `Displays version, commit, and build date information.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if asJSON || !isStdoutTTY(cmd.OutOrStdout()) {
				output := map[string]interface{}{
					"version":                   Version,
					"commit":                    GitCommit,
					"build_date":                BuildDate,
					"machine_interface_version": 1,
					"supported_commands": []string{
						"init", "whoami", "actors", "actor",
						"mkdir", "touch", "ls", "tree", "stat", "ids", "resolve",
						"cat", "set", "mv", "rm", "restore",
						"version", "completion", "config",
						"edit", "apply", "log", "watch", "diff", "find",
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
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}
