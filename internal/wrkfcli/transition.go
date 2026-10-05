package wrkfcli

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func transitionCmd() *cobra.Command {
	var expectRevision int64
	var idempotencyKey string
	var runChecks, dryRun bool
	var checks []string
	cmd := &cobra.Command{
		Use:   "transition TASK TRANSITION",
		Short: "Apply a workflow transition to a task",
		Args:  cobra.ExactArgs(2),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			var exp *int64
			if cmd.Flags().Changed("expect-revision") {
				exp = &expectRevision
			}
			out, err := applyTransition(cmd, a, wrkfapi.TransitionApplyParams{
				TaskSelector: args[0], Transition: args[1], PrincipalRef: a.principalRef, Role: a.role, ExpectRevision: exp,
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
				PrincipalRef:   a.principalRef,
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
