package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

// agentTarget is the fixed agent this alias dispatches to. It is intentionally
// hardcoded: `wrkq agent` is a convenience alias for one predefined agent.
const agentTarget = "nestor"

var (
	agentNew    bool
	agentFormat string
	agentFollow string
	agentFile   string
)

var agentCmd = &cobra.Command{
	Use:   "agent [prompt] [-- extra hrcchat-turn args]",
	Short: "Run a chat turn with the nestor agent",
	Long: `Run a chat turn against the predefined agent "nestor".

This is a thin pass-through to 'hrcchat turn nestor'. The 'hrcchat' binary must
be on PATH; wrkq does not depend on it at build time. Project context is
resolved by hrcchat itself (ASP_PROJECT), exactly as for any other caller.

The prompt is taken from positional args (joined), --file, or stdin ("-").
All hrcchat output is streamed through unchanged.

Common 'hrcchat turn' flags are surfaced directly (--new, --format, --follow,
--file). Anything after "--" is forwarded verbatim, so any other hrcchat flag
remains reachable without a wrkq change:

  wrkq agent "summarize open risks"
  wrkq agent --follow 30s "work the inbox"
  echo "long prompt" | wrkq agent -
  wrkq agent "do X" -- --stall-after 20m --reply-to msg_01J...`,
	RunE: runAgent,
}

func init() {
	rootCmd.AddCommand(agentCmd)
	agentCmd.Flags().BoolVar(&agentNew, "new", false, "Clear context before dispatching (clean slate)")
	agentCmd.Flags().StringVar(&agentFormat, "format", "", "Output format: tree, compact, ndjson, json")
	agentCmd.Flags().StringVar(&agentFollow, "follow", "", "Emit bounded progress at the given interval (e.g. 30s)")
	agentCmd.Flags().StringVar(&agentFile, "file", "", "Read prompt from file")
}

func runAgent(cmd *cobra.Command, args []string) error {
	bin, err := exec.LookPath("hrcchat")
	if err != nil {
		return exitError(1, fmt.Errorf("hrcchat not found on PATH: %w", err))
	}

	// Split positional prompt args from anything after "--", which is
	// forwarded verbatim to hrcchat turn.
	promptArgs := args
	var passthrough []string
	if dash := cmd.ArgsLenAtDash(); dash >= 0 {
		promptArgs = args[:dash]
		passthrough = args[dash:]
	}

	argv := []string{"turn"}
	if agentNew {
		argv = append(argv, "--new")
	}
	if agentFormat != "" {
		argv = append(argv, "--format", agentFormat)
	}
	if agentFollow != "" {
		argv = append(argv, "--follow", agentFollow)
	}
	if agentFile != "" {
		argv = append(argv, "--file", agentFile)
	}
	argv = append(argv, agentTarget)
	argv = append(argv, passthrough...)

	prompt := strings.TrimSpace(strings.Join(promptArgs, " "))
	switch {
	case prompt != "":
		argv = append(argv, prompt)
	case agentFile == "" && !isStdinTTY():
		// No inline prompt and no --file, but stdin is piped: let hrcchat
		// read the prompt from stdin.
		argv = append(argv, "-")
	}

	child := exec.Command(bin, argv...)
	child.Env = os.Environ()
	child.Stdin = os.Stdin
	child.Stdout = cmd.OutOrStdout()
	child.Stderr = cmd.ErrOrStderr()

	if err := child.Run(); err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			// hrcchat already wrote its own diagnostics; propagate its code.
			return exitErrorReported(exit.ExitCode(), err)
		}
		return exitError(1, fmt.Errorf("running hrcchat turn: %w", err))
	}
	return nil
}

// isStdinTTY reports whether stdin is an interactive terminal.
func isStdinTTY() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
