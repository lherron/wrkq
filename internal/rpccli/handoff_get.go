package rpccli

// handoff_get.go — `wrkq handoff get`: one handoff by ID or UUID.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

func newHandoffGetCmd() *cobra.Command {
	var asJSON, human bool
	cmd := &cobra.Command{
		Use:     "get <handoff-id>",
		Aliases: []string{"cat"},
		Short:   "Get a single handoff by ID",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHandoffGet(cmd, args[0], asJSON, human)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Force JSON output")
	cmd.Flags().BoolVar(&human, "human", false, "Force human-readable output")
	return cmd
}

const handoffGetExample = "wrkq handoff get H-00001 --json"

func runHandoffGet(cmd *cobra.Command, arg string, asJSON, human bool) error {
	stderr := cmd.ErrOrStderr()
	mode := handoffMode(cmd, asJSON, false, human, handoffOutputJSON)
	idOrUUID := strings.TrimSpace(arg)

	tr, closeFn, err := openHandoffMirror(cmd)
	if err != nil {
		return writeHandoffError(stderr, mode, 1, "runtime_error", idOrUUID, err.Error(), nil, "")
	}
	defer closeFn()

	raw, rerr := tr.Call(cmd.Context(), "wrkq.handoff.get", map[string]string{"handoff": idOrUUID})
	if rerr != nil {
		if isNotFound(rerr) {
			message := fmt.Sprintf("handoff %s was not found; pass a handoff ID like H-00001 or a handoff UUID", idOrUUID)
			return writeHandoffError(stderr, mode, 4, "handoff_not_found", idOrUUID, message, nil, handoffGetExample)
		}
		return writeHandoffError(stderr, mode, 1, "runtime_error", idOrUUID, errors.New(rpcMessage(rerr)).Error(), nil, "")
	}
	handoff, herr := handoffFromRPC(raw)
	if herr != nil {
		return herr
	}
	return writeHandoffGetOutput(cmd, mode, handoff)
}

func writeHandoffGetOutput(cmd *cobra.Command, mode handoffOutputMode, h handoffJSON) error {
	stdout := cmd.OutOrStdout()
	if mode == handoffOutputHuman {
		fmt.Fprintf(stdout, "ID: %s\n", h.ID)
		fmt.Fprintf(stdout, "UUID: %s\n", h.UUID)
		fmt.Fprintf(stdout, "Scope: %s\n", h.ScopeRef)
		fmt.Fprintf(stdout, "Scope Kind: %s\n", h.ScopeKind)
		fmt.Fprintf(stdout, "Agent: %s\n", h.AgentID)
		fmt.Fprintf(stdout, "Project: %s\n", h.ProjectID)
		fmt.Fprintf(stdout, "Status: %s\n", h.Status)
		fmt.Fprintf(stdout, "Title: %s\n", h.Title)
		fmt.Fprintf(stdout, "Created: %s\n", style.FormatLocalTime(h.CreatedAt))
		fmt.Fprintf(stdout, "Updated: %s\n", style.FormatLocalTime(h.UpdatedAt))
		if h.AcknowledgedAt != nil {
			fmt.Fprintf(stdout, "Acknowledged At: %s\n", style.FormatLocalTime(*h.AcknowledgedAt))
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
		return nil
	}
	return writeHandoffIndentedJSON(stdout, h)
}
