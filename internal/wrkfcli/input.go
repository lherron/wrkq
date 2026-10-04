package wrkfcli

import (
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// stdinTextFlagNames lists wrkf flags whose values are caller-authored text.
// A literal dash follows the wrkq convention and reads the value from stdin.
var stdinTextFlagNames = []string{
	"summary",
	"run-summary",
	"terminal-summary",
	"facts",
	"data",
	"explanation",
	"reason",
}

func resolveStdinTextFlags(cmd *cobra.Command) error {
	claimedBy := ""
	for _, name := range stdinTextFlagNames {
		if cmd.Flags().Lookup(name) == nil {
			continue
		}
		value, err := cmd.Flags().GetString(name)
		if err != nil {
			return fmt.Errorf("read --%s: %w", name, err)
		}
		if value != "-" {
			continue
		}
		if claimedBy != "" {
			return fmt.Errorf("stdin already claimed by --%s", claimedBy)
		}
		claimedBy = name
	}
	if claimedBy == "" {
		return nil
	}

	stdin := cmd.InOrStdin()
	if file, ok := stdin.(*os.File); ok {
		// The terminal driver, not ModeCharDevice: /dev/null is a character
		// device but must read as empty input (CLI standard §8).
		if term.IsTerminal(int(file.Fd())) {
			return fmt.Errorf("--%s: stdin is a terminal; pipe input or use a heredoc", claimedBy)
		}
	}
	data, err := io.ReadAll(stdin)
	if err != nil {
		return fmt.Errorf("--%s: failed to read from stdin: %w", claimedBy, err)
	}
	if len(data) == 0 {
		return fmt.Errorf("--%s: stdin is empty; pipe the value in or use a heredoc (<<'EOF')", claimedBy)
	}
	if err := cmd.Flags().Set(claimedBy, string(data)); err != nil {
		return fmt.Errorf("set --%s from stdin: %w", claimedBy, err)
	}
	return nil
}
