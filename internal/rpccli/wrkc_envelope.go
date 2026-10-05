package rpccli

// wrkc_envelope.go — the envelope verbs that act on obligations rather than
// rooms: list your own (inbox), pause one (defer), take back unpresented mail
// (withdraw), and the operator-only ack.

import (
	"errors"
	"strings"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

func newWrkcWithdrawCmd() *cobra.Command {
	var group bool
	var reason string
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use: "withdraw <EN-xxxxx>", Short: "Withdraw unpresented mail",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{"envelope": args[0]}
			if group {
				params["group"] = true
			}
			if reason != "" {
				params["reason"] = reason
			}
			var result envelopeWithdrawResultWire
			if _, err := c.call("wrkq.envelope.withdraw", params, &result); err != nil {
				return err
			}
			return renderWrkcWithdrawResult(cmd, result, output)
		},
	}
	cmd.Flags().BoolVar(&group, "group", false, "Withdraw every unpresented envelope in this fan-out group")
	cmd.Flags().StringVar(&reason, "reason", "", "Reason for withdrawal")
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newWrkcInboxCmd() *cobra.Command {
	var includeFailed bool
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Reply-required envelopes addressed to your scope",
		Long: `List the obligations standing against your scope, grouped by room.

fyi is never listed: it carries no obligation and is acked at its own
presentation. Deferred envelopes appear under their own heading with the time
they come back. EN- ids are shown so you can tell an at-least-once
delivery duplicate from something new. A presented obligation belongs to that
runtime: it is never presented across runtimes, and a runtime ending undisposed
fails it. One pointer reminder may occur inside the same runtime; defer with a
reason to hold the obligation across rotation.

Every obligation here gates your turn and wakes you, whatever its room looks
like: there is no room state that excuses one. A group whose work has gone
terminal is marked as such for context — the seat that asked may have moved on —
and answering it is a normal say.

Your own failed, expired and withdrawn sends are listed while they still ask
something of you, judged at read time: never a fyi; a reply_required with a
task while that task is open; one without a task while it is under 24h old and
its room is not stale. show and log still read the rest.
The JSON forms carry obligations as full envelopes and every sent line as a
summary row {id, room, to, obligation, state, failureReason, taskId, createdAt}.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{}
			if includeFailed {
				params["includeFailed"] = true
			}
			var view envelopeInboxViewWire
			if _, err := c.call("wrkq.envelope.inboxView", params, &view); err != nil {
				return err
			}
			mode, stable, err := resolvePromiseOutputMode(cmd, output, true)
			if err != nil {
				return err
			}
			if mode == "human" || mode == "table" {
				return renderWrkcInbox(cmd, view)
			}
			if mode == "json" || mode == "yaml" {
				return render.NewRenderer(cmd.OutOrStdout(), render.Options{Porcelain: stable}).RenderJSON(wrkcInboxJSON(view))
			}
			flat := []envelopeWire{}
			for _, group := range view.Groups {
				flat = append(flat, group.Items...)
			}
			flat = append(flat, view.Deferred...)
			flat = append(flat, view.Failed...)
			if mode == "ndjson" {
				return renderWrkcInboxNDJSON(cmd, flat, view, stable)
			}
			flat = append(flat, view.SentFailed...)
			return renderWrkcEnvelopesMode(cmd, flat, mode, stable, false)
		},
	}
	cmd.Flags().BoolVar(&includeFailed, "failed", false, "Also list failed envelopes addressed to you")
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newWrkcDeferCmd() *cobra.Command {
	var reason, retryAfter, retryAt string
	var ifMatch, etag int64
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "defer <EN-xxxxx>",
		Short: "Pause one obligation with a reason",
		Long: `Pause one obligation. Deferred is PAUSED, never terminal: replies leave it
paused, and the sender keeps visibility rather than a silent drop.

--retry-after arms a wrkq promise; when that time arrives the envelope returns to
pending and the kicker re-drives it. Deferring without a retry time is legal —
the protection for the sender is visibility, not a timer.

Defer is also how you exclude ONE obligation from a reply: saying --to acks
every pending or presented obligation from that counterparty, so defer the one
you are not answering first.

To finish a deferred obligation, name it in the reply:
wrkc say EN-xxxxx --to <sender> --discharges EN-xxxxx -`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			claims := &stdinClaims{}
			if strings.TrimSpace(reason) == "" {
				return errors.New("defer requires --reason")
			}
			value, err := readTextValue(reason, "--reason", cmd.InOrStdin(), claims)
			if err != nil {
				return err
			}
			if retryAfter != "" && retryAt != "" {
				return errors.New("--retry-after and --retry-at are mutually exclusive")
			}
			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{"envelope": args[0], "reason": value}
			if retryAfter != "" {
				params["retryAfter"] = retryAfter
			}
			if retryAt != "" {
				params["retryAt"] = retryAt
			}
			match, err := promiseIfMatch(cmd, ifMatch, etag)
			if err != nil {
				return err
			}
			if match > 0 {
				params["ifMatch"] = match
			}
			var envelope envelopeWire
			if _, err := c.call("wrkq.envelope.defer", params, &envelope); err != nil {
				return err
			}
			return renderWrkcEnvelopes(cmd, []envelopeWire{envelope}, output, true)
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Why this is deferred (literal, @file, or - for stdin)")
	cmd.Flags().StringVar(&retryAfter, "retry-after", "", "Relative retry time resolved by the server (e.g. 2h, 1d)")
	cmd.Flags().StringVar(&retryAt, "retry-at", "", "Absolute retry timestamp")
	addPromiseETagFlags(cmd, &ifMatch, &etag)
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func newWrkcAckCmd() *cobra.Command {
	var note string
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "ack <EN-xxxxx>...",
		Short: "Operator-only: clear envelopes without replying",
		Long: `Clear envelopes without replying. This is an OPERATOR verb, intended for a human
principal (wrkc ack EN-00042 --as agent:lance) discharging an obligation by hand.
A failed envelope is terminal and ack refuses it.

Agents do not ack: for an agent the reply IS the ack. If you are an agent and you
want to put something down, defer it with a reason.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{"envelopes": args}
			if note != "" {
				params["note"] = note
			}
			var view roomLogViewWire
			if _, err := c.call("wrkq.envelope.ack", params, &view); err != nil {
				return err
			}
			return renderWrkcEnvelopes(cmd, view.Items, output, false)
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Why this was cleared")
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}
