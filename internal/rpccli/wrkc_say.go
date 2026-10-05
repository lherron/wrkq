package rpccli

// wrkc_say.go — wrkc say: route a message into a room, optionally fan it out
// to addressees, and with --wait block until every envelope is terminal and
// hand back the replies.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

func newWrkcSayCmd() *cobra.Command {
	var to, discharges []string
	var fyi, newRoom, record bool
	var respondTo, idempotencyKey, timeout, message, ttl string
	var wait, preempt bool
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "say [ref] [body|-] [-m body]",
		Short: "Send a message into a room; only --to presents it",
		Long: `Say something in the room the ref routes to.

With no ref, --to must name one full agent@project[:scope] addressee;
that addressee becomes the ref. In this form, body is read from stdin
directly or with a positional "-". A positional ref keeps its usual meaning.

Routing (first match wins):
  R-xxxxx / EN-xxxxx   that room (an envelope resolves to its room)
  T-xxxxx              the task's CAMPAIGN room if the task is in a campaign,
                       else the task room. Strict coalesce; no override. The
                       envelope is tagged with the task either way.
  container id/path    campaign-adorned -> campaign room; project -> project
                       room; any other container is refused.
  agent@project[:task] derived from the work context of both parties, TARGET
                       WINS. Target task-scoped -> the target's task room. Sender
                       task-scoped and target not -> the SENDER's task room, so a
                       worker escalating to its supervisor lands on the work.
                       Neither task-scoped -> an ad-hoc pair room, reused unless
                       --new.

Only --to fires. Without it this is a log entry and nobody is presented.
--to a,b fans out to one envelope per addressee sharing a group id, so one
recipient's reply, defer, or failure never disposes another's obligation.

Delivery is HRC-side: an addressed say is steered into the addressee's live turn
when its harness accepts steering, otherwise presented at the next turn
boundary; an idle seat starts a turn. --preempt interrupts the turn (operator
authority). A turn ending is never a reply; --wait returns only when every
envelope is terminal.

Saying with --to also ACKS your own standing obligations in this room from the
same counterparty: for an agent, the reply IS the ack. To hold one back, defer
it first.

A bare name in --to resolves, in order: the seat that is WAITING on you — the
sender of your most recently presented obligation in this room with that name —
then the room's single member with that name, then the room's own shape (task
room -> agent@project:T-xxx, campaign/project room -> agent@project:primary).
Two members of that name and no obligation refuses and names them: reply to a
seat that never asked and its obligation can fail unanswered. An envelope's
replyTo is the exact token that answers it; a full handle always wins. Use
agent:<id> to address a scope-less principal such as a human.`,
		Args: cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref, bodyArg, implicitRef, err := wrkcSayRef(args, to)
			if err != nil {
				return err
			}
			if message != "" && (len(args) > 1 || (implicitRef && len(args) == 1)) {
				return errors.New("say takes the body either positionally or with -m, not both")
			}
			if message != "" {
				// T-07624: -m/--message is the wrkq reflex (comment add -m, touch -d);
				// the positional body stays canonical.
				bodyArg = message
			}
			body, err := readTextValue(bodyArg, "body", cmd.InOrStdin(), &stdinClaims{})
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return errors.New("say requires a body (literal, @file, or - for stdin)")
			}
			if wait && len(to) == 0 {
				return errors.New("--wait requires --to")
			}
			if (preempt || ttl != "" || cmd.Flags().Changed("discharges")) && len(to) == 0 {
				return errors.New("--preempt, --ttl, and --discharges require --to")
			}

			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			params := map[string]any{"ref": ref, "body": body}
			if len(to) > 0 {
				params["to"] = to
			}
			if ttl != "" {
				params["ttl"] = ttl
			}
			if preempt {
				// The ledger stores the intent as delivery "hold"; the verb is HRC's.
				params["hold"] = true
			}
			if cmd.Flags().Changed("discharges") {
				params["dischargeEnvelopeIds"] = discharges
			}
			for key, set := range map[string]bool{"fyi": fyi, "new": newRoom, "record": record} {
				if set {
					params[key] = true
				}
			}
			if respondTo != "" {
				params["respondTo"] = respondTo
			}
			if idempotencyKey != "" {
				params["idempotencyKey"] = idempotencyKey
			}

			var result roomSayResultWire
			if _, err := c.call("wrkq.room.say", params, &result); err != nil {
				return err
			}
			printWrkcSayNotices(cmd, result)
			if wait {
				return wrkcWaitForGroup(c, result, timeout, output)
			}
			return renderWrkcSayResult(cmd, result, output)
		},
	}
	cmd.Flags().StringSliceVar(&to, "to", nil, "Addressees (repeatable or comma-separated); fans out one envelope each")
	cmd.Flags().BoolVar(&fyi, "fyi", false, "No reply obligation, never gates, never itself a wake; delivered like any say into a seated addressee (drives a turn there), may re-seat an existing session when that target is driven for another reason, never births a target that was never born")
	cmd.Flags().StringVarP(&message, "message", "m", "", "Body (literal, @file, or - for stdin); alias for the positional body")
	cmd.Flags().BoolVar(&newRoom, "new", false, "Force a fresh ad-hoc room instead of reusing the open pair room")
	cmd.Flags().BoolVar(&wait, "wait", false, "Block until every envelope in the group is terminal, then print each reply")
	cmd.Flags().StringVar(&timeout, "timeout", "", "Maximum --wait duration (e.g. 10m)")
	cmd.Flags().StringVar(&respondTo, "respond-to", "", "Principal the reply should be addressed to")
	cmd.Flags().BoolVar(&record, "record", false, "Also write the body as a wrkq comment on the room's task")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key for this say; carried by every envelope of the fan-out")
	cmd.Flags().StringVar(&ttl, "ttl", "", "Expire if never presented within this duration (for example 30s)")
	cmd.Flags().BoolVar(&preempt, "preempt", false, "Ask HRC to interrupt the addressee's active turn (operator authority; without it, delivered like an ordinary say with receipt outcome hold_refused_authority). Stored as delivery intent \"hold\"; wrkq never routes it")
	cmd.Flags().StringSliceVar(&discharges, "discharges", nil, "Pending or presented envelope ids this reply discharges, exactly")
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

// wrkcSayRef resolves the room ref and the positional body argument. With no
// ref (or only "-" alongside --to), the single full agent@project[:scope]
// addressee is the ref and the body comes from stdin.
func wrkcSayRef(args, to []string) (ref, bodyArg string, implicit bool, err error) {
	implicit = len(args) == 0 || (len(args) == 1 && args[0] == "-" && len(to) > 0)
	if !implicit {
		bodyArg = "-"
		if len(args) > 1 {
			bodyArg = args[1]
		}
		return args[0], bodyArg, false, nil
	}
	if len(to) == 0 {
		return "", "", true, errors.New("say requires a room ref (R-/EN-/T-/container/agent@project) or one full --to addressee")
	}
	if len(to) != 1 {
		return "", "", true, errors.New("say with multiple --to addressees requires an explicit room ref")
	}
	parsed, perr := scope.ParseScopeHandle(strings.TrimSpace(to[0]))
	if perr != nil || parsed.ProjectID == "" {
		return "", "", true, errors.New("say without a room ref requires a full agent@project[:scope] --to address; bare names need an explicit ref")
	}
	return to[0], "-", true, nil
}

// printWrkcSayNotices writes the server's advisories to stderr so they never
// contaminate a piped raw/json read. They are never errors: the say already
// wrote.
func printWrkcSayNotices(cmd *cobra.Command, result roomSayResultWire) {
	if len(result.Notices) > 0 {
		for _, notice := range result.Notices {
			fmt.Fprintln(cmd.ErrOrStderr(), "notice: "+notice)
		}
	} else if result.Notice != nil {
		// Compatibility with a server from before the notices array.
		fmt.Fprintln(cmd.ErrOrStderr(), "notice: "+*result.Notice)
	}
}

// wrkcWaitForGroup blocks on the whole fan-out group and then prints the
// replies. It is `wrkq monitor wait <group> --until terminal` in-process: the
// same server-owned condition snapshot, the same client-owned poll loop.
func wrkcWaitForGroup(c wrkcClient, result roomSayResultWire, timeout string, output promiseOutputFlags) error {
	cmd := c.cmd
	if result.GroupID == "" {
		return errors.New("say returned no group to wait on")
	}
	duration, err := parseMonitorDuration(timeout, 0)
	if err != nil {
		return err
	}
	principal, _ := actorFlag(cmd)
	terminal, unmet, _, err := monitorWaitLoop(cmd.Context(), c.tr, monitorStreamOpts{
		scopedTasks:  []string{result.GroupID},
		condition:    "terminal",
		timeout:      duration,
		principalRef: principal,
		scopeRef:     wrkcScopeRef(cmd),
	})
	if err != nil {
		return err
	}
	if terminal != monitorResultMet {
		fmt.Fprintf(cmd.ErrOrStderr(), "wrkc say --wait ended %s; still open: %s\n",
			terminal, strings.Join(unmet, ", "))
		return fmt.Errorf("wrkc say --wait ended %s", terminal)
	}

	view, err := c.roomLog(result.Room.Key)
	if err != nil {
		return err
	}
	if failures := wrkcGroupFailures(view, result); len(failures) > 0 {
		for _, failure := range failures {
			fmt.Fprintln(cmd.OutOrStdout(), failure)
		}
		return errors.New("one or more envelopes failed")
	}
	replies := wrkcGroupReplies(view, result)
	if len(replies) == 0 {
		return renderWrkcSayResult(cmd, result, output)
	}
	ids := make([]string, 0, len(replies))
	for _, reply := range replies {
		ids = append(ids, reply.ID)
	}
	var consumed roomLogViewWire
	if _, err := c.call("wrkq.envelope.ack", map[string]any{"envelopes": ids, "reason": "consumed_by_wait"}, &consumed); err != nil {
		return err
	}
	return renderWrkcEnvelopes(cmd, consumed.Items, output, false)
}

// wrkcGroupFailures lists "<member> <reason>" for each envelope of this say
// that ended failed, expired or withdrawn.
func wrkcGroupFailures(view roomLogViewWire, result roomSayResultWire) []string {
	wanted := map[string]bool{}
	for _, envelope := range result.Envelopes {
		wanted[envelope.ID] = true
	}
	failures := []string{}
	for _, envelope := range view.Items {
		if !wanted[envelope.ID] || (envelope.State != "failed" && envelope.State != "expired" && envelope.State != "withdrawn") {
			continue
		}
		member := envelope.ID
		if envelope.To != nil {
			member = envelopePartyLabel(*envelope.To)
		}
		reason := envelope.State
		if envelope.FailureReason != nil {
			reason = *envelope.FailureReason
		}
		failures = append(failures, member+" "+reason)
	}
	return failures
}

// wrkcGroupReplies returns the envelopes each addressee sent back to the
// sender after this say. wrkq keeps no reply pointer: a reply is an envelope,
// and typed replies are cut by design.
func wrkcGroupReplies(view roomLogViewWire, result roomSayResultWire) []envelopeWire {
	sent := map[string]bool{}
	counterparties := map[string]bool{}
	var sender envelopePartyWire
	last := ""
	for _, envelope := range result.Envelopes {
		sent[envelope.ID] = true
		sender = envelope.From
		if envelope.To != nil {
			counterparties[envelopePartyKey(*envelope.To)] = true
		}
		if envelope.ID > last {
			last = envelope.ID
		}
	}

	replies := []envelopeWire{}
	for _, envelope := range view.Items {
		if sent[envelope.ID] || envelope.ID <= last {
			continue
		}
		if !counterparties[envelopePartyKey(envelope.From)] {
			continue
		}
		if envelope.Obligation != "reply_required" || envelope.To == nil || !sameEnvelopeParty(*envelope.To, sender) {
			continue
		}
		replies = append(replies, envelope)
	}
	return replies
}
