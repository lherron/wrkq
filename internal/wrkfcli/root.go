package wrkfcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/wrkfapi"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
	"github.com/spf13/cobra"
)

type app struct {
	principalRef string
	role         string
	json         bool
	transport    workrpcclient.Transport
	remote       bool
}

var rootCmd = &cobra.Command{
	Use:           "wrkf",
	Short:         "Workflow engine CLI for wrkq tasks",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var (
	flagDB           string
	flagPrincipalRef string
	flagRole         string
	flagTask         string
	flagJSON         bool
	flagVerbose      bool
	flagHookCatalog  string
)

func Execute() error {
	return clifunnel.Execute(context.Background(), rootCmd, os.Args[1:],
		clifunnel.Options{NotFoundHint: wrkfNotFoundHint})
}

// wrkfNotFoundHint names the valid forms and the listing command for a missing
// resource in wrkf's vocabulary (CLI standard §4).
func wrkfNotFoundHint(kind, ref string) string {
	switch strings.ToLower(kind) {
	case "task":
		return "a task is T-<n>, its uuid, or a path like inbox/<slug>. Find it with: wrkq find --state all --type t"
	case "workflow instance":
		if ref != "" {
			return "list a task's workflow instances with: wrkf task instances <task>"
		}
		return "the task has no workflow attached. Attach one with: wrkf task attach <task> --workflow <id>@<version>  (templates: wrkf workflow list)"
	case "template":
		return "templates are addressed as <id>@<version>. List installed templates with: wrkf workflow list"
	case "run":
		return "list a task's runs with: wrkf run list <task>"
	case "effect":
		return "list a task's effects with: wrkf effect list <task>"
	case "obligation":
		return "list a task's obligations with: wrkf obligation list <task>"
	case "hook":
		return "list the hook catalog with: wrkf hook list"
	case "evidence":
		return "list a task's evidence with: wrkf evidence list <task>"
	case "check", "check run":
		return "list a task's checks with: wrkf check list <task>"
	}
	return ""
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagDB, "db", "", "Database path or rpc:// locator (overrides WRKQ_DB and WRKQ_DB_PATH)")
	rootCmd.PersistentFlags().StringVar(&flagPrincipalRef, "principal-ref", "", "Workflow caller principal ref (agent:<id>)")
	rootCmd.PersistentFlags().StringVar(&flagRole, "role", "", "Workflow role")
	rootCmd.PersistentFlags().StringVar(&flagTask, "task", "", "Default task")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output JSON")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "Verbose output")
	rootCmd.PersistentFlags().StringVar(&flagHookCatalog, "hook-catalog", "", "Path to wrkf hook catalog JSON (defaults to WRKF_HOOK_CATALOG; required in local mode)")

	rootCmd.AddCommand(workflowCmd())
	rootCmd.AddCommand(taskCmd())
	rootCmd.AddCommand(runCmd())
	rootCmd.AddCommand(watchCmd())
	rootCmd.AddCommand(actionCmd())
	rootCmd.AddCommand(nextCmd())
	rootCmd.AddCommand(instanceCmd())
	rootCmd.AddCommand(checkCmd())
	rootCmd.AddCommand(transitionCmd())
	rootCmd.AddCommand(suspensionCmd())
	rootCmd.AddCommand(evidenceCmd())
	rootCmd.AddCommand(ledgerCmd())
	rootCmd.AddCommand(obligationCmd())
	rootCmd.AddCommand(effectCmd())
	rootCmd.AddCommand(hookCmd())
	rootCmd.AddCommand(rpcCmd())
	rootCmd.AddCommand(supervisorCmd())
	rootCmd.AddCommand(versionCmd())
}

// errReported signals that a command error has already been rendered (as a
// --json envelope on stderr); main.go uses IsReported to avoid printing the
// error a second time while still exiting non-zero.
var errReported = errors.New("wrkf: error reported")

// IsReported reports whether err was already rendered to the user.
func IsReported(err error) bool {
	return errors.Is(err, errReported) || errorAlreadyReported(err)
}

// renderJSONErrorEnvelope emits the structured {"error":{...}} envelope to
// stderr (F1; CLI standard §4, T-10234: errors never share stdout with data).
// It prefers the typed workflow ErrorDetail; any other error goes through the
// shared funnel's structure, so its code is a field and never message text.
func renderJSONErrorEnvelope(cmd *cobra.Command, err error) {
	if detail, ok := wrkfapi.AsErrorDetail(err); ok {
		b, _ := json.MarshalIndent(map[string]any{"error": detail}, "", "  ")
		fmt.Fprintln(cmd.ErrOrStderr(), string(b))
		return
	}
	clifunnel.Report(cmd.ErrOrStderr(), err, clifunnel.ErrorJSON, "WRKF")
}

const wrkfPrincipalEnv = "WRKF_PRINCIPAL_REF"

func wrkfPrincipalDefault(cmd *cobra.Command, cfg *config.Config) (string, error) {
	attr, err := attribution.ResolveWithPrincipalEnvs(attribution.ResolveOptions{
		Command:       cmd,
		Config:        cfg,
		ResolvedScope: resolvedRuntimeScope(),
	}, wrkfPrincipalEnv)
	if err != nil {
		const fix = "set --principal-ref agent:<id> or WRKF_PRINCIPAL_REF=agent:<id>"
		if attribution.IsNoPrincipalConfigured(err) {
			return "", fmt.Errorf("workflow caller principal is required; %s", fix)
		}
		return "", fmt.Errorf("invalid workflow caller principal; %s: %w", fix, err)
	}
	return attr.PrincipalRef, nil
}

func resolvedRuntimeScope() *scope.ResolvedScope {
	resolved, _, err := scope.Resolve("")
	if err != nil {
		return nil
	}
	return &resolved
}

func roleDefault() string {
	if flagRole != "" {
		return flagRole
	}
	if v := os.Getenv("WRKF_ROLE"); v != "" {
		return v
	}
	return ""
}

func printJSON(cmd *cobra.Command, v interface{}) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), string(b))
	return nil
}

func printAny(cmd *cobra.Command, forceJSON bool, v interface{}) error {
	if flagJSON || forceJSON {
		return printJSON(cmd, v)
	}
	switch t := v.(type) {
	case string:
		fmt.Fprintln(cmd.OutOrStdout(), t)
	default:
		b, _ := json.Marshal(v)
		fmt.Fprintln(cmd.OutOrStdout(), string(b))
	}
	return nil
}

// printCall makes one RPC call and prints its typed result.
func printCall[T any](cmd *cobra.Command, a *app, method string, params any) error {
	result, err := rpcCall[T](cmd, a, method, params)
	if err != nil {
		return err
	}
	return printAny(cmd, flagJSON, result)
}
