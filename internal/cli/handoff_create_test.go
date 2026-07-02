package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type handoffCreateExecResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

func runHandoffCreateCLI(t *testing.T, args []string, stdin string) handoffCreateExecResult {
	t.Helper()
	t.Setenv("AGENT_SCOPE_REF", "")

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetIn(strings.NewReader(stdin))

	err := rootCmd.Execute()
	result := handoffCreateExecResult{
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

func decodeHandoffCreateOutput(t *testing.T, raw string) handoffCreateOutput {
	t.Helper()
	var out handoffCreateOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode JSON output: %v\n%s", err, raw)
	}
	return out
}

func decodeHandoffCreateError(t *testing.T, raw string) handoffErrorOutput {
	t.Helper()
	var out handoffErrorOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode JSON error: %v\n%s", err, raw)
	}
	return out
}

func TestHandoffCreateHappyPathUsesEnvScope(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq:task:T-01597")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")
	t.Setenv("ASP_HANDLE", "")

	res := runHandoffCreateCLI(t, []string{"handoff", "create", "-t", "Carry forward", "--body-file", "-"}, "body text\n\n")
	if res.code != 0 || res.err != nil {
		t.Fatalf("create failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}

	out := decodeHandoffCreateOutput(t, res.stdout)
	if out.Handoff.ID != "H-00001" {
		t.Fatalf("ID=%q, want H-00001", out.Handoff.ID)
	}
	if out.Handoff.ScopeRef != "agent:larry:project:wrkq" {
		t.Errorf("ScopeRef=%q", out.Handoff.ScopeRef)
	}
	if out.Handoff.ScopeKind != "project" || out.Handoff.Status != "pending" {
		t.Errorf("unexpected scope/status: %#v", out.Handoff)
	}
	if out.Handoff.Body != "body text" {
		t.Errorf("body should be right-trimmed, got %q", out.Handoff.Body)
	}

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM handoffs WHERE scope_ref = ? AND status = 'pending'`, "agent:larry:project:wrkq").Scan(&count); err != nil {
		t.Fatalf("failed to count handoffs: %v", err)
	}
	if count != 1 {
		t.Fatalf("handoff count=%d, want 1", count)
	}
}

func TestHandoffCreateIdempotencyReplayAndMismatch(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")

	args := []string{"handoff", "create", "-t", "Retry me", "--body-file", "-", "--idempotency-key", "retry-1"}
	first := runHandoffCreateCLI(t, args, "same body")
	if first.code != 0 || first.err != nil {
		t.Fatalf("first create failed code=%d err=%v stdout=%s stderr=%s", first.code, first.err, first.stdout, first.stderr)
	}
	firstOut := decodeHandoffCreateOutput(t, first.stdout)

	second := runHandoffCreateCLI(t, args, "same body")
	if second.code != 0 || second.err != nil {
		t.Fatalf("second create failed code=%d err=%v stdout=%s stderr=%s", second.code, second.err, second.stdout, second.stderr)
	}
	secondOut := decodeHandoffCreateOutput(t, second.stdout)
	if !secondOut.IdempotentReplay {
		t.Fatalf("second create idempotent_replay=false")
	}
	if secondOut.Handoff.ID != firstOut.Handoff.ID || secondOut.Handoff.UUID != firstOut.Handoff.UUID {
		t.Fatalf("replay returned different handoff: first=%s/%s second=%s/%s",
			firstOut.Handoff.ID, firstOut.Handoff.UUID, secondOut.Handoff.ID, secondOut.Handoff.UUID)
	}

	mismatch := runHandoffCreateCLI(t, args, "different body")
	if mismatch.code != 3 {
		t.Fatalf("mismatch code=%d, want 3 err=%v stdout=%s stderr=%s", mismatch.code, mismatch.err, mismatch.stdout, mismatch.stderr)
	}
	errOut := decodeHandoffCreateError(t, mismatch.stderr)
	if errOut.Error.Code != "idempotency_payload_mismatch" {
		t.Fatalf("error code=%q", errOut.Error.Code)
	}
}

func TestHandoffCreateSelfScopeGuard(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")

	res := runHandoffCreateCLI(t, []string{"handoff", "create", "--scope", "cody@wrkq", "-t", "Wrong agent", "--body-file", "-"}, "body")
	if res.code != 1 {
		t.Fatalf("code=%d, want 1 err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	errOut := decodeHandoffCreateError(t, res.stderr)
	if errOut.Error.Code != "self_scope_violation" {
		t.Fatalf("error code=%q", errOut.Error.Code)
	}
}

func TestHandoffCreateDryRunDoesNotWrite(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")

	res := runHandoffCreateCLI(t, []string{"handoff", "create", "-t", "Preview", "--body-file", "-", "--dry-run"}, "body")
	if res.code != 0 || res.err != nil {
		t.Fatalf("dry-run failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffCreateOutput(t, res.stdout)
	if !out.DryRun {
		t.Fatalf("dry_run=false")
	}
	if out.Handoff.Status != "pending" || out.Handoff.ID != "H-00001" {
		t.Fatalf("unexpected projected handoff: %#v", out.Handoff)
	}

	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM handoffs`).Scan(&count); err != nil {
		t.Fatalf("failed to count handoffs: %v", err)
	}
	if count != 0 {
		t.Fatalf("dry-run wrote %d handoffs", count)
	}
}

func TestHandoffCreateOutputModes(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")

	jsonDefault := runHandoffCreateCLI(t, []string{"handoff", "create", "-t", "JSON default", "--body-file", "-"}, "body")
	if jsonDefault.code != 0 || !strings.HasPrefix(strings.TrimSpace(jsonDefault.stdout), "{") {
		t.Fatalf("default piped output should be JSON: code=%d stdout=%s stderr=%s", jsonDefault.code, jsonDefault.stdout, jsonDefault.stderr)
	}

	human := runHandoffCreateCLI(t, []string{"handoff", "create", "-t", "Human", "--body-file", "-", "--human"}, "body")
	if human.code != 0 || !strings.Contains(human.stdout, "ID\tScope\tStatus\tTitle\tCreated At") {
		t.Fatalf("--human did not produce table: code=%d stdout=%s stderr=%s", human.code, human.stdout, human.stderr)
	}

	ndjson := runHandoffCreateCLI(t, []string{"handoff", "create", "-t", "NDJSON", "--body-file", "-", "--ndjson"}, "body")
	if ndjson.code != 0 {
		t.Fatalf("--ndjson failed: code=%d stdout=%s stderr=%s", ndjson.code, ndjson.stdout, ndjson.stderr)
	}
	if strings.Count(strings.TrimSpace(ndjson.stdout), "\n") != 0 {
		t.Fatalf("--ndjson should write one line, got %q", ndjson.stdout)
	}
	_ = decodeHandoffCreateOutput(t, ndjson.stdout)
}

func TestHandoffCreateBodyFilePath(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")

	bodyPath := filepath.Join(t.TempDir(), "body.md")
	if err := os.WriteFile(bodyPath, []byte("from file\n"), 0644); err != nil {
		t.Fatalf("failed to write body file: %v", err)
	}

	res := runHandoffCreateCLI(t, []string{"handoff", "create", "-t", "File body", "--body-file", bodyPath}, "")
	if res.code != 0 || res.err != nil {
		t.Fatalf("create failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffCreateOutput(t, res.stdout)
	if out.Handoff.Body != "from file" {
		t.Fatalf("body=%q", out.Handoff.Body)
	}
}

func TestHandoffCreateInitializesFreshTempDB(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "fresh.db")
	t.Setenv("WRKQ_DB_PATH", dbPath)
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")

	res := runHandoffCreateCLI(t, []string{"handoff", "create", "-t", "Fresh DB", "--body-file", "-"}, "body")
	if res.code != 0 || res.err != nil {
		t.Fatalf("create against fresh DB failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffCreateOutput(t, res.stdout)
	if out.Handoff.ID != "H-00001" || out.Handoff.ScopeRef != "agent:larry:project:wrkq" {
		t.Fatalf("unexpected handoff from fresh DB: %#v", out.Handoff)
	}
}

func TestHandoffCreateMissingInputErrors(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())
	t.Setenv("ASP_SCOPE_REF", "agent:larry:project:wrkq")
	t.Setenv("ASP_AGENT_ID", "larry")
	t.Setenv("ASP_PROJECT", "wrkq")

	missingTitle := runHandoffCreateCLI(t, []string{"handoff", "create", "--body-file", "-"}, "body")
	if missingTitle.code != 1 {
		t.Fatalf("missing title code=%d stdout=%s stderr=%s", missingTitle.code, missingTitle.stdout, missingTitle.stderr)
	}
	titleErr := decodeHandoffCreateError(t, missingTitle.stderr)
	if !strings.Contains(titleErr.Error.Message, "title is required") {
		t.Fatalf("unexpected title error: %#v", titleErr.Error)
	}

	missingBody := runHandoffCreateCLI(t, []string{"handoff", "create", "-t", "No body"}, "")
	if missingBody.code != 1 {
		t.Fatalf("missing body code=%d stdout=%s stderr=%s", missingBody.code, missingBody.stdout, missingBody.stderr)
	}
	bodyErr := decodeHandoffCreateError(t, missingBody.stderr)
	if !strings.Contains(bodyErr.Error.Message, "body is required") {
		t.Fatalf("unexpected body error: %#v", bodyErr.Error)
	}
}
