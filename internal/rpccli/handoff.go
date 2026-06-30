package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
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

// handoffCreateParams builds the wrkq.handoff.create request from the
// CALLER-resolved effective scope/actor. The scope fields are ALWAYS explicit so
// the server never derives them from env; meta/idempotencyKey/dryRun/actorAgentId
// are omitted when empty (legacy semantics). Pure + side-effect-free for unit
// testing (the no-server-env-read contract is asserted on these explicit fields).
func handoffCreateParams(scopeRef, agentID, projectID, title, body string, meta, idemKey *string, dryRun bool, actor string) map[string]any {
	params := map[string]any{
		"scopeRef":  scopeRef,
		"agentId":   agentID,
		"projectId": projectID,
		"title":     title,
		"body":      body,
	}
	if meta != nil {
		params["meta"] = *meta
	}
	if idemKey != nil {
		params["idempotencyKey"] = *idemKey
	}
	if dryRun {
		params["dryRun"] = true
	}
	if actor != "" {
		params["actorAgentId"] = actor
	}
	return params
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

// handoffFromRPC decodes a WrkqHandoff RPC result into the legacy handoffJSON.
func handoffFromRPC(raw json.RawMessage) (handoffJSON, error) {
	var h handoffJSON
	if err := json.Unmarshal(raw, &h); err != nil {
		return handoffJSON{}, err
	}
	return h, nil
}

// ─── handoff create ───────────────────────────────────────────────────────────

func newHandoffCreateCmd() *cobra.Command {
	var (
		title          string
		bodyFile       string
		scopeOverride  string
		profile        string
		idempotencyKey string
		meta           string
		dryRun         bool
		asJSON         bool
		ndjson         bool
		human          bool
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a new pending handoff for the resolved agent/project scope",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHandoffCreate(cmd, handoffCreateFlags{
				title: title, bodyFile: bodyFile, scopeOverride: scopeOverride,
				idempotencyKey: idempotencyKey, meta: meta, dryRun: dryRun,
				asJSON: asJSON, ndjson: ndjson, human: human,
			})
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "Handoff title (required)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Path to a file containing the markdown body, or '-' to read from stdin (required)")
	cmd.Flags().StringVar(&scopeOverride, "scope", "", "Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	cmd.Flags().StringVar(&profile, "profile", "", "Named profile to resolve scope/defaults from")
	cmd.Flags().StringVar(&idempotencyKey, "idempotency-key", "", "Idempotency key to safely retry create without producing duplicates")
	cmd.Flags().StringVar(&meta, "meta", "", "Optional JSON object of additional metadata")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "Validate inputs and print the prospective handoff without writing it")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Force JSON output")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Force NDJSON output")
	cmd.Flags().BoolVar(&human, "human", false, "Force human-readable output")
	return cmd
}

type handoffCreateFlags struct {
	title, bodyFile, scopeOverride, idempotencyKey, meta string
	dryRun, asJSON, ndjson, human                        bool
}

func runHandoffCreate(cmd *cobra.Command, f handoffCreateFlags) error {
	stderr := cmd.ErrOrStderr()
	mode := handoffMode(cmd, f.asJSON, f.ndjson, f.human, handoffOutputJSON)

	title, body, meta, idemKey, validationErr := readHandoffCreateInputs(cmd, f)
	if validationErr != nil {
		return writeHandoffError(stderr, mode, 1, "validation_error", "", validationErr.Error(), nil,
			"wrkq handoff create -t 'Next steps' --body-file notes.md")
	}

	// CALLER-side scope resolution + self-scope enforcement (the server never
	// reads env). Mirrors internal/cli/handoff_create.go:123-134.
	resolved, diags, resolveErr := scope.Resolve(strings.TrimSpace(f.scopeOverride))
	if resolveErr != nil {
		return writeHandoffError(stderr, mode, 2, "scope_unresolvable", "", resolveErr.Error(), diags, handoffScopeExample)
	}
	if err := scope.EnforceSelfScope(resolved, scope.RuntimeIdentity{AgentID: scope.ReadEnv().ASPAgentID}); err != nil {
		return writeHandoffError(stderr, mode, 1, "self_scope_violation", "", err.Error(), diags, handoffScopeExample)
	}
	if resolved.ProjectID == "" {
		return writeHandoffError(stderr, mode, 2, "scope_unresolvable",
			"", "handoff create requires an agent/project scope; pass --scope cody@wrkq or set ASP_SCOPE_REF=agent:cody:project:wrkq",
			diags, handoffScopeExample)
	}

	tr, closeFn, err := openHandoffMirror(cmd)
	if err != nil {
		return writeHandoffError(stderr, mode, 1, "runtime_error", "", err.Error(), diags, "")
	}
	defer closeFn()

	// Legacy `wrkq handoff create` attributes createdBy to the RESOLVED SCOPE agent
	// (handoff_create.go: CreatedByAgentID = resolved.AgentID), NOT the global --as
	// actor. So we pass an empty actorAgentId and let the server default createdBy to
	// the scope agent — preserving byte parity even when --as is set.
	params := handoffCreateParams(resolved.CanonicalRef, resolved.AgentID, resolved.ProjectID,
		title, body, meta, idemKey, f.dryRun, "")

	raw, rerr := tr.Call(cmd.Context(), "wrkq.handoff.create", params)
	if rerr != nil {
		if re, ok := rerr.(*Error); ok {
			if re.DomainID == "WRKQ_CONFLICT" && hasReason(re.Data, "") && isIdempotencyMismatch(re) {
				return writeHandoffError(stderr, mode, 3, "idempotency_payload_mismatch", "", re.Message, diags,
					"retry with the original title/body or use a new --idempotency-key")
			}
			return writeHandoffError(stderr, mode, 1, "runtime_error", "", re.Message, diags, "")
		}
		return writeHandoffError(stderr, mode, 1, "runtime_error", "", rerr.Error(), diags, "")
	}

	var result struct {
		Handoff          json.RawMessage `json:"handoff"`
		IdempotentReplay bool            `json:"idempotentReplay"`
	}
	if uerr := json.Unmarshal(raw, &result); uerr != nil {
		return uerr
	}
	handoff, herr := handoffFromRPC(result.Handoff)
	if herr != nil {
		return herr
	}

	return writeHandoffCreateOutput(cmd, mode, handoffCreateOutput{
		Handoff:          handoff,
		IdempotentReplay: result.IdempotentReplay,
		Diagnostics:      diags,
		DryRun:           f.dryRun,
	})
}

type handoffCreateOutput struct {
	Handoff          handoffJSON        `json:"handoff"`
	IdempotentReplay bool               `json:"idempotent_replay"`
	Diagnostics      []scope.Diagnostic `json:"diagnostics,omitempty"`
	DryRun           bool               `json:"dry_run,omitempty"`
}

func readHandoffCreateInputs(cmd *cobra.Command, f handoffCreateFlags) (string, string, *string, *string, error) {
	title := strings.TrimSpace(f.title)
	if title == "" {
		return "", "", nil, nil, fmt.Errorf("title is required (use -t or --title)")
	}
	bodyFile := strings.TrimSpace(f.bodyFile)
	if bodyFile == "" {
		return "", "", nil, nil, fmt.Errorf("body is required (use --body-file <path|->)")
	}
	var bodyBytes []byte
	var err error
	if bodyFile == "-" {
		bodyBytes, err = io.ReadAll(cmd.InOrStdin())
		if err != nil {
			return "", "", nil, nil, fmt.Errorf("failed to read body from stdin: %w", err)
		}
	} else {
		bodyBytes, err = os.ReadFile(bodyFile)
		if err != nil {
			return "", "", nil, nil, fmt.Errorf("failed to read --body-file %s: %w", bodyFile, err)
		}
	}
	body := strings.TrimRightFunc(string(bodyBytes), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if strings.TrimSpace(body) == "" {
		return "", "", nil, nil, fmt.Errorf("body cannot be empty")
	}

	var meta *string
	if strings.TrimSpace(f.meta) != "" {
		var obj map[string]interface{}
		if jerr := json.Unmarshal([]byte(f.meta), &obj); jerr != nil {
			return "", "", nil, nil, fmt.Errorf("invalid JSON object for --meta: %w", jerr)
		}
		trimmed := strings.TrimSpace(f.meta)
		meta = &trimmed
	}
	var idemKey *string
	if strings.TrimSpace(f.idempotencyKey) != "" {
		trimmed := strings.TrimSpace(f.idempotencyKey)
		idemKey = &trimmed
	}
	return title, body, meta, idemKey, nil
}

func writeHandoffCreateOutput(cmd *cobra.Command, mode handoffOutputMode, out handoffCreateOutput) error {
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	switch mode {
	case handoffOutputHuman:
		for _, d := range out.Diagnostics {
			fmt.Fprintf(stderr, "[%s] %s: %s\n", d.Level, d.Code, d.Message)
		}
		fmt.Fprintf(stdout, "ID\tScope\tStatus\tTitle\tCreated At\n")
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n",
			out.Handoff.ID, out.Handoff.ScopeRef, out.Handoff.Status, out.Handoff.Title,
			out.Handoff.CreatedAt.Format(time.RFC3339))
		if out.IdempotentReplay {
			fmt.Fprintln(stdout, "(idempotent replay)")
		}
		if out.DryRun {
			fmt.Fprintln(stdout, "(dry run)")
		}
		return nil
	case handoffOutputNDJSON:
		return json.NewEncoder(stdout).Encode(out)
	default:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
}

// ─── handoff get ────────────────────────────────────────────────────────────

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
		return nil
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(h)
}

// ─── handoff list ────────────────────────────────────────────────────────────

const (
	handoffListDefaultLimit = 50
	handoffListMaxLimit     = 500
)

func newHandoffListCmd() *cobra.Command {
	var (
		scopeOverride string
		status        string
		limit         int
		cursor        string
		asJSON        bool
		ndjson        bool
		human         bool
		porcelain     bool
	)
	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		Short:   "List handoffs for the resolved agent/project scope",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHandoffList(cmd, handoffListFlags{
				scopeOverride: scopeOverride, status: status, limit: limit, cursor: cursor,
				asJSON: asJSON, ndjson: ndjson, human: human, porcelain: porcelain,
			})
		},
	}
	cmd.Flags().StringVar(&scopeOverride, "scope", "", "Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	cmd.Flags().StringVar(&status, "status", "pending", "Status filter: pending, acknowledged, or all (default: pending)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results (0 = server default)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor from a previous page")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Force JSON output")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Force NDJSON output")
	cmd.Flags().BoolVar(&human, "human", false, "Force human-readable output")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Emit next_cursor=<token> on stderr (mirrors 'wrkq comment ls --porcelain')")
	return cmd
}

type handoffListFlags struct {
	scopeOverride, status, cursor    string
	limit                            int
	asJSON, ndjson, human, porcelain bool
}

type handoffListOutput struct {
	Handoffs    []handoffJSON      `json:"handoffs"`
	NextCursor  *string            `json:"next_cursor"`
	Truncated   bool               `json:"truncated"`
	Diagnostics []scope.Diagnostic `json:"diagnostics,omitempty"`
}

type handoffSearchOutput = handoffListOutput

func runHandoffList(cmd *cobra.Command, f handoffListFlags) error {
	stderr := cmd.ErrOrStderr()
	mode := handoffMode(cmd, f.asJSON, f.ndjson, f.human, handoffOutputNDJSON)

	resolved, diags, resolveErr := scope.Resolve(strings.TrimSpace(f.scopeOverride))
	if resolveErr != nil {
		return writeHandoffListErr(stderr, mode, 2, "scope_unresolvable", resolveErr.Error(), diags, handoffScopeExample)
	}
	if resolved.ProjectID == "" {
		return writeHandoffListErr(stderr, mode, 2, "scope_unresolvable",
			"handoff list requires an agent/project scope; pass --scope cody@wrkq or set ASP_SCOPE_REF=agent:cody:project:wrkq",
			diags, handoffScopeExample)
	}

	limit, limitErr := resolveHandoffListLimit(f.limit, stderr)
	if limitErr != nil {
		return writeHandoffListErr(stderr, mode, 1, "validation_error", limitErr.Error(), diags, "")
	}
	status, statusErr := normalizeHandoffListStatus(f.status)
	if statusErr != nil {
		return writeHandoffListErr(stderr, mode, 1, "invalid_status", statusErr.Error(), diags,
			"wrkq handoff list --status pending|acknowledged|all")
	}

	tr, closeFn, err := openHandoffMirror(cmd)
	if err != nil {
		return writeHandoffListErr(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}
	defer closeFn()

	params := map[string]any{
		"scopeRef": resolved.CanonicalRef,
		"status":   status,
		"limit":    limit,
	}
	if c := strings.TrimSpace(f.cursor); c != "" {
		params["cursor"] = c
	}
	raw, rerr := tr.Call(cmd.Context(), "wrkq.handoff.listView", params)
	if rerr != nil {
		return writeHandoffListErr(stderr, mode, 1, "runtime_error", rpcMessage(rerr), diags, "")
	}

	var page struct {
		Items      []handoffJSON `json:"items"`
		NextCursor string        `json:"nextCursor"`
	}
	if uerr := json.Unmarshal(raw, &page); uerr != nil {
		return uerr
	}

	out := handoffListOutput{
		Handoffs:    make([]handoffJSON, 0, len(page.Items)),
		Diagnostics: diags,
		Truncated:   page.NextCursor != "",
	}
	out.Handoffs = append(out.Handoffs, page.Items...)
	if page.NextCursor != "" {
		nc := page.NextCursor
		out.NextCursor = &nc
	}

	// Porcelain mode (or NDJSON stable mode) echoes next_cursor on stderr exactly
	// as legacy comment ls / handoff list does.
	if (f.porcelain || mode == handoffOutputNDJSON) && page.NextCursor != "" {
		fmt.Fprintf(stderr, "next_cursor=%s\n", page.NextCursor)
	}

	return writeHandoffListOutput(cmd, mode, resolved.CanonicalRef, status, out)
}

func resolveHandoffListLimit(requested int, stderr io.Writer) (int, error) {
	if requested < 0 {
		return 0, fmt.Errorf("--limit cannot be negative")
	}
	if requested == 0 {
		return handoffListDefaultLimit, nil
	}
	if requested > handoffListMaxLimit {
		fmt.Fprintf(stderr, "warning: --limit %d exceeds maximum %d; clamping to %d\n",
			requested, handoffListMaxLimit, handoffListMaxLimit)
		return handoffListMaxLimit, nil
	}
	return requested, nil
}

func normalizeHandoffListStatus(status string) (string, error) {
	switch strings.TrimSpace(status) {
	case "", "pending":
		return "pending", nil
	case "acknowledged":
		return "acknowledged", nil
	case "all":
		return "all", nil
	default:
		return "", fmt.Errorf("invalid --status %q: must be pending, acknowledged, or all", status)
	}
}

func writeHandoffListOutput(cmd *cobra.Command, mode handoffOutputMode, scopeRef, status string, out handoffListOutput) error {
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	switch mode {
	case handoffOutputHuman:
		for _, d := range out.Diagnostics {
			fmt.Fprintf(stderr, "[%s] %s: %s\n", d.Level, d.Code, d.Message)
		}
		if len(out.Handoffs) == 0 {
			label := "pending"
			switch status {
			case "all":
				label = "all"
			case "acknowledged":
				label = "acknowledged"
			}
			fmt.Fprintf(stdout, "No %s handoffs for %s.\n", label, scopeRef)
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tScope\tStatus\tCreated\tTitle")
		for _, h := range out.Handoffs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				h.ID, h.ScopeRef, h.Status,
				h.CreatedAt.Format(time.RFC3339),
				truncateHandoffTitle(h.Title, 60),
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if out.NextCursor != nil {
			fmt.Fprintf(stdout, "(%d shown, more available — use --cursor %s)\n", len(out.Handoffs), *out.NextCursor)
		}
		return nil
	case handoffOutputNDJSON:
		enc := json.NewEncoder(stdout)
		for _, h := range out.Handoffs {
			if err := enc.Encode(h); err != nil {
				return err
			}
		}
		footer := map[string]interface{}{
			"type":        "wrkq.pagination",
			"next_cursor": out.NextCursor,
			"truncated":   out.Truncated,
		}
		return enc.Encode(footer)
	default:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
}

// ─── handoff search ──────────────────────────────────────────────────────────

const handoffSearchExample = "wrkq handoff search quartz --scope cody@wrkq --status all"

func newHandoffSearchCmd() *cobra.Command {
	var (
		scopeOverride string
		status        string
		limit         int
		cursor        string
		asJSON        bool
		ndjson        bool
		human         bool
		porcelain     bool
	)
	cmd := &cobra.Command{
		Use:   "search <query>",
		Short: "Search handoffs by title, body, scope, or status",
		Args:  cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runHandoffSearch(cmd, args, handoffSearchFlags{
				scopeOverride: scopeOverride, status: status, limit: limit, cursor: cursor,
				asJSON: asJSON, ndjson: ndjson, human: human, porcelain: porcelain,
			})
		},
	}
	cmd.Flags().StringVar(&scopeOverride, "scope", "", "Override scope (ScopeRef like agent:cody:project:wrkq or handle like cody@wrkq)")
	cmd.Flags().StringVar(&status, "status", "pending", "Status filter: pending, acknowledged, or all (default: pending)")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum number of results (0 = server default)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Pagination cursor from a previous page")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Force JSON output")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Force NDJSON output")
	cmd.Flags().BoolVar(&human, "human", false, "Force human-readable output")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Emit next_cursor=<token> on stderr (mirrors 'wrkq handoff list --porcelain')")
	return cmd
}

type handoffSearchFlags struct {
	scopeOverride, status, cursor    string
	limit                            int
	asJSON, ndjson, human, porcelain bool
}

func runHandoffSearch(cmd *cobra.Command, args []string, f handoffSearchFlags) error {
	stderr := cmd.ErrOrStderr()
	mode := handoffMode(cmd, f.asJSON, f.ndjson, f.human, handoffOutputNDJSON)

	query, queryErr := readHandoffSearchQuery(args)
	if queryErr != nil {
		return writeHandoffListErr(stderr, mode, 1, "validation_error", queryErr.Error(), nil, handoffSearchExample)
	}
	resolved, diags, resolveErr := scope.Resolve(strings.TrimSpace(f.scopeOverride))
	if resolveErr != nil {
		return writeHandoffListErr(stderr, mode, 2, "scope_unresolvable", resolveErr.Error(), diags, handoffSearchExample)
	}
	if resolved.ProjectID == "" {
		return writeHandoffListErr(stderr, mode, 2, "scope_unresolvable",
			"handoff search requires an agent/project scope; pass --scope cody@wrkq or set ASP_SCOPE_REF=agent:cody:project:wrkq",
			diags, handoffSearchExample)
	}
	limit, limitErr := resolveHandoffListLimit(f.limit, stderr)
	if limitErr != nil {
		return writeHandoffListErr(stderr, mode, 1, "validation_error", limitErr.Error(), diags, "")
	}
	status, statusErr := normalizeHandoffListStatus(f.status)
	if statusErr != nil {
		return writeHandoffListErr(stderr, mode, 1, "invalid_status", statusErr.Error(), diags,
			"wrkq handoff search quartz --status pending|acknowledged|all")
	}

	tr, closeFn, err := openHandoffMirror(cmd)
	if err != nil {
		return writeHandoffListErr(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}
	defer closeFn()

	params := map[string]any{
		"query":    query,
		"scopeRef": resolved.CanonicalRef,
		"status":   status,
		"limit":    limit,
	}
	if c := strings.TrimSpace(f.cursor); c != "" {
		params["cursor"] = c
	}
	raw, rerr := tr.Call(cmd.Context(), "wrkq.handoff.searchView", params)
	if rerr != nil {
		re, ok := rerr.(*Error)
		if ok && re.DomainID == "WRKQ_VALIDATION" && strings.Contains(re.Message, "search is disabled") {
			msg := "search index unavailable: search is disabled (try `wrkq index rebuild`)"
			return writeHandoffListErr(stderr, mode, 1, "search_unavailable", msg, diags, "wrkq index rebuild")
		}
		return writeHandoffListErr(stderr, mode, 1, "runtime_error", rpcMessage(rerr), diags, "")
	}

	var page struct {
		Handoffs        []handoffJSON `json:"handoffs"`
		NextCursor      *string       `json:"next_cursor"`
		Truncated       bool          `json:"truncated"`
		Stale           bool          `json:"stale,omitempty"`
		StaleEventCount int64         `json:"stale_event_count,omitempty"`
		IndexWarning    string        `json:"index_warning,omitempty"`
	}
	if uerr := json.Unmarshal(raw, &page); uerr != nil {
		return uerr
	}
	if page.IndexWarning != "" {
		fmt.Fprintf(stderr, "warning: %s\n", page.IndexWarning)
	}
	if page.Stale {
		fmt.Fprintf(stderr, "warning: search index is stale by %d event(s); run `wrkq index rebuild` for fresh results\n",
			page.StaleEventCount)
	}
	if (f.porcelain || mode == handoffOutputNDJSON) && page.NextCursor != nil {
		fmt.Fprintf(stderr, "next_cursor=%s\n", *page.NextCursor)
	}

	out := handoffSearchOutput{
		Handoffs:    append([]handoffJSON{}, page.Handoffs...),
		NextCursor:  page.NextCursor,
		Truncated:   page.Truncated,
		Diagnostics: diags,
	}
	return writeHandoffSearchOutput(cmd, mode, query, resolved.CanonicalRef, out)
}

func readHandoffSearchQuery(args []string) (string, error) {
	if len(args) != 1 {
		return "", fmt.Errorf("query is required")
	}
	query := strings.TrimSpace(args[0])
	if query == "" {
		return "", fmt.Errorf("query cannot be empty")
	}
	return query, nil
}

func writeHandoffSearchOutput(cmd *cobra.Command, mode handoffOutputMode, query, scopeRef string, out handoffSearchOutput) error {
	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	switch mode {
	case handoffOutputHuman:
		for _, d := range out.Diagnostics {
			fmt.Fprintf(stderr, "[%s] %s: %s\n", d.Level, d.Code, d.Message)
		}
		if len(out.Handoffs) == 0 {
			fmt.Fprintf(stdout, "No handoffs match %q in %s.\n", query, scopeRef)
			return nil
		}
		tw := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tScope\tStatus\tCreated\tTitle")
		for _, h := range out.Handoffs {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				h.ID, h.ScopeRef, h.Status,
				h.CreatedAt.Format(time.RFC3339),
				truncateHandoffTitle(h.Title, 60),
			)
		}
		if err := tw.Flush(); err != nil {
			return err
		}
		if out.NextCursor != nil {
			fmt.Fprintf(stdout, "(%d shown, more available - use --cursor %s)\n", len(out.Handoffs), *out.NextCursor)
		}
		return nil
	case handoffOutputNDJSON:
		enc := json.NewEncoder(stdout)
		for _, h := range out.Handoffs {
			if err := enc.Encode(h); err != nil {
				return err
			}
		}
		footer := map[string]interface{}{
			"type":        "wrkq.pagination",
			"next_cursor": out.NextCursor,
			"truncated":   out.Truncated,
		}
		return enc.Encode(footer)
	default:
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
}

func truncateHandoffTitle(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	if max <= 3 {
		return s[:max]
	}
	return s[:max-3] + "..."
}

// ─── handoff acknowledge ──────────────────────────────────────────────────────

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
		trimmed := strings.TrimSpace(f.note)
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
// 5 already acknowledged, 6 etag mismatch, 1 otherwise. Mirrors
// internal/cli/handoff_acknowledge.go classifyHandoffAckError.
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
					existingMsg = fmt.Sprintf("%s (acknowledged_at=%s)", re.Message, h.AcknowledgedAt.Format(time.RFC3339))
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
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
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
		for _, d := range diags {
			fmt.Fprintf(stderr, "[%s] %s: %s\n", d.Level, d.Code, d.Message)
		}
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
	if want == "" {
		return true
	}
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
