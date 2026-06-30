package cli

import (
	"fmt"
	"io"
	"os"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

var (
	agentContextScope string
	agentContextJSON  bool
	agentContextHuman bool
)

var agentContextCmd = &cobra.Command{
	Use:   "agent-context",
	Short: "Print the resolved agent scope and environment context",
	Long: `Resolve the active agent scope from --scope, ASP_SCOPE_REF, ASP_HANDLE,
or ASP_AGENT_ID + ASP_PROJECT and print the result.

Useful for debugging agent runtime context. Works without a database
connection — actor / container UUID lookups are best-effort extras.

Exits 0 on success, 2 when no scope is resolvable.

Output defaults to a human-readable table on a TTY and JSON when piped.
Use --json or --human to force a mode.`,
	RunE: runAgentContext,
}

func init() {
	rootCmd.AddCommand(agentContextCmd)
	agentContextCmd.Flags().StringVar(&agentContextScope, "scope", "",
		"Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	agentContextCmd.Flags().BoolVar(&agentContextJSON, "json", false, "Force JSON output")
	agentContextCmd.Flags().BoolVar(&agentContextHuman, "human", false, "Force human-readable output")
}

// agentContextOutput is the JSON shape printed by --json.
type agentContextOutput struct {
	Env          scope.EnvSnapshot    `json:"env"`
	Scope        *scope.ResolvedScope `json:"scope,omitempty"`
	Diagnostics  []scope.Diagnostic   `json:"diagnostics,omitempty"`
	Error        string               `json:"error,omitempty"`
	Lookups      *agentContextLookups `json:"lookups,omitempty"`
	DBPath       string               `json:"db_path,omitempty"`
	DBReachable  bool                 `json:"db_reachable"`
	DBError      string               `json:"db_error,omitempty"`
	OverrideFlag string               `json:"override_flag,omitempty"`
}

// agentContextLookups carries optional DB-backed identity info. All fields are
// best-effort: any failed lookup leaves the corresponding field empty.
type agentContextLookups struct {
	ContainerUUID       string `json:"container_uuid,omitempty"`
	ContainerFriendlyID string `json:"container_friendly_id,omitempty"`
}

func runAgentContext(cmd *cobra.Command, _ []string) error {
	stderr := cmd.ErrOrStderr()
	stdout := cmd.OutOrStdout()
	cfg, cfgErr := config.Load()
	if cfgErr != nil {
		return fmt.Errorf("failed to load config: %w", cfgErr)
	}
	sel, modeErr := resolveOutputMode(cmd, cfg, outputShapeSingleton, outputResolveOptions{
		Allow:      []outputMode{outputModeHuman, outputModeJSON, outputModeNDJSON},
		DefaultTTY: outputModeHuman,
	})
	if modeErr != nil {
		return modeErr
	}

	env := scope.ReadEnv()
	resolved, diags, resolveErr := scope.Resolve(agentContextScope)

	// DB lookups are best-effort — never fail the command if they fail.
	lookups, dbPath, dbReachable, dbErr := tryDBLookups(cmd, resolved, resolveErr)

	out := agentContextOutput{
		Env:         env,
		Diagnostics: diags,
		DBPath:      dbPath,
		DBReachable: dbReachable,
	}
	if dbErr != "" {
		out.DBError = dbErr
	}
	if agentContextScope != "" {
		out.OverrideFlag = agentContextScope
	}
	if resolveErr != nil {
		out.Error = resolveErr.Error()
	} else {
		r := resolved
		out.Scope = &r
		out.Lookups = lookups
	}

	switch sel.Mode {
	case outputModeJSON:
		if err := writeJSONOutput(stdout, sel, out); err != nil {
			return err
		}
	case outputModeNDJSON:
		if err := writeNDJSONOutput(stdout, out); err != nil {
			return err
		}
	default:
		printAgentContextHuman(stdout, stderr, out)
	}

	if resolveErr != nil {
		// Documented: exit 2 on unresolvable scope.
		os.Exit(2)
	}
	return nil
}

// tryDBLookups opens the DB if possible and looks up the actor (by AgentID
// slug) and container (by ProjectID slug). Failures are silently absorbed —
// the agent-context command must work without a DB.
func tryDBLookups(cmd *cobra.Command, resolved scope.ResolvedScope, resolveErr error) (*agentContextLookups, string, bool, string) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", false, fmt.Sprintf("config load failed: %v", err)
	}

	// Honor --db override on rootCmd.
	if dbFlag := cmd.Flag("db"); dbFlag != nil {
		if v := dbFlag.Value.String(); v != "" {
			cfg.DBPath = v
		}
	}

	if cfg.DBPath == "" {
		return nil, "", false, ""
	}

	if _, statErr := os.Stat(cfg.DBPath); statErr != nil {
		return nil, cfg.DBPath, false, fmt.Sprintf("db not present: %v", statErr)
	}

	database, openErr := db.Open(cfg.DBPath)
	if openErr != nil {
		return nil, cfg.DBPath, false, fmt.Sprintf("db open failed: %v", openErr)
	}
	defer func() { _ = database.Close() }()

	// If a migration is required, treat DB as unreachable for lookups but
	// keep the path/reachable signal honest. Don't return an error.
	if mErr := database.RequiresMigrationError(); mErr != nil {
		return nil, cfg.DBPath, true, fmt.Sprintf("db requires migration: %v", mErr)
	}

	if resolveErr != nil {
		// Nothing to look up if scope didn't resolve.
		return nil, cfg.DBPath, true, ""
	}

	lookups := &agentContextLookups{}

	// Container lookup: ProjectID slug → top-level project container.
	if resolved.ProjectID != "" {
		var contUUID, contID string
		err := database.QueryRow(
			`SELECT uuid, id FROM containers
			 WHERE slug = ? AND parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')
			 LIMIT 1`,
			resolved.ProjectID,
		).Scan(&contUUID, &contID)
		if err == nil {
			lookups.ContainerUUID = contUUID
			lookups.ContainerFriendlyID = contID
		}
		// Silently ignore non-ENOENT lookup errors — best effort only.
	}

	return lookups, cfg.DBPath, true, ""
}

func printAgentContextHuman(stdout, stderr io.Writer, out agentContextOutput) {
	fmt.Fprintln(stdout, "Environment:")
	fmt.Fprintf(stdout, "  ASP_SCOPE_REF = %s\n", quoteOrEmpty(out.Env.ASPScopeRef))
	fmt.Fprintf(stdout, "  ASP_HANDLE    = %s\n", quoteOrEmpty(out.Env.ASPHandle))
	fmt.Fprintf(stdout, "  ASP_AGENT_ID  = %s\n", quoteOrEmpty(out.Env.ASPAgentID))
	fmt.Fprintf(stdout, "  ASP_PROJECT   = %s\n", quoteOrEmpty(out.Env.ASPProject))
	if out.OverrideFlag != "" {
		fmt.Fprintf(stdout, "  --scope       = %s\n", out.OverrideFlag)
	}

	fmt.Fprintln(stdout)
	if out.Scope != nil {
		r := out.Scope
		fmt.Fprintln(stdout, "Resolved scope:")
		fmt.Fprintf(stdout, "  source        = %s\n", r.Source)
		fmt.Fprintf(stdout, "  raw           = %s\n", r.Raw)
		fmt.Fprintf(stdout, "  kind          = %s\n", r.Kind)
		fmt.Fprintf(stdout, "  agent_id      = %s\n", r.AgentID)
		if r.ProjectID != "" {
			fmt.Fprintf(stdout, "  project_id    = %s\n", r.ProjectID)
		}
		if r.TaskID != "" {
			fmt.Fprintf(stdout, "  task_id       = %s (dropped in canonical_ref for v1)\n", r.TaskID)
		}
		if r.RoleName != "" {
			fmt.Fprintf(stdout, "  role_name     = %s (dropped in canonical_ref for v1)\n", r.RoleName)
		}
		fmt.Fprintf(stdout, "  canonical_ref = %s\n", r.CanonicalRef)
	} else if out.Error != "" {
		fmt.Fprintln(stdout, "Resolved scope: <unresolved>")
		fmt.Fprintf(stderr, "Error: %s\n", out.Error)
	}

	if len(out.Diagnostics) > 0 {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Diagnostics:")
		for _, d := range out.Diagnostics {
			fmt.Fprintf(stdout, "  [%s] %s: %s\n", d.Level, d.Code, d.Message)
		}
	}

	fmt.Fprintln(stdout)
	fmt.Fprintln(stdout, "Database:")
	if out.DBPath == "" {
		fmt.Fprintln(stdout, "  path        = <not configured>")
	} else {
		fmt.Fprintf(stdout, "  path        = %s\n", out.DBPath)
		fmt.Fprintf(stdout, "  reachable   = %t\n", out.DBReachable)
		if out.DBError != "" {
			fmt.Fprintf(stdout, "  status      = %s\n", out.DBError)
		}
	}

	if out.Lookups != nil && out.Lookups.ContainerUUID != "" {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Lookups (best-effort):")
		if out.Lookups.ContainerUUID != "" {
			fmt.Fprintf(stdout, "  container   = %s (%s)\n",
				out.Lookups.ContainerFriendlyID, out.Lookups.ContainerUUID)
		}
	}
}

func quoteOrEmpty(s string) string {
	if s == "" {
		return "<unset>"
	}
	return s
}

// isStdoutTTY reports whether w is an interactive terminal. Returns false for
// non-*os.File writers (e.g. tests using a bytes.Buffer).
func isStdoutTTY(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}
