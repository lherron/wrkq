package rpccli

// handoff_ack.go — `wrkq handoff acknowledge`: resolve the acting identity
// (--as, runtime scope, ASP env, or the handoff row itself as a last resort),
// check it against the row, and map acknowledge failures to the legacy exit
// codes.

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

func newHandoffAckCmd() *cobra.Command {
	var (
		note    string
		dryRun  bool
		ifMatch int64
		asJSON  bool
		human   bool
	)
	cmd := &cobra.Command{
		Use:   "acknowledge <handoff-id>",
		Short: "Acknowledge a handoff so it is no longer pending",
		Long: `Acknowledge a handoff so it no longer appears in default pending listings.

Acknowledgement is the only retirement mechanism - handoffs do not expire
automatically. Acknowledged handoffs are retained for history and search.

The actor is resolved from --as, agent runtime env, or, for an exact handoff ID,
the handoff row's agent/project as a last-resort sparse-shell fallback.

Use --note to attach a short acknowledgement note describing why the handoff
was consumed or is obsolete. Use --dry-run to inspect the mutation before
applying it.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHandoffAck(cmd, args[0], handoffAckFlags{
				note: note, dryRun: dryRun, ifMatch: ifMatch, asJSON: asJSON, human: human,
				noteChanged: cmd.Flags().Changed("note"),
			})
		},
	}
	cmd.Flags().StringVar(&note, "note", "", "Optional acknowledgement note describing why the handoff was consumed or is obsolete")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Inspect the mutation without applying it")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Reject the acknowledgement when the current etag does not equal this value")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Force JSON output")
	cmd.Flags().BoolVar(&human, "human", false, "Force human-readable output")
	return cmd
}

type handoffAckFlags struct {
	note          string
	dryRun        bool
	ifMatch       int64
	asJSON, human bool
	noteChanged   bool
}

const handoffAckExample = "wrkq handoff acknowledge H-00001 --note \"loaded next session\" --json"

type handoffAckOutput struct {
	Handoff handoffJSON `json:"handoff"`
	DryRun  bool        `json:"dry_run"`
}

func runHandoffAck(cmd *cobra.Command, arg string, f handoffAckFlags) error {
	stderr := cmd.ErrOrStderr()
	mode := handoffMode(cmd, f.asJSON, false, f.human, handoffOutputJSON)

	idOrUUID := strings.TrimSpace(arg)
	if idOrUUID == "" {
		return writeHandoffError(stderr, mode, 1, "validation_error", "", "handoff id is required", nil, handoffAckExample)
	}

	var note *string
	if f.noteChanged {
		noteValue, nerr := readTextValue(f.note, "--note", cmd.InOrStdin(), &stdinClaims{})
		if nerr != nil {
			return writeHandoffError(stderr, mode, 1, "validation_error", idOrUUID, nerr.Error(), nil, handoffAckExample)
		}
		trimmed := strings.TrimSpace(noteValue)
		if trimmed == "" {
			return writeHandoffError(stderr, mode, 1, "validation_error", idOrUUID, "--note cannot be empty", nil, handoffAckExample)
		}
		note = &trimmed
	}
	if f.ifMatch < 0 {
		return writeHandoffError(stderr, mode, 1, "validation_error", idOrUUID, "--if-match must be a non-negative etag value", nil, handoffAckExample)
	}

	tr, closeFn, err := openHandoffMirror(cmd)
	if err != nil {
		return writeHandoffError(stderr, mode, 1, "runtime_error", idOrUUID, err.Error(), nil, "")
	}
	defer closeFn()

	rowRaw, gerr := tr.Call(cmd.Context(), "wrkq.handoff.get", map[string]string{"handoff": idOrUUID})
	if gerr != nil {
		return classifyHandoffAckError(cmd, tr, stderr, mode, idOrUUID, gerr)
	}
	row, herr := handoffFromRPC(rowRaw)
	if herr != nil {
		return herr
	}
	identity, ierr := resolveHandoffAckIdentity(cmd, row)
	if ierr != nil {
		return writeHandoffError(stderr, mode, 1, "validation_error", idOrUUID, ierr.Error(), nil, handoffAckExample)
	}

	params := handoffAckParams(idOrUUID, identity.actorAgentID, identity.principalRef, identity.scopeRef, note, f.dryRun, f.ifMatch)

	raw, rerr := tr.Call(cmd.Context(), "wrkq.handoff.acknowledge", params)
	if rerr != nil {
		return classifyHandoffAckError(cmd, tr, stderr, mode, idOrUUID, rerr)
	}
	handoff, herr := handoffFromRPC(raw)
	if herr != nil {
		return herr
	}
	return writeHandoffAckOutput(cmd, mode, handoffAckOutput{Handoff: handoff, DryRun: f.dryRun})
}

type handoffAckIdentity struct {
	actorAgentID string
	principalRef string
	scopeRef     string
}

func resolveHandoffAckIdentity(cmd *cobra.Command, handoff handoffJSON) (handoffAckIdentity, error) {
	asFlag := changedStringFlag(cmd, "as")
	if asFlag != "" {
		principalRef, err := attribution.NormalizeCanonical(asFlag)
		if err != nil {
			return handoffAckIdentity{}, fmt.Errorf("invalid --as: %w", err)
		}
		actorAgentID := strings.TrimPrefix(principalRef, "agent:")
		return handoffAckIdentityFromCandidate("explicit --as", actorAgentID, principalRef, "", handoff)
	}

	if resolved, _, err := scope.Resolve(""); err == nil && strings.TrimSpace(resolved.AgentID) != "" {
		return handoffAckIdentityFromCandidate("runtime scope", resolved.AgentID, "agent:"+resolved.AgentID, resolved.FullRef(), handoff)
	}

	env := scope.ReadEnv()
	if strings.TrimSpace(env.ASPAgentID) != "" {
		scopeRef := ""
		if strings.TrimSpace(env.ASPProject) != "" {
			scopeRef = "agent:" + strings.TrimSpace(env.ASPAgentID) + ":project:" + strings.TrimSpace(env.ASPProject)
		}
		return handoffAckIdentityFromCandidate("runtime ASP_AGENT_ID", strings.TrimSpace(env.ASPAgentID), "agent:"+strings.TrimSpace(env.ASPAgentID), scopeRef, handoff)
	}

	if strings.TrimSpace(handoff.AgentID) == "" || strings.TrimSpace(handoff.ProjectID) == "" {
		return handoffAckIdentity{}, fmt.Errorf("actor agent id not resolved; set --as, ASP_SCOPE_REF, ASP_HANDLE, or ASP_AGENT_ID/ASP_PROJECT; exact handoff row %s lacks unambiguous agent_id/project_id", handoff.ID)
	}
	return handoffAckIdentityFromCandidate("handoff row", handoff.AgentID, "agent:"+handoff.AgentID, handoff.ScopeRef, handoff)
}

func handoffAckIdentityFromCandidate(source, actorAgentID, principalRef, scopeRef string, handoff handoffJSON) (handoffAckIdentity, error) {
	actorAgentID = strings.TrimSpace(actorAgentID)
	principalRef = strings.TrimSpace(principalRef)
	scopeRef = strings.TrimSpace(scopeRef)
	if actorAgentID == "" {
		return handoffAckIdentity{}, fmt.Errorf("actor agent id not resolved from %s", source)
	}
	if strings.TrimSpace(handoff.AgentID) != "" && actorAgentID != strings.TrimSpace(handoff.AgentID) {
		return handoffAckIdentity{}, fmt.Errorf("ambiguous actor: %s resolved %q but handoff row agent_id is %q", source, actorAgentID, handoff.AgentID)
	}
	if principalRef == "" {
		principalRef = "agent:" + actorAgentID
	}
	projectID := strings.TrimSpace(handoff.ProjectID)
	if scopeRef != "" {
		if parsed, err := scope.ParseScopeRef(scopeRef); err == nil {
			if strings.TrimSpace(parsed.ProjectID) != "" {
				if projectID != "" && parsed.ProjectID != projectID {
					return handoffAckIdentity{}, fmt.Errorf("ambiguous project: %s resolved %q but handoff row project_id is %q", source, parsed.ProjectID, projectID)
				}
				projectID = parsed.ProjectID
			}
		}
	}
	if projectID == "" {
		return handoffAckIdentity{}, fmt.Errorf("project id not resolved from %s and handoff row lacks project_id", source)
	}
	if scopeRef == "" {
		scopeRef = "agent:" + actorAgentID + ":project:" + projectID
	}
	return handoffAckIdentity{
		actorAgentID: actorAgentID,
		principalRef: principalRef,
		scopeRef:     scopeRef,
	}, nil
}

func changedStringFlag(cmd *cobra.Command, name string) string {
	if cmd == nil {
		return ""
	}
	if f := cmd.Flag(name); f != nil && f.Changed {
		return strings.TrimSpace(f.Value.String())
	}
	return ""
}

// classifyHandoffAckError maps RPC errors to the legacy exit codes: 4 not found,
// 5 already acknowledged, 6 etag mismatch, 1 otherwise.
func classifyHandoffAckError(cmd *cobra.Command, tr Transport, stderr io.Writer, mode handoffOutputMode, idOrUUID string, err error) error {
	re, ok := err.(*Error)
	if !ok {
		return writeHandoffError(stderr, mode, 1, "runtime_error", idOrUUID, err.Error(), nil, "")
	}
	switch re.DomainID {
	case "WRKQ_NOT_FOUND":
		message := fmt.Sprintf("handoff %s was not found; pass a handoff ID like H-00001 or a handoff UUID", idOrUUID)
		return writeHandoffError(stderr, mode, 4, "handoff_not_found", idOrUUID, message, nil, handoffAckExample)
	case "WRKQ_CONFLICT":
		if isAlreadyAcknowledged(re) {
			existingMsg := re.Message
			if existing, gerr := tr.Call(cmd.Context(), "wrkq.handoff.get", map[string]string{"handoff": idOrUUID}); gerr == nil {
				if h, herr := handoffFromRPC(existing); herr == nil && h.AcknowledgedAt != nil {
					stamp := h.AcknowledgedAt.Format(time.RFC3339)
					if mode == handoffOutputHuman {
						stamp = style.FormatLocalTime(*h.AcknowledgedAt)
					}
					existingMsg = fmt.Sprintf("%s (acknowledged_at=%s)", re.Message, stamp)
				}
			}
			return writeHandoffError(stderr, mode, 5, "already_acknowledged", idOrUUID, existingMsg, nil, "")
		}
		return writeHandoffError(stderr, mode, 6, "etag_mismatch", idOrUUID, re.Message, nil,
			"wrkq handoff get "+idOrUUID+" --json # inspect current etag")
	default:
		return writeHandoffError(stderr, mode, 1, "runtime_error", idOrUUID, re.Message, nil, "")
	}
}

func writeHandoffAckOutput(cmd *cobra.Command, mode handoffOutputMode, out handoffAckOutput) error {
	stdout := cmd.OutOrStdout()
	if mode == handoffOutputHuman {
		ts := ""
		if out.Handoff.AcknowledgedAt != nil {
			ts = style.FormatLocalTime(*out.Handoff.AcknowledgedAt)
		}
		fmt.Fprintf(stdout, "Acknowledged %s at %s.", out.Handoff.ID, ts)
		if out.Handoff.AcknowledgementNote != nil {
			fmt.Fprintf(stdout, " Note: %q", *out.Handoff.AcknowledgementNote)
		}
		fmt.Fprintln(stdout)
		fmt.Fprintf(stdout, "Status: %s (etag=%d)\n", out.Handoff.Status, out.Handoff.ETag)
		if out.DryRun {
			fmt.Fprintln(stdout, "(dry run — no changes were written)")
		}
		return nil
	}
	return writeHandoffIndentedJSON(stdout, out)
}

// handoffAckParams builds the wrkq.handoff.acknowledge request from the
// CALLER-resolved acting identity. actorAgentId/principalRef/scopeRef are always
// explicit; note/dryRun/ifMatch are omitted when unset.
func handoffAckParams(handoff, actorAgentID, principalRef, scopeRef string, note *string, dryRun bool, ifMatch int64) map[string]any {
	params := map[string]any{
		"handoff":      handoff,
		"actorAgentId": actorAgentID,
		"principalRef": principalRef,
		"scopeRef":     scopeRef,
	}
	if note != nil {
		params["note"] = *note
	}
	if dryRun {
		params["dryRun"] = true
	}
	if ifMatch != 0 {
		params["ifMatch"] = ifMatch
	}
	return params
}
