package cli

import "github.com/spf13/cobra"

// handoffCmd is the parent group for all `wrkq handoff` subcommands.
//
// Handoffs are a sibling collaboration primitive to tasks, comments, and
// memory for agent-scoped session continuity:
//
//   - Tasks coordinate work between actors.
//   - Comments add discussion or updates to a task.
//   - Memory is auto-managed/pruned agent knowledge.
//   - Handoffs are intentional context records for the same agent to consume
//     in a later session.
//
// Pending handoffs are eligible to be loaded into future agent context.
// Acknowledged handoffs are retained for history and search, but should not
// appear in default pending listings or be loaded by default.
//
// Phase B2 (T-01596) registers the command surface only. Each verb returns a
// "not implemented yet" error until its Phase C task lands.
var handoffCmd = &cobra.Command{
	Use:   "handoff",
	Short: "Manage agent session handoffs",
	Long: `Manage agent session handoffs.

Handoffs are a sibling collaboration primitive to tasks, comments, and memory
for agent-scoped session continuity. A handoff is an intentional context
record for the same agent to consume in a later session — manually created,
auditable, searchable, and intentionally retired when no longer relevant.

Pending handoffs are eligible to be loaded into future agent context.
Acknowledged handoffs are retained for history and search but do not appear
in default pending listings.

Scope is agent/project for v1 (e.g. cody@wrkq). Task and lane scope are
intentionally out of scope for the first implementation. Default scope
resolution reads agent runtime env first (ASP_SCOPE_REF, ASP_HANDLE,
ASP_AGENT_ID + ASP_PROJECT). Subcommands that accept --scope use it as an
explicit override.`,
}

// ---- handoff create -------------------------------------------------------

var (
	handoffCreateTitle          string
	handoffCreateBodyFile       string
	handoffCreateScope          string
	handoffCreateProfile        string
	handoffCreateIdempotencyKey string
	handoffCreateMeta           string
	handoffCreateDryRun         bool
	handoffCreateJSON           bool
	handoffCreateNDJSON         bool
	handoffCreateHuman          bool
)

var handoffCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a new pending handoff for the resolved agent/project scope",
	Long: `Create a new pending handoff.

The handoff is scoped to the current env-derived agent/project by default
(ASP_SCOPE_REF → ASP_HANDLE → ASP_AGENT_ID + ASP_PROJECT). Use --scope to
override with an explicit ScopeRef (agent:cody:project:wrkq) or handle
(cody@wrkq).

Body content is supplied via --body-file (path or '-' for stdin). The CLI is
non-interactive: there are no prompts in headless contexts.

Create supports --idempotency-key so retrying an agent call does not create
duplicate handoffs for the same scope.

Output defaults to human-readable on a TTY and JSON when piped. Use --json,
--ndjson, or --human to force a mode.`,
	RunE: runHandoffCreate,
}

// ---- handoff list ---------------------------------------------------------

var (
	handoffListScope  string
	handoffListStatus string
	handoffListLimit  int
	handoffListCursor string
	handoffListJSON   bool
	handoffListNDJSON bool
	handoffListHuman  bool
)

var handoffListCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List handoffs for the resolved agent/project scope",
	Long: `List handoffs for the resolved agent/project scope.

By default lists pending handoffs for the current default scope (no flags
required). Use --status to widen to acknowledged or all handoffs.

Results are bounded and paginated. Use --limit and --cursor to page through
larger result sets; the response includes a next_cursor token when more
results are available.

Output defaults to human-readable on a TTY and NDJSON when piped. Use --json,
--ndjson, or --human to force a mode.

Alias: 'wrkq handoff ls'.`,
	RunE: runHandoffList,
}

// ---- handoff get ----------------------------------------------------------

var (
	handoffGetJSON  bool
	handoffGetHuman bool
)

var handoffGetCmd = &cobra.Command{
	Use:     "get <handoff-id>",
	Aliases: []string{"cat"},
	Short:   "Get a single handoff by ID",
	Long: `Get a single handoff by ID.

The handoff ID is the friendly ID (e.g. H-00001). Returns the full handoff
record including title, body, scope, status, and acknowledgement metadata
when present.

Output defaults to human-readable on a TTY and JSON when piped. Use --json
or --human to force a mode.

Alias: 'wrkq handoff cat <id>'.`,
	Args: cobra.ExactArgs(1),
	RunE: runHandoffGet,
}

// ---- handoff acknowledge --------------------------------------------------

var (
	handoffAckNote    string
	handoffAckDryRun  bool
	handoffAckIfMatch int64
	handoffAckJSON    bool
	handoffAckHuman   bool
)

var handoffAckCmd = &cobra.Command{
	Use:   "acknowledge <handoff-id>",
	Short: "Acknowledge a handoff so it is no longer pending",
	Long: `Acknowledge a handoff so it no longer appears in default pending listings.

Acknowledgement is the only retirement mechanism — handoffs do not expire
automatically. Acknowledged handoffs are retained for history and search.

The actor is resolved from --as, agent runtime env, or, for an exact handoff ID,
the handoff row's agent/project as a last-resort sparse-shell fallback.

Use --note to attach a short acknowledgement note describing why the
handoff was consumed or is obsolete. Use --dry-run to inspect the mutation
before applying it.

Output defaults to human-readable on a TTY and JSON when piped. Use --json
or --human to force a mode.`,
	Args: cobra.ExactArgs(1),
	RunE: runHandoffAck,
}

// ---- handoff search -------------------------------------------------------

var (
	handoffSearchScope  string
	handoffSearchStatus string
	handoffSearchLimit  int
	handoffSearchCursor string
	handoffSearchJSON   bool
	handoffSearchNDJSON bool
	handoffSearchHuman  bool
)

var handoffSearchCmd = &cobra.Command{
	Use:   "search <query>",
	Short: "Search handoffs by title, body, scope, or status",
	Long: `Search handoffs by title, body, scope, or status.

The query is matched against title and body content; --scope and --status
narrow the result set further. Results are bounded and paginated via
--limit and --cursor.

Output defaults to human-readable on a TTY and NDJSON when piped. Use --json,
--ndjson, or --human to force a mode.`,
	Args: cobra.ArbitraryArgs,
	RunE: runHandoffSearch,
}

func init() {
	rootCmd.AddCommand(handoffCmd)

	handoffCmd.AddCommand(handoffCreateCmd)
	handoffCmd.AddCommand(handoffListCmd)
	handoffCmd.AddCommand(handoffGetCmd)
	handoffCmd.AddCommand(handoffAckCmd)
	handoffCmd.AddCommand(handoffSearchCmd)

	// create flags
	handoffCreateCmd.Flags().StringVarP(&handoffCreateTitle, "title", "t", "",
		"Handoff title (required)")
	handoffCreateCmd.Flags().StringVar(&handoffCreateBodyFile, "body-file", "",
		"Path to a file containing the markdown body, or '-' to read from stdin (required)")
	handoffCreateCmd.Flags().StringVar(&handoffCreateScope, "scope", "",
		"Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	handoffCreateCmd.Flags().StringVar(&handoffCreateProfile, "profile", "",
		"Named profile to resolve scope/defaults from")
	handoffCreateCmd.Flags().StringVar(&handoffCreateIdempotencyKey, "idempotency-key", "",
		"Idempotency key to safely retry create without producing duplicates")
	handoffCreateCmd.Flags().StringVar(&handoffCreateMeta, "meta", "",
		"Optional JSON object of additional metadata")
	handoffCreateCmd.Flags().BoolVar(&handoffCreateDryRun, "dry-run", false,
		"Validate inputs and print the prospective handoff without writing it")
	handoffCreateCmd.Flags().BoolVar(&handoffCreateJSON, "json", false, "Force JSON output")
	handoffCreateCmd.Flags().BoolVar(&handoffCreateNDJSON, "ndjson", false, "Force NDJSON output")
	handoffCreateCmd.Flags().BoolVar(&handoffCreateHuman, "human", false, "Force human-readable output")

	// list flags
	handoffListCmd.Flags().StringVar(&handoffListScope, "scope", "",
		"Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	handoffListCmd.Flags().StringVar(&handoffListStatus, "status", "pending",
		"Status filter: pending, acknowledged, or all (default: pending)")
	handoffListCmd.Flags().IntVar(&handoffListLimit, "limit", 0,
		"Maximum number of results (0 = server default)")
	handoffListCmd.Flags().StringVar(&handoffListCursor, "cursor", "",
		"Pagination cursor from a previous page")
	handoffListCmd.Flags().BoolVar(&handoffListJSON, "json", false, "Force JSON output")
	handoffListCmd.Flags().BoolVar(&handoffListNDJSON, "ndjson", false, "Force NDJSON output")
	handoffListCmd.Flags().BoolVar(&handoffListHuman, "human", false, "Force human-readable output")

	// get flags
	handoffGetCmd.Flags().BoolVar(&handoffGetJSON, "json", false, "Force JSON output")
	handoffGetCmd.Flags().BoolVar(&handoffGetHuman, "human", false, "Force human-readable output")

	// acknowledge flags
	handoffAckCmd.Flags().StringVar(&handoffAckNote, "note", "",
		"Optional acknowledgement note describing why the handoff was consumed or is obsolete")
	handoffAckCmd.Flags().BoolVar(&handoffAckDryRun, "dry-run", false,
		"Inspect the mutation without applying it")
	handoffAckCmd.Flags().Int64Var(&handoffAckIfMatch, "if-match", 0,
		"Reject the acknowledgement when the current etag does not equal this value")
	handoffAckCmd.Flags().BoolVar(&handoffAckJSON, "json", false, "Force JSON output")
	handoffAckCmd.Flags().BoolVar(&handoffAckHuman, "human", false, "Force human-readable output")

	// search flags
	handoffSearchCmd.Flags().StringVar(&handoffSearchScope, "scope", "",
		"Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	handoffSearchCmd.Flags().StringVar(&handoffSearchStatus, "status", "pending",
		"Status filter: pending, acknowledged, or all (default: pending)")
	handoffSearchCmd.Flags().IntVar(&handoffSearchLimit, "limit", 0,
		"Maximum number of results (0 = server default)")
	handoffSearchCmd.Flags().StringVar(&handoffSearchCursor, "cursor", "",
		"Pagination cursor from a previous page")
	handoffSearchCmd.Flags().BoolVar(&handoffSearchJSON, "json", false, "Force JSON output")
	handoffSearchCmd.Flags().BoolVar(&handoffSearchNDJSON, "ndjson", false, "Force NDJSON output")
	handoffSearchCmd.Flags().BoolVar(&handoffSearchHuman, "human", false, "Force human-readable output")
}

// runHandoffList lives in handoff_list.go (Phase C2 / T-01598).

// runHandoffGet lives in handoff_get.go (Phase C3 / T-01599).

// runHandoffAck lives in handoff_acknowledge.go (Phase C4 / T-01600).

// runHandoffSearch lives in handoff_search.go (Phase C5 / T-01601).
