package wrkfcli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/wrkqapi"
	"github.com/spf13/cobra"
)

func taskCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "task", Short: "Attach and inspect workflow instances on tasks"}
	var workflowRef string
	var supersede bool
	var predecessorInstance string
	var predecessorRevision int64
	var attachDiscontinued bool
	attach := &cobra.Command{
		Use:   "attach TASK --workflow ID@VERSION",
		Short: "Attach a workflow template version to a task",
		Args:  cobra.ExactArgs(1),
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
				// Actor is the retained wrkq compatibility wire key; the value is
				// always the resolved canonical caller principal.
				Actor: a.principalRef,
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
		Use:   "inspect TASK",
		Short: "Inspect a task's current workflow instance",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkqapi.WrkqWorkflowInspectResult](cmd, a, "wrkq.workflow.inspect", map[string]any{"task": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result.Instance)
		}),
	}
	instances := &cobra.Command{
		Use:   "instances TASK",
		Short: "List every workflow instance generation attached to a task",
		Long: "List every workflow instance generation attached to a task.\n" +
			"Use --json for the stable {\"instances\": [...]} machine envelope. " +
			"Human output is presentation-only: one tab-separated id, status, phase, template, and creation time per row.",
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkqapi.WrkqWorkflowInstancesResult](cmd, a, "wrkq.workflow.instances", map[string]any{"task": args[0]})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, result)
			}
			if len(result.Instances) == 0 {
				fmt.Fprintln(cmd.OutOrStdout(), "No workflow instances.")
				return nil
			}
			for _, inst := range result.Instances {
				fmt.Fprintf(cmd.OutOrStdout(), "%s\t%s\t%s\t%s@%s\t%s\n",
					inst.ID, inst.Status, inst.Phase, inst.TemplateID, inst.TemplateVersion, inst.CreatedAt)
			}
			return nil
		}),
	}
	timeline := &cobra.Command{
		Use:   "timeline TASK",
		Short: "Show a task's workflow timeline",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkqapi.WrkqWorkflowTimelineResult](cmd, a, "wrkq.workflow.timeline", map[string]any{"task": args[0]})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"events": result.Events})
		}),
	}
	refresh := &cobra.Command{
		Use:   "refresh TASK",
		Short: "Refresh a task's workflow projection",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkqapi.WrkqWorkflowInspectResult](cmd, a, "wrkq.workflow.refresh", map[string]any{
				"task": args[0],
				// Retained wrkq compatibility wire key; never a legacy authority source.
				"actor": a.principalRef,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"instance": result.Instance})
		}),
	}
	syncMeta := &cobra.Command{
		Use:   "sync-meta [TASK]",
		Short: "Sync workflow metadata onto task projections",
		Args:  cobra.MaximumNArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			task := ""
			if len(args) > 0 {
				task = args[0]
			}
			result, err := rpcCall[wrkqapi.WrkqWorkflowSyncMetaResult](cmd, a, "wrkq.workflow.syncMeta", wrkqapi.WorkflowSyncMetaParams{
				Task: task,
				// Actor is the retained wrkq compatibility wire key.
				Actor: a.principalRef,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, result)
		}),
	}
	syncMeta.Flags().Bool("all", false, "Sync all workflow task projections")
	cmd.AddCommand(attach, inspect, instances, timeline, refresh, syncMeta)
	return cmd
}

func nextCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "next TASK",
		Short: "Show the next action for a task and role",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.NextActionResponse](cmd, a, "wrkf.instance.next", map[string]any{"task": args[0], "role": a.role})
		}),
	}
}

func instanceCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "instance", Short: "Inspect and terminalize workflow instances"}
	var instanceID, explanation string
	var expectRevision int64
	cancel := &cobra.Command{
		Use:   "cancel TASK",
		Short: "Atomically cancel an active unsuspended instance",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			var expected *int64
			if cmd.Flags().Changed("expect-revision") {
				expected = &expectRevision
			}
			out, err := rpcCall[wrkfapi.InstanceCancelResult](cmd, a, "wrkf.instance.cancel", wrkfapi.InstanceCancelParams{
				TaskSelector: args[0], InstanceID: instanceID, ExpectRevision: expected, Explanation: explanation,
				PrincipalRef: a.principalRef, Role: a.role,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, out)
		}),
	}
	cancel.Flags().StringVar(&instanceID, "instance", "", "Exact workflow instance id")
	cancel.Flags().Int64Var(&expectRevision, "expect-revision", 0, "Expected workflow revision (CAS precondition)")
	cancel.Flags().StringVar(&explanation, "explanation", "", "Operator explanation (recorded free text; - reads stdin)")
	cmd.AddCommand(cancel)
	return cmd
}
