package rpccli

// handoff_create.go — `wrkq handoff create`: read the title/body/meta inputs,
// resolve the caller's own agent/project scope (self-scope enforced), and
// submit wrkq.handoff.create with that scope explicit.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

func newHandoffCreateCmd() *cobra.Command {
	var (
		title          string
		body           string
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
		Long: "Create a new pending handoff for the resolved agent/project scope.\n\n" +
			"Exactly one body source must be explicitly selected: --body or --body-file.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runHandoffCreate(cmd, handoffCreateFlags{
				title: title, body: body, bodyFile: bodyFile, scopeOverride: scopeOverride,
				idempotencyKey: idempotencyKey, meta: meta, dryRun: dryRun,
				asJSON: asJSON, ndjson: ndjson, human: human,
			})
		},
	}
	cmd.Flags().StringVarP(&title, "title", "t", "", "Handoff title (required)")
	cmd.Flags().StringVar(&body, "body", "", "Inline Markdown body (mutually exclusive with --body-file)")
	cmd.Flags().StringVar(&bodyFile, "body-file", "", "Path to a Markdown body file, or '-' for stdin (mutually exclusive with --body)")
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
	title, body, bodyFile, scopeOverride, idempotencyKey, meta string
	dryRun, asJSON, ndjson, human                              bool
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
	// reads env).
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
			if re.DomainID == "WRKQ_CONFLICT" && isIdempotencyMismatch(re) {
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
	claims := &stdinClaims{}
	bodySelected := cmd.Flags().Changed("body")
	bodyFileSelected := cmd.Flags().Changed("body-file")
	if bodySelected == bodyFileSelected {
		return "", "", nil, nil, fmt.Errorf("exactly one body source is required: use either --body or --body-file")
	}

	title := strings.TrimSpace(f.title)
	if title == "" {
		return "", "", nil, nil, fmt.Errorf("title is required (use -t or --title)")
	}

	var bodyBytes []byte
	var err error
	if bodySelected {
		bodyBytes = []byte(f.body)
	} else {
		bodyFile := strings.TrimSpace(f.bodyFile)
		if bodyFile == "-" {
			bodyBytes, err = readStdinValue("--body-file", cmd.InOrStdin(), claims)
			if err != nil {
				return "", "", nil, nil, err
			}
		} else if bodyFile != "" {
			bodyBytes, err = os.ReadFile(bodyFile)
			if err != nil {
				return "", "", nil, nil, fmt.Errorf("failed to read --body-file %s: %w", bodyFile, err)
			}
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
	switch mode {
	case handoffOutputHuman:
		writeHandoffDiagnostics(cmd.ErrOrStderr(), out.Diagnostics)
		fmt.Fprintf(stdout, "ID\tScope\tStatus\tTitle\tCreated At\n")
		fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n",
			out.Handoff.ID, out.Handoff.ScopeRef, out.Handoff.Status, out.Handoff.Title,
			style.FormatLocalTime(out.Handoff.CreatedAt))
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
		return writeHandoffIndentedJSON(stdout, out)
	}
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
