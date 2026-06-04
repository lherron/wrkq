package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

const handoffGetExample = "wrkq handoff get H-00001 --json"

func runHandoffGet(cmd *cobra.Command, args []string) error {
	defer resetHandoffGetFlags()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	sel, modeErr := resolveHandoffSelection(cmd, outputShapeSingleton, true)
	mode := handoffModeFromSelection(sel)
	if modeErr != nil {
		return writeHandoffGetError(stderr, mode, 1, "validation_error", "", modeErr.Error(), "")
	}

	idOrUUID := strings.TrimSpace(args[0])
	database, err := openHandoffCreateDB(cmd)
	if err != nil {
		return writeHandoffGetError(stderr, mode, 1, "runtime_error", idOrUUID, err.Error(), "")
	}
	defer func() { _ = database.Close() }()

	handoff, err := store.GetHandoff(cmd.Context(), database, idOrUUID)
	if err != nil {
		if strings.HasPrefix(err.Error(), "handoff not found:") {
			message := fmt.Sprintf("handoff %s was not found; pass a handoff ID like H-00001 or a handoff UUID", idOrUUID)
			return writeHandoffGetError(stderr, mode, 4, "handoff_not_found", idOrUUID, message, handoffGetExample)
		}
		return writeHandoffGetError(stderr, mode, 1, "runtime_error", idOrUUID, err.Error(), "")
	}

	if err := writeHandoffGetOutput(stdout, mode, toHandoffJSON(handoff)); err != nil {
		return exitError(1, err)
	}
	return nil
}

func writeHandoffGetOutput(stdout io.Writer, mode handoffOutputMode, out handoffJSON) error {
	if mode == handoffOutputHuman {
		writeHandoffGetHuman(stdout, out)
		return nil
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(out)
}

func writeHandoffGetHuman(stdout io.Writer, h handoffJSON) {
	fmt.Fprintf(stdout, "ID: %s\n", h.ID)
	fmt.Fprintf(stdout, "UUID: %s\n", h.UUID)
	fmt.Fprintf(stdout, "Scope: %s\n", h.ScopeRef)
	fmt.Fprintf(stdout, "Scope Kind: %s\n", h.ScopeKind)
	fmt.Fprintf(stdout, "Agent: %s\n", h.AgentID)
	fmt.Fprintf(stdout, "Project: %s\n", h.ProjectID)
	fmt.Fprintf(stdout, "Status: %s\n", h.Status)
	fmt.Fprintf(stdout, "Title: %s\n", h.Title)
	fmt.Fprintf(stdout, "Created: %s\n", h.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(stdout, "Updated: %s\n", h.UpdatedAt.Format(time.RFC3339))
	if h.AcknowledgedAt != nil {
		fmt.Fprintf(stdout, "Acknowledged At: %s\n", h.AcknowledgedAt.Format(time.RFC3339))
	}
	if h.AcknowledgedByAgentID != nil {
		fmt.Fprintf(stdout, "Acknowledged By Agent ID: %s\n", *h.AcknowledgedByAgentID)
	}
	if h.AcknowledgementNote != nil {
		fmt.Fprintf(stdout, "Acknowledgement Note: %s\n", *h.AcknowledgementNote)
	}
	fmt.Fprintln(stdout, "")
	fmt.Fprintln(stdout, "Body:")
	fmt.Fprintln(stdout, "---")
	fmt.Fprintln(stdout, h.Body)
}

func writeHandoffGetError(stderr io.Writer, mode handoffOutputMode, exitCode int, code, handoffID, message, example string) error {
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

func resetHandoffGetFlags() {
	handoffGetJSON = false
	handoffGetHuman = false
}
