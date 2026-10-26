//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// Completing a parent reports, in open_subtasks, only the named subtasks that
// are still unfinished (open, draft) — not the completed or cancelled ones.
func TestCompletingAParentReportsOnlyItsUnfinishedSubtasks(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	run := func(args ...string) string {
		t.Helper()
		cmd := NewRootCmdFor("wrkq")
		cmd.SetArgs(append([]string{"--db", dbPath, "--project", "rpccli-test-proj", "--principal-ref", "agent:subtask-test"}, args...))
		var out, errOut bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("wrkq %s: %v\n%s", strings.Join(args, " "), err, errOut.String())
		}
		return out.String()
	}

	var created []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(run("touch", "parent", "-t", "Parent", "--json")), &created); err != nil || len(created) != 1 {
		t.Fatalf("decode parent: %v (%d rows)", err, len(created))
	}
	parent := created[0]
	for _, sub := range []struct{ slug, state string }{
		{"preview", "open"}, {"diagram", "completed"}, {"draft", "draft"}, {"cancel", "cancelled"},
	} {
		run("touch", parent.ID+"."+sub.slug, "--subtask", "-t", sub.slug, "--state", sub.state)
	}

	var result struct {
		Succeeded    int      `json:"succeeded"`
		OpenSubtasks []string `json:"open_subtasks"`
	}
	out := run("set", parent.ID, "--state", "completed", "--json")
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode completion: %v\n%s", err, out)
	}
	want := []string{parent.ID + ".draft", parent.ID + ".preview"} // server lists subtasks by id
	if result.Succeeded != 1 || !reflect.DeepEqual(result.OpenSubtasks, want) {
		t.Fatalf("completion = succeeded %d open_subtasks %q, want 1 and %q", result.Succeeded, result.OpenSubtasks, want)
	}
}
