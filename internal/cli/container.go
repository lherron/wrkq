package cli

import (
	"github.com/spf13/cobra"
)

var containerCmd = &cobra.Command{
	Use:   "container",
	Short: "Manage containers",
	Long:  "Manage container configuration and metadata.",
}

func init() {
	rootCmd.AddCommand(containerCmd)
}
