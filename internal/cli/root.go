package cli

import (
	"github.com/spf13/cobra"
)

var rootOutput string

var rootCmd = &cobra.Command{
	Use:   "wrkq",
	Short: "Filesystem-flavored CLI for managing projects and tasks",
	Long: `wrkq is a Unix-style CLI for managing projects, subprojects, and tasks
on a SQLite backend. It feels like Unix utilities (ls, cat, mv, rm)
and is pipe-friendly.`,
	SilenceUsage:  true,
	SilenceErrors: true,
}

// Execute runs the root command
func Execute() error {
	return rootCmd.Execute()
}

func init() {
	// Global flags can be added here
	rootCmd.PersistentFlags().String("db", "", "Path to database file (overrides WRKQ_DB_PATH)")
	rootCmd.PersistentFlags().String("principal-ref", "", "Canonical caller principal ref for write attribution (agent:<id>)")
	rootCmd.PersistentFlags().String("as", "", "Alias for --principal-ref; must be exactly agent:<id>")
	rootCmd.PersistentFlags().String("project", "", "Project to operate under (overrides WRKQ_PROJECT_ROOT)")
	rootCmd.PersistentFlags().StringVar(&rootOutput, "output", "", "Output mode: table, human, json, ndjson, porcelain, yaml, tsv, raw")
}
