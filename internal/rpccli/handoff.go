package rpccli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/scope"
	"github.com/spf13/cobra"
)

// newHandoffCmd mirrors `wrkq handoff` on the CALLER-OWNED-SCOPE seam
// (architecture/records/invariants/wrkq.handoff.caller-owned-scope.yaml).
//
// Handoff scope is caller-owned but NOT project-root: the mirror resolves
// --scope / agent-runtime env via scope.Resolve and enforces self-scope for
// create (scope.EnforceSelfScope) BEFORE submitting. The server receives the
// EXPLICIT effective scope/actor fields and never reads ASP_SCOPE_REF /
// ASP_HANDLE / ASP_AGENT_ID / ASP_PROJECT. Every durable mutation/read crosses
// the RPC boundary (wrkq.handoff.create/get/listView/acknowledge); the mirror
// owns ONLY the caller-side scope resolution, diagnostics, output modes, and the
// legacy CLI wording.
func newHandoffCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "handoff",
		Short: "Manage agent session handoffs",
	}
	cmd.AddCommand(newHandoffCreateCmd())
	cmd.AddCommand(newHandoffListCmd())
	cmd.AddCommand(newHandoffGetCmd())
	cmd.AddCommand(newHandoffAckCmd())
	cmd.AddCommand(newHandoffSearchCmd())
	return cmd
}

// ─── shared output plumbing (mirrors internal/cli handoff output) ────────────

type handoffOutputMode string

const (
	handoffOutputJSON   handoffOutputMode = "json"
	handoffOutputNDJSON handoffOutputMode = "ndjson"
	handoffOutputHuman  handoffOutputMode = "human"
)

const handoffScopeExample = "wrkq handoff create --scope cody@wrkq -t 'Next steps' --body-file -"

// handoffJSON reproduces the legacy internal/cli handoffJSON shape EXACTLY so the
// mirror re-marshals the RPC DTO into byte-identical output. Field order + tags +
// pointer/omitempty match the WrkqHandoff DTO (which itself pins the legacy order).
type handoffJSON struct {
	UUID                       string     `json:"uuid"`
	ID                         string     `json:"id"`
	ScopeRef                   string     `json:"scope_ref"`
	ScopeKind                  string     `json:"scope_kind"`
	AgentID                    string     `json:"agent_id"`
	ProjectID                  string     `json:"project_id"`
	AgentPrincipalRef          *string    `json:"agent_principal_ref,omitempty"`
	ProjectContainerUUID       *string    `json:"project_container_uuid"`
	CreatedByAgentID           string     `json:"created_by_agent_id"`
	CreatedByPrincipalRef      string     `json:"created_by_principal_ref,omitempty"`
	Title                      string     `json:"title"`
	Body                       string     `json:"body"`
	Status                     string     `json:"status"`
	IdempotencyKey             *string    `json:"idempotency_key"`
	AcknowledgedAt             *time.Time `json:"acknowledged_at"`
	AcknowledgedByAgentID      *string    `json:"acknowledged_by_agent_id"`
	AcknowledgedByPrincipalRef *string    `json:"acknowledged_by_principal_ref,omitempty"`
	AcknowledgementNote        *string    `json:"acknowledgement_note"`
	Meta                       *string    `json:"meta"`
	ETag                       int64      `json:"etag"`
	CreatedAt                  time.Time  `json:"created_at"`
	UpdatedAt                  time.Time  `json:"updated_at"`
}

type structuredCLIError struct {
	Code      string `json:"code"`
	HandoffID string `json:"handoff_id,omitempty"`
	Message   string `json:"message"`
	Example   string `json:"example,omitempty"`
}

type handoffErrorOutput struct {
	Error       structuredCLIError `json:"error"`
	Diagnostics []scope.Diagnostic `json:"diagnostics,omitempty"`
}

// handoffMode selects the legacy output mode from the --json/--ndjson/--human
// flags and TTY default. The mirror's persistent --output flag is unused by
// handoff (legacy handoff uses its own per-command flags); it defaults to human
// on a TTY, JSON otherwise (NDJSON for list).
func handoffMode(cmd *cobra.Command, asJSON, ndjson, human bool, defaultStable handoffOutputMode) handoffOutputMode {
	switch {
	case human:
		return handoffOutputHuman
	case ndjson:
		return handoffOutputNDJSON
	case asJSON:
		return handoffOutputJSON
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return handoffOutputHuman
	}
	return defaultStable
}

// openHandoffMirror opens the RPC transport. Unlike openMirror it does NOT build
// the project-root scoper: handoff scope is resolved caller-side from --scope /
// env, not from project-root.
func openHandoffMirror(cmd *cobra.Command) (Transport, func(), error) {
	tr, _, closeFn, err := openConfiguredTransport(cmd)
	if err != nil {
		return nil, nil, err
	}
	return tr, closeFn, nil
}

// handoffFromRPC decodes a WrkqHandoff RPC result into the legacy handoffJSON.
func handoffFromRPC(raw json.RawMessage) (handoffJSON, error) {
	var h handoffJSON
	if err := json.Unmarshal(raw, &h); err != nil {
		return handoffJSON{}, err
	}
	return h, nil
}

// ─── shared error rendering ──────────────────────────────────────────────────

func writeHandoffError(stderr io.Writer, mode handoffOutputMode, exitCode int, code, handoffID, message string, diags []scope.Diagnostic, example string) error {
	errOut := handoffErrorOutput{
		Error: structuredCLIError{
			Code:      code,
			HandoffID: handoffID,
			Message:   message,
			Example:   example,
		},
		Diagnostics: diags,
	}
	if mode == handoffOutputHuman {
		fmt.Fprintf(stderr, "Error: %s\n", message)
		if example != "" {
			fmt.Fprintf(stderr, "Example: %s\n", example)
		}
		writeHandoffDiagnostics(stderr, diags)
	} else {
		enc := json.NewEncoder(stderr)
		if mode == handoffOutputJSON {
			enc.SetIndent("", "  ")
		}
		_ = enc.Encode(errOut)
	}
	return exitErrorReported(exitCode, fmt.Errorf("%s: %s", code, message))
}

func writeHandoffListErr(stderr io.Writer, mode handoffOutputMode, exitCode int, code, message string, diags []scope.Diagnostic, example string) error {
	return writeHandoffError(stderr, mode, exitCode, code, "", message, diags, example)
}

// ─── RPC error data probes ────────────────────────────────────────────────────

func isAlreadyAcknowledged(re *Error) bool {
	return hasReason(re.Data, "already_acknowledged") || strings.Contains(re.Message, "is already acknowledged")
}

func isIdempotencyMismatch(re *Error) bool {
	if len(re.Data) == 0 {
		return strings.Contains(re.Message, "idempotency key")
	}
	var d struct {
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if json.Unmarshal(re.Data, &d) == nil && d.IdempotencyKey != "" {
		return true
	}
	return strings.Contains(re.Message, "idempotency key")
}

func hasReason(data json.RawMessage, want string) bool {
	if len(data) == 0 {
		return false
	}
	var d struct {
		Reason string `json:"reason"`
	}
	if json.Unmarshal(data, &d) == nil {
		return d.Reason == want
	}
	return false
}

// writeHandoffDiagnostics prints scope diagnostics in the legacy human form.
func writeHandoffDiagnostics(w io.Writer, diags []scope.Diagnostic) {
	for _, d := range diags {
		fmt.Fprintf(w, "[%s] %s: %s\n", d.Level, d.Code, d.Message)
	}
}

// writeHandoffIndentedJSON writes v as two-space-indented JSON, the legacy
// handoff JSON mode.
func writeHandoffIndentedJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
