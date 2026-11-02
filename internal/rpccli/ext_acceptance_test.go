//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtCLIIndependentFenceAndFiltering(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	run := func(input string, args ...string) (string, error) {
		t.Helper()
		cmd := NewRootCmd()
		cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", "agent:test-user"}, args...))
		cmd.SetIn(strings.NewReader(input))
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		err := cmd.Execute()
		return out.String(), err
	}
	must := func(input string, args ...string) string {
		t.Helper()
		out, err := run(input, args...)
		if err != nil {
			t.Fatalf("%v: %v\n%s", args, err, out)
		}
		return out
	}
	before := must("", "cat", taskID, "--json", "--one")
	out := must(`{"rev":1}`, "ext", "put", taskID, "acp", "request", "main", "--body", "-", "--create")
	if !strings.Contains(out, `"created": true`) {
		t.Fatalf("create: %s", out)
	}
	if _, err := run("", "ext", "put", taskID, "acp", "request", "main", "--body", "{}", "--create"); err == nil {
		t.Fatal("duplicate --create accepted")
	}
	bodyPath := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(bodyPath, []byte(`{"rev":2}`), 0600); err != nil {
		t.Fatal(err)
	}
	must("", "ext", "put", taskID, "acp", "request", "main", "--body", "@"+bodyPath, "--if-match", "1")
	if _, err := run("", "ext", "put", taskID, "acp", "request", "main", "--body", "{}", "--if-match", "1"); err == nil {
		t.Fatal("stale etag accepted")
	}
	out = must("", "ext", "get", taskID, "acp", "request", "main")
	if !strings.Contains(out, `"etag": 2`) {
		t.Fatalf("show: %s", out)
	}
	for _, filter := range [][]string{{"--ns", "absent"}, {"--kind", "absent"}, {"--states", "absent"}} {
		out = must("", append([]string{"ext", "list", taskID}, filter...)...)
		var page struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal([]byte(out), &page); err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 0 {
			t.Fatalf("filter %v did not bite: %s", filter, out)
		}
	}
	after := must("", "cat", taskID, "--json", "--one")
	var a, b map[string]json.RawMessage
	if err := json.Unmarshal([]byte(before), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(after), &b); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"etag", "updated_at"} {
		if string(a[field]) != string(b[field]) {
			t.Errorf("extension changed task %s: %s -> %s", field, a[field], b[field])
		}
	}
	must("", "ext", "delete", taskID, "acp", "request", "main", "--if-match", "2")
}

func TestWorkstreamCLICoversRoundTrip(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	runCommentCLI(t, dbPath, "set", taskID, "--kind", "workstream", "--covers", `{"tasks":["`+taskID+`"]}`)
	out := runCommentCLI(t, dbPath, "cat", taskID, "--output", "raw")
	if !strings.Contains(out, `covers: {"tasks":["`+taskID+`"]}`) {
		t.Fatalf("cat missing coverage: %s", out)
	}
	runCommentCLI(t, dbPath, "set", taskID, "--covers", "null", "--kind", "task")
}
