package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

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

	mode, modeErr := resolveHandoffAckOutputMode(stdout)
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

	// Resolve the acting agent from runtime env. Acknowledging requires an
	// actor agent id so the store can log handoff.acknowledged and attribute
	// the transition.
	env := scope.ReadEnv()
	actorAgentID := strings.TrimSpace(env.ASPAgentID)
	if actorAgentID == "" {
		return writeHandoffAckError(stderr, mode, 1, "validation_error", idOrUUID,
			"actor agent id not resolved; set ASP_AGENT_ID or ASP_SCOPE_REF in runtime env",
			handoffAckExample)
	}

	database, err := openHandoffCreateDB(cmd)
	if err != nil {
		return writeHandoffAckError(stderr, mode, 1, "runtime_error", idOrUUID, err.Error(), "")
	}
	defer func() { _ = database.Close() }()

	// Best-effort actor UUID lookup; the store accepts a nil pointer when the
	// running agent has no corresponding actor row (matches handoff_create).
	var actorUUID *string
	{
		var u string
		if err := database.QueryRowContext(cmd.Context(),
			"SELECT uuid FROM actors WHERE slug = ? LIMIT 1", actorAgentID).Scan(&u); err == nil {
			actorUUID = &u
		}
	}

	ackArgs := store.AcknowledgeHandoffArgs{
		Note:         note,
		ActorAgentID: actorAgentID,
		ActorUUID:    actorUUID,
		DryRun:       handoffAckDryRun,
		IfMatch:      handoffAckIfMatch,
	}

	handoff, err := store.AcknowledgeHandoff(cmd.Context(), database, idOrUUID, ackArgs)
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

// resolveHandoffAckOutputMode mirrors the get/create resolvers: explicit
// flags win, otherwise TTY-aware (human on TTY, JSON when piped).
func resolveHandoffAckOutputMode(stdout io.Writer) (handoffOutputMode, error) {
	selected := 0
	if handoffAckJSON {
		selected++
	}
	if handoffAckHuman {
		selected++
	}
	if selected > 1 {
		return handoffOutputJSON, fmt.Errorf("choose only one output mode: --json or --human")
	}
	switch {
	case handoffAckJSON:
		return handoffOutputJSON, nil
	case handoffAckHuman:
		return handoffOutputHuman, nil
	case isStdoutTTY(stdout):
		return handoffOutputHuman, nil
	default:
		return handoffOutputJSON, nil
	}
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
