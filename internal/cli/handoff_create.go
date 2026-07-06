package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/id"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

type handoffCreateOutput struct {
	Handoff          handoffJSON        `json:"handoff"`
	IdempotentReplay bool               `json:"idempotent_replay"`
	Diagnostics      []scope.Diagnostic `json:"diagnostics,omitempty"`
	DryRun           bool               `json:"dry_run,omitempty"`
}

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

type handoffErrorOutput struct {
	Error       structuredCLIError `json:"error"`
	Diagnostics []scope.Diagnostic `json:"diagnostics,omitempty"`
}

type structuredCLIError struct {
	Code      string `json:"code"`
	HandoffID string `json:"handoff_id,omitempty"`
	Message   string `json:"message"`
	Example   string `json:"example,omitempty"`
}

type handoffOutputMode string

const (
	handoffOutputJSON   handoffOutputMode = "json"
	handoffOutputNDJSON handoffOutputMode = "ndjson"
	handoffOutputHuman  handoffOutputMode = "human"
)

const handoffScopeExample = "wrkq handoff create --scope cody@wrkq -t 'Next steps' --body-file -"

func handoffModeFromSelection(sel outputSelection) handoffOutputMode {
	switch sel.Mode {
	case outputModeHuman, outputModeTable:
		return handoffOutputHuman
	case outputModeNDJSON:
		return handoffOutputNDJSON
	default:
		return handoffOutputJSON
	}
}

func resolveHandoffSelection(cmd *cobra.Command, shape outputShape, allowNDJSON bool) (outputSelection, error) {
	cfg, err := config.Load()
	if err != nil {
		return outputSelection{}, fmt.Errorf("failed to load config: %w", err)
	}
	allow := []outputMode{outputModeHuman, outputModeJSON}
	if allowNDJSON {
		allow = append(allow, outputModeNDJSON)
	}
	return resolveOutputMode(cmd, cfg, shape, outputResolveOptions{
		Allow:      allow,
		DefaultTTY: outputModeHuman,
	})
}

func runHandoffCreate(cmd *cobra.Command, _ []string) error {
	defer resetHandoffCreateFlags()

	stdout := cmd.OutOrStdout()
	stderr := cmd.ErrOrStderr()
	sel, modeErr := resolveHandoffSelection(cmd, outputShapeMutation, true)
	mode := handoffModeFromSelection(sel)
	if modeErr != nil {
		return writeHandoffCreateError(stderr, mode, 1, "validation_error", modeErr.Error(), nil, "")
	}

	title, body, meta, idemKey, validationErr := readHandoffCreateInputs(cmd)
	if validationErr != nil {
		return writeHandoffCreateError(stderr, mode, 1, "validation_error", validationErr.Error(), nil,
			"wrkq handoff create -t 'Next steps' --body-file notes.md")
	}

	resolved, diags, resolveErr := scope.Resolve(strings.TrimSpace(handoffCreateScope))
	if resolveErr != nil {
		return writeHandoffCreateError(stderr, mode, 2, "scope_unresolvable", resolveErr.Error(), diags, handoffScopeExample)
	}
	if err := scope.EnforceSelfScope(resolved, scope.RuntimeIdentity{AgentID: scope.ReadEnv().ASPAgentID}); err != nil {
		return writeHandoffCreateError(stderr, mode, 1, "self_scope_violation", err.Error(), diags, handoffScopeExample)
	}
	if resolved.ProjectID == "" {
		return writeHandoffCreateError(stderr, mode, 2, "scope_unresolvable",
			"handoff create requires an agent/project scope; pass --scope cody@wrkq or set ASP_SCOPE_REF=agent:cody:project:wrkq",
			diags, handoffScopeExample)
	}

	database, err := openHandoffCreateDB(cmd)
	if err != nil {
		return writeHandoffCreateError(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
	}
	defer func() { _ = database.Close() }()

	projectContainerUUID := lookupHandoffScopeRows(cmd.Context(), database.DB, resolved)
	agentPrincipalRef := "agent:" + resolved.AgentID
	args := store.CreateHandoffArgs{
		ScopeRef:              resolved.CanonicalRef,
		ScopeKind:             string(scope.KindProject),
		AgentID:               resolved.AgentID,
		ProjectID:             resolved.ProjectID,
		CreatedByAgentID:      resolved.AgentID,
		Title:                 title,
		Body:                  body,
		IdempotencyKey:        idemKey,
		Meta:                  meta,
		AgentPrincipalRef:     &agentPrincipalRef,
		ProjectContainerUUID:  projectContainerUUID,
		CreatedByPrincipalRef: agentPrincipalRef,
	}

	var result store.CreateHandoffResult
	if handoffCreateDryRun {
		result = store.CreateHandoffResult{Handoff: projectedHandoff(cmd.Context(), database.DB, args)}
	} else {
		tx, err := database.Begin()
		if err != nil {
			return writeHandoffCreateError(stderr, mode, 1, "runtime_error", fmt.Sprintf("failed to begin transaction: %v", err), diags, "")
		}
		defer func() { _ = tx.Rollback() }()

		result, err = store.CreateHandoff(cmd.Context(), tx, args)
		if err != nil {
			var mismatch *store.HandoffIdempotencyPayloadMismatchError
			if errors.As(err, &mismatch) {
				return writeHandoffCreateError(stderr, mode, 3, "idempotency_payload_mismatch", err.Error(), diags,
					"retry with the original title/body or use a new --idempotency-key")
			}
			return writeHandoffCreateError(stderr, mode, 1, "runtime_error", err.Error(), diags, "")
		}
		if err := tx.Commit(); err != nil {
			return writeHandoffCreateError(stderr, mode, 1, "runtime_error", fmt.Sprintf("failed to commit transaction: %v", err), diags, "")
		}
	}

	out := handoffCreateOutput{
		Handoff:          toHandoffJSON(result.Handoff),
		IdempotentReplay: result.IdempotentReplay,
		Diagnostics:      diags,
		DryRun:           handoffCreateDryRun,
	}
	if err := writeHandoffCreateOutput(stdout, stderr, mode, out); err != nil {
		return exitError(1, err)
	}
	return nil
}

func openHandoffCreateDB(cmd *cobra.Command) (*db.DB, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}
	if dbFlag := cmd.Flag("db"); dbFlag != nil {
		if dbPath := dbFlag.Value.String(); dbPath != "" {
			cfg.DBPath = dbPath
		}
	}
	if cfg.DBPath == "" {
		return nil, fmt.Errorf("database path not specified (use --db flag or set WRKQ_DB_PATH)")
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}
	applied, pending, err := database.MigrationStatus()
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("failed to check migration status: %w", err)
	}
	if len(pending) > 0 {
		if len(applied) > 0 {
			_ = database.Close()
			return nil, database.RequiresMigrationError()
		}
		if err := database.Migrate(); err != nil {
			_ = database.Close()
			return nil, fmt.Errorf("failed to initialize database: %w", err)
		}
	}
	return database, nil
}

func resetHandoffCreateFlags() {
	handoffCreateTitle = ""
	handoffCreateBodyFile = ""
	handoffCreateScope = ""
	handoffCreateProfile = ""
	handoffCreateIdempotencyKey = ""
	handoffCreateMeta = ""
	handoffCreateDryRun = false
	handoffCreateJSON = false
	handoffCreateNDJSON = false
	handoffCreateHuman = false
}

func readHandoffCreateInputs(cmd *cobra.Command) (string, string, *string, *string, error) {
	claims := &stdinClaims{}
	title := strings.TrimSpace(handoffCreateTitle)
	if title == "" {
		return "", "", nil, nil, fmt.Errorf("title is required (use -t or --title)")
	}

	bodyFile := strings.TrimSpace(handoffCreateBodyFile)
	if bodyFile == "" {
		return "", "", nil, nil, fmt.Errorf("body is required (use --body-file <path|->)")
	}
	bodyBytes, err := readHandoffBodyWithClaims(cmd, bodyFile, claims)
	if err != nil {
		return "", "", nil, nil, err
	}
	body := strings.TrimRightFunc(string(bodyBytes), func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
	if strings.TrimSpace(body) == "" {
		return "", "", nil, nil, fmt.Errorf("body cannot be empty")
	}

	var meta *string
	if strings.TrimSpace(handoffCreateMeta) != "" {
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(handoffCreateMeta), &obj); err != nil {
			return "", "", nil, nil, fmt.Errorf("invalid JSON object for --meta: %w", err)
		}
		trimmed := strings.TrimSpace(handoffCreateMeta)
		meta = &trimmed
	}

	var idemKey *string
	if strings.TrimSpace(handoffCreateIdempotencyKey) != "" {
		trimmed := strings.TrimSpace(handoffCreateIdempotencyKey)
		idemKey = &trimmed
	}
	return title, body, meta, idemKey, nil
}

func readHandoffBodyWithClaims(cmd *cobra.Command, bodyFile string, claims *stdinClaims) ([]byte, error) {
	if bodyFile == "-" {
		data, err := readStdinValue("--body-file", cmd.InOrStdin(), claims)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	data, err := os.ReadFile(bodyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read --body-file %s: %w", bodyFile, err)
	}
	return data, nil
}

func lookupHandoffScopeRows(ctx context.Context, db *sql.DB, resolved scope.ResolvedScope) *string {
	var containerUUID *string
	if resolved.ProjectID != "" {
		var uuid string
		if err := db.QueryRowContext(ctx, `SELECT uuid FROM containers WHERE slug = ? AND parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root') LIMIT 1`, resolved.ProjectID).Scan(&uuid); err == nil {
			containerUUID = &uuid
		}
	}
	return containerUUID
}

func projectedHandoff(ctx context.Context, db *sql.DB, args store.CreateHandoffArgs) store.Handoff {
	nextID := "H-00001"
	var nextSeq int
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) + 1 FROM handoffs`).Scan(&nextSeq); err == nil && nextSeq > 0 {
		nextID = id.FormatHandoff(nextSeq)
	}
	now := time.Now().UTC().Truncate(time.Second)
	return store.Handoff{
		UUID:                  uuid.New().String(),
		ID:                    nextID,
		ScopeRef:              args.ScopeRef,
		ScopeKind:             args.ScopeKind,
		AgentID:               args.AgentID,
		ProjectID:             args.ProjectID,
		AgentPrincipalRef:     args.AgentPrincipalRef,
		ProjectContainerUUID:  args.ProjectContainerUUID,
		CreatedByAgentID:      args.CreatedByAgentID,
		CreatedByPrincipalRef: args.CreatedByPrincipalRef,
		Title:                 args.Title,
		Body:                  args.Body,
		Status:                store.HandoffStatusPending,
		IdempotencyKey:        args.IdempotencyKey,
		Meta:                  args.Meta,
		ETag:                  1,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func writeHandoffCreateOutput(stdout, stderr io.Writer, mode handoffOutputMode, out handoffCreateOutput) error {
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
		encoder := json.NewEncoder(stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(out)
	}
}

func writeHandoffCreateError(stderr io.Writer, mode handoffOutputMode, exitCode int, code, message string, diags []scope.Diagnostic, example string) error {
	errOut := handoffErrorOutput{
		Error: structuredCLIError{
			Code:    code,
			Message: message,
			Example: example,
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
		encoder := json.NewEncoder(stderr)
		if mode == handoffOutputJSON {
			encoder.SetIndent("", "  ")
		}
		_ = encoder.Encode(errOut)
	}
	return exitErrorReported(exitCode, fmt.Errorf("%s: %s", code, message))
}

func toHandoffJSON(h store.Handoff) handoffJSON {
	return handoffJSON{
		UUID:                       h.UUID,
		ID:                         h.ID,
		ScopeRef:                   h.ScopeRef,
		ScopeKind:                  h.ScopeKind,
		AgentID:                    h.AgentID,
		ProjectID:                  h.ProjectID,
		AgentPrincipalRef:          h.AgentPrincipalRef,
		ProjectContainerUUID:       h.ProjectContainerUUID,
		CreatedByAgentID:           h.CreatedByAgentID,
		CreatedByPrincipalRef:      h.CreatedByPrincipalRef,
		Title:                      h.Title,
		Body:                       h.Body,
		Status:                     h.Status,
		IdempotencyKey:             h.IdempotencyKey,
		AcknowledgedAt:             h.AcknowledgedAt,
		AcknowledgedByAgentID:      h.AcknowledgedByAgentID,
		AcknowledgedByPrincipalRef: h.AcknowledgedByPrincipalRef,
		AcknowledgementNote:        h.AcknowledgementNote,
		Meta:                       h.Meta,
		ETag:                       h.ETag,
		CreatedAt:                  h.CreatedAt,
		UpdatedAt:                  h.UpdatedAt,
	}
}
