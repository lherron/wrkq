package wrkfcli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func checkCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "check TASK TRANSITION",
		Short: "Run workflow checks",
		Args:  cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.NextActionResponse](cmd, a, "wrkf.check.preflight", map[string]any{"task": args[0], "transition": args[1], "role": a.role})
		}),
	}
	run := &cobra.Command{
		Use:   "run TASK TRANSITION",
		Short: "Run and record the checks for a transition",
		Args:  cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkfapi.CheckRunResult](cmd, a, "wrkf.check.run", wrkfapi.CheckRunParams{
				TaskSelector: args[0], Transition: args[1], PrincipalRef: a.principalRef, Role: a.role,
			})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"checks": result.Runs})
		}),
	}
	show := &cobra.Command{
		Use:   "show CHECK",
		Short: "Show a check run",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.CheckRun](cmd, a, "wrkf.check.show", map[string]any{"id": args[0]})
		}),
	}
	var listTransition string
	list := &cobra.Command{
		Use:   "list TASK",
		Short: "List check runs for a task",
		Args:  cobra.ExactArgs(1),
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
		Use:   "preflight TASK TRANSITION",
		Short: "Preview what a transition still needs before applying it",
		Args:  cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.NextActionResponse](cmd, a, "wrkf.check.preflight", map[string]any{"task": args[0], "transition": args[1], "role": a.role})
		}),
	}
	cmd.AddCommand(preflight, run, show, list)
	return cmd
}

func hookCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "hook", Short: "Inspect and debug local hook catalog"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List hooks in the local catalog",
		Args:  cobra.NoArgs,
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.HookListResult](cmd, a, "wrkf.hook.list", map[string]any{})
		}),
	}
	show := &cobra.Command{
		Use:   "show HOOK",
		Short: "Show a hook from the local catalog",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.HookShowResult](cmd, a, "wrkf.hook.show", map[string]any{"id": args[0]})
		}),
	}
	var hookID string
	run := &cobra.Command{
		Use:   "run TASK TRANSITION --hook HOOK",
		Short: "Run one catalog hook for a task transition",
		Args:  cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if hookID == "" {
				return fmt.Errorf("--hook is required")
			}
			out, err := rpcCall[wrkfapi.CheckRun](cmd, a, "wrkf.hook.run", wrkfapi.HookRunParams{
				TaskSelector: args[0], Transition: args[1], HookID: hookID, PrincipalRef: a.principalRef, Role: a.role,
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
