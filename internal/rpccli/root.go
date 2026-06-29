package rpccli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// mirroredCommand describes a command surface represented by an explicit
// not-implemented mirror stub. Top-level stubs are expected to remain empty for
// primary cutover; nested development stubs may still use newStubCmd directly.
// Coverage status per command lives in docs/rpc-cli-migration.md.
type mirroredCommand struct {
	use     string
	aliases []string
}

// topLevelCommands is the remaining top-level stub inventory. It should stay
// empty unless a newly added legacy command is intentionally classified as a
// mirror gap with docs/tests.
var topLevelCommands = []mirroredCommand{
	// ack is RPC-backed (real parity command); registered separately.
	// agent is local pass-through to hrcchat; registered separately.
	// agent-context is local/RPC-lookup parity; registered separately.
	// agent-info is local-only and byte-proven; registered separately.
	// apply is RPC-backed (wrkq.task.update via the caller-side parse/gate); registered separately.
	// bundle is sunset before production cutover; see T-04371.
	// cat is the one real command this slice; registered separately.
	// check / check-inbox are RPC-backed (real parity commands); registered separately.
	// cp is RPC-backed (server-owned deep copy via wrkq.task.copy; caller owns
	// fan-out/prompt/dry-run/output); registered separately.
	// diff is RPC-backed (real parity command); registered separately.
	// find is RPC-backed (real parity command); registered separately.
	// handoff is RPC-backed (wrkq.handoff.create/get/listView/acknowledge; caller-
	// owned scope resolution + self-scope enforcement client-side; searchView
	// deferred); registered separately.
	// index is RPC-backed (server-owned sidecar lifecycle via wrkq.index.*,
	// T-05114); registered separately.
	// log is RPC-backed (real parity command); registered separately.
	// ls is RPC-backed (real parity command); registered separately.
	// monitor is RPC-backed (bounded polling via wrkq.monitor.eventsView +
	// wrkq.monitor.stateView; caller owns the loop/terminal/exit codes); registered separately.
	// projects is RPC-backed via wrkq.project.listView; registered separately.
	// rename-container is RPC-backed (new wrkq.container.update, narrow slug/title
	// patch + etag CAS, T-05112); registered separately.
	// restore is RPC-backed (extended wrkq.task.restore for tasks,
	// wrkq.container.restore for containers); registered separately.
	// rm is RPC-backed (caller-owned-confirmation seam); registered separately.
	// rpc --stdio is local protocol serving via workrpc.ServeStdio; registered separately.
	// search is RPC-backed (server-owned wrkq.search.listView, T-05114);
	// registered separately.
	// server is local daemon control; registered separately.
	// stat is RPC-backed (real parity command); registered separately.
	// tree is RPC-backed (real parity command); registered separately.
	// usage/info is local-only and byte-proven; registered separately.
	// version is local-only and byte-proven; registered separately.
	// watch is RPC-backed (bounded raw tail via wrkq.history.tailView; caller owns
	// the follow loop + deprecation warning + rendering); registered separately.
	// webhook is RPC-backed (DEDICATED family wrkq.webhook.add/remove/listView,
	// T-05119); registered separately.
	// whoami is local/config attribution parity; registered separately.
}

// NewRootCmd builds the compatibility mirror's cobra tree. Tests keep using the
// wrkq-rpccli command name while the production entrypoint calls
// NewRootCmdFor("wrkq").
func NewRootCmd() *cobra.Command {
	return NewRootCmdFor("wrkq-rpccli")
}

// NewRootCmdFor builds the RPC-backed cobra tree using the supplied command
// name. The tree reproduces the production persistent flags and top-level
// command surface, wiring implemented RPC-backed commands plus any explicit
// temporary stubs listed in topLevelCommands.
func NewRootCmdFor(commandName string) *cobra.Command {
	if commandName == "" {
		commandName = "wrkq"
	}
	short := "Task management CLI"
	if commandName == "wrkq-rpccli" {
		short = "RPC-backed mirror of wrkq (parity harness, not for production use)"
	}
	root := &cobra.Command{
		Use:           commandName,
		Short:         short,
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
	root.AddCommand(newDiffCmd())
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
	root.AddCommand(newLogCmd())
	root.AddCommand(newLsCmd())
	root.AddCommand(newFindCmd())
	root.AddCommand(newTreeCmd())
	root.AddCommand(newCheckCmd())
	root.AddCommand(newCheckInboxCmd())
	root.AddCommand(newRmCmd())
	root.AddCommand(newApplyCmd())
	root.AddCommand(newRestoreCmd())
	root.AddCommand(newRenameContainerCmd())
	root.AddCommand(newCpCmd())
	root.AddCommand(newWebhookCmd())
	root.AddCommand(newHandoffCmd())
	root.AddCommand(newMonitorCmd())
	root.AddCommand(newWatchCmd())
	root.AddCommand(newSearchCmd())
	root.AddCommand(newIndexCmd())
	root.AddCommand(newVersionCmd())
	root.AddCommand(newUsageCmd())
	root.AddCommand(newAgentInfoCmd())
	root.AddCommand(newWhoamiCmd())
	root.AddCommand(newProjectsCmd())
	root.AddCommand(newAgentContextCmd())
	root.AddCommand(newRPCCmd())
	root.AddCommand(newAgentCmd())
	root.AddCommand(newServerCmd())
	for _, mc := range topLevelCommands {
		root.AddCommand(newStubCmd(mc))
	}
	return root
}

// Execute runs the mirror root command.
func Execute() error {
	return NewRootCmd().Execute()
}

// ExecuteAs runs the RPC-backed CLI under a specific executable name.
func ExecuteAs(commandName string) error {
	return NewRootCmdFor(commandName).Execute()
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
