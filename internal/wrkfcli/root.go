package wrkfcli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/scope"
	workrpcclient "github.com/lherron/wrkq/internal/workrpc/client"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/wrkqapi"
	"github.com/spf13/cobra"
)

type app struct {
	actor     string
	wrkqActor string
	role      string
	json      bool
	transport workrpcclient.Transport
	remote    bool
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
	rootCmd.AddCommand(watchCmd())
	rootCmd.AddCommand(actionCmd())
	rootCmd.AddCommand(nextCmd())
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
// --json envelope on stdout); main.go uses IsReported to avoid printing the
// text "Error: ..." line to stderr while still exiting non-zero.
var errReported = errors.New("wrkf: error reported")

// IsReported reports whether err was already rendered to the user.
func IsReported(err error) bool {
	return errors.Is(err, errReported) || errorAlreadyReported(err)
}

// renderJSONErrorEnvelope emits the structured {"error":{...}} envelope to
// stdout (F1). It prefers the typed workflow ErrorDetail; otherwise it
// synthesizes a best-effort envelope from any coded error.
func renderJSONErrorEnvelope(cmd *cobra.Command, err error) {
	detail, ok := wrkfapi.AsErrorDetail(err)
	if !ok {
		detail = wrkfapi.ErrorDetail{Code: codeFromError(err), Message: err.Error()}
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

const wrkfPrincipalEnv = "WRKF_PRINCIPAL_REF"

func actorDefaults(cmd *cobra.Command) (workflowActor, wrkqDefaultActor string, err error) {
	principal, err := wrkfPrincipalDefault(cmd)
	if err != nil {
		return "", "", err
	}
	if principal != "" {
		return principal, principal, nil
	}
	if v := os.Getenv("WRKF_ACTOR"); v != "" {
		return v, "", nil
	}
	if v := os.Getenv("WRKQ_ACTOR"); v != "" {
		return v, "", nil
	}
	return "system:wrkf", "", nil
}

func wrkfPrincipalDefault(cmd *cobra.Command) (string, error) {
	attr, err := attribution.ResolveWithPrincipalEnvs(attribution.ResolveOptions{
		Command:       cmd,
		ResolvedScope: resolvedRuntimeScope(),
	}, wrkfPrincipalEnv)
	if attribution.IsNoPrincipalConfigured(err) {
		return "", nil
	}
	if err != nil {
		return "", err
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

func workflowCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workflow", Short: "Validate, install, and inspect workflow templates"}
	validate := &cobra.Command{
		Use:  "validate TEMPLATE",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			body, err := readTemplateBody(args[0])
			if err != nil {
				return err
			}
			result, err := rpcCall[wrkfapi.ValidateResult](cmd, a, "wrkf.workflow.validate", wrkfapi.WorkflowContentParams{Body: body, SourceName: args[0]})
			if err != nil {
				return err
			}
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			body, err := readTemplateBody(args[0])
			if err != nil {
				return err
			}
			out, err := rpcCall[wrkfapi.InstallResult](cmd, a, "wrkf.workflow.install", wrkfapi.WorkflowInstallParams{
				Body: body, SourceName: args[0], PrincipalRef: a.actor,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]any{"id": out.ID, "version": out.Version, "hash": out.Hash, "installed": out.Installed})
		}),
	}
	show := &cobra.Command{
		Use:  "show ID@VERSION",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			info, err := rpcCall[wrkfapi.WorkflowShowResult](cmd, a, "wrkf.workflow.show", map[string]any{"ref": args[0]})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, info)
			}
			marker := ""
			if info.DiscontinuedAt != "" {
				marker = fmt.Sprintf(" DISCONTINUED at %s", info.DiscontinuedAt)
				if info.DiscontinuedBy != "" {
					marker += " by " + info.DiscontinuedBy
				}
			}
			cmd.Printf("%s@%s %s%s\n", info.Template.ID, info.Template.Version, info.Hash, marker)
			return nil
		}),
	}
	list := &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkfapi.WorkflowListResult](cmd, a, "wrkf.workflow.list", map[string]any{})
			if err != nil {
				return err
			}
			var templates []map[string]interface{}
			for _, template := range result.Templates {
				row := map[string]interface{}{
					"id": template.ID, "version": template.Version, "hash": template.Hash, "installedAt": template.InstalledAt,
				}
				if template.InstalledBy != "" {
					row["installedBy"] = template.InstalledBy
				}
				if template.DiscontinuedAt != "" {
					row["discontinuedAt"] = template.DiscontinuedAt
				}
				if template.DiscontinuedBy != "" {
					row["discontinuedBy"] = template.DiscontinuedBy
				}
				templates = append(templates, row)
			}
			if flagJSON {
				return printJSON(cmd, map[string]interface{}{"templates": templates})
			}
			for _, template := range templates {
				marker := ""
				if _, discontinued := template["discontinuedAt"]; discontinued {
					marker = " DISCONTINUED"
				}
				cmd.Printf("%s@%s %s%s\n", template["id"], template["version"], template["hash"], marker)
			}
			return nil
		}),
	}
	discontinue := templateLifecycleCommand("discontinue")
	reinstate := templateLifecycleCommand("reinstate")
	diff := &cobra.Command{
		Use:  "diff OLD NEW",
		Args: cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			oldBody, err := readTemplateBody(args[0])
			if err != nil {
				return err
			}
			newBody, err := readTemplateBody(args[1])
			if err != nil {
				return err
			}
			if len(oldBody)+len(newBody) > wrkfapi.MaxTemplateDiffBodyBytes {
				return fmt.Errorf("template diff bodies exceed %d-byte aggregate limit", wrkfapi.MaxTemplateDiffBodyBytes)
			}
			out, err := rpcCall[wrkfapi.DiffResult](cmd, a, "wrkf.workflow.diff", wrkfapi.WorkflowDiffParams{
				OldBody: oldBody, NewBody: newBody, OldSourceName: args[0], NewSourceName: args[1],
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	cmd.AddCommand(validate, install, show, list, diff, discontinue, reinstate)
	return cmd
}

func readTemplateBody(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > wrkfapi.MaxTemplateBodyBytes {
		return "", fmt.Errorf("%s exceeds %d-byte template body limit", path, wrkfapi.MaxTemplateBodyBytes)
	}
	return string(data), nil
}

func templateLifecycleCommand(verb string) *cobra.Command {
	return &cobra.Command{
		Use:  verb + " ID@VERSION",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			info, err := rpcCall[wrkfapi.WorkflowShowResult](cmd, a, "wrkf.workflow."+verb, map[string]any{
				"ref": args[0], "principal_ref": a.actor,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, info)
			}
			cmd.Printf("%s %s\n", verb, args[0])
			return nil
		}),
	}
}

func taskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Attach and inspect workflow instances on tasks"}
	var workflowRef string
	var supersede bool
	var predecessorInstance string
	var predecessorRevision int64
	var attachDiscontinued bool
	attach := &cobra.Command{
		Use:  "attach TASK --workflow ID@VERSION",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if workflowRef == "" {
				return fmt.Errorf("--workflow is required")
			}
			var revision *int64
			if cmd.Flags().Changed("predecessor-revision") {
				revision = &predecessorRevision
			}
			result, err := rpcCall[wrkqapi.WrkqWorkflowAttachResult](cmd, a, "wrkq.workflow.attach", wrkqapi.WorkflowAttachParams{
				Task:                  args[0],
				Workflow:              workflowRef,
				Supersede:             supersede,
				PredecessorInstanceID: predecessorInstance,
				PredecessorRevision:   revision,
				AttachDiscontinued:    attachDiscontinued,
				Actor:                 a.actor,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result.Instance)
		}),
	}
	attach.Flags().StringVar(&workflowRef, "workflow", "", "Template ref id@version")
	attach.Flags().BoolVar(&supersede, "supersede", false, "Supersede the current live workflow instance")
	attach.Flags().StringVar(&predecessorInstance, "predecessor-instance", "", "Expected current workflow instance id for --supersede")
	attach.Flags().Int64Var(&predecessorRevision, "predecessor-revision", 0, "Expected current workflow revision for --supersede")
	attach.Flags().BoolVar(&attachDiscontinued, "attach-discontinued", false, "Deliberately attach a discontinued template version")
	inspect := &cobra.Command{
		Use:  "inspect TASK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkqapi.WrkqWorkflowInspectResult](cmd, a, "wrkq.workflow.inspect", map[string]any{"task": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result.Instance)
		}),
	}
	timeline := &cobra.Command{
		Use:  "timeline TASK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkqapi.WrkqWorkflowTimelineResult](cmd, a, "wrkq.workflow.timeline", map[string]any{"task": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"events": result.Events})
		}),
	}
	refresh := &cobra.Command{
		Use:  "refresh TASK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkqapi.WrkqWorkflowInspectResult](cmd, a, "wrkq.workflow.refresh", map[string]any{"task": args[0], "actor": a.actor})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"instance": result.Instance})
		}),
	}
	syncMeta := &cobra.Command{
		Use:  "sync-meta [TASK]",
		Args: cobra.MaximumNArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			task := ""
			if len(args) > 0 {
				task = args[0]
			}
			result, err := rpcCall[wrkqapi.WrkqWorkflowSyncMetaResult](cmd, a, "wrkq.workflow.syncMeta", wrkqapi.WorkflowSyncMetaParams{Task: task, Actor: a.actor})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result)
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			resp, err := rpcCall[wrkfapi.NextActionResponse](cmd, a, "wrkf.instance.next", map[string]any{"task": args[0], "role": a.role})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if kind == "" || ref == "" {
				return fmt.Errorf("--kind and --ref are required")
			}
			ev, err := rpcCall[wrkfapi.Evidence](cmd, a, "wrkf.evidence.add", wrkfapi.EvidenceAddParams{
				TaskSelector: args[0],
				Kind:         kind,
				Ref:          ref,
				Summary:      summary,
				Facts:        rawJSON(facts),
				Data:         rawJSON(data),
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
	add.Flags().StringVar(&summary, "summary", "", "Evidence summary (- reads stdin)")
	add.Flags().StringVar(&facts, "facts", "", "Evidence routing facts JSON object (- reads stdin)")
	add.Flags().StringVar(&data, "data", "", "Evidence JSON data (- reads stdin)")
	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			ev, err := rpcCall[[]wrkfapi.Evidence](cmd, a, "wrkf.evidence.list", map[string]any{"task": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"evidence": ev})
		}),
	}
	show := &cobra.Command{
		Use:  "show EVIDENCE",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			ev, err := rpcCall[wrkfapi.Evidence](cmd, a, "wrkf.evidence.show", map[string]any{"id": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, ev)
		}),
	}
	suggest := &cobra.Command{
		Use:  "suggest TASK --transition TRANSITION",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if transition == "" {
				return fmt.Errorf("--transition is required")
			}
			out, err := rpcCall[wrkfapi.SuggestResult](cmd, a, "wrkf.evidence.suggest", map[string]any{"task": args[0], "transition": transition})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{
				"transition": out.Transition, "required": out.Required, "missing": out.Missing, "checks": out.Checks, "warnings": out.Warnings,
			})
		}),
	}
	suggest.Flags().StringVar(&transition, "transition", "", "Transition id")
	schema := &cobra.Command{
		Use:  "schema TASK --kind KIND",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if kind == "" {
				return fmt.Errorf("--kind is required")
			}
			out, err := rpcCall[wrkfapi.EvidenceSchema](cmd, a, "wrkf.evidence.schema", wrkfapi.EvidenceSchemaParams{TaskSelector: args[0], Kind: kind})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
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
			ev, addErr := rpcCall[wrkfapi.Evidence](cmd, a, "wrkf.evidence.add", wrkfapi.EvidenceAddParams{
				TaskSelector: task,
				Kind:         kind,
				Ref:          ref,
				Summary:      summary,
				Facts:        rawJSON(facts),
				Data:         dataJSON,
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
	execCmd.Flags().StringVar(&summary, "summary", "", "Evidence summary (- reads stdin)")
	execCmd.Flags().StringVar(&facts, "facts", "", "Evidence routing facts JSON object (- reads stdin)")
	cmd.AddCommand(add, list, show, suggest, schema, execCmd)
	return cmd
}

func ledgerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ledger", Short: "Append and project immutable workflow ledger entries"}
	var appendTask, appendKind, appendAbout, bodySource string
	appendCmd := &cobra.Command{
		Use:  "append --task TASK --kind KIND --about PRINCIPAL --body FILE|-",
		Args: cobra.NoArgs,
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if appendTask == "" || appendKind == "" || appendAbout == "" || bodySource == "" {
				return fmt.Errorf("--task, --kind, --about, and --body are required")
			}
			body, err := readLedgerBody(cmd, bodySource)
			if err != nil {
				return err
			}
			entry, err := rpcCall[wrkfapi.LedgerEntry](cmd, a, "wrkf.ledger.append", wrkfapi.LedgerAppendParams{
				TaskID: appendTask, Kind: appendKind, AboutPrincipalRef: appendAbout, Body: body,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, entry)
			}
			return printLedger(cmd, []wrkfapi.LedgerEntry{entry}, "", false)
		}),
	}
	appendCmd.Flags().StringVar(&appendTask, "task", "", "Task selector")
	appendCmd.Flags().StringVar(&appendKind, "kind", "", "Ledger kind")
	appendCmd.Flags().StringVar(&appendAbout, "about", "", "Principal the entry is about")
	appendCmd.Flags().StringVar(&bodySource, "body", "", "JSON body file, - for stdin, or a JSON object")

	var listTask, listPrincipal, listKind, listSince, listUntil, listCursor string
	var listLimit int
	var ndjson bool
	listCmd := &cobra.Command{
		Use:  "list",
		Args: cobra.NoArgs,
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkfapi.LedgerListResult](cmd, a, "wrkf.ledger.list", wrkfapi.LedgerListParams{
				TaskID: listTask, AboutPrincipalRef: listPrincipal, Kind: listKind, Since: listSince, Until: listUntil, Limit: listLimit, Cursor: listCursor,
			})
			if err != nil {
				return err
			}
			return printLedger(cmd, result.Entries, result.NextCursor, ndjson)
		}),
	}
	listCmd.Flags().StringVar(&listTask, "task", "", "Replay one task's ledger")
	listCmd.Flags().StringVar(&listPrincipal, "principal", "", "Project entries about this principal")
	listCmd.Flags().StringVar(&listKind, "kind", "", "Filter by kind")
	listCmd.Flags().StringVar(&listSince, "since", "", "Inclusive ISO-8601 lower timestamp")
	listCmd.Flags().StringVar(&listUntil, "until", "", "Inclusive ISO-8601 upper timestamp")
	listCmd.Flags().IntVar(&listLimit, "limit", 0, "Maximum projection entries (0 is unlimited)")
	listCmd.Flags().StringVar(&listCursor, "cursor", "", "Opaque projection cursor")
	listCmd.Flags().BoolVar(&ndjson, "ndjson", false, "Emit one JSON entry per line")
	cmd.AddCommand(appendCmd, listCmd)
	return cmd
}

func readLedgerBody(cmd *cobra.Command, source string) (json.RawMessage, error) {
	if source == "-" {
		b, err := io.ReadAll(cmd.InOrStdin())
		return json.RawMessage(b), err
	}
	if strings.HasPrefix(strings.TrimSpace(source), "{") {
		return json.RawMessage(source), nil
	}
	b, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("read ledger body %q: %w", source, err)
	}
	return json.RawMessage(b), nil
}

func printLedger(cmd *cobra.Command, entries []wrkfapi.LedgerEntry, nextCursor string, ndjson bool) error {
	if flagJSON {
		return printJSON(cmd, wrkfapi.LedgerListResult{Entries: entries, NextCursor: nextCursor})
	}
	if ndjson {
		for _, entry := range entries {
			b, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
		}
		return nil
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SEQ\tTS\tINSTANCE\tKIND\tABOUT\tWRITTEN BY")
	for _, entry := range entries {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", entry.Seq, entry.TS, entry.InstanceID, entry.Kind, entry.AboutPrincipalRef, entry.WrittenBy)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if nextCursor != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "next_cursor\t%s\n", nextCursor)
	}
	return nil
}

func obligationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "obligation", Short: "Inspect and resolve workflow obligations"}
	var all bool
	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			obl, err := rpcCall[[]wrkfapi.Obligation](cmd, a, "wrkf.obligation.list", map[string]any{"task": args[0], "all": all})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			obl, err := rpcCall[wrkfapi.Obligation](cmd, a, "wrkf.obligation.show", map[string]any{"id": args[0]})
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
			RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
				obl, err := rpcCall[wrkfapi.Obligation](cmd, a, "wrkf.obligation."+use, wrkfapi.ObligationStatusParams{
					TaskSelector: args[0], ID: args[1], EvidenceID: evidenceID, Reason: reason, PrincipalRef: a.actor, Role: a.role,
				})
				if err != nil {
					return err
				}
				return printAny(cmd, flagJSON, obl)
			}),
		}
		c.Flags().StringVar(&evidenceID, "evidence", "", "Evidence id")
		c.Flags().StringVar(&reason, "reason", "", "Reason (- reads stdin)")
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			effects, err := rpcCall[[]wrkfapi.Effect](cmd, a, "wrkf.effect.list", map[string]any{"task": args[0], "all": true})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"effects": effects})
		}),
	}
	show := &cobra.Command{
		Use:  "show EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := rpcCall[wrkfapi.Effect](cmd, a, "wrkf.effect.show", map[string]any{"id": args[0]})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			taskSelector := ""
			if len(args) > 0 {
				taskSelector = args[0]
			}
			claimAdapter := adapter
			if claimAdapter == "" {
				claimAdapter = a.actor
			}
			claim, err := rpcCall[wrkfapi.EffectClaim](cmd, a, "wrkf.effect.claim", wrkfapi.EffectClaimParams{
				Adapter: claimAdapter, Limit: limit, LeaseMs: leaseMs, TaskSelector: taskSelector, Kind: kind,
			})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := rpcCall[wrkfapi.Effect](cmd, a, "wrkf.effect.ack", wrkfapi.EffectAckParams{
				EffectID: args[0], LeaseToken: leaseToken, Force: force,
			})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			out, err := rpcCall[wrkfapi.EffectDelivery](cmd, a, "wrkf.effect.deliver", wrkfapi.EffectDeliverParams{EffectID: args[0], Adapter: a.actor})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	fail := &cobra.Command{
		Use:  "fail EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := rpcCall[wrkfapi.Effect](cmd, a, "wrkf.effect.fail", wrkfapi.EffectFailParams{
				EffectID: args[0], LeaseToken: leaseToken, Reason: reason, Force: force,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	fail.Flags().StringVar(&reason, "reason", "", "Failure reason (- reads stdin)")
	fail.Flags().StringVar(&leaseToken, "lease-token", "", "Lease token")
	fail.Flags().BoolVar(&force, "force", false, "Bypass lease token check")
	retry := &cobra.Command{
		Use:  "retry EFFECT",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := rpcCall[wrkfapi.Effect](cmd, a, "wrkf.effect.retry", map[string]any{"effectId": args[0]})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			resp, err := rpcCall[wrkfapi.NextActionResponse](cmd, a, "wrkf.check.preflight", map[string]any{"task": args[0], "transition": args[1], "role": a.role})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, resp)
		}),
	}
	run := &cobra.Command{
		Use:  "run TASK TRANSITION",
		Args: cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkfapi.CheckRunResult](cmd, a, "wrkf.check.run", wrkfapi.CheckRunParams{
				TaskSelector: args[0], Transition: args[1], PrincipalRef: a.actor, Role: a.role,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"checks": result.Runs})
		}),
	}
	show := &cobra.Command{
		Use:  "show CHECK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			cr, err := rpcCall[wrkfapi.CheckRun](cmd, a, "wrkf.check.show", map[string]any{"id": args[0]})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			checks, err := rpcCall[[]wrkfapi.CheckRun](cmd, a, "wrkf.check.list", map[string]any{"task": args[0], "transition": listTransition})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			resp, err := rpcCall[wrkfapi.NextActionResponse](cmd, a, "wrkf.check.preflight", map[string]any{"task": args[0], "transition": args[1], "role": a.role})
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
	var idempotencyKey string
	var runChecks, dryRun bool
	var checks []string
	cmd := &cobra.Command{
		Use:  "transition TASK TRANSITION",
		Args: cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			var exp *int64
			if cmd.Flags().Changed("expect-revision") {
				exp = &expectRevision
			}
			out, err := applyTransition(cmd, a, wrkfapi.TransitionApplyParams{
				TaskSelector: args[0], Transition: args[1], PrincipalRef: a.actor, Role: a.role, ExpectRevision: exp,
				IdempotencyKey: idempotencyKey, CheckIDs: checks, DryRun: dryRun,
			}, runChecks)
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	cmd.Flags().Int64Var(&expectRevision, "expect-revision", 0, "Expected workflow revision")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key")
	cmd.Flags().BoolVar(&runChecks, "run-checks", false, "Run transition checks before committing")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate without committing")
	cmd.Flags().StringArrayVar(&checks, "check", nil, "Check run id")
	return cmd
}

func applyTransition(cmd *cobra.Command, a *app, params wrkfapi.TransitionApplyParams, runChecks bool) (wrkfapi.TransitionResult, error) {
	if runChecks {
		result, err := rpcCall[wrkfapi.CheckRunResult](cmd, a, "wrkf.check.run", wrkfapi.CheckRunParams{
			TaskSelector: params.TaskSelector,
			Transition:   params.Transition,
			PrincipalRef: params.PrincipalRef,
			Role:         params.Role,
		})
		if err != nil {
			return wrkfapi.TransitionResult{}, err
		}
		for _, run := range result.Runs {
			if strings.TrimSpace(run.ID) == "" {
				return wrkfapi.TransitionResult{}, fmt.Errorf("wrkf.check.run returned a non-persisted check without an id")
			}
			params.CheckIDs = append(params.CheckIDs, run.ID)
		}
	}
	params.RunChecks = false
	return rpcCall[wrkfapi.TransitionResult](cmd, a, "wrkf.transition.apply", params)
}

func suspensionCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "suspension", Short: "Resolve an instance's active suspension"}
	var disposition, explanation string
	var expectRevision int64
	resolve := &cobra.Command{
		Use:   "resolve SUSPENSION_ID --disposition resume|close|cancel",
		Short: "Atomically resolve the active suspension named by SUSPENSION_ID",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			var exp *int64
			if cmd.Flags().Changed("expect-revision") {
				exp = &expectRevision
			}
			out, err := rpcCall[wrkfapi.SuspensionResolveResult](cmd, a, "wrkf.suspension.resolve", wrkfapi.SuspensionResolveParams{
				SuspensionID:   args[0],
				Disposition:    disposition,
				Explanation:    explanation,
				ExpectRevision: exp,
				PrincipalRef:   a.actor,
				Role:           a.role,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	resolve.Flags().StringVar(&disposition, "disposition", "", "Resolution disposition: resume, close, or cancel")
	resolve.Flags().StringVar(&explanation, "explanation", "", "Operator explanation (recorded free text, never validated; - reads stdin)")
	resolve.Flags().Int64Var(&expectRevision, "expect-revision", 0, "Expected workflow revision (CAS precondition)")
	cmd.AddCommand(resolve)
	return cmd
}

func runCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Bind principals to workflow runs"}
	var actor, role, delivery, lane, externalRunRef, idempotencyKey, summary string
	start := &cobra.Command{
		Use:  "start TASK --role ROLE --principal-ref PRINCIPAL_REF",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if actor == "" {
				actor = a.actor
			}
			if role == "" {
				role = a.role
			}
			if role == "" {
				return fmt.Errorf("--role is required")
			}
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.start", wrkfapi.RunStartParams{
				TaskSelector: args[0], Role: role, PrincipalRef: actor,
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			task, role, handle := args[0], args[1], args[2]
			if !strings.Contains(handle, "@") || !strings.Contains(handle, ":") {
				return fmt.Errorf("handle must be project/task-scoped, e.g. observer@agent-spaces:%s~observer", task)
			}
			lane := ""
			if i := strings.LastIndex(handle, "~"); i >= 0 && i+1 < len(handle) {
				lane = handle[i+1:]
			}
			actor := strings.SplitN(handle, "@", 2)[0]
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.start", wrkfapi.RunStartParams{
				TaskSelector: task, Role: role, PrincipalRef: actor, DeliveryRef: handle, Lane: lane,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	finish := &cobra.Command{
		Use:  "finish RUN",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.finish", wrkfapi.RunFinishParams{RunID: args[0], Status: "completed", Summary: summary})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	finish.Flags().StringVar(&summary, "summary", "", "Terminal summary (- reads stdin)")
	fail := &cobra.Command{
		Use:  "fail RUN",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.fail", wrkfapi.RunFailParams{RunID: args[0], Summary: summary})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	fail.Flags().String("kind", "", "Failure kind")
	fail.Flags().StringVar(&summary, "summary", "", "Terminal summary (- reads stdin)")
	show := &cobra.Command{
		Use:  "show RUN",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.show", map[string]any{"id": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	list := &cobra.Command{
		Use:  "list TASK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			runs, err := rpcCall[[]wrkfapi.Run](cmd, a, "wrkf.run.list", map[string]any{"task": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"runs": runs})
		}),
	}
	cmd.AddCommand(start, bind, finish, fail, show, list)
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkfapi.HookListResult](cmd, a, "wrkf.hook.list", map[string]any{})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result)
		}),
	}
	show := &cobra.Command{
		Use:  "show HOOK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkfapi.HookShowResult](cmd, a, "wrkf.hook.show", map[string]any{"id": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result)
		}),
	}
	var hookID string
	run := &cobra.Command{
		Use:  "run TASK TRANSITION --hook HOOK",
		Args: cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if hookID == "" {
				return fmt.Errorf("--hook is required")
			}
			out, err := rpcCall[wrkfapi.CheckRun](cmd, a, "wrkf.hook.run", wrkfapi.HookRunParams{
				TaskSelector: args[0], Transition: args[1], HookID: hookID, PrincipalRef: a.actor, Role: a.role,
			})
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
				if flagHookCatalog != "" {
					return fmt.Errorf("--hook-catalog is local-only; hook catalog is canonical-node configuration in remote mode")
				}
				return workrpcclient.ServeRemoteStdio(cmd.Context(), os.Stdin, os.Stdout, cfg.RemoteEndpoint, workrpcclient.TokenFromEnv())
			}
			actor, wrkqActor, err := actorDefaults(cmd)
			if err != nil {
				return err
			}
			return workrpcclient.ServeConfiguredLocalStdio(cmd.Context(), os.Stdin, os.Stdout, cfg.DBLocator, flagHookCatalog, workrpcclient.LocalServerOptions{
				Entrypoint:       "wrkf",
				ServerVersion:    Version,
				DefaultActor:     actor,
				WrkqDefaultActor: wrkqActor,
				UseWrkqDefault:   true,
				DefaultRole:      roleDefault(),
			})
		},
	}
	cmd.Flags().BoolVar(&stdio, "stdio", false, "Use stdin/stdout JSON-RPC transport")
	return cmd
}

func supervisorCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "supervisor", Short: "Operate recovery and escalation role"}
	var actor, reason string
	start := &cobra.Command{
		Use:  "start TASK",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if actor == "" {
				actor = a.actor
			}
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.start", wrkfapi.RunStartParams{TaskSelector: args[0], Role: "supervisor", PrincipalRef: actor})
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
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			eff, err := rpcCall[wrkfapi.Effect](cmd, a, "wrkf.supervisor.call", wrkfapi.SupervisorParams{TaskSelector: args[0], Reason: reason})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, eff)
		}),
	}
	call.Flags().StringVar(&reason, "reason", "", "Reason (- reads stdin)")
	action := &cobra.Command{
		Use:  "action TASK ACTION",
		Args: cobra.MinimumNArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			switch args[1] {
			case "escalate":
				eff, err := rpcCall[wrkfapi.Effect](cmd, a, "wrkf.supervisor.escalate", wrkfapi.SupervisorParams{TaskSelector: args[0], Reason: reason})
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
				out, err := rpcCall[wrkfapi.TransitionResult](cmd, a, "wrkf.transition.apply", wrkfapi.TransitionApplyParams{
					TaskSelector: args[0], Transition: args[2], PrincipalRef: a.actor, Role: "supervisor",
				})
				if err != nil {
					return err
				}
				return printAny(cmd, flagJSON, out)
			case "create-obligation":
				if len(args) < 3 {
					return fmt.Errorf("create-obligation action requires obligation kind")
				}
				obl, err := rpcCall[wrkfapi.Obligation](cmd, a, "wrkf.obligation.create", wrkfapi.ObligationCreateParams{
					TaskSelector: args[0], Kind: args[2], OwnerRole: "supervisor", Blocking: true, Reason: reason,
				})
				if err != nil {
					return err
				}
				return printAny(cmd, flagJSON, obl)
			default:
				return fmt.Errorf("unknown supervisor action: %s", args[1])
			}
		}),
	}
	action.Flags().StringVar(&reason, "reason", "", "Reason (- reads stdin)")
	action.Flags().String("role", "", "Role")
	action.Flags().String("from-check", "", "Check run")
	action.Flags().String("target", "", "Target")
	cmd.AddCommand(start, call, action)
	return cmd
}
