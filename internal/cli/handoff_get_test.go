package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

type handoffGetExecResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

func runHandoffGetCLI(t *testing.T, args []string) handoffGetExecResult {
	t.Helper()

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)
	rootCmd.SetIn(strings.NewReader(""))

	err := rootCmd.Execute()
	result := handoffGetExecResult{
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

func decodeHandoffGetOutput(t *testing.T, raw string) handoffJSON {
	t.Helper()
	var out handoffJSON
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode JSON output: %v\n%s", err, raw)
	}
	return out
}

func decodeHandoffGetError(t *testing.T, raw string) handoffErrorOutput {
	t.Helper()
	var out handoffErrorOutput
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("failed to decode JSON error: %v\n%s", err, raw)
	}
	return out
}

func TestHandoffGetByID(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())

	created := seedHandoff(t, database, "larry", "wrkq", "handoff title", "body text")

	res := runHandoffGetCLI(t, []string{"handoff", "get", created.ID})
	if res.code != 0 || res.err != nil {
		t.Fatalf("get failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffGetOutput(t, res.stdout)
	if out.ID != created.ID || out.UUID != created.UUID {
		t.Fatalf("unexpected handoff: %#v", out)
	}
	if out.Title != "handoff title" || out.Body != "body text" {
		t.Fatalf("unexpected content: %#v", out)
	}
	if !strings.Contains(res.stdout, `"acknowledged_at": null`) {
		t.Fatalf("expected nullable fields in JSON output, got:\n%s", res.stdout)
	}
}

func TestHandoffGetByUUID(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())

	created := seedHandoff(t, database, "larry", "wrkq", "uuid title", "uuid body")

	res := runHandoffGetCLI(t, []string{"handoff", "get", created.UUID, "--json"})
	if res.code != 0 || res.err != nil {
		t.Fatalf("get by UUID failed code=%d err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	out := decodeHandoffGetOutput(t, res.stdout)
	if out.ID != created.ID || out.UUID != created.UUID {
		t.Fatalf("unexpected handoff: %#v", out)
	}
}

func TestHandoffGetNotFoundStructuredExit4(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())

	res := runHandoffGetCLI(t, []string{"handoff", "get", "H-99999"})
	if res.code != 4 {
		t.Fatalf("not found code=%d, want 4 err=%v stdout=%s stderr=%s", res.code, res.err, res.stdout, res.stderr)
	}
	if strings.TrimSpace(res.stdout) != "" {
		t.Fatalf("expected empty stdout, got %q", res.stdout)
	}
	errOut := decodeHandoffGetError(t, res.stderr)
	if errOut.Error.Code != "handoff_not_found" {
		t.Fatalf("error code=%q", errOut.Error.Code)
	}
	if errOut.Error.HandoffID != "H-99999" {
		t.Fatalf("handoff_id=%q", errOut.Error.HandoffID)
	}
	if errOut.Error.Example == "" || !strings.Contains(errOut.Error.Message, "H-99999") {
		t.Fatalf("missing corrective error details: %#v", errOut.Error)
	}
}

func TestHandoffGetOutputModes(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())

	created := seedHandoff(t, database, "larry", "wrkq", "modes", "mode body")

	jsonDefault := runHandoffGetCLI(t, []string{"handoff", "get", created.ID})
	if jsonDefault.code != 0 || !strings.HasPrefix(strings.TrimSpace(jsonDefault.stdout), "{") {
		t.Fatalf("default piped output should be JSON: code=%d stdout=%s stderr=%s", jsonDefault.code, jsonDefault.stdout, jsonDefault.stderr)
	}

	human := runHandoffGetCLI(t, []string{"handoff", "get", created.ID, "--human"})
	if human.code != 0 {
		t.Fatalf("--human failed code=%d stdout=%s stderr=%s", human.code, human.stdout, human.stderr)
	}
	for _, want := range []string{"ID: " + created.ID, "Scope: agent:larry:project:wrkq", "Status: pending", "Body:\n---\nmode body"} {
		if !strings.Contains(human.stdout, want) {
			t.Fatalf("human output missing %q:\n%s", want, human.stdout)
		}
	}

	conflict := runHandoffGetCLI(t, []string{"handoff", "get", created.ID, "--json", "--human"})
	if conflict.code != 1 {
		t.Fatalf("conflicting modes should exit 1, got %d stdout=%s stderr=%s", conflict.code, conflict.stdout, conflict.stderr)
	}
}

func TestHandoffGetAcknowledgedFields(t *testing.T) {
	database, _ := setupTestEnv(t)
	t.Setenv("WRKQ_DB_PATH", database.Path())

	created := seedHandoff(t, database, "larry", "wrkq", "acked", "acked body")
	note := "loaded into next session"
	acknowledged, err := store.AcknowledgeHandoff(context.Background(), database, created.ID, store.AcknowledgeHandoffArgs{
		ActorAgentID: "larry",
		Note:         &note,
	})
	if err != nil {
		t.Fatalf("acknowledge failed: %v", err)
	}

	jsonRes := runHandoffGetCLI(t, []string{"handoff", "get", acknowledged.ID, "--json"})
	if jsonRes.code != 0 {
		t.Fatalf("json get failed code=%d stdout=%s stderr=%s", jsonRes.code, jsonRes.stdout, jsonRes.stderr)
	}
	out := decodeHandoffGetOutput(t, jsonRes.stdout)
	if out.Status != store.HandoffStatusAcknowledged || out.AcknowledgedAt == nil {
		t.Fatalf("acknowledgement fields missing from JSON: %#v", out)
	}
	if out.AcknowledgedByAgentID == nil || *out.AcknowledgedByAgentID != "larry" {
		t.Fatalf("acknowledged_by_agent_id missing: %#v", out)
	}
	if out.AcknowledgementNote == nil || *out.AcknowledgementNote != note {
		t.Fatalf("acknowledgement_note missing: %#v", out)
	}

	human := runHandoffGetCLI(t, []string{"handoff", "get", acknowledged.ID, "--human"})
	if human.code != 0 {
		t.Fatalf("human get failed code=%d stdout=%s stderr=%s", human.code, human.stdout, human.stderr)
	}
	for _, want := range []string{"Acknowledged At:", "Acknowledged By Agent ID: larry", "Acknowledgement Note: " + note} {
		if !strings.Contains(human.stdout, want) {
			t.Fatalf("human output missing %q:\n%s", want, human.stdout)
		}
	}
}
