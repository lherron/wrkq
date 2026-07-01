package wrkfcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/wrkqapi"
	"github.com/spf13/cobra"
)

type app struct {
	db          *db.DB
	service     *workflow.Service
	actor       string
	role        string
	json        bool
	hookCatalog *workflow.HookCatalog
	hookPath    string
}

var rootCmd = &cobra.Command{
	Use:           "wrkf",
	Short:         "Workflow engine CLI for wrkq tasks",
	SilenceUsage:  true,
	SilenceErrors: true,
}

var (
	flagDB          string
	flagActor       string
	flagRole        string
	flagTask        string
	flagJSON        bool
	flagVerbose     bool
	flagHookCatalog string
)

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&flagDB, "db", "", "Path to wrkq database file")
	rootCmd.PersistentFlags().StringVar(&flagActor, "principal-ref", "", "Workflow principal ref (agent:<id>)")
	rootCmd.PersistentFlags().StringVar(&flagRole, "role", "", "Workflow role")
	rootCmd.PersistentFlags().StringVar(&flagTask, "task", "", "Default task")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output JSON")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "Verbose output")
	rootCmd.PersistentFlags().StringVar(&flagHookCatalog, "hook-catalog", "", "Path to wrkf hook catalog JSON (overrides WRKF_HOOK_CATALOG and autodiscovery)")

	rootCmd.AddCommand(workflowCmd())
	rootCmd.AddCommand(taskCmd())
	rootCmd.AddCommand(runCmd())
	rootCmd.AddCommand(actionCmd())
	rootCmd.AddCommand(nextCmd())
	rootCmd.AddCommand(checkCmd())
	rootCmd.AddCommand(transitionCmd())
	rootCmd.AddCommand(evidenceCmd())
	rootCmd.AddCommand(obligationCmd())
	rootCmd.AddCommand(effectCmd())
	rootCmd.AddCommand(hookCmd())
	rootCmd.AddCommand(rpcCmd())
	rootCmd.AddCommand(supervisorCmd())
	rootCmd.AddCommand(versionCmd())
}

func withApp(needsDB bool, fn func(*app, *cobra.Command, []string) error) func(*cobra.Command, []string) error {
	return func(cmd *cobra.Command, args []string) error {
		hookPath, err := workflow.ResolveHookCatalogPath(flagHookCatalog)
		if err != nil {
			return fmt.Errorf("failed to resolve hook catalog: %w", err)
		}
		a := &app{actor: actorDefault(), role: roleDefault(), json: flagJSON, hookPath: hookPath}
		cat, err := workflow.LoadHookCatalog(hookPath)
		if err != nil {
			return fmt.Errorf("failed to load hook catalog: %w", err)
		}
		a.hookCatalog = cat
		if needsDB {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if flagDB != "" {
				if err := config.ApplyDBLocator(cfg, flagDB, true); err != nil {
					return err
				}
			}
			if cfg.RemoteEndpoint != "" {
				return fmt.Errorf("wrkf requires a local database path; WRKQ_DB_PATH and --db must not be rpc:// locators")
			}
			database, err := db.Open(cfg.DBPath)
			if err != nil {
				return err
			}
			defer func() { _ = database.Close() }()
			if err := database.RequiresMigrationError(); err != nil {
				return err
			}
			a.db = database
			a.service = workflow.NewService(database)
		}
		runErr := fn(a, cmd, args)
		if runErr != nil && flagJSON {
			renderJSONErrorEnvelope(cmd, runErr)
			return errReported
		}
		return runErr
	}
}

// errReported signals that a command error has already been rendered (as a
// --json envelope on stdout); main.go uses IsReported to avoid printing the
// text "Error: ..." line to stderr while still exiting non-zero.
var errReported = errors.New("wrkf: error reported")

// IsReported reports whether err was already rendered to the user.
func IsReported(err error) bool {
	return errors.Is(err, errReported)
}

// renderJSONErrorEnvelope emits the structured {"error":{...}} envelope to
// stdout (F1). It prefers the typed workflow ErrorDetail; otherwise it
// synthesizes a best-effort envelope from any coded error.
func renderJSONErrorEnvelope(cmd *cobra.Command, err error) {
	detail, ok := workflow.AsErrorDetail(err)
	if !ok {
		detail = workflow.ErrorDetail{Code: codeFromError(err), Message: err.Error()}
	}
	b, _ := json.MarshalIndent(map[string]any{"error": detail}, "", "  ")
	fmt.Fprintln(cmd.OutOrStdout(), string(b))
}

func codeFromError(err error) string {
	var coded interface{ Code() string }
	if errors.As(err, &coded) && coded.Code() != "" {
		return coded.Code()
	}
	return "WRKF_ERROR"
}

func actorDefault() string {
	if flagActor != "" {
		return flagActor
	}
	if v := os.Getenv("WRKF_PRINCIPAL_REF"); v != "" {
		return v
	}
	if v := os.Getenv("WRKF_ACTOR"); v != "" {
		return v
	}
	if v := os.Getenv("WRKQ_ACTOR"); v != "" {
		return v
	}
	return "system:wrkf"
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

func workflowCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workflow", Short: "Validate, install, and inspect workflow templates"}
	validate := &cobra.Command{
		Use:  "validate TEMPLATE",
		Args: cobra.ExactArgs(1),
		RunE: withApp(false, func(a *app, cmd *cobra.Command, args []string) error {
			svc := workflow.NewService(nil)
			result := svc.ValidateTemplateFile(args[0], a.hookCatalog)
			if flagJSON {
				_ = printJSON(cmd, result)
			} else if result.Valid {
				cmd.Printf("valid %s@%s %s\n", result.ID, result.Version, result.Hash)
			} else {
				cmd.Printf("invalid\n")
				for _, e := range result.Errors {
					cmd.Printf("- %s\n", e)
				}
			}
			if !result.Valid {
				return fmt.Errorf("template validation failed")
			}
			return nil
		}),
	}
	install := &cobra.Command{
		Use:  "install TEMPLATE",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			out, err := a.service.InstallTemplate(args[0], a.actor, a.hookCatalog)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	show := &cobra.Command{
		Use:  "show ID@VERSION",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			tpl, hash, err := a.service.ShowTemplate(args[0])
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, tpl)
			}
			cmd.Printf("%s@%s %s\n", tpl.ID, tpl.Version, hash)
			return nil
		}),
	}
	list := &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			templates, err := a.service.ListTemplates()
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"templates": templates})
		}),
	}
	diff := &cobra.Command{
		Use:  "diff OLD NEW",
		Args: cobra.ExactArgs(2),
		RunE: withApp(false, func(a *app, cmd *cobra.Command, args []string) error {
			out, err := workflow.NewService(nil).DiffTemplateFiles(args[0], args[1])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	cmd.AddCommand(validate, install, show, list, diff)
	return cmd
}

func taskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Attach and inspect workflow instances on tasks"}
	var workflowRef string
	attach := &cobra.Command{
		Use:  "attach TASK --workflow ID@VERSION",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if workflowRef == "" {
				return fmt.Errorf("--workflow is required")
			}
			inst, err := a.service.AttachTask(args[0], workflowRef, a.actor)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, inst)
		}),
	}
	attach.Flags().StringVar(&workflowRef, "workflow", "", "Template ref id@version")
	inspect := &cobra.Command{
		Use:  "inspect TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			inst, err := a.service.InspectTask(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, inst)
		}),
	}
	timeline := &cobra.Command{
		Use:  "timeline TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			events, err := a.service.Timeline(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"events": events})
		}),
	}
	refresh := &cobra.Command{
		Use:  "refresh TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			inst, err := a.service.Refresh(args[0], a.actor)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"instance": inst})
		}),
	}
	syncMeta := &cobra.Command{
		Use:  "sync-meta [TASK]",
		Args: cobra.MaximumNArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			task := ""
			if len(args) > 0 {
				task = args[0]
			}
			count, err := a.service.SyncMeta(task, a.actor)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"synced": count})
		}),
	}
	syncMeta.Flags().Bool("all", false, "Sync all workflow task projections")
	cmd.AddCommand(attach, inspect, timeline, refresh, syncMeta)
	return cmd
}

func nextCmd() *cobra.Command {
	return &cobra.Command{
		Use:  "next TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			resp, err := a.service.Next(args[0], a.role)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, resp)
		}),
	}
}

func evidenceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "evidence", Short: "Add and inspect workflow evidence"}
	var kind, ref, summary, facts, data, transition string
	add := &cobra.Command{
		Use:  "add TASK --kind KIND --ref REF",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if kind == "" || ref == "" {
				return fmt.Errorf("--kind and --ref are required")
			}
			ev, err := a.service.AddEvidence(workflow.AddEvidenceParams{
				TaskSelector: args[0],
				Kind:         kind,
				Ref:          ref,
				Summary:      summary,
				Facts:        facts,
				Data:         data,
				PrincipalRef: a.actor,
				Role:         a.role,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, ev)
		}),
	}
	add.Flags().StringVar(&kind, "kind", "", "Evidence kind")
	add.Flags().StringVar(&ref, "ref", "", "Evidence reference")
	add.Flags().StringVar(&summary, "summary", "", "Evidence summary")
	add.Flags().StringVar(&facts, "facts", "", "Evidence routing facts JSON object")
	add.Flags().StringVar(&data, "data", "", "Evidence JSON data")
	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			ev, err := a.service.ListEvidence(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"evidence": ev})
		}),
	}
	show := &cobra.Command{
		Use:  "show EVIDENCE",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			ev, err := a.service.ShowEvidence(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, ev)
		}),
	}
	suggest := &cobra.Command{
		Use:  "suggest TASK --transition TRANSITION",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if transition == "" {
				return fmt.Errorf("--transition is required")
			}
			out, err := a.service.SuggestEvidence(args[0], transition)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	suggest.Flags().StringVar(&transition, "transition", "", "Transition id")
	schema := &cobra.Command{
		Use:  "schema TASK --kind KIND",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if kind == "" {
				return fmt.Errorf("--kind is required")
			}
			out, err := a.service.EvidenceSchema(args[0], kind)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	schema.Flags().StringVar(&kind, "kind", "", "Evidence kind")
	execCmd := &cobra.Command{
		Use:  "exec TASK --kind KIND -- COMMAND...",
		Args: cobra.MinimumNArgs(2),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if kind == "" {
				return fmt.Errorf("--kind is required")
			}
			task := args[0]
			commandArgs := args[1:]
			var stdout, stderr bytes.Buffer
			c := exec.Command(commandArgs[0], commandArgs[1:]...)
			c.Stdout = &stdout
			c.Stderr = &stderr
			err := c.Run()
			exitCode := 0
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					exitCode = exitErr.ExitCode()
				} else {
					return err
				}
			}
			dataDoc := map[string]interface{}{
				"argv": commandArgs, "exitCode": exitCode,
				"stdout": stdout.String(), "stderr": stderr.String(),
			}
			dataJSON, _ := json.Marshal(dataDoc)
			ref := "command:" + strings.Join(commandArgs, " ")
			ev, addErr := a.service.AddEvidence(workflow.AddEvidenceParams{
				TaskSelector: task,
				Kind:         kind,
				Ref:          ref,
				Summary:      summary,
				Facts:        facts,
				Data:         string(dataJSON),
				PrincipalRef: a.actor,
				Role:         a.role,
			})
			if addErr != nil {
				return addErr
			}
			if exitCode != 0 {
				return fmt.Errorf("command exited %d after recording evidence %s", exitCode, ev.ID)
			}
			return printAny(cmd, flagJSON, ev)
		}),
	}
	execCmd.Flags().StringVar(&kind, "kind", "", "Evidence kind")
	execCmd.Flags().StringVar(&summary, "summary", "", "Evidence summary")
	execCmd.Flags().StringVar(&facts, "facts", "", "Evidence routing facts JSON object")
	cmd.AddCommand(add, list, show, suggest, schema, execCmd)
	return cmd
}

func obligationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "obligation", Short: "Inspect and resolve workflow obligations"}
	var all bool
	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			obl, err := a.service.ListObligations(args[0], all)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"obligations": obl})
		}),
	}
	list.Flags().BoolVar(&all, "all", false, "Include satisfied, waived, and cancelled obligations")
	show := &cobra.Command{
		Use:  "show OBLIGATION",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			obl, err := a.service.ShowObligation(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, obl)
		}),
	}
	var evidenceID, reason string
	statusCmd := func(use, status string) *cobra.Command {
		c := &cobra.Command{
			Use:  use + " TASK OBLIGATION",
			Args: cobra.ExactArgs(2),
			RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
				obl, err := a.service.SetObligationStatusWithAuthority(args[0], args[1], status, evidenceID, reason, workflow.ObligationStatusOptions{PrincipalRef: a.actor, Role: a.role})
				if err != nil {
					return err
				}
				return printAny(cmd, flagJSON, obl)
			}),
		}
		c.Flags().StringVar(&evidenceID, "evidence", "", "Evidence id")
		c.Flags().StringVar(&reason, "reason", "", "Reason")
		return c
	}
	cmd.AddCommand(list, show, statusCmd("satisfy", "satisfied"), statusCmd("waive", "waived"), statusCmd("cancel", "cancelled"))
	return cmd
}

func effectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "effect", Short: "Inspect and operate workflow effects"}
	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			effects, err := a.service.ListEffects(args[0], true)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"effects": effects})
		}),
	}
	show := &cobra.Command{
		Use:  "show EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := a.service.ShowEffect(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	var adapter, leaseToken, reason string
	var limit int
	var leaseMs int64
	var kind string
	var force bool
	claim := &cobra.Command{
		Use:  "claim [TASK]",
		Args: cobra.MaximumNArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			taskSelector := ""
			if len(args) > 0 {
				taskSelector = args[0]
			}
			claimAdapter := adapter
			if claimAdapter == "" {
				claimAdapter = a.actor
			}
			claim, err := a.service.ClaimEffects(claimAdapter, limit, leaseMs, taskSelector, kind)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, claim)
		}),
	}
	claim.Flags().StringVar(&adapter, "adapter", "", "Adapter id")
	claim.Flags().IntVar(&limit, "limit", 10, "Maximum effects to claim")
	claim.Flags().Int64Var(&leaseMs, "lease-ms", 60000, "Lease duration in milliseconds")
	claim.Flags().StringVar(&kind, "kind", "", "Effect kind")
	ack := &cobra.Command{
		Use:  "ack EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			var (
				eff *workflow.Effect
				err error
			)
			if force {
				eff, err = a.service.ForceAckEffect(args[0])
			} else {
				eff, err = a.service.AckEffect(args[0], leaseToken)
			}
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	ack.Flags().StringVar(&leaseToken, "lease-token", "", "Lease token")
	ack.Flags().BoolVar(&force, "force", false, "Bypass lease token check")
	deliver := &cobra.Command{
		Use:  "deliver EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			out, err := a.service.DeliverEffect(args[0], a.actor, a.hookCatalog, workflow.HookCatalogDir(a.hookPath))
			if err != nil {
				if out != nil {
					_ = printAny(cmd, flagJSON, out)
				}
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	fail := &cobra.Command{
		Use:  "fail EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			var (
				eff *workflow.Effect
				err error
			)
			if force {
				eff, err = a.service.ForceFailEffect(args[0], reason)
			} else {
				eff, err = a.service.FailEffect(args[0], leaseToken, reason, false)
			}
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	fail.Flags().StringVar(&reason, "reason", "", "Failure reason")
	fail.Flags().StringVar(&leaseToken, "lease-token", "", "Lease token")
	fail.Flags().BoolVar(&force, "force", false, "Bypass lease token check")
	retry := &cobra.Command{
		Use:  "retry EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := a.service.RetryEffect(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	cmd.AddCommand(list, show, claim, deliver, ack, fail, retry)
	return cmd
}

func checkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check TASK TRANSITION",
		Short: "Run workflow checks",
		Args:  cobra.ExactArgs(2),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			resp, err := a.service.Next(args[0], a.role)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, resp)
		}),
	}
	run := &cobra.Command{
		Use:  "run TASK TRANSITION",
		Args: cobra.ExactArgs(2),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			checks, err := a.service.RunChecks(args[0], args[1], a.actor, a.role, a.hookCatalog, workflow.HookCatalogDir(a.hookPath))
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"checks": checks})
		}),
	}
	show := &cobra.Command{
		Use:  "show CHECK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			cr, err := a.service.ShowCheckRun(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, cr)
		}),
	}
	var listTransition string
	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			checks, err := a.service.ListCheckRuns(args[0], listTransition)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"checks": checks})
		}),
	}
	list.Flags().StringVar(&listTransition, "transition", "", "Filter by transition id")
	preflight := &cobra.Command{
		Use:  "preflight TASK TRANSITION",
		Args: cobra.ExactArgs(2),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			resp, err := a.service.Next(args[0], a.role)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, resp)
		}),
	}
	cmd.AddCommand(preflight, run, show, list)
	return cmd
}

func transitionCmd() *cobra.Command {
	var expectRevision int64
	var idempotencyKey, contextHash string
	var runChecks, dryRun bool
	var checks []string
	cmd := &cobra.Command{
		Use:  "transition TASK TRANSITION",
		Args: cobra.ExactArgs(2),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			var exp *int64
			if cmd.Flags().Changed("expect-revision") {
				exp = &expectRevision
			}
			out, err := a.service.Transition(args[0], args[1], workflow.TransitionOptions{
				PrincipalRef: a.actor, Role: a.role, ExpectRevision: exp, IdempotencyKey: idempotencyKey,
				ContextHash: contextHash, CheckIDs: checks, RunChecks: runChecks, DryRun: dryRun,
				HookCatalog: a.hookCatalog, TemplateDir: workflow.HookCatalogDir(a.hookPath),
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	cmd.Flags().Int64Var(&expectRevision, "expect-revision", 0, "Expected workflow revision")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key")
	cmd.Flags().StringVar(&contextHash, "context", "", "Expected context hash")
	cmd.Flags().BoolVar(&runChecks, "run-checks", false, "Run transition checks before committing")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate without committing")
	cmd.Flags().StringArrayVar(&checks, "check", nil, "Check run id")
	return cmd
}

func runCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Bind principals to workflow runs"}
	var actor, role, delivery, lane, externalRunRef, idempotencyKey, summary string
	start := &cobra.Command{
		Use:  "start TASK --role ROLE --principal-ref PRINCIPAL_REF",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if actor == "" {
				actor = a.actor
			}
			if role == "" {
				role = a.role
			}
			if role == "" {
				return fmt.Errorf("--role is required")
			}
			run, err := a.service.StartRun(args[0], role, actor, workflow.StartRunOptions{
				IdempotencyKey: idempotencyKey,
				DeliveryRef:    delivery,
				Lane:           lane,
				ExternalRunRef: externalRunRef,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	start.Flags().StringVar(&role, "role", "", "Workflow role")
	start.Flags().StringVar(&actor, "principal-ref", "", "Principal ref (agent:<id>)")
	start.Flags().StringVar(&delivery, "delivery-ref", "", "Delivery ref")
	start.Flags().StringVar(&lane, "lane", "", "Lane")
	start.Flags().StringVar(&externalRunRef, "external-run-ref", "", "External run ref")
	start.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key")
	bind := &cobra.Command{
		Use:  "bind TASK ROLE HANDLE",
		Args: cobra.ExactArgs(3),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			task, role, handle := args[0], args[1], args[2]
			if !strings.Contains(handle, "@") || !strings.Contains(handle, ":") {
				return fmt.Errorf("handle must be project/task-scoped, e.g. observer@agent-spaces:%s~observer", task)
			}
			lane := ""
			if i := strings.LastIndex(handle, "~"); i >= 0 && i+1 < len(handle) {
				lane = handle[i+1:]
			}
			actor := strings.SplitN(handle, "@", 2)[0]
			run, err := a.service.StartRun(task, role, actor, workflow.StartRunOptions{DeliveryRef: handle, Lane: lane})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	finish := &cobra.Command{
		Use:  "finish RUN",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			run, err := a.service.FinishRun(args[0], "completed", summary)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	finish.Flags().StringVar(&summary, "summary", "", "Terminal summary")
	fail := &cobra.Command{
		Use:  "fail RUN",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			run, err := a.service.FailRun(args[0], summary)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	fail.Flags().String("kind", "", "Failure kind")
	fail.Flags().StringVar(&summary, "summary", "", "Terminal summary")
	show := &cobra.Command{
		Use:  "show RUN",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			run, err := a.service.ShowRun(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			runs, err := a.service.ListRuns(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"runs": runs})
		}),
	}
	cmd.AddCommand(start, bind, finish, fail, show, list)
	return cmd
}

func actionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "action", Short: "Low-ceremony task lifecycle actions over wrkf runs"}
	var (
		workflowRef, action, role, actor, lane, deliveryRef, externalRunRef, idempotencyKey string
		leaseOwner, leaseToken, expiredBefore, legacyActiveBefore                           string
		evidenceKind, evidenceRef, evidenceSummary, evidenceFacts, evidenceData             string
		runSummary, transition                                                              string
		noTransition, includeClosed                                                         bool
		status, actionFilter                                                                string
		limit                                                                               int
		leaseMs                                                                             int64
	)

	actionEvidence := func() *workflow.ActionEvidenceInput {
		if evidenceKind == "" && evidenceRef == "" && evidenceSummary == "" && evidenceFacts == "" && evidenceData == "" {
			return nil
		}
		return &workflow.ActionEvidenceInput{
			Kind: evidenceKind, Ref: evidenceRef, Summary: evidenceSummary,
			Facts: evidenceFacts, Data: evidenceData,
		}
	}

	start := &cobra.Command{
		Use:  "start TASK --action ACTION",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if action == "" {
				return fmt.Errorf("--action is required")
			}
			run, err := a.service.StartAction(workflow.StartActionParams{
				Task:           args[0],
				Workflow:       workflowRef,
				Action:         action,
				Role:           role,
				PrincipalRef:   firstNonEmpty(actor, a.actor),
				Lane:           lane,
				DeliveryRef:    deliveryRef,
				ExternalRunRef: externalRunRef,
				IdempotencyKey: idempotencyKey,
				LeaseOwner:     leaseOwner,
				LeaseMs:        leaseMs,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	start.Flags().StringVar(&action, "action", "", "Semantic action (triage|implement|review|verify|...)")
	start.Flags().StringVar(&workflowRef, "workflow", "", "Workflow ref to attach when none exists (defaults to built-in wrkq-simple-task@1)")
	start.Flags().StringVar(&role, "role", "", "Workflow role (defaults from action)")
	start.Flags().StringVar(&actor, "principal-ref", "", "Principal ref (agent:<id>)")
	start.Flags().StringVar(&lane, "lane", "", "Lane (defaults from action)")
	start.Flags().StringVar(&deliveryRef, "delivery-ref", "", "Delivery ref")
	start.Flags().StringVar(&externalRunRef, "external-run-ref", "", "External run ref (hrc:<runId>)")
	start.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key")
	start.Flags().StringVar(&leaseOwner, "lease-owner", "", "Lease owner")
	start.Flags().Int64Var(&leaseMs, "lease-ms", 0, "Lease duration in milliseconds")

	bind := &cobra.Command{
		Use:  "bind ACTION_RUN --external-run-ref hrc:RUNID",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if externalRunRef == "" {
				return fmt.Errorf("--external-run-ref is required")
			}
			run, err := a.service.BindActionExternal(workflow.BindActionExternalParams{
				ActionRunID:    args[0],
				ExternalRunRef: externalRunRef,
				DeliveryRef:    deliveryRef,
				Lane:           lane,
				IdempotencyKey: idempotencyKey,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	bind.Flags().StringVar(&externalRunRef, "external-run-ref", "", "External run ref (hrc:<runId>)")
	bind.Flags().StringVar(&deliveryRef, "delivery-ref", "", "Delivery ref")
	bind.Flags().StringVar(&lane, "lane", "", "Lane")
	bind.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key")

	complete := &cobra.Command{
		Use:  "complete ACTION_RUN",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			mode := workflow.TransitionDefault
			transitionID := ""
			switch {
			case noTransition:
				mode = workflow.TransitionSkip
			case transition != "":
				mode = workflow.TransitionExplicit
				transitionID = transition
			}
			out, err := a.service.CompleteAction(workflow.CompleteActionParams{
				ActionRunID:    args[0],
				LeaseToken:     leaseToken,
				Evidence:       actionEvidence(),
				TransitionMode: mode,
				TransitionID:   transitionID,
				RunSummary:     runSummary,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	complete.Flags().StringVar(&evidenceKind, "kind", "", "Evidence kind (defaults to <action>_result)")
	complete.Flags().StringVar(&evidenceRef, "ref", "", "Evidence ref")
	complete.Flags().StringVar(&evidenceSummary, "summary", "", "Evidence summary")
	complete.Flags().StringVar(&evidenceFacts, "facts", "", "Evidence facts JSON object")
	complete.Flags().StringVar(&evidenceData, "data", "", "Evidence data JSON")
	complete.Flags().StringVar(&runSummary, "run-summary", "", "Run terminal summary")
	complete.Flags().StringVar(&leaseToken, "lease-token", "", "Lease token for leased action runs")
	complete.Flags().StringVar(&transition, "transition", "", "Explicit transition id (default: auto-resolve)")
	complete.Flags().BoolVar(&noTransition, "no-transition", false, "Skip the transition; finish the run only")

	fail := &cobra.Command{
		Use:  "fail ACTION_RUN --run-summary SUMMARY",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if runSummary == "" {
				return fmt.Errorf("--run-summary is required")
			}
			run, err := a.service.FailAction(workflow.FailActionParams{
				ActionRunID: args[0],
				LeaseToken:  leaseToken,
				Summary:     runSummary,
				Evidence:    actionEvidence(),
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	fail.Flags().StringVar(&runSummary, "run-summary", "", "Failure summary")
	fail.Flags().StringVar(&leaseToken, "lease-token", "", "Lease token for leased action runs")
	fail.Flags().StringVar(&evidenceKind, "kind", "", "Failure evidence kind (defaults to failure_result)")
	fail.Flags().StringVar(&evidenceRef, "ref", "", "Failure evidence ref")
	fail.Flags().StringVar(&evidenceSummary, "summary", "", "Failure evidence summary")
	fail.Flags().StringVar(&evidenceFacts, "facts", "", "Failure evidence facts JSON object")
	fail.Flags().StringVar(&evidenceData, "data", "", "Failure evidence data JSON")

	show := &cobra.Command{
		Use:  "show ACTION_RUN",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			run, err := a.service.ShowAction(args[0])
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}

	heartbeat := &cobra.Command{
		Use:     "heartbeat ACTION_RUN --lease-token TOKEN",
		Aliases: []string{"renew-lease"},
		Args:    cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			run, err := a.service.HeartbeatAction(workflow.HeartbeatActionParams{
				ActionRunID: args[0],
				LeaseToken:  leaseToken,
				LeaseMs:     leaseMs,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	heartbeat.Flags().StringVar(&leaseToken, "lease-token", "", "Lease token")
	heartbeat.Flags().Int64Var(&leaseMs, "lease-ms", 0, "Lease duration in milliseconds")

	reap := &cobra.Command{
		Use:  "reap",
		Args: cobra.NoArgs,
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			out, err := a.service.ReapActions(workflow.ReapActionsParams{
				Task:               firstNonEmpty(cmd.Flag("task").Value.String()),
				Action:             actionFilter,
				ExpiredBefore:      expiredBefore,
				LegacyActiveBefore: legacyActiveBefore,
				Limit:              limit,
				PrincipalRef:       firstNonEmpty(actor, a.actor),
				Summary:            runSummary,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	reap.Flags().String("task", "", "Task selector")
	reap.Flags().StringVar(&actionFilter, "action", "", "Filter by action")
	reap.Flags().StringVar(&expiredBefore, "expired-before", "", "Reap leased active action runs expiring at or before this RFC3339 time (defaults to now)")
	reap.Flags().StringVar(&legacyActiveBefore, "legacy-active-before", "", "Explicit cutoff for legacy unleased active action runs")
	reap.Flags().StringVar(&actor, "principal-ref", "", "Principal ref (agent:<id>)")
	reap.Flags().StringVar(&runSummary, "summary", "", "Terminal summary")
	reap.Flags().IntVar(&limit, "limit", 0, "Maximum runs to reap")

	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			runs, err := a.service.ListActions(workflow.ListActionsParams{
				Task:                   args[0],
				IncludeClosedInstances: includeClosed,
				Status:                 status,
				Action:                 actionFilter,
				Limit:                  limit,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"items": runs})
		}),
	}
	list.Flags().BoolVar(&includeClosed, "include-closed-instances", false, "Include runs from closed workflow instances")
	list.Flags().StringVar(&status, "status", "", "Filter by run status")
	list.Flags().StringVar(&actionFilter, "action", "", "Filter by action")
	list.Flags().IntVar(&limit, "limit", 0, "Maximum runs to return")

	cmd.AddCommand(start, bind, complete, fail, heartbeat, reap, show, list)
	return cmd
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func hookCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "hook", Short: "Inspect and debug local hook catalog"}
	list := &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: withApp(false, func(a *app, cmd *cobra.Command, args []string) error {
			var hooks []map[string]interface{}
			if a.hookCatalog != nil {
				for id, h := range a.hookCatalog.Hooks {
					hooks = append(hooks, map[string]interface{}{"id": id, "kind": h.Kind, "argv": h.Argv})
				}
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"hooks": hooks})
		}),
	}
	show := &cobra.Command{
		Use:  "show HOOK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(false, func(a *app, cmd *cobra.Command, args []string) error {
			if a.hookCatalog == nil {
				return fmt.Errorf("hook catalog not configured")
			}
			h, ok := a.hookCatalog.Hooks[args[0]]
			if !ok {
				return fmt.Errorf("hook not found: %s", args[0])
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"id": args[0], "hook": h})
		}),
	}
	var hookID string
	run := &cobra.Command{
		Use:  "run TASK TRANSITION --hook HOOK",
		Args: cobra.ExactArgs(2),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if hookID == "" {
				return fmt.Errorf("--hook is required")
			}
			out, err := a.service.RunSingleHook(args[0], args[1], hookID, a.actor, a.role, a.hookCatalog, workflow.HookCatalogDir(a.hookPath))
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	run.Flags().StringVar(&hookID, "hook", "", "Hook id")
	cmd.AddCommand(list, show, run)
	return cmd
}

func rpcCmd() *cobra.Command {
	var stdio bool
	cmd := &cobra.Command{
		Use:   "rpc --stdio",
		Short: "Serve wrkf JSON-RPC over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !stdio {
				return fmt.Errorf("--stdio is required")
			}
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if flagDB != "" {
				if err := config.ApplyDBLocator(cfg, flagDB, false); err != nil {
					return err
				}
			}
			if cfg.RemoteEndpoint != "" {
				return workrpc.ServeRemoteStdio(cmd.Context(), os.Stdin, os.Stdout, cfg.RemoteEndpoint, wrkqdTokenFromEnv())
			}
			return withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
				if !stdio {
					return fmt.Errorf("--stdio is required")
				}
				api := wrkfapi.New(
					a.service,
					wrkfapi.WithHookCatalog(a.hookCatalog),
					wrkfapi.WithTemplateDir(workflow.HookCatalogDir(a.hookPath)),
				)
				// Load the real wrkq config so the wrkf rpc entrypoint serves
				// attachments with the SAME attach dir / size limit as the wrkq
				// entrypoint (T-04448 entrypoint equivalence).
				attachDir := ""
				attachMaxMB := 0
				// search host config so the wrkf rpc entrypoint serves the same
				// server-owned search/index methods as the wrkq entrypoint (T-05114
				// entrypoint equivalence — the method is registered on BOTH entrypoints).
				var searchCfg wrkqapi.SearchConfig
				if cfg, cerr := config.Load(); cerr == nil {
					attachDir = rpcAttachDir(cfg.AttachDir)
					attachMaxMB = cfg.AttachmentsMaxMB
					searchCfg = wrkqapi.SearchConfig{
						Enabled:          cfg.Search.Enabled,
						CanonicalDBPath:  a.db.Path(),
						DBPath:           cfg.Search.DBPath,
						DenseProvider:    cfg.Search.DenseProvider,
						DenseBaseURL:     cfg.Search.DenseBaseURL,
						DenseModel:       cfg.Search.DenseModel,
						DenseDimension:   cfg.Search.DenseDimension,
						QueryInstruction: cfg.Search.QueryInstruction,
						IndexBatchSize:   cfg.Search.IndexBatchSize,
						CandidateLimit:   cfg.Search.CandidateLimit,
					}
				}
				srv := workrpc.NewServer(os.Stdout)
				workrpc.RegisterAPI(srv, api, workrpc.RegistryOptions{
					Database:         a.db,
					DatabasePath:     a.db.Path(),
					ServerVersion:    Version,
					Entrypoint:       "wrkf",
					DefaultActor:     a.actor,
					DefaultRole:      a.role,
					AttachDir:        attachDir,
					AttachmentsMaxMB: attachMaxMB,
					Search:           searchCfg,
				})
				return srv.Serve(context.Background(), os.Stdin)
			})(cmd, args)
		},
	}
	cmd.Flags().BoolVar(&stdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	return cmd
}

func wrkqdTokenFromEnv() string {
	if token := strings.TrimSpace(os.Getenv("WRKQD_TOKEN")); token != "" {
		return token
	}
	if path := strings.TrimSpace(os.Getenv("WRKQD_TOKEN_FILE")); path != "" {
		if b, err := os.ReadFile(path); err == nil {
			return strings.TrimSpace(string(b))
		}
	}
	return ""
}

// rpcAttachDir resolves the attachment storage directory for the RPC server,
// returning only an explicitly-configured directory (WRKQ_ATTACH_DIR, or a
// config.yaml attach_dir that differs from the cwd/home auto-default). An empty
// result makes attachment.add report WRKQ_VALIDATION rather than silently
// writing to the per-user default. Mirrors internal/cli.rpcAttachDir so both
// entrypoints behave identically.
func rpcAttachDir(configured string) string {
	if env := os.Getenv("WRKQ_ATTACH_DIR"); env != "" {
		return env
	}
	if configured != "" && configured != defaultAttachDir() {
		return configured
	}
	return ""
}

// defaultAttachDir mirrors config.Load's home auto-default.
func defaultAttachDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "wrkq", "attachments")
}

func supervisorCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "supervisor", Short: "Operate recovery and escalation role"}
	var actor, reason string
	start := &cobra.Command{
		Use:  "start TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if actor == "" {
				actor = a.actor
			}
			run, err := a.service.StartRun(args[0], "supervisor", actor, workflow.StartRunOptions{})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	start.Flags().StringVar(&actor, "principal-ref", "", "Principal ref (agent:<id>)")
	call := &cobra.Command{
		Use:  "call TASK",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := a.service.SupervisorCall(args[0], reason)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	call.Flags().StringVar(&reason, "reason", "", "Reason")
	action := &cobra.Command{
		Use:  "action TASK ACTION",
		Args: cobra.MinimumNArgs(2),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			switch args[1] {
			case "escalate":
				eff, err := a.service.SupervisorEscalate(args[0], reason)
				if err != nil {
					return err
				}
				return printAny(cmd, flagJSON, eff)
			case "retry":
				return printAny(cmd, flagJSON, map[string]string{"action": "retry", "status": "recorded"})
			case "transition":
				if len(args) < 3 {
					return fmt.Errorf("transition action requires target transition id")
				}
				out, err := a.service.Transition(args[0], args[2], workflow.TransitionOptions{PrincipalRef: a.actor, Role: "supervisor"})
				if err != nil {
					return err
				}
				return printAny(cmd, flagJSON, out)
			case "create-obligation":
				if len(args) < 3 {
					return fmt.Errorf("create-obligation action requires obligation kind")
				}
				obl, err := a.service.CreateObligation(args[0], args[2], "supervisor", "", true, reason)
				if err != nil {
					return err
				}
				return printAny(cmd, flagJSON, obl)
			default:
				return fmt.Errorf("unknown supervisor action: %s", args[1])
			}
		}),
	}
	action.Flags().StringVar(&reason, "reason", "", "Reason")
	action.Flags().String("role", "", "Role")
	action.Flags().String("from-check", "", "Check run")
	action.Flags().String("target", "", "Target")
	cmd.AddCommand(start, call, action)
	return cmd
}
