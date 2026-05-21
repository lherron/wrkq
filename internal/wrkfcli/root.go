package wrkfcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
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
	rootCmd.PersistentFlags().StringVar(&flagActor, "actor", "", "Workflow actor id")
	rootCmd.PersistentFlags().StringVar(&flagRole, "role", "", "Workflow role")
	rootCmd.PersistentFlags().StringVar(&flagTask, "task", "", "Default task")
	rootCmd.PersistentFlags().BoolVar(&flagJSON, "json", false, "Output JSON")
	rootCmd.PersistentFlags().BoolVar(&flagVerbose, "verbose", false, "Verbose output")
	rootCmd.PersistentFlags().StringVar(&flagHookCatalog, "hook-catalog", "", "Path to wrkf hook catalog JSON (overrides WRKF_HOOK_CATALOG and autodiscovery)")

	rootCmd.AddCommand(workflowCmd())
	rootCmd.AddCommand(taskCmd())
	rootCmd.AddCommand(runCmd())
	rootCmd.AddCommand(nextCmd())
	rootCmd.AddCommand(checkCmd())
	rootCmd.AddCommand(transitionCmd())
	rootCmd.AddCommand(evidenceCmd())
	rootCmd.AddCommand(obligationCmd())
	rootCmd.AddCommand(effectCmd())
	rootCmd.AddCommand(hookCmd())
	rootCmd.AddCommand(supervisorCmd())
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
				cfg.DBPath = flagDB
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
		return fn(a, cmd, args)
	}
}

func actorDefault() string {
	if flagActor != "" {
		return flagActor
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
	var kind, ref, summary, data, transition string
	add := &cobra.Command{
		Use:  "add TASK --kind KIND --ref REF",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			if kind == "" || ref == "" {
				return fmt.Errorf("--kind and --ref are required")
			}
			ev, err := a.service.AddEvidence(args[0], kind, ref, summary, data, a.actor, a.role)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, ev)
		}),
	}
	add.Flags().StringVar(&kind, "kind", "", "Evidence kind")
	add.Flags().StringVar(&ref, "ref", "", "Evidence reference")
	add.Flags().StringVar(&summary, "summary", "", "Evidence summary")
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
			ev, addErr := a.service.AddEvidence(task, kind, ref, summary, string(dataJSON), a.actor, a.role)
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
	cmd.AddCommand(add, list, show, suggest, execCmd)
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
				obl, err := a.service.SetObligationStatus(args[0], args[1], status, evidenceID, reason)
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
	var adapter, reason string
	ack := &cobra.Command{
		Use:  "ack EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withApp(true, func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := a.service.AckEffect(args[0], adapter)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	ack.Flags().StringVar(&adapter, "adapter", "", "Adapter id")
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
			eff, err := a.service.FailEffect(args[0], reason)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	fail.Flags().StringVar(&reason, "reason", "", "Failure reason")
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
	cmd.AddCommand(list, show, deliver, ack, fail, retry)
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
				Actor: a.actor, Role: a.role, ExpectRevision: exp, IdempotencyKey: idempotencyKey,
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
	cmd := &cobra.Command{Use: "run", Short: "Bind actors to workflow runs"}
	var actor, role, delivery, lane, summary string
	start := &cobra.Command{
		Use:  "start TASK --role ROLE --actor ACTOR",
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
			run, err := a.service.StartRun(args[0], role, actor, delivery, lane)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	start.Flags().StringVar(&role, "role", "", "Workflow role")
	start.Flags().StringVar(&actor, "actor", "", "Actor id")
	start.Flags().StringVar(&delivery, "delivery-ref", "", "Delivery ref")
	start.Flags().StringVar(&lane, "lane", "", "Lane")
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
			run, err := a.service.StartRun(task, role, actor, handle, lane)
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
			run, err := a.service.FinishRun(args[0], "failed", summary)
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
			run, err := a.service.StartRun(args[0], "supervisor", actor, "", "")
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	start.Flags().StringVar(&actor, "actor", "", "Actor id")
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
				out, err := a.service.Transition(args[0], args[2], workflow.TransitionOptions{Actor: a.actor, Role: "supervisor"})
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
