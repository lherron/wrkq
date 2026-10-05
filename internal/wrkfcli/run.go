package wrkfcli

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func runCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Bind principals to workflow runs"}
	var principalRef, role, delivery, lane, externalRunRef, idempotencyKey, summary string
	start := &cobra.Command{
		Use:   "start TASK --role ROLE --principal-ref PRINCIPAL_REF",
		Short: "Start a workflow run for a role and principal",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if principalRef == "" {
				principalRef = a.principalRef
			}
			if role == "" {
				role = a.role
			}
			if role == "" {
				return fmt.Errorf("--role is required")
			}
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.start", wrkfapi.RunStartParams{
				TaskSelector: args[0], Role: role, PrincipalRef: principalRef,
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
	start.Flags().StringVar(&principalRef, "principal-ref", "", "Principal ref (agent:<id>)")
	start.Flags().StringVar(&delivery, "delivery-ref", "", "Delivery ref")
	start.Flags().StringVar(&lane, "lane", "", "Lane")
	start.Flags().StringVar(&externalRunRef, "external-run-ref", "", "External run ref")
	start.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key")
	bind := &cobra.Command{
		Use:   "bind TASK ROLE HANDLE",
		Short: "Start a run bound to a role and delivery handle",
		Args:  cobra.ExactArgs(3),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			task, role, handle := args[0], args[1], args[2]
			if !strings.Contains(handle, "@") || !strings.Contains(handle, ":") {
				return fmt.Errorf("handle must be project/task-scoped, e.g. observer@agent-spaces:%s~observer", task)
			}
			lane := ""
			if i := strings.LastIndex(handle, "~"); i >= 0 && i+1 < len(handle) {
				lane = handle[i+1:]
			}
			principalRef, err := attribution.NormalizeCanonical("agent:" + strings.SplitN(handle, "@", 2)[0])
			if err != nil {
				return fmt.Errorf("invalid delivery handle principal: %w", err)
			}
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.start", wrkfapi.RunStartParams{
				TaskSelector: task, Role: role, PrincipalRef: principalRef, DeliveryRef: handle, Lane: lane,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	finish := &cobra.Command{
		Use:   "finish RUN",
		Short: "Mark a workflow run completed",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Run](cmd, a, "wrkf.run.finish", wrkfapi.RunFinishParams{RunID: args[0], Status: "completed", Summary: summary})
		}),
	}
	finish.Flags().StringVar(&summary, "summary", "", "Terminal summary (- reads stdin)")
	fail := &cobra.Command{
		Use:   "fail RUN",
		Short: "Mark a workflow run failed",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Run](cmd, a, "wrkf.run.fail", wrkfapi.RunFailParams{RunID: args[0], Summary: summary})
		}),
	}
	fail.Flags().String("kind", "", "Failure kind")
	fail.Flags().StringVar(&summary, "summary", "", "Terminal summary (- reads stdin)")
	show := &cobra.Command{
		Use:   "show RUN",
		Short: "Show a workflow run",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Run](cmd, a, "wrkf.run.show", map[string]any{"id": args[0]})
		}),
	}
	list := &cobra.Command{
		Use:   "list TASK",
		Short: "List workflow runs for a task",
		Args:  cobra.ExactArgs(1),
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

func supervisorCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "supervisor", Short: "Operate recovery and escalation role"}
	var principalRef, reason string
	start := &cobra.Command{
		Use:   "start TASK",
		Short: "Start a supervisor run on a task",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if principalRef == "" {
				principalRef = a.principalRef
			}
			run, err := rpcCall[wrkfapi.Run](cmd, a, "wrkf.run.start", wrkfapi.RunStartParams{TaskSelector: args[0], Role: "supervisor", PrincipalRef: principalRef})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, run)
		}),
	}
	start.Flags().StringVar(&principalRef, "principal-ref", "", "Principal ref (agent:<id>)")
	call := &cobra.Command{
		Use:   "call TASK",
		Short: "Call the supervisor on a task with a reason",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Effect](cmd, a, "wrkf.supervisor.call", wrkfapi.SupervisorParams{TaskSelector: args[0], Reason: reason})
		}),
	}
	call.Flags().StringVar(&reason, "reason", "", "Reason (- reads stdin)")
	action := &cobra.Command{
		Use:   "action TASK ACTION",
		Short: "Apply a supervisor action to a task: escalate, retry, transition, create-obligation",
		Args:  cobra.MinimumNArgs(2),
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
					TaskSelector: args[0], Transition: args[2], PrincipalRef: a.principalRef, Role: "supervisor",
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
