package wrkfcli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func evidenceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "evidence", Short: "Add and inspect workflow evidence"}
	var kind, ref, summary, facts, data, transition string
	add := &cobra.Command{
		Use:   "add TASK --kind KIND --ref REF",
		Short: "Add evidence of a kind and ref to a task",
		Args:  cobra.ExactArgs(1),
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
				PrincipalRef: a.principalRef,
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
		Use:   "list TASK",
		Short: "List evidence on a task",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			ev, err := rpcCall[[]wrkfapi.Evidence](cmd, a, "wrkf.evidence.list", map[string]any{"task": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"evidence": ev})
		}),
	}
	show := &cobra.Command{
		Use:   "show EVIDENCE",
		Short: "Show an evidence record",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Evidence](cmd, a, "wrkf.evidence.show", map[string]any{"id": args[0]})
		}),
	}
	suggest := &cobra.Command{
		Use:   "suggest TASK --transition TRANSITION",
		Short: "Suggest evidence needed for a transition",
		Args:  cobra.ExactArgs(1),
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
		Use:   "schema TASK --kind KIND",
		Short: "Show the evidence schema for a kind on a task",
		Args:  cobra.ExactArgs(1),
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
		Use:   "exec TASK --kind KIND -- COMMAND...",
		Short: "Run a command and record its result as evidence",
		Args:  cobra.MinimumNArgs(2),
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
				PrincipalRef: a.principalRef,
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
