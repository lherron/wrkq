package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

// wrkc is the collaboration CLI of the wrkq collaboration ledger: rooms and
// envelopes, the durable side of agent↔agent talk. It shares wrkq's transport,
// principal resolution, output modes, and idempotency conventions, and it has NO
// HRC dependency — every verb here works with every HRC daemon down, which is
// the whole point of moving the objects to their owner (T-07612 §2).
//
// The one piece of HRC vocabulary wrkc touches is the caller's own session
// handle, read from HRC_SESSION_REF and forwarded verbatim as `scopeRef`. wrkq
// parses it as a scope handle and knows nothing else about it.

const wrkcSessionEnv = "HRC_SESSION_REF"

// ─── wire DTOs ────────────────────────────────────────────────────────────────

type roomWire struct {
	UUID                 string         `json:"uuid"`
	ID                   *string        `json:"id,omitempty"`
	Key                  string         `json:"key"`
	Kind                 string         `json:"kind"`
	Subject              *string        `json:"subject,omitempty"`
	State                string         `json:"state"`
	StoredState          string         `json:"storedState"`
	WorkRef              *roomWorkRef   `json:"workRef"`
	Links                []roomLinkWire `json:"links"`
	OpenedByPrincipalRef string         `json:"openedByPrincipalRef"`
	OpenedAt             string         `json:"openedAt"`
	ClosedAt             *string        `json:"closedAt,omitempty"`
	LastActivityAt       string         `json:"lastActivityAt"`
	MemberCount          int            `json:"memberCount"`
	MessageCount         int            `json:"messageCount"`
	ETag                 int64          `json:"etag"`
	CreatedAt            string         `json:"createdAt"`
	UpdatedAt            string         `json:"updatedAt"`
}

type roomWorkRef struct {
	Type string `json:"type"`
	UUID string `json:"uuid"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type roomLinkWire struct {
	Relation string `json:"relation"`
	Key      string `json:"key"`
	UUID     string `json:"uuid"`
	Kind     string `json:"kind"`
}

type envelopePartyWire struct {
	PrincipalRef string  `json:"principalRef"`
	ScopeRef     *string `json:"scopeRef,omitempty"`
}

type envelopePresentationWire struct {
	MemberRef      string  `json:"memberRef"`
	Node           *string `json:"node,omitempty"`
	RuntimeID      *string `json:"runtimeId,omitempty"`
	HostSessionID  *string `json:"hostSessionId,omitempty"`
	Generation     *string `json:"generation,omitempty"`
	RunID          *string `json:"runId,omitempty"`
	DriveAttemptID *string `json:"driveAttemptId,omitempty"`
	PresentedAt    string  `json:"presentedAt"`
}

type envelopeWire struct {
	UUID                  string                     `json:"uuid"`
	ID                    string                     `json:"id"`
	RoomUUID              string                     `json:"roomUuid"`
	RoomKey               string                     `json:"roomKey"`
	RoomKind              string                     `json:"roomKind"`
	GroupID               *string                    `json:"groupId,omitempty"`
	From                  envelopePartyWire          `json:"from"`
	To                    *envelopePartyWire         `json:"to"`
	ReplyTo               string                     `json:"replyTo"`
	Obligation            string                     `json:"obligation"`
	Body                  string                     `json:"body"`
	TaskID                *string                    `json:"taskId,omitempty"`
	State                 string                     `json:"state"`
	Terminal              bool                       `json:"terminal"`
	RoundCount            int64                      `json:"roundCount"`
	RetryAt               *string                    `json:"retryAt,omitempty"`
	DeferReason           *string                    `json:"deferReason,omitempty"`
	TerminalActor         *string                    `json:"terminalActor,omitempty"`
	Urgent                bool                       `json:"urgent"`
	MaterializationIntent *string                    `json:"materializationIntent,omitempty"`
	RespondToPrincipalRef *string                    `json:"respondToPrincipalRef,omitempty"`
	RetryPromiseID        *string                    `json:"retryPromiseId,omitempty"`
	IdempotencyKey        *string                    `json:"idempotencyKey,omitempty"`
	Meta                  map[string]any             `json:"meta"`
	PresentedTo           []envelopePresentationWire `json:"presentedTo"`
	ETag                  int64                      `json:"etag"`
	CreatedAt             string                     `json:"createdAt"`
	UpdatedAt             string                     `json:"updatedAt"`
}

type roomMemberWire struct {
	MemberRef          string                    `json:"memberRef"`
	MemberPrincipalRef string                    `json:"memberPrincipalRef"`
	Scoped             bool                      `json:"scoped"`
	Source             string                    `json:"source"`
	JoinedAt           string                    `json:"joinedAt"`
	LeftAt             *string                   `json:"leftAt,omitempty"`
	Attendance         *envelopePresentationWire `json:"attendance"`
}

type roomSayResultWire struct {
	Room              roomWire       `json:"room"`
	GroupID           string         `json:"groupId"`
	Envelopes         []envelopeWire `json:"envelopes"`
	Acked             []string       `json:"acked"`
	RecordedCommentID *string        `json:"recordedCommentId,omitempty"`
}

type roomLogViewWire struct {
	Room  roomWire       `json:"room"`
	Items []envelopeWire `json:"items"`
}

type roomMembersViewWire struct {
	Room  roomWire         `json:"room"`
	Items []roomMemberWire `json:"items"`
}

type envelopeInboxGroupWire struct {
	Room  roomWire       `json:"room"`
	Items []envelopeWire `json:"items"`
}

type envelopeInboxViewWire struct {
	ScopeRef     *string                  `json:"scopeRef,omitempty"`
	PrincipalRef string                   `json:"principalRef"`
	Groups       []envelopeInboxGroupWire `json:"groups"`
	Deferred     []envelopeWire           `json:"deferred"`
	Dead         []envelopeWire           `json:"dead"`
}

// ─── root ─────────────────────────────────────────────────────────────────────

// NewWrkcRootCmd builds the wrkc cobra tree. It mirrors wrkq's persistent flags
// so --db/--as/--output behave identically across both binaries.
func NewWrkcRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "wrkc",
		Short: "Durable agent collaboration: rooms and envelopes",
		Long: `wrkc is the collaboration surface of the wrkq ledger.

A room is a durable conversation keyed by a work identity — a campaign, a task,
a project, or an ad-hoc pair. An envelope is one message in a room, addressed to
exactly one recipient. Talk survives every runtime that carried it, so context
is PULLED from the room rather than remembered by a session.

Two rules worth knowing before you use it:
  · Only --to fires. A say without --to is a log entry; nobody is presented.
  · Rooms are talk; comments are record. --record is the only bridge.

wrkc has no HRC dependency: every verb works with every HRC daemon down.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("db", "", "Path to database file (overrides WRKQ_DB_PATH)")
	root.PersistentFlags().String("principal-ref", "", "Caller principal for write attribution: agent:<id> or full agent ScopeRef")
	root.PersistentFlags().String("as", "", "Alias for --principal-ref; accepts agent:<id> or a full agent ScopeRef")
	root.PersistentFlags().String("project", "", "Project to operate under (overrides WRKQ_PROJECT_ROOT)")
	root.PersistentFlags().String("output", "", "Output mode: table, human, json, ndjson, porcelain, yaml, tsv, raw")
	root.PersistentFlags().String("scope-ref", "", "Caller scope handle (defaults to $HRC_SESSION_REF)")

	root.AddCommand(newWrkcSayCmd())
	root.AddCommand(newWrkcOpenCmd())
	root.AddCommand(newWrkcLogCmd())
	root.AddCommand(newWrkcShowCmd())
	root.AddCommand(newWrkcLsCmd())
	root.AddCommand(newWrkcInboxCmd())
	root.AddCommand(newWrkcDeferCmd())
	root.AddCommand(newWrkcLifecycleCmd("close"))
	root.AddCommand(newWrkcLifecycleCmd("reopen"))
	root.AddCommand(newWrkcJoinCmd())
	root.AddCommand(newWrkcLeaveCmd())
	root.AddCommand(newWrkcInviteCmd())
	root.AddCommand(newWrkcMembersCmd())
	root.AddCommand(newWrkcAckCmd())
	root.AddCommand(newWrkcInfoCmd())
	root.AddCommand(newVersionCmd())
	applyHelpTemplates(root)
	return root
}

// ExecuteWrkc runs the wrkc CLI.
func ExecuteWrkc() error {
	return NewWrkcRootCmd().Execute()
}

// wrkcScopeRef resolves the caller's own scope handle: the --scope-ref flag when
// given, otherwise HRC_SESSION_REF. An empty result is legitimate — a scope-less
// principal (a human) has no scope and is never kicked or summoned.
func wrkcScopeRef(cmd *cobra.Command) string {
	if flag := cmd.Flags().Lookup("scope-ref"); flag != nil {
		if value := strings.TrimSpace(flag.Value.String()); value != "" {
			return value
		}
	}
	return strings.TrimSpace(os.Getenv(wrkcSessionEnv))
}

// wrkcParams seeds the common principal + scope fields on every call.
func wrkcParams(cmd *cobra.Command) (map[string]any, error) {
	params := map[string]any{}
	principal, err := actorFlag(cmd)
	if err != nil {
		return nil, err
	}
	if principal != "" {
		params["principalRef"] = principal
	}
	if scopeRef := wrkcScopeRef(cmd); scopeRef != "" {
		params["scopeRef"] = scopeRef
	}
	return params, nil
}

// ─── say ──────────────────────────────────────────────────────────────────────

func newWrkcSayCmd() *cobra.Command {
	var to []string
	var fyi, newRoom, urgent, record bool
	var subject, respondTo, idempotencyKey, timeout string
	var wait bool
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "say <ref> [body|-]",
		Short: "Say something in a room",
		Long: `Say something in the room the ref routes to.

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
recipient's reply, defer, or dead never disposes another's obligation.

Saying with --to also ACKS your own standing obligations in this room from the
same counterparty: for an agent, the reply IS the ack. To hold one back, defer
it first.

A bare name in --to resolves, in order: the seat that is WAITING on you — the
sender of your most recently presented obligation in this room with that name —
then the room's single member with that name, then the room's own shape (task
room -> agent@project:T-xxx, campaign/project room -> agent@project:primary).
Two members of that name and no obligation refuses and names them: reply to a
seat that never asked and its obligation dead-letters unanswered. An envelope's
replyTo is the exact token that answers it; a full handle always wins. Use
agent:<id> to address a scope-less principal such as a human.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			claims := &stdinClaims{}
			body := ""
			if len(args) > 1 {
				value, err := readTextValue(args[1], "body", cmd.InOrStdin(), claims)
				if err != nil {
					return err
				}
				body = value
			} else {
				value, err := readTextValue("-", "body", cmd.InOrStdin(), claims)
				if err != nil {
					return err
				}
				body = value
			}
			if strings.TrimSpace(body) == "" {
				return errors.New("say requires a body (literal, @file, or - for stdin)")
			}
			if wait && len(to) == 0 {
				return errors.New("--wait requires --to")
			}

			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			params["ref"] = args[0]
			params["body"] = body
			if len(to) > 0 {
				params["to"] = to
			}
			if fyi {
				params["fyi"] = true
			}
			if subject != "" {
				params["subject"] = subject
			}
			if newRoom {
				params["new"] = true
			}
			if urgent {
				params["urgent"] = true
			}
			if record {
				params["record"] = true
			}
			if respondTo != "" {
				params["respondTo"] = respondTo
			}
			if idempotencyKey != "" {
				params["idempotencyKey"] = idempotencyKey
			}

			raw, err := tr.Call(cmd.Context(), "wrkq.room.say", params)
			if err != nil {
				return err
			}
			var result roomSayResultWire
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			if wait {
				return wrkcWaitForGroup(cmd, tr, result, timeout, output)
			}
			return renderWrkcSayResult(cmd, result, output)
		},
	}
	cmd.Flags().StringSliceVar(&to, "to", nil, "Addressees (repeatable or comma-separated); fans out one envelope each")
	cmd.Flags().BoolVar(&fyi, "fyi", false, "No reply obligation; presented into a live generation or on the next attend, never summons")
	cmd.Flags().StringVar(&subject, "subject", "", "Subject for a new ad-hoc room")
	cmd.Flags().BoolVar(&newRoom, "new", false, "Force a fresh ad-hoc room instead of reusing the open pair room")
	cmd.Flags().BoolVar(&wait, "wait", false, "Block until every envelope in the group is terminal, then print each reply")
	cmd.Flags().StringVar(&timeout, "timeout", "", "Maximum --wait duration (e.g. 10m)")
	cmd.Flags().BoolVar(&urgent, "urgent", false, "Delivery intent stored for HRC to actuate; wrkq does not know what steer is")
	cmd.Flags().StringVar(&respondTo, "respond-to", "", "Principal the reply should be addressed to")
	cmd.Flags().BoolVar(&record, "record", false, "Also write the body as a wrkq comment on the room's task")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key for this say; carried by every envelope of the fan-out")
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

// wrkcWaitForGroup blocks on the whole fan-out group and then prints the
// replies. It is `wrkq monitor wait <group> --until terminal` in-process: the
// same server-owned condition snapshot, the same client-owned poll loop.
func wrkcWaitForGroup(cmd *cobra.Command, tr Transport, result roomSayResultWire, timeout string, output promiseOutputFlags) error {
	if result.GroupID == "" {
		return errors.New("say returned no group to wait on")
	}
	duration, err := parseMonitorDuration(timeout, 0)
	if err != nil {
		return err
	}
	terminal, unmet, _, err := monitorWaitLoop(cmd.Context(), tr, monitorStreamOpts{
		scopedTasks: []string{result.GroupID},
		condition:   "terminal",
		timeout:     duration,
	})
	if err != nil {
		return err
	}
	if terminal != monitorResultMet {
		fmt.Fprintf(cmd.ErrOrStderr(), "wrkc say --wait ended %s; still open: %s\n",
			terminal, strings.Join(unmet, ", "))
	}

	replies, err := wrkcCollectReplies(cmd, tr, result)
	if err != nil {
		return err
	}
	if len(replies) == 0 {
		return renderWrkcSayResult(cmd, result, output)
	}
	return renderWrkcEnvelopes(cmd, replies, output, false)
}

// wrkcCollectReplies reads back the room and returns the envelopes each
// addressee sent to the sender after this say. wrkq keeps no reply pointer: a
// reply is an envelope, and typed replies are cut by design.
func wrkcCollectReplies(cmd *cobra.Command, tr Transport, result roomSayResultWire) ([]envelopeWire, error) {
	params, err := wrkcParams(cmd)
	if err != nil {
		return nil, err
	}
	params["room"] = result.Room.Key
	raw, err := tr.Call(cmd.Context(), "wrkq.room.logView", params)
	if err != nil {
		return nil, err
	}
	var view roomLogViewWire
	if err := json.Unmarshal(raw, &view); err != nil {
		return nil, err
	}

	sent := map[string]bool{}
	counterparties := map[string]bool{}
	sender := ""
	last := ""
	for _, envelope := range result.Envelopes {
		sent[envelope.ID] = true
		sender = envelope.From.PrincipalRef
		if envelope.To != nil {
			counterparties[envelope.To.PrincipalRef] = true
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
		if !counterparties[envelope.From.PrincipalRef] {
			continue
		}
		if envelope.To == nil || envelope.To.PrincipalRef != sender {
			continue
		}
		replies = append(replies, envelope)
	}
	return replies, nil
}

// ─── room verbs ───────────────────────────────────────────────────────────────

func newWrkcOpenCmd() *cobra.Command {
	var subject, task string
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "open <scope>...",
		Short: "Open an explicit ad-hoc or group room",
		Long: `Open an ad-hoc room with named members and a subject.

Use this when the conversation has no work identity to key on. Talk that belongs
to a task, campaign, or project should just say into that work instead — an
ad-hoc room auto-archives after 24h idle, and work rooms do not.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(subject) == "" {
				return errors.New("open requires --subject")
			}
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			params["members"] = args
			params["subject"] = subject
			if task != "" {
				params["task"] = task
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.room.open", params)
			if err != nil {
				return err
			}
			return renderWrkcRoomSingleton(cmd, raw, output)
		},
	}
	cmd.Flags().StringVarP(&subject, "subject", "s", "", "Room subject (required)")
	cmd.Flags().StringVar(&task, "task", "", "Task this room is about")
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func newWrkcLogCmd() *cobra.Command {
	var task string
	var limit int
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "log <room>",
		Short: "Read a room's history",
		Long: `Read a room's history, oldest first.

This is the pull the injected "history:" cue asks for. Room history is NEVER
injected: a message you do not recognize means you have not read the room yet.

The room selector is the room key: T-xxxxx, a container id or path, or R-xxxxx.
In a campaign room, --task narrows to the traffic that came through one task.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			params["room"] = args[0]
			if task != "" {
				params["task"] = task
			}
			if limit > 0 {
				params["limit"] = limit
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.room.logView", params)
			if err != nil {
				return err
			}
			var view roomLogViewWire
			if err := json.Unmarshal(raw, &view); err != nil {
				return err
			}
			mode, stable, err := resolvePromiseOutputMode(cmd, output, true)
			if err != nil {
				return err
			}
			if mode == "human" || mode == "table" {
				return renderWrkcTranscript(cmd, view)
			}
			return renderWrkcEnvelopesMode(cmd, view.Items, mode, stable, false)
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "Narrow a campaign room to one task's traffic")
	cmd.Flags().IntVar(&limit, "limit", 0, "Return only the newest N messages (still oldest-first)")
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newWrkcShowCmd() *cobra.Command {
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "show <EN-xxxxx|room>",
		Short: "Show one envelope or room",
		Long: `Show one envelope (EN-xxxxx) or one room.

EN- ids are internal: inbox, show, and log surface them so an agent can tell
"already handled" from "new", but the injected presentation never carries one.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			selector := strings.TrimSpace(args[0])
			if isEnvelopeSelector(selector) {
				params["envelope"] = selector
				raw, cerr := tr.Call(cmd.Context(), "wrkq.envelope.show", params)
				if cerr != nil {
					return cerr
				}
				var envelope envelopeWire
				if err := json.Unmarshal(raw, &envelope); err != nil {
					return err
				}
				mode, stable, merr := resolvePromiseOutputMode(cmd, output, false)
				if merr != nil {
					return merr
				}
				if mode == "human" || mode == "table" {
					return renderWrkcEnvelopeDetail(cmd, envelope)
				}
				return renderWrkcEnvelopesMode(cmd, []envelopeWire{envelope}, mode, stable, true)
			}
			params["room"] = selector
			raw, err := tr.Call(cmd.Context(), "wrkq.room.show", params)
			if err != nil {
				return err
			}
			return renderWrkcRoomSingleton(cmd, raw, output)
		},
	}
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func newWrkcLsCmd() *cobra.Command {
	var openOnly, dead bool
	var kind, scopeFilter string
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List rooms, or dead mail",
		Long: `List rooms. Rooms are readable by any principal: membership is identity and
attendance, never an ACL, so --scope me is a convenience filter and not a
permission boundary.

--dead lists dead-lettered envelopes addressed to you instead of rooms. An
envelope dead-letters when kicker-driven redelivery is exhausted without a reply
or a defer; it is visible, never silent, and an operator ack clears it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			if dead {
				params["includeDead"] = true
				raw, cerr := tr.Call(cmd.Context(), "wrkq.envelope.inboxView", params)
				if cerr != nil {
					return cerr
				}
				var view envelopeInboxViewWire
				if err := json.Unmarshal(raw, &view); err != nil {
					return err
				}
				return renderWrkcEnvelopes(cmd, view.Dead, output, false)
			}
			if openOnly {
				params["state"] = "open"
			} else {
				params["state"] = "all"
			}
			if kind != "" {
				params["kind"] = kind
			}
			if scopeFilter != "" {
				if scopeFilter != "me" {
					return fmt.Errorf("--scope accepts only \"me\"; rooms are readable by any principal, so this is a filter and not a permission boundary")
				}
				params["scope"] = scopeFilter
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.room.list", params)
			if err != nil {
				return err
			}
			var result struct {
				Items []roomWire `json:"items"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			return renderWrkcRooms(cmd, result.Items, output)
		},
	}
	cmd.Flags().BoolVar(&openOnly, "open", false, "Only open rooms")
	cmd.Flags().BoolVar(&dead, "dead", false, "List dead-lettered envelopes addressed to you")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by room kind: campaign, task, project, adhoc")
	// A value, not a boolean: `--scope me` is the §9.1 surface. No NoOptDefVal —
	// that would make cobra read the value as a positional and reject it.
	cmd.Flags().StringVar(&scopeFilter, "scope", "", "Restrict to rooms your own scope is a member of (only value: me)")
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newWrkcInboxCmd() *cobra.Command {
	var includeDead bool
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "inbox",
		Short: "Reply-required envelopes addressed to your scope",
		Long: `List the obligations standing against your scope, grouped by room.

fyi is never listed: it carries no obligation and is acked at its own
presentation. Deferred envelopes appear under their own heading with the time
they come back. EN- ids are shown so you can tell an at-least-once
re-presentation from something new.

An obligation whose room has since closed is still yours, but it gates nothing:
a closed room refuses a say, so there is no reply to make and the stop hook lets
the turn end. Those appear under a "closed room" heading — reopen the room to
reply, or have an operator ack it.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			if includeDead {
				params["includeDead"] = true
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.envelope.inboxView", params)
			if err != nil {
				return err
			}
			var view envelopeInboxViewWire
			if err := json.Unmarshal(raw, &view); err != nil {
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
				return render.NewRenderer(cmd.OutOrStdout(), render.Options{Porcelain: stable}).RenderJSON(view)
			}
			flat := []envelopeWire{}
			for _, group := range view.Groups {
				flat = append(flat, group.Items...)
			}
			flat = append(flat, view.Deferred...)
			flat = append(flat, view.Dead...)
			return renderWrkcEnvelopesMode(cmd, flat, mode, stable, false)
		},
	}
	cmd.Flags().BoolVar(&includeDead, "dead", false, "Also list dead-lettered envelopes")
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
		Long: `Pause one obligation. Deferred is PAUSED, never terminal: a later reply still
acks it, and the sender keeps visibility rather than a silent drop.

--retry-after arms a wrkq promise; when that time arrives the envelope returns to
pending and the kicker re-drives it. Deferring without a retry time is legal —
the protection for the sender is visibility, not a timer.

Defer is also how you exclude ONE obligation from a reply: saying --to acks
every presented obligation from that counterparty, so defer the one you are not
answering first.`,
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
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			params["envelope"] = args[0]
			params["reason"] = value
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
			raw, err := tr.Call(cmd.Context(), "wrkq.envelope.defer", params)
			if err != nil {
				return err
			}
			var envelope envelopeWire
			if err := json.Unmarshal(raw, &envelope); err != nil {
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

func newWrkcLifecycleCmd(verb string) *cobra.Command {
	var ifMatch, etag int64
	var output promiseOutputFlags
	short := "Close a room to further talk"
	long := `Close a room. Closed rooms refuse a say with a typed error naming the state.

A derived room also READS as closed once its work is terminal — a task room whose
task completed, a campaign room whose campaign closed — without a stored
transition. The signal already rides the same monitor selector as the
conversation, because a task room's key IS the task id.`
	method := "wrkq.room.close"
	if verb == "reopen" {
		short = "Reopen a closed room"
		long = `Reopen a room. This is the explicit inverse of close, and it also clears a
DERIVED closure: a task room outlives its task's terminal transition once you
have deliberately reopened it.`
		method = "wrkq.room.reopen"
	}
	cmd := &cobra.Command{
		Use:   verb + " <room>",
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			params["room"] = args[0]
			match, err := promiseIfMatch(cmd, ifMatch, etag)
			if err != nil {
				return err
			}
			if match > 0 {
				params["ifMatch"] = match
			}
			raw, err := tr.Call(cmd.Context(), method, params)
			if err != nil {
				return err
			}
			return renderWrkcRoomSingleton(cmd, raw, output)
		},
	}
	addPromiseETagFlags(cmd, &ifMatch, &etag)
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func newWrkcJoinCmd() *cobra.Command {
	return newWrkcMembershipCmd("join", "wrkq.room.join",
		"Join a room",
		`Join a room so you appear in its member list and attendance.

Membership is identity and attendance, not delivery: nothing fires from it. Only
--to fires, so joining a room does NOT start sending you its traffic.`)
}

func newWrkcLeaveCmd() *cobra.Command {
	return newWrkcMembershipCmd("leave", "wrkq.room.leave",
		"Leave a room",
		`Leave a room. Leaving is not a delete: your attendance record stays readable,
and any obligation already addressed to you stays yours.`)
}

func newWrkcMembershipCmd(verb, method, short, long string) *cobra.Command {
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   verb + " <room>",
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWrkcMembership(cmd, method, args[0], "", output)
		},
	}
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newWrkcInviteCmd() *cobra.Command {
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "invite <room> <scope>",
		Short: "Invite a scope into a room",
		Long: `Invite a scope into a room.

This is how a pair room deliberately grows: a third member makes it a group room,
and the next unsolicited pair say opens a fresh pair room rather than joining the
conversation you widened on purpose.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWrkcMembership(cmd, "wrkq.room.join", args[0], args[1], output)
		},
	}
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func runWrkcMembership(cmd *cobra.Command, method, room, member string, output promiseOutputFlags) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	params, err := wrkcParams(cmd)
	if err != nil {
		return err
	}
	params["room"] = room
	if member != "" {
		params["member"] = member
	}
	raw, err := tr.Call(cmd.Context(), method, params)
	if err != nil {
		return err
	}
	var view roomMembersViewWire
	if err := json.Unmarshal(raw, &view); err != nil {
		return err
	}
	return renderWrkcMembers(cmd, view, output)
}

func newWrkcMembersCmd() *cobra.Command {
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "members <room>",
		Short: "List members, source, and attendance",
		Long: `List a room's members with how each got there and when they were last presented
anything in it.

Attendance is the latest presentation receipt per member, which is the only
durable answer to "did that agent actually see this". Scope-less members
(humans) have no attendance: they are never presented through a runtime.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			params["room"] = args[0]
			raw, err := tr.Call(cmd.Context(), "wrkq.room.membersView", params)
			if err != nil {
				return err
			}
			var view roomMembersViewWire
			if err := json.Unmarshal(raw, &view); err != nil {
				return err
			}
			return renderWrkcMembers(cmd, view, output)
		},
	}
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newWrkcAckCmd() *cobra.Command {
	var note string
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "ack <EN-xxxxx>...",
		Short: "Operator-only: clear envelopes without replying",
		Long: `Clear envelopes without replying. This is an OPERATOR verb, intended for a human
principal (wrkc ack EN-00042 --as agent:lance) clearing dead mail.

Agents do not ack: for an agent the reply IS the ack. If you are an agent and you
want to put something down, defer it with a reason.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, _, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params, err := wrkcParams(cmd)
			if err != nil {
				return err
			}
			params["envelopes"] = args
			if note != "" {
				params["note"] = note
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.envelope.ack", params)
			if err != nil {
				return err
			}
			var view roomLogViewWire
			if err := json.Unmarshal(raw, &view); err != nil {
				return err
			}
			return renderWrkcEnvelopes(cmd, view.Items, output, false)
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Why this was cleared")
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newWrkcInfoCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:     "info",
		Aliases: []string{"usage"},
		Short:   "Display wrkc usage documentation",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return renderEmbeddedUsage(cmd, wrkcUsageContent, asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	return cmd
}

func isEnvelopeSelector(selector string) bool {
	return strings.HasPrefix(strings.ToUpper(selector), "EN-")
}
