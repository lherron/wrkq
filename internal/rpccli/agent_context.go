package rpccli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	"github.com/spf13/cobra"
)

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

type agentContextLookups struct {
	ActorUUID           string `json:"actor_uuid,omitempty"`
	ActorFriendlyID     string `json:"actor_friendly_id,omitempty"`
	ContainerUUID       string `json:"container_uuid,omitempty"`
	ContainerFriendlyID string `json:"container_friendly_id,omitempty"`
}

func newAgentContextCmd() *cobra.Command {
	var overrideScope string
	var asJSON, human bool
	cmd := &cobra.Command{
		Use:   "agent-context",
		Short: "Print the resolved agent scope and environment context",
		Long: `Resolve the active agent scope from --scope, ASP_SCOPE_REF, ASP_HANDLE,
or ASP_AGENT_ID + ASP_PROJECT and print the result.

Useful for debugging agent runtime context. Works without a database
connection — actor / container UUID lookups are best-effort extras.

Exits 0 on success, 2 when no scope is resolvable.

Output defaults to a human-readable table on a TTY and JSON when piped.
Use --json or --human to force a mode.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			mode, err := resolveAgentContextMode(cmd, asJSON, human)
			if err != nil {
				return err
			}

			env := scope.ReadEnv()
			resolved, diags, resolveErr := scope.Resolve(overrideScope)
			lookups, dbPath, dbReachable, dbErr := tryAgentContextLookups(cmd, resolved, resolveErr)

			out := agentContextOutput{
				Env:         env,
				Diagnostics: diags,
				DBPath:      dbPath,
				DBReachable: dbReachable,
			}
			if dbErr != "" {
				out.DBError = dbErr
			}
			if overrideScope != "" {
				out.OverrideFlag = overrideScope
			}
			if resolveErr != nil {
				out.Error = resolveErr.Error()
			} else {
				r := resolved
				out.Scope = &r
				out.Lookups = lookups
			}

			switch mode {
			case "json":
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				if err := enc.Encode(out); err != nil {
					return err
				}
			case "ndjson":
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(out); err != nil {
					return err
				}
			default:
				printAgentContextHuman(cmd.OutOrStdout(), cmd.ErrOrStderr(), out)
			}

			if resolveErr != nil {
				os.Exit(2)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&overrideScope, "scope", "",
		"Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Force JSON output")
	cmd.Flags().BoolVar(&human, "human", false, "Force human-readable output")
	return cmd
}

func resolveAgentContextMode(cmd *cobra.Command, asJSON, human bool) (string, error) {
	if asJSON && human {
		return "", fmt.Errorf("choose only one output mode")
	}
	if asJSON {
		return "json", nil
	}
	if human {
		return "human", nil
	}
	if outF := cmd.Flag("output"); outF != nil && outF.Changed {
		m := strings.ToLower(strings.TrimSpace(outF.Value.String()))
		switch m {
		case "json":
			return "json", nil
		case "ndjson", "porcelain":
			return "ndjson", nil
		case "human":
			return "human", nil
		case "table", "yaml", "tsv", "raw":
			return "", fmt.Errorf("output mode %q is not supported for this command", m)
		default:
			return "", fmt.Errorf("unknown output mode %q", m)
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "human", nil
	}
	return "json", nil
}

func tryAgentContextLookups(cmd *cobra.Command, resolved scope.ResolvedScope, resolveErr error) (*agentContextLookups, string, bool, string) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", false, fmt.Sprintf("config load failed: %v", err)
	}
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

	h, openErr := bootstrap.Open(dbOverride(cmd))
	if openErr != nil {
		if strings.Contains(openErr.Error(), "requires migration") {
			return nil, cfg.DBPath, true, fmt.Sprintf("db requires migration: %v", openErr)
		}
		return nil, cfg.DBPath, false, fmt.Sprintf("db open failed: %v", openErr)
	}
	tr, trErr := NewInProcess(h)
	if trErr != nil {
		_ = h.Close()
		return nil, cfg.DBPath, false, fmt.Sprintf("db open failed: %v", trErr)
	}
	defer func() { _ = tr.Close() }()

	if resolveErr != nil {
		return nil, cfg.DBPath, true, ""
	}

	lookups := &agentContextLookups{}
	if resolved.AgentID != "" {
		fillAgentContextActorLookup(cmd, tr, resolved.AgentID, lookups)
	}
	if resolved.ProjectID != "" {
		fillAgentContextContainerLookup(cmd, tr, resolved.ProjectID, lookups)
	}
	return lookups, cfg.DBPath, true, ""
}

func fillAgentContextActorLookup(cmd *cobra.Command, tr Transport, slug string, lookups *agentContextLookups) {
	raw, err := tr.Call(cmd.Context(), "wrkq.admin.legacyActor.list", map[string]any{})
	if err != nil {
		return
	}
	var res struct {
		Items []struct {
			UUID string `json:"uuid"`
			ID   string `json:"id"`
			Slug string `json:"slug"`
		} `json:"items"`
	}
	if json.Unmarshal(raw, &res) != nil {
		return
	}
	for _, actor := range res.Items {
		if actor.Slug == slug {
			lookups.ActorUUID = actor.UUID
			lookups.ActorFriendlyID = actor.ID
			return
		}
	}
}

func fillAgentContextContainerLookup(cmd *cobra.Command, tr Transport, project string, lookups *agentContextLookups) {
	raw, err := tr.Call(cmd.Context(), "wrkq.container.show", map[string]any{"path": project})
	if err != nil {
		return
	}
	var container struct {
		UUID string `json:"uuid"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(raw, &container) != nil {
		return
	}
	lookups.ContainerUUID = container.UUID
	lookups.ContainerFriendlyID = container.ID
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

	if out.Lookups != nil && (out.Lookups.ActorUUID != "" || out.Lookups.ContainerUUID != "") {
		fmt.Fprintln(stdout)
		fmt.Fprintln(stdout, "Lookups (best-effort):")
		if out.Lookups.ActorUUID != "" {
			fmt.Fprintf(stdout, "  actor       = %s (%s)\n",
				out.Lookups.ActorFriendlyID, out.Lookups.ActorUUID)
		}
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
