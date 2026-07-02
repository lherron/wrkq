package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

// ---- helpers --------------------------------------------------------------

type handoffAckExecResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

func runHandoffAckCLI(t *testing.T, args []string) handoffAckExecResult {
	t.Helper()
	t.Setenv("AGENT_SCOPE_REF", "")

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetIn(strings.NewReader(""))

	err := rootCmd.Execute()
	result := handoffAckExecResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		code:   ExitCodeForError(err),
		err:    err,
	}

	rootCmd.SetArgs(nil)
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	rootCmd.SetIn(os.Stdin)
	_ = rootCmd.PersistentFlags().Set("db", "")
	_ = rootCmd.PersistentFlags().Set("as", "")
	_ = rootCmd.PersistentFlags().Set("project", "")
	return result
}

func decodeHandoffAckOutput(t *testing.T, raw string) handoffAckOutput {
	t.Helper()
	var out handoffAckOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode JSON output: %v\n%s", err, raw)
	}
	return out
}

func decodeHandoffAckError(t *testing.T, raw string) handoffErrorOutput {
	t.Helper()
	var out handoffErrorOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode JSON error: %v\n%s", err, raw)
	}
	return out
}

// setHandoffAckEnv configures the runtime env so the acknowledge command can
// resolve the acting agent (ASP_AGENT_ID is the only required value).
func setHandoffAckEnv(t *testing.T) {
	t.Helper()
	t.Setenv("ASP_SCOPE_REF", "agent:curly:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "curly")
	t.Setenv("ASP_PROJECT", "wrkq")
	t.Setenv("ASP_HANDLE", "")
}

// ---- tests ----------------------------------------------------------------

func TestHandoffAckHappyPath(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	setHandoffAckEnv(t)

	created := seedHandoff(t, database, "curly", "wrkq", "carry forward", "body")

	res := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID})
	if res.code != 0 || res.err != nil {
		t.Fatalf("ack failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffAckOutput(t, res.stdout)
	if out.DryRun {
		t.Errorf("dry_run should be false on real ack")
	}
	if out.Handoff.ID != created.ID {
		t.Fatalf("ID=%q want %q", out.Handoff.ID, created.ID)
	}
	if out.Handoff.Status != store.HandoffStatusAcknowledged {
		t.Errorf("Status=%q, want acknowledged", out.Handoff.Status)
	}
	if out.Handoff.AcknowledgedAt == nil {
		t.Errorf("AcknowledgedAt should be set")
	}
	if out.Handoff.AcknowledgementNote != nil {
		t.Errorf("note should be nil when --note omitted, got %v", *out.Handoff.AcknowledgementNote)
	}
	if out.Handoff.ETag != created.ETag+1 {
		t.Errorf("etag=%d, want %d", out.Handoff.ETag, created.ETag+1)
	}
	if !out.Handoff.UpdatedAt.After(created.UpdatedAt) && !out.Handoff.UpdatedAt.Equal(created.UpdatedAt) {
		t.Errorf("updated_at should advance or equal, got %v vs %v", out.Handoff.UpdatedAt, created.UpdatedAt)
	}

	// Verify persistence via fresh read
	var status string
	if err := database.QueryRow(`SELECT status FROM handoffs WHERE id = ?`, created.ID).Scan(&status); err != nil {
		t.Fatalf("verify db status: %v", err)
	}
	if status != store.HandoffStatusAcknowledged {
		t.Errorf("db status=%q, want acknowledged", status)
	}
}

func TestHandoffAckWithNoteStored(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	setHandoffAckEnv(t)

	created := seedHandoff(t, database, "curly", "wrkq", "with note", "body")

	res := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID, "--note", "loaded next session"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("ack with note failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffAckOutput(t, res.stdout)
	if out.Handoff.AcknowledgementNote == nil || *out.Handoff.AcknowledgementNote != "loaded next session" {
		t.Fatalf("note=%v want %q", out.Handoff.AcknowledgementNote, "loaded next session")
	}

	// Confirm note is persisted (not just present in response).
	var dbNote *string
	if err := database.QueryRow(`SELECT acknowledgement_note FROM handoffs WHERE id = ?`, created.ID).Scan(&dbNote); err != nil {
		t.Fatalf("read db note: %v", err)
	}
	if dbNote == nil || *dbNote != "loaded next session" {
		t.Errorf("db note=%v, want %q", dbNote, "loaded next session")
	}
}

func TestHandoffAckEmptyNoteIsValidationError(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	setHandoffAckEnv(t)

	created := seedHandoff(t, database, "curly", "wrkq", "blank note", "body")

	res := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID, "--note", "   "})
	if res.code != 1 {
		t.Fatalf("expected exit 1 for blank --note, got %d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	errOut := decodeHandoffAckError(t, res.stderr)
	if errOut.Error.Code != "validation_error" {
		t.Errorf("expected validation_error, got %s", errOut.Error.Code)
	}
}

func TestHandoffAckDryRunDoesNotPersist(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	setHandoffAckEnv(t)

	created := seedHandoff(t, database, "curly", "wrkq", "dry run", "body")

	res := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID, "--dry-run", "--note", "projected"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("dry-run ack failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffAckOutput(t, res.stdout)
	if !out.DryRun {
		t.Errorf("dry_run should be true")
	}
	// The projected state should reflect acknowledged + note.
	if out.Handoff.Status != store.HandoffStatusAcknowledged {
		t.Errorf("projected status should be acknowledged, got %s", out.Handoff.Status)
	}
	if out.Handoff.AcknowledgementNote == nil || *out.Handoff.AcknowledgementNote != "projected" {
		t.Errorf("projected note missing: %v", out.Handoff.AcknowledgementNote)
	}

	// DB must still show pending and the original etag.
	var status string
	var etag int64
	if err := database.QueryRow(`SELECT status, etag FROM handoffs WHERE id = ?`, created.ID).Scan(&status, &etag); err != nil {
		t.Fatalf("verify db: %v", err)
	}
	if status != store.HandoffStatusPending {
		t.Errorf("db status=%q, want pending (dry-run should not persist)", status)
	}
	if etag != created.ETag {
		t.Errorf("etag=%d, want %d (dry-run should not bump etag)", etag, created.ETag)
	}
}

func TestHandoffAckAlreadyAcknowledgedExit5(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	setHandoffAckEnv(t)

	created := seedHandoff(t, database, "curly", "wrkq", "double ack", "body")

	first := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID})
	if first.code != 0 {
		t.Fatalf("first ack failed code=%d stdout=%s stderr=%s", first.code, first.stdout, first.stderr)
	}

	second := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID})
	if second.code != 5 {
		t.Fatalf("second ack expected exit 5, got %d stdout=%s stderr=%s", second.code, second.stdout, second.stderr)
	}
	if strings.TrimSpace(second.stdout) != "" {
		t.Errorf("expected empty stdout on error, got %q", second.stdout)
	}
	errOut := decodeHandoffAckError(t, second.stderr)
	if errOut.Error.Code != "already_acknowledged" {
		t.Errorf("error code=%q, want already_acknowledged", errOut.Error.Code)
	}
	if errOut.Error.HandoffID != created.ID {
		t.Errorf("error handoff_id=%q, want %q", errOut.Error.HandoffID, created.ID)
	}
	if !strings.Contains(errOut.Error.Message, "acknowledged_at=") {
		t.Errorf("expected acknowledged_at hint in message, got %q", errOut.Error.Message)
	}
}

func TestHandoffAckNotFoundExit4(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	setHandoffAckEnv(t)

	res := runHandoffAckCLI(t, []string{"handoff", "acknowledge", "H-99999"})
	if res.code != 4 {
		t.Fatalf("expected exit 4, got %d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	errOut := decodeHandoffAckError(t, res.stderr)
	if errOut.Error.Code != "handoff_not_found" {
		t.Errorf("error code=%q want handoff_not_found", errOut.Error.Code)
	}
	if errOut.Error.HandoffID != "H-99999" {
		t.Errorf("handoff_id=%q want H-99999", errOut.Error.HandoffID)
	}
	if errOut.Error.Example == "" {
		t.Errorf("expected corrective example, got empty")
	}
}

func TestHandoffAckIfMatchMismatchExit6(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	setHandoffAckEnv(t)

	created := seedHandoff(t, database, "curly", "wrkq", "etag check", "body")

	// Pending handoff has etag=1; pass --if-match 999 to force mismatch.
	res := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID, "--if-match", "999"})
	if res.code != 6 {
		t.Fatalf("expected exit 6, got %d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	errOut := decodeHandoffAckError(t, res.stderr)
	if errOut.Error.Code != "etag_mismatch" {
		t.Errorf("error code=%q want etag_mismatch", errOut.Error.Code)
	}

	// Must not have mutated the row.
	var status string
	if err := database.QueryRow(`SELECT status FROM handoffs WHERE id = ?`, created.ID).Scan(&status); err != nil {
		t.Fatalf("verify db: %v", err)
	}
	if status != store.HandoffStatusPending {
		t.Errorf("status=%q want pending (etag mismatch must not mutate)", status)
	}
}

func TestHandoffAckOutputModes(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	setHandoffAckEnv(t)

	created := seedHandoff(t, database, "curly", "wrkq", "modes", "body")

	// Piped → JSON
	jsonRes := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID, "--dry-run"})
	if jsonRes.code != 0 || !strings.HasPrefix(strings.TrimSpace(jsonRes.stdout), "{") {
		t.Fatalf("default piped output should be JSON: code=%d stdout=%s", jsonRes.code, jsonRes.stdout)
	}

	// --human forces a confirmation line even when piped
	humanRes := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID, "--human", "--dry-run", "--note", "n"})
	if humanRes.code != 0 {
		t.Fatalf("--human failed code=%d stdout=%s stderr=%s", humanRes.code, humanRes.stdout, humanRes.stderr)
	}
	for _, want := range []string{"Acknowledged " + created.ID, "Status: acknowledged", "(dry run", `Note: "n"`} {
		if !strings.Contains(humanRes.stdout, want) {
			t.Errorf("human output missing %q:\n%s", want, humanRes.stdout)
		}
	}

	// --json + --human together = validation error (exit 1)
	conflict := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID, "--json", "--human"})
	if conflict.code != 1 {
		t.Fatalf("conflicting modes should exit 1, got %d stdout=%s stderr=%s", conflict.code, conflict.stdout, conflict.stderr)
	}
}

func TestHandoffAckExactIDFallsBackToRowWithoutRuntimeEnv(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "")
	t.Setenv("ASP_AGENT_ID", "")
	t.Setenv("ASP_HANDLE", "")
	t.Setenv("ASP_PROJECT", "")

	created := seedHandoff(t, database, "curly", "wrkq", "row fallback", "body")

	res := runHandoffAckCLI(t, []string{"handoff", "acknowledge", created.ID})
	if res.code != 0 || res.err != nil {
		t.Fatalf("row fallback ack failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffAckOutput(t, res.stdout)
	if out.Handoff.AcknowledgedByAgentID == nil || *out.Handoff.AcknowledgedByAgentID != "curly" {
		t.Fatalf("acknowledged_by_agent_id=%v want curly", out.Handoff.AcknowledgedByAgentID)
	}
	if out.Handoff.Status != store.HandoffStatusAcknowledged {
		t.Fatalf("status=%q want acknowledged", out.Handoff.Status)
	}
}

func TestHandoffAckExplicitAsUsesRowProjectWithoutRuntimeEnv(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "")
	t.Setenv("ASP_AGENT_ID", "")
	t.Setenv("ASP_HANDLE", "")
	t.Setenv("ASP_PROJECT", "")

	created := seedHandoff(t, database, "curly", "wrkq", "--as fallback", "body")

	res := runHandoffAckCLI(t, []string{"--as", "agent:curly", "handoff", "acknowledge", created.ID})
	if res.code != 0 || res.err != nil {
		t.Fatalf("--as row-project ack failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffAckOutput(t, res.stdout)
	if out.Handoff.AcknowledgedByPrincipalRef == nil || *out.Handoff.AcknowledgedByPrincipalRef != "agent:curly" {
		t.Fatalf("acknowledged_by_principal_ref=%v want agent:curly", out.Handoff.AcknowledgedByPrincipalRef)
	}
}

func TestHandoffAckMismatchedActorIsValidationError(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "")
	t.Setenv("ASP_AGENT_ID", "")
	t.Setenv("ASP_HANDLE", "")
	t.Setenv("ASP_PROJECT", "")

	created := seedHandoff(t, database, "curly", "wrkq", "wrong actor", "body")

	res := runHandoffAckCLI(t, []string{"--as", "agent:cody", "handoff", "acknowledge", created.ID})
	if res.code != 1 {
		t.Fatalf("expected exit 1 for mismatched actor, got %d stdout=%s stderr=%s", res.code, res.stdout, res.stderr)
	}
	errOut := decodeHandoffAckError(t, res.stderr)
	if errOut.Error.Code != "validation_error" {
		t.Errorf("expected validation_error, got %s", errOut.Error.Code)
	}
	if !strings.Contains(errOut.Error.Message, "ambiguous actor") || !strings.Contains(errOut.Error.Message, "cody") || !strings.Contains(errOut.Error.Message, "curly") {
		t.Errorf("expected actor conflict details in message, got %q", errOut.Error.Message)
	}
}
