package rpccli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

//go:generate go run ../../cmd/gen-rpccli-surface-manifest --out cli_surface_manifest.json

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

// NewRootCmd builds the production RPC-backed cobra tree.
func NewRootCmd() *cobra.Command {
	return NewRootCmdFor("wrkq")
}

// NewRootCmdFor builds the RPC-backed cobra tree using the supplied command
// name. The tree reproduces the production persistent flags and top-level
// command surface, wiring implemented RPC-backed commands plus any explicit
// temporary stubs listed in topLevelCommands.
func NewRootCmdFor(commandName string) *cobra.Command {
	if commandName == "" {
		commandName = "wrkq"
	}
	root := &cobra.Command{
		Use:           commandName,
		Short:         "Task management CLI",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Mirror the production persistent flags verbatim so the parity test and the
	// transport DB override line up.
	root.PersistentFlags().String("db", "", "Path to database file (overrides WRKQ_DB_PATH)")
	root.PersistentFlags().String("principal-ref", "", "Caller principal for write attribution: agent:<id> or full agent ScopeRef")
	root.PersistentFlags().String("as", "", "Alias for --principal-ref; accepts agent:<id> or a full agent ScopeRef")
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
	root.AddCommand(newClaimCmd())
	root.AddCommand(newReleaseCmd())
	root.AddCommand(newCommentCmd())
	root.AddCommand(newRelationCmd())
	root.AddCommand(newContainerCmd())
	root.AddCommand(newCampaignCmd())
	root.AddCommand(newPromiseCmd())
	root.AddCommand(newTimelineCmd())
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
	applyHelpTemplates(root)
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
			return fmt.Errorf("%s: not implemented in wrkq RPC CLI", cmd.Name())
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

const rootHelpTemplate = `{{with .Short}}{{.}}{{end}}
Usage:
  {{.UseLine}}
Commands:
{{commandColumns .Commands 4}}{{if .HasAvailableLocalFlags}}Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}
Use "{{.CommandPath}} <command> --help" for command details.
`

// wrkcRootHelpTemplate lists one command per line with its Short. wrkc has a
// small, unfamiliar verb set whose names do not say what they do (`defer`,
// `ack`, `withdraw`), so the description earns its width. wrkq keeps the
// four-column grid: at 46 commands a description list stops being scannable.
const wrkcRootHelpTemplate = `{{with .Short}}{{.}}{{end}}
Usage:
  {{.UseLine}}
Commands:
{{commandDescriptions .Commands}}{{if .HasAvailableLocalFlags}}Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}
Use "{{.CommandPath}} <command> --help" for command details.
`

const commandHelpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}{{end}}
Usage:
  {{.UseLine}}{{if gt (len .Aliases) 0}}
Aliases:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}
Examples:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}
Commands:
{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}  {{rpad .Name .NamePadding }} {{.Short}}
{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}
Flags:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}
Global Flags:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}
`

func applyHelpTemplates(root *cobra.Command) {
	registerHelpTemplateFuncs()
	root.SetHelpTemplate(rootHelpTemplate)
	for _, cmd := range root.Commands() {
		applyCommandHelpTemplate(cmd)
	}
}

// applyWrkcHelpTemplates is applyHelpTemplates with the description-list root.
func applyWrkcHelpTemplates(root *cobra.Command) {
	registerHelpTemplateFuncs()
	root.SetHelpTemplate(wrkcRootHelpTemplate)
	for _, cmd := range root.Commands() {
		applyCommandHelpTemplate(cmd)
	}
}

func registerHelpTemplateFuncs() {
	cobra.AddTemplateFunc("commandColumns", commandColumns)
	cobra.AddTemplateFunc("commandDescriptions", commandDescriptions)
}

func applyCommandHelpTemplate(cmd *cobra.Command) {
	cmd.SetHelpTemplate(commandHelpTemplate)
	for _, child := range cmd.Commands() {
		applyCommandHelpTemplate(child)
	}
}

func commandColumns(commands []*cobra.Command, columns int) string {
	if columns <= 0 {
		columns = 4
	}
	names := make([]string, 0, len(commands))
	maxLen := 0
	for _, cmd := range commands {
		if !cmd.IsAvailableCommand() && cmd.Name() != "help" {
			continue
		}
		name := cmd.Name()
		names = append(names, name)
		if len(name) > maxLen {
			maxLen = len(name)
		}
	}
	if len(names) == 0 {
		return ""
	}

	width := maxLen + 2
	var b strings.Builder
	for i := 0; i < len(names); i += columns {
		end := i + columns
		if end > len(names) {
			end = len(names)
		}
		b.WriteString("  ")
		for j, name := range names[i:end] {
			if j > 0 {
				b.WriteString("  ")
			}
			if j == end-i-1 {
				b.WriteString(name)
				continue
			}
			fmt.Fprintf(&b, "%-*s", width, name)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// commandDescriptions renders one "name  short" line per available command,
// name column padded to align the descriptions. Like commandColumns it emits a
// trailing newline, which the root templates depend on.
func commandDescriptions(commands []*cobra.Command) string {
	type entry struct{ name, short string }

	entries := make([]entry, 0, len(commands))
	maxLen := 0
	for _, cmd := range commands {
		if !cmd.IsAvailableCommand() && cmd.Name() != "help" {
			continue
		}
		entries = append(entries, entry{name: cmd.Name(), short: cmd.Short})
		if len(cmd.Name()) > maxLen {
			maxLen = len(cmd.Name())
		}
	}
	if len(entries) == 0 {
		return ""
	}

	var b strings.Builder
	for _, e := range entries {
		if e.short == "" {
			fmt.Fprintf(&b, "  %s\n", e.name)
			continue
		}
		fmt.Fprintf(&b, "  %-*s  %s\n", maxLen, e.name, e.short)
	}
	return b.String()
}
