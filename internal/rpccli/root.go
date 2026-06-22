package rpccli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// mirroredCommand describes one top-level command of the production `wrkq`
// surface that the mirror reproduces. For the seam smoke slice every command
// except `cat` is a not-implemented stub; the stubs exist so the command-tree
// parity test can fail on path/alias drift early (see tree parity test in
// internal/cli). Coverage status per command lives in docs/rpc-cli-migration.md.
type mirroredCommand struct {
	use     string
	aliases []string
}

// topLevelCommands mirrors `wrkq`'s top-level command paths and aliases exactly.
// Keep this in lockstep with internal/cli rootCmd.AddCommand registrations; the
// parity test is the guardrail.
var topLevelCommands = []mirroredCommand{
	// ack is RPC-backed (real parity command); registered separately.
	{use: "agent [prompt] [-- extra hrcchat-turn args]"},
	{use: "agent-context"},
	{use: "agent-info"},
	{use: "apply <PATHSPEC|ID> <FILE|->"},
	{use: "bundle"},
	// cat is the one real command this slice; registered separately.
	// check / check-inbox are RPC-backed (real parity commands); registered separately.
	{use: "cp <source>... <destination>"},
	{use: "diff <A> [B]"},
	// find is RPC-backed (real parity command); registered separately.
	{use: "handoff"},
	{use: "index"},
	{use: "log <PATHSPEC|ID>"},
	// ls is RPC-backed (real parity command); registered separately.
	{use: "monitor"},
	{use: "projects"},
	{use: "rename-container <path|id> <new-slug>"},
	{use: "restore <path|id|uuid>"},
	{use: "rm <path|id>..."},
	{use: "rpc --stdio"},
	{use: "search <query> [PATH...]"},
	{use: "server"},
	// stat is RPC-backed (real parity command); registered separately.
	// tree is RPC-backed (real parity command); registered separately.
	{use: "usage", aliases: []string{"info"}},
	{use: "version"},
	{use: "watch [PATH...]"},
	{use: "webhook"},
	{use: "whoami"},
}

// NewRootCmd builds the mirror's cobra tree. It reproduces the production
// persistent flags and top-level command surface, wiring the real `cat`
// seam-smoke command and not-implemented stubs for everything else.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "wrkq-rpccli",
		Short:         "RPC-backed mirror of wrkq (parity harness, not for production use)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Mirror the production persistent flags verbatim so the parity test and the
	// transport DB override line up.
	root.PersistentFlags().String("db", "", "Path to database file (overrides WRKQ_DB_PATH)")
	root.PersistentFlags().String("as", "", "Principal ref or compat actor slug for write attribution")
	root.PersistentFlags().String("project", "", "Project to operate under (overrides WRKQ_PROJECT_ROOT)")
	root.PersistentFlags().String("output", "", "Output mode: table, human, json, ndjson, porcelain, yaml, tsv, raw")

	root.AddCommand(newCatCmd())
	root.AddCommand(newAckCmd())
	root.AddCommand(newStatCmd())
	root.AddCommand(newMkdirCmd())
	root.AddCommand(newRmdirCmd())
	root.AddCommand(newTouchCmd())
	root.AddCommand(newMvCmd())
	root.AddCommand(newSetCmd())
	root.AddCommand(newCommentCmd())
	root.AddCommand(newRelationCmd())
	root.AddCommand(newContainerCmd())
	root.AddCommand(newAttachCmd())
	root.AddCommand(newLsCmd())
	root.AddCommand(newFindCmd())
	root.AddCommand(newTreeCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newCheckInboxCmd())
	for _, mc := range topLevelCommands {
		root.AddCommand(newStubCmd(mc))
	}
	return root
}

// Execute runs the mirror root command.
func Execute() error {
	return NewRootCmd().Execute()
}

func newStubCmd(mc mirroredCommand) *cobra.Command {
	return &cobra.Command{
		Use:     mc.use,
		Aliases: mc.aliases,
		Short:   "(mirror stub — not implemented in seam smoke slice)",
		Args:    cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return fmt.Errorf("%s: not implemented in wrkq-rpccli (seam smoke slice)", cmd.Name())
		},
	}
}

// dbOverride returns the value of the inherited --db persistent flag.
func dbOverride(cmd *cobra.Command) string {
	if f := cmd.Flag("db"); f != nil {
		return f.Value.String()
	}
	return ""
}
