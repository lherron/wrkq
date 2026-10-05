package wrkfcli

import (
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func effectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "effect", Short: "Inspect and operate workflow effects"}
	list := &cobra.Command{
		Use:   "list TASK",
		Short: "List effects on a task",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			effects, err := rpcCall[[]wrkfapi.Effect](cmd, a, "wrkf.effect.list", map[string]any{"task": args[0], "all": true})
			if err != nil {
				return err
			}
			return printAny(cmd, flagJSON, map[string]interface{}{"effects": effects})
		}),
	}
	show := &cobra.Command{
		Use:   "show EFFECT",
		Short: "Show an effect",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Effect](cmd, a, "wrkf.effect.show", map[string]any{"id": args[0]})
		}),
	}
	var adapter, leaseToken, reason string
	var limit int
	var leaseMs int64
	var kind string
	var force bool
	claim := &cobra.Command{
		Use:   "claim [TASK]",
		Short: "Claim a pending effect for delivery",
		Args:  cobra.MaximumNArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			taskSelector := ""
			if len(args) > 0 {
				taskSelector = args[0]
			}
			claimAdapter := adapter
			if claimAdapter == "" {
				claimAdapter = a.principalRef
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
		Use:   "ack EFFECT",
		Short: "Acknowledge an effect",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Effect](cmd, a, "wrkf.effect.ack", wrkfapi.EffectAckParams{
				EffectID: args[0], LeaseToken: leaseToken, Force: force,
			})
		}),
	}
	ack.Flags().StringVar(&leaseToken, "lease-token", "", "Lease token")
	ack.Flags().BoolVar(&force, "force", false, "Bypass lease token check")
	deliver := &cobra.Command{
		Use:   "deliver EFFECT",
		Short: "Deliver an effect through its adapter",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.EffectDelivery](cmd, a, "wrkf.effect.deliver", wrkfapi.EffectDeliverParams{EffectID: args[0], Adapter: a.principalRef})
		}),
	}
	fail := &cobra.Command{
		Use:   "fail EFFECT",
		Short: "Record an effect delivery failure",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Effect](cmd, a, "wrkf.effect.fail", wrkfapi.EffectFailParams{
				EffectID: args[0], LeaseToken: leaseToken, Reason: reason, Force: force,
			})
		}),
	}
	fail.Flags().StringVar(&reason, "reason", "", "Failure reason (- reads stdin)")
	fail.Flags().StringVar(&leaseToken, "lease-token", "", "Lease token")
	fail.Flags().BoolVar(&force, "force", false, "Bypass lease token check")
	retry := &cobra.Command{
		Use:   "retry EFFECT",
		Short: "Retry a failed effect",
		Args:  cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return printCall[wrkfapi.Effect](cmd, a, "wrkf.effect.retry", map[string]any{"effectId": args[0]})
		}),
	}
	cmd.AddCommand(list, show, claim, deliver, ack, fail, retry)
	return cmd
}
