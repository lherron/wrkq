package rpccli

import (
	"context"
	"encoding/json"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/clifunnel"
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
//
// This file holds the root command, the caller's scope/principal params and
// the wrkcClient every verb calls through. The verbs live in wrkc_say.go,
// wrkc_room.go and wrkc_envelope.go; the wire DTOs in wrkc_wire.go.

const wrkcSessionEnv = "HRC_SESSION_REF"

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

Three rules worth knowing before you use it:
  · Only --to fires. A say without --to is a log entry; nobody is presented.
  · Rooms are talk; comments are record. --record is the only bridge.
  · A say is never refused for what a room IS. There is no close and no reopen:
    a room you can resolve always accepts talk, and a stale one only says so.

wrkc has no HRC dependency: every verb works with every HRC daemon down.`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("db", "", "Database path or rpc:// locator (overrides WRKQ_DB and WRKQ_DB_PATH)")
	root.PersistentFlags().String("principal-ref", "", "Caller principal for write attribution: agent:<id> or full agent ScopeRef")
	root.PersistentFlags().String("as", "", "Alias for --principal-ref; accepts agent:<id> or a full agent ScopeRef")
	root.PersistentFlags().String("project", "", "Project to operate under (overrides WRKQ_PROJECT_ROOT)")
	root.PersistentFlags().String("output", "", "Output mode: table, human, json, ndjson, porcelain, yaml, tsv, raw")
	root.PersistentFlags().String("scope-ref", "", "Caller scope handle (defaults to $HRC_SESSION_REF)")

	root.AddCommand(newWrkcSayCmd())
	root.AddCommand(newWrkcLogCmd())
	root.AddCommand(newWrkcShowCmd())
	root.AddCommand(newWrkcNounShowCmd("envelope", "EN-xxxxx"))
	root.AddCommand(newWrkcNounShowCmd("room", "room"))
	root.AddCommand(newWrkcLsCmd())
	root.AddCommand(newWrkcInboxCmd())
	root.AddCommand(newWrkcDeferCmd())
	root.AddCommand(newWrkcWithdrawCmd())
	root.AddCommand(newWrkcVisibilityCmd("hide"))
	root.AddCommand(newWrkcVisibilityCmd("unhide"))
	root.AddCommand(newWrkcJoinCmd())
	root.AddCommand(newWrkcLeaveCmd())
	root.AddCommand(newWrkcInviteCmd())
	root.AddCommand(newWrkcMembersCmd())
	root.AddCommand(newWrkcAckCmd())
	root.AddCommand(newWrkcInfoCmd())
	root.AddCommand(newVersionCmd())
	applyWrkcHelpTemplates(root)
	return root
}

// ExecuteWrkc runs the wrkc CLI.
func ExecuteWrkc() error {
	return clifunnel.Execute(context.Background(), NewWrkcRootCmd(), os.Args[1:],
		clifunnel.Options{NotFoundHint: wrkcNotFoundHint, OutputModes: wrkqOutputModes})
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

// wrkcClient is one wrkc verb's open transport bound to the caller's
// principal and scope.
type wrkcClient struct {
	cmd    *cobra.Command
	tr     Transport
	common map[string]any
}

// openWrkcClient opens the transport and resolves the caller params every
// wrkc call carries. The returned func closes the transport.
func openWrkcClient(cmd *cobra.Command) (wrkcClient, func(), error) {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return wrkcClient{}, nil, err
	}
	common, err := wrkcParams(cmd)
	if err != nil {
		closeFn()
		return wrkcClient{}, nil, err
	}
	return wrkcClient{cmd: cmd, tr: tr, common: common}, closeFn, nil
}

// call sends method with the caller params plus args and, when out is non-nil,
// decodes the result into it. The raw result is returned for renderers that
// re-emit the wire shape.
func (c wrkcClient) call(method string, args map[string]any, out any) (json.RawMessage, error) {
	params := make(map[string]any, len(c.common)+len(args))
	for k, v := range c.common {
		params[k] = v
	}
	for k, v := range args {
		params[k] = v
	}
	raw, err := c.tr.Call(c.cmd.Context(), method, params)
	if err != nil {
		return nil, err
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// roomLog reads one room's full log view.
func (c wrkcClient) roomLog(room string) (roomLogViewWire, error) {
	var view roomLogViewWire
	_, err := c.call("wrkq.room.logView", map[string]any{"room": room}, &view)
	return view, err
}
