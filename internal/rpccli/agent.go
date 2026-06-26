package rpccli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"
)

const agentTarget = "seshat"

func newAgentCmd() *cobra.Command {
	var clearContext bool
	var format, follow, file string
	cmd := &cobra.Command{
		Use:   "agent [prompt] [-- extra hrcchat-turn args]",
		Short: "Run a chat turn with the seshat agent",
		Long: `Run a chat turn against the predefined agent "seshat".

This is a thin pass-through to 'hrcchat turn seshat'. The 'hrcchat' binary must
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
		RunE: func(cmd *cobra.Command, args []string) error {
			bin, err := exec.LookPath("hrcchat")
			if err != nil {
				return exitError(1, fmt.Errorf("hrcchat not found on PATH: %w", err))
			}

			promptArgs := args
			var passthrough []string
			if dash := cmd.ArgsLenAtDash(); dash >= 0 {
				promptArgs = args[:dash]
				passthrough = args[dash:]
			}

			argv := []string{"turn"}
			if clearContext {
				argv = append(argv, "--new")
			}
			if format != "" {
				argv = append(argv, "--format", format)
			}
			if follow != "" {
				argv = append(argv, "--follow", follow)
			}
			if file != "" {
				argv = append(argv, "--file", file)
			}
			argv = append(argv, agentTarget)
			argv = append(argv, passthrough...)

			prompt := strings.TrimSpace(strings.Join(promptArgs, " "))
			switch {
			case prompt != "":
				argv = append(argv, prompt)
			case file == "" && !isStdinTTY():
				argv = append(argv, "-")
			}

			child := exec.Command(bin, argv...)
			child.Env = os.Environ()
			child.Stdin = os.Stdin
			child.Stdout = cmd.OutOrStdout()
			child.Stderr = cmd.ErrOrStderr()

			if err := child.Run(); err != nil {
				if exit, ok := err.(*exec.ExitError); ok {
					return exitErrorReported(exit.ExitCode(), err)
				}
				return exitError(1, fmt.Errorf("running hrcchat turn: %w", err))
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&clearContext, "new", false, "Clear context before dispatching (clean slate)")
	cmd.Flags().StringVar(&format, "format", "", "Output format: tree, compact, ndjson, json")
	cmd.Flags().StringVar(&follow, "follow", "", "Emit bounded progress at the given interval (e.g. 30s)")
	cmd.Flags().StringVar(&file, "file", "", "Read prompt from file")
	return cmd
}

func isStdinTTY() bool {
	info, err := os.Stdin.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
