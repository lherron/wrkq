package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

const handoffAckExample = "wrkq handoff acknowledge H-00001 --note \"loaded next session\" --json"

// handoffAckOutput is the structured payload returned by JSON mode.
//
// Mirrors the parent-task spec: `{"handoff": {...post-state...}, "dry_run": bool}`.
type handoffAckOutput struct {
	Handoff handoffJSON `json:"handoff"`
	DryRun  bool        `json:"dry_run"`
}

func runHandoffAck(cmd *cobra.Command, args []string) error {
	defer resetHandoffAckFlags(cmd)

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()

	sel, modeErr := resolveHandoffSelection(cmd, outputShapeMutation, true)
	mode := handoffModeFromSelection(sel)
	if modeErr != nil {
		return writeHandoffAckError(stderr, mode, 1, "validation_error", "", modeErr.Error(), "")
	}

	idOrUUID := strings.TrimSpace(args[0])
	if idOrUUID == "" {
		return writeHandoffAckError(stderr, mode, 1, "validation_error", "",
			"handoff id is required", handoffAckExample)
	}

	// --note is OPTIONAL (cody amendment overriding parent spec). If supplied
	// with a non-empty value it must round-trip to the store; an explicit but
	// blank/whitespace --note value is a validation error.
	var note *string
	if cmd.Flags().Changed("note") {
		trimmed := strings.TrimSpace(handoffAckNote)
		if trimmed == "" {
			return writeHandoffAckError(stderr, mode, 1, "validation_error", idOrUUID,
				"--note cannot be empty", handoffAckExample)
		}
		note = &trimmed
	}

	if handoffAckIfMatch < 0 {
		return writeHandoffAckError(stderr, mode, 1, "validation_error", idOrUUID,
			"--if-match must be a non-negative etag value", handoffAckExample)
	}

	database, err := openHandoffCreateDB(cmd)
	if err != nil {
		return writeHandoffAckError(stderr, mode, 1, "runtime_error", idOrUUID, err.Error(), "")
	}
	defer func() { _ = database.Close() }()

	handoff, err := store.GetHandoff(cmd.Context(), database, idOrUUID)
	if err != nil {
		return classifyHandoffAckError(cmd.Context(), database, stderr, mode, idOrUUID, err)
	}

	identity, err := resolveHandoffAckIdentity(cmd, handoff)
	if err != nil {
		return writeHandoffAckError(stderr, mode, 1, "validation_error", idOrUUID, err.Error(), handoffAckExample)
	}

	// Best-effort actor UUID lookup; the store accepts a nil pointer when the
	// running agent has no corresponding actor row (matches handoff_create).
	var actorUUID *string
	{
		var u string
		if err := database.QueryRowContext(cmd.Context(),
			"SELECT uuid FROM actors WHERE slug = ? LIMIT 1", identity.actorAgentID).Scan(&u); err == nil {
			actorUUID = &u
		}
	}

	ackArgs := store.AcknowledgeHandoffArgs{
		Note:         note,
		ActorAgentID: identity.actorAgentID,
		ActorUUID:    actorUUID,
		PrincipalRef: identity.principalRef,
		ScopeRef:     identity.scopeRef,
		DryRun:       handoffAckDryRun,
		IfMatch:      handoffAckIfMatch,
	}

	handoff, err = store.AcknowledgeHandoff(cmd.Context(), database, idOrUUID, ackArgs)
	if err != nil {
		return classifyHandoffAckError(cmd.Context(), database, stderr, mode, idOrUUID, err)
	}

	out := handoffAckOutput{
		Handoff: toHandoffJSON(handoff),
		DryRun:  handoffAckDryRun,
	}
	if err := writeHandoffAckOutput(stdout, mode, out); err != nil {
		return exitError(1, err)
	}
	return nil
}

type handoffAckIdentity struct {
	actorAgentID string
	principalRef string
	scopeRef     string
	source       string
}

func resolveHandoffAckIdentity(cmd *cobra.Command, handoff store.Handoff) (handoffAckIdentity, error) {
	asFlag := changedStringFlag(cmd, "as")
	if asFlag != "" {
		principalRef, err := attribution.NormalizeCompat(asFlag)
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

func handoffAckIdentityFromCandidate(source, actorAgentID, principalRef, scopeRef string, handoff store.Handoff) (handoffAckIdentity, error) {
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
		source:       source,
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

// classifyHandoffAckError maps store errors to the documented CLI exit codes
// for acknowledge: 4 not found, 5 already acknowledged, 6 etag mismatch, 1 otherwise.
func classifyHandoffAckError(ctx context.Context, database *db.DB, stderr io.Writer, mode handoffOutputMode, idOrUUID string, err error) error {
	msg := err.Error()
	switch {
	case strings.HasPrefix(msg, "handoff not found:"):
		message := fmt.Sprintf("handoff %s was not found; pass a handoff ID like H-00001 or a handoff UUID", idOrUUID)
		return writeHandoffAckError(stderr, mode, 4, "handoff_not_found", idOrUUID, message, handoffAckExample)

	case strings.Contains(msg, "is already acknowledged"):
		// Reload to surface the existing acknowledged_at so callers can correlate.
		existingMsg := msg
		if existing, getErr := store.GetHandoff(ctx, database, idOrUUID); getErr == nil && existing.AcknowledgedAt != nil {
			existingMsg = fmt.Sprintf("%s (acknowledged_at=%s)", msg, existing.AcknowledgedAt.Format(time.RFC3339))
		}
		return writeHandoffAckError(stderr, mode, 5, "already_acknowledged", idOrUUID, existingMsg, "")
	}
	var etagErr *domain.ETagMismatchError
	if errors.As(err, &etagErr) {
		return writeHandoffAckError(stderr, mode, 6, "etag_mismatch", idOrUUID, err.Error(),
			"wrkq handoff get "+idOrUUID+" --json # inspect current etag")
	}
	return writeHandoffAckError(stderr, mode, 1, "runtime_error", idOrUUID, err.Error(), "")
}

func writeHandoffAckOutput(stdout io.Writer, mode handoffOutputMode, out handoffAckOutput) error {
	if mode == handoffOutputHuman {
		ts := ""
		if out.Handoff.AcknowledgedAt != nil {
			ts = out.Handoff.AcknowledgedAt.Format(time.RFC3339)
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
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func writeHandoffAckError(stderr io.Writer, mode handoffOutputMode, exitCode int, code, handoffID, message, example string) error {
	errOut := handoffErrorOutput{
		Error: structuredCLIError{
			Code:      code,
			HandoffID: handoffID,
			Message:   message,
			Example:   example,
		},
	}
	if mode == handoffOutputHuman {
		fmt.Fprintf(stderr, "Error: %s\n", message)
		if example != "" {
			fmt.Fprintf(stderr, "Example: %s\n", example)
		}
	} else {
		encoder := json.NewEncoder(stderr)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(errOut)
	}
	return exitErrorReported(exitCode, fmt.Errorf("%s: %s", code, message))
}

func resetHandoffAckFlags(cmd *cobra.Command) {
	handoffAckNote = ""
	handoffAckDryRun = false
	handoffAckJSON = false
	handoffAckHuman = false
	handoffAckIfMatch = 0
	// Reset cobra's Changed bit so subsequent invocations in the same process
	// (tests share rootCmd) don't see stale state. Without this, --note from a
	// previous test would still report Flags().Changed("note") == true.
	if cmd == nil {
		return
	}
	for _, name := range []string{"note", "dry-run", "if-match", "json", "human"} {
		if f := cmd.Flags().Lookup(name); f != nil {
			f.Changed = false
		}
	}
}
