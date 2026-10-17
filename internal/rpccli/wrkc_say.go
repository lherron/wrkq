package rpccli

// wrkc_say.go — wrkc say: route a message into a room, optionally fan it out
// to addressees, and with --wait block until every envelope is terminal and
// hand back the replies.

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/spf13/cobra"
)

func newWrkcSayCmd() *cobra.Command {
	var to, discharges []string
	var fyi, note, newRoom, record bool
	var respondTo, idempotencyKey, timeout, message, ttl string
	var wait, preempt bool
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "say <ref> (-m <body> | -)",
		Short: "Send a message into a room; only --to presents it",
		Long: `Say something in the room the ref routes to.

The ref is the one positional and is required. The body source is explicit
and exactly one of:
  -m <body>      a literal, or @file to read a file
  -              the body on stdin, from a heredoc or a pipe:
                 wrkc say EN-xxxxx --to <sender> - <<'EOF'
                 ...
                 EOF
wrkc never reads stdin unless you pass "-".

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
--note records addressed mail that is never presented: it never wakes, injects,
steers, or births anyone, and is written terminal. The addressee reads it with
wrkc notes. Use it for information the recipient doesn't need to see now. It
needs --to and refuses --fyi, --preempt, --wait and --ttl.
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
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			fromStdin, err := wrkcSayBodySource(args, cmd.Flags().Changed("message"), message)
			if err != nil {
				return &clifunnel.UsageError{Err: err, Cmd: cmd}
			}
			ref, bodyArg := args[0], message
			if fromStdin {
				if wrkcStdinCannotCarryABody(cmd.InOrStdin()) {
					return &clifunnel.UsageError{Err: errors.New("body comes from stdin: use a heredoc (<<'EOF') or -m"), Cmd: cmd}
				}
				bodyArg = "-"
			}
			body, err := readTextValue(bodyArg, "body", cmd.InOrStdin(), &stdinClaims{})
			if err != nil {
				return err
			}
			if strings.TrimSpace(body) == "" {
				return errors.New("say requires a non-empty body")
			}
			if note {
				if len(to) == 0 {
					return &clifunnel.UsageError{Err: errors.New("--note requires --to; a say without --to is already a log entry"), Cmd: cmd}
				}
				for _, flag := range []string{"fyi", "preempt", "wait", "ttl"} {
					if cmd.Flags().Changed(flag) {
						return &clifunnel.UsageError{Err: fmt.Errorf("--note cannot be combined with --%s: a note is never presented", flag), Cmd: cmd}
					}
				}
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
			for key, set := range map[string]bool{"fyi": fyi, "note": note, "new": newRoom, "record": record} {
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
	cmd.Flags().BoolVar(&note, "note", false, "Record addressed mail that is never presented: never wakes, injects, or births anyone; the addressee reads it with wrkc notes. A reply with --note still acks, so it closes a thread without waking the other side")
	cmd.Flags().StringVarP(&message, "message", "m", "", "Body: a literal, or @file (for stdin pass a positional -)")
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

// wrkcSayBodySource checks say's one positional (the ref) and its explicit body
// source, and reports whether the body comes from stdin. Every refusal here
// happens before anything reads: under an agent harness stdin is an open
// socket that never sends EOF, so an implicit read would park forever.
func wrkcSayBodySource(args []string, messageSet bool, message string) (fromStdin bool, err error) {
	const sources = "-m <body> (a literal or @file) or - with the body on stdin (- <<'EOF')"
	if len(args) == 0 || args[0] == "-" {
		return false, errors.New("say requires a room ref (R-/EN-/T-/container/agent@project) as its one positional")
	}
	// No ref contains whitespace; a quoted body in the ref slot gets the
	// better message.
	if strings.ContainsFunc(args[0], unicode.IsSpace) {
		return false, fmt.Errorf("%q is not a room ref (refs never contain whitespace); pass the body with %s", truncateWrkcRef(args[0]), sources)
	}
	if messageSet && message == "-" {
		return false, errors.New("-m takes a literal or @file; for stdin pass the positional - instead (- <<'EOF')")
	}
	switch {
	case len(args) > 2 || (len(args) == 2 && args[1] != "-"):
		return false, fmt.Errorf("say takes one positional, the ref; pass the body with %s", sources)
	case len(args) == 2 && messageSet:
		return false, errors.New("say takes the body from -m or from -, not both")
	case len(args) == 2:
		return true, nil
	case !messageSet:
		return false, fmt.Errorf("say needs a body source: %s", sources)
	}
	return false, nil
}

// wrkcStdinCannotCarryABody reports a stdin that a heredoc or pipe never
// arrives as: a terminal, or a socket (an agent harness's stdin, which never
// sends EOF).
func wrkcStdinCannotCarryABody(stdin io.Reader) bool {
	if isReaderTTY(stdin) {
		return true
	}
	f, ok := stdin.(*os.File)
	if !ok {
		return false
	}
	info, err := f.Stat()
	return err == nil && info.Mode()&os.ModeSocket != 0
}

func truncateWrkcRef(ref string) string {
	if r := []rune(ref); len(r) > 40 {
		return string(r[:40]) + "…"
	}
	return ref
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
