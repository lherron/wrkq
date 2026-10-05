package rpccli

// wrkc_room.go — the room verbs: read a room (log, show, ls), label it
// (hide/unhide), and change or read its membership (join, leave, invite,
// members).

import (
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

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
In a campaign room, --task narrows to the traffic that came through one task.

--json returns {"room": {...}, "items": [envelope, ...]}; --ndjson emits one
envelope per line.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{"room": args[0]}
			if task != "" {
				params["task"] = task
			}
			if limit > 0 {
				params["limit"] = limit
			}
			var view roomLogViewWire
			if _, err := c.call("wrkq.room.logView", params, &view); err != nil {
				return err
			}
			mode, stable, err := resolvePromiseOutputMode(cmd, output, true)
			if err != nil {
				return err
			}
			if mode == "human" || mode == "table" {
				identity, err := loadWrkcAdhocIdentity(cmd.Context(), c.tr, c.common, view.Room, false)
				if err != nil {
					return err
				}
				return renderWrkcTranscript(cmd, view, identity)
			}
			// JSON/YAML keep the {room, items} view, like members and the RPC
			// wire; only the line-oriented modes flatten to one envelope each.
			if mode == "json" || mode == "yaml" {
				renderer := render.NewRenderer(cmd.OutOrStdout(), render.Options{Porcelain: stable})
				if mode == "yaml" {
					return renderer.RenderYAML(view)
				}
				return renderer.RenderJSON(view)
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
			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			selector := strings.TrimSpace(args[0])
			if isEnvelopeSelector(selector) {
				var envelope envelopeWire
				if _, err := c.call("wrkq.envelope.show", map[string]any{"envelope": selector}, &envelope); err != nil {
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
			var room roomWire
			raw, err := c.call("wrkq.room.show", map[string]any{"room": selector}, &room)
			if err != nil {
				return err
			}
			mode, _, err := resolvePromiseOutputMode(cmd, output, false)
			if err != nil {
				return err
			}
			var identity *wrkcAdhocIdentity
			if mode == "human" || mode == "table" || mode == "tsv" {
				identity, err = loadWrkcAdhocIdentity(cmd.Context(), c.tr, c.common, room, false)
				if err != nil {
					return err
				}
			}
			return renderWrkcRoomSingleton(cmd, raw, output, identity)
		},
	}
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

// newWrkcNounShowCmd accepts the noun-first spelling agents reach for
// (`wrkc envelope show EN-…`, `wrkc room get R-…`) and routes it to show, which
// already dispatches on the selector shape. Hidden: show is the documented verb.
func newWrkcNounShowCmd(noun, selector string) *cobra.Command {
	cmd := &cobra.Command{
		Use:    noun,
		Short:  "Alias group: " + noun + " show <" + selector + "> is wrkc show",
		Hidden: true,
	}
	show := newWrkcShowCmd()
	show.Use = "show <" + selector + ">"
	show.Aliases = []string{"get", "inspect"}
	cmd.AddCommand(show)
	return cmd
}

func newWrkcLsCmd() *cobra.Command {
	var all, failed bool
	var kind, scopeFilter string
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List rooms, or failed mail",
		Long: `List rooms. Rooms are readable by any principal: membership is identity and
attendance, never an ACL, so --scope me is a convenience filter and not a
permission boundary.

This is DISCOVERY, not reachability. The default omits rooms whose activity is
stale (terminal work, quiet more than 4h) and rooms carrying the hidden label;
--all shows every room. Everything omitted is still fully addressable — say into
it by key and it writes — and its obligations still gate and wake.

--failed lists failed envelopes addressed to you instead of rooms. A failure is
terminal and carries its reason. Nothing clears it: ack refuses a failed
envelope, and the record stays readable through show and log.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			if failed {
				var view envelopeInboxViewWire
				if _, err := c.call("wrkq.envelope.inboxView", map[string]any{"includeFailed": true}, &view); err != nil {
					return err
				}
				return renderWrkcEnvelopes(cmd, view.Failed, output, false)
			}
			params := map[string]any{}
			if all {
				params["all"] = true
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
			var result struct {
				Items []roomWire `json:"items"`
			}
			if _, err := c.call("wrkq.room.list", params, &result); err != nil {
				return err
			}
			var inbox envelopeInboxViewWire
			if _, err := c.call("wrkq.envelope.inboxView", nil, &inbox); err != nil {
				return err
			}
			mode, _, err := resolvePromiseOutputMode(cmd, output, true)
			if err != nil {
				return err
			}
			var identities map[string]wrkcAdhocIdentity
			if mode == "human" || mode == "table" || mode == "tsv" {
				identities, err = loadWrkcAdhocIdentities(cmd.Context(), c.tr, c.common, result.Items)
				if err != nil {
					return err
				}
			}
			return renderWrkcRooms(cmd, result.Items, identities, len(inbox.SentFailed), output)
		},
	}
	cmd.Flags().BoolVar(&all, "all", false, "Include stale and hidden rooms")
	cmd.Flags().BoolVar(&failed, "failed", false, "List failed envelopes addressed to you")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by room kind: campaign, task, project, adhoc")
	// A value, not a boolean: `--scope me` is the §9.1 surface. No NoOptDefVal —
	// that would make cobra read the value as a positional and reject it.
	cmd.Flags().StringVar(&scopeFilter, "scope", "", "Restrict to rooms your own scope is a member of (only value: me)")
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newWrkcVisibilityCmd(verb string) *cobra.Command {
	var output promiseOutputFlags
	short := "Hide a room from the default listing"
	long := "Hide a room from the default `wrkc ls`.\n\n" + `This is a LABEL, not a state. A hidden room still accepts says, still delivers,
and its obligations still gate your turn and wake the kicker — it simply stops
appearing in a listing that has no --all. Any principal may set it: what a
listing shows is not an ownership boundary.

There is no close and no reopen. A room you can resolve always accepts talk.`
	method := "wrkq.room.hide"
	if verb == "unhide" {
		short = "Return a room to the default listing"
		long = "Clear the hidden label, returning the room to the default `wrkc ls`."
		method = "wrkq.room.unhide"
	}
	cmd := &cobra.Command{
		Use:   verb + " <room>",
		Short: short,
		Long:  long,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, closeFn, err := openWrkcClient(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			raw, err := c.call(method, map[string]any{"room": args[0]}, nil)
			if err != nil {
				return err
			}
			return renderWrkcRoomSingleton(cmd, raw, output, nil)
		},
	}
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func newWrkcJoinCmd() *cobra.Command {
	return newWrkcMembershipCmd("join", "wrkq.room.join",
		"Join a room so you appear in its members and attendance",
		`Join a room so you appear in its member list and attendance.

Membership is identity and attendance, not delivery: nothing fires from it. Only
--to fires, so joining a room does NOT start sending you its traffic.`)
}

func newWrkcLeaveCmd() *cobra.Command {
	return newWrkcMembershipCmd("leave", "wrkq.room.leave",
		"Leave a room, keeping your attendance and obligations",
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

// runWrkcMembership calls a membership method for one room (inviting member
// when non-empty) and renders the resulting member view.
func runWrkcMembership(cmd *cobra.Command, method, room, member string, output promiseOutputFlags) error {
	c, closeFn, err := openWrkcClient(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	params := map[string]any{"room": room}
	if member != "" {
		params["member"] = member
	}
	var view roomMembersViewWire
	if _, err := c.call(method, params, &view); err != nil {
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
			return runWrkcMembership(cmd, "wrkq.room.membersView", args[0], "", output)
		},
	}
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}
