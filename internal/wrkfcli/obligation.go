package wrkfcli

import (
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func obligationCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "obligation", Short: "Inspect and resolve workflow obligations"}
	var all bool
	list := &cobra.Command{
		Use:   "list TASK",
		Short: "List obligations on a task",
		Args:  cobra.ExactArgs(1),
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
		Use:   "show OBLIGATION",
		Short: "Show an obligation",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Obligation](cmd, a, "wrkf.obligation.show", map[string]any{"id": args[0]})
		}),
	}
	var evidenceID, reason string
	statusCmd := func(use, status string) *cobra.Command {
		c := &cobra.Command{
			Use:   use + " TASK OBLIGATION",
			Short: "Mark an obligation " + status,
			Args:  cobra.ExactArgs(2),
			RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
				return printCall[wrkfapi.Obligation](cmd, a, "wrkf.obligation."+use, wrkfapi.ObligationStatusParams{
					TaskSelector: args[0], ID: args[1], EvidenceID: evidenceID, Reason: reason, PrincipalRef: a.principalRef, Role: a.role,
				})
			}),
		}
		c.Flags().StringVar(&evidenceID, "evidence", "", "Evidence id")
		c.Flags().StringVar(&reason, "reason", "", "Reason (- reads stdin)")
		return c
	}
	cmd.AddCommand(list, show, statusCmd("satisfy", "satisfied"), statusCmd("waive", "waived"), statusCmd("cancel", "cancelled"))
	return cmd
}
