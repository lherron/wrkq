package cli

import (
	"github.com/spf13/cobra"
)

var rootAdmCmd = &cobra.Command{
	Use:   "wrkqadm",
	Short: "Administrative CLI for wrkq database lifecycle and infrastructure",
	Long: `wrkqadm is the administrative companion to wrkq. It handles database
lifecycle (init, snapshot), actor management, bundle application, and
health checks. These operations should not be exposed to agents.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// ExecuteAdmin runs the admin root command
func ExecuteAdmin() error {
	return rootAdmCmd.Execute()
}

func init() {
	// Global flags for wrkqadm
	rootAdmCmd.PersistentFlags().String("db", "", "Path to database file (overrides WRKQ_DB_PATH)")
	rootAdmCmd.PersistentFlags().String("as", "", "Actor to perform action as (slug or friendly ID)")
}
