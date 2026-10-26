//go:build wrkq_local

package rpccli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/style"
)

// `tree --pretty` shows each task's priority after its title; with colour on,
// each priority carries its own colour (P1 stop, P2 open, P3 wip, P4 dim).
func TestTreeHumanDisplaysTaskPriority(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	previousColor := style.ColorEnabled
	t.Cleanup(func() { style.ColorEnabled = previousColor })
	tree := func() string {
		t.Helper()
		cmd := NewRootCmdFor("wrkq")
		cmd.SetArgs([]string{"--db", dbPath, "--project", "rpccli-test-proj", "--principal-ref", "agent:tree-test", "tree", "--pretty", "--all"})
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("tree --pretty --all: %v\n%s", err, output.String())
		}
		return output.String()
	}

	style.ColorEnabled = false
	// Address then meaning: ID, slug, title, then the priority this test is about.
	want := fmt.Sprintf("%s rpccli-smoke-task rpccli smoke ✓ \"task\" P1 <completed>", taskID)
	if out := tree(); !strings.Contains(out, want) {
		t.Fatalf("human tree missing task priority %q:\n%s", want, out)
	}

	for p := 2; p <= 4; p++ {
		cmd := NewRootCmdFor("wrkq")
		cmd.SetArgs([]string{"--db", dbPath, "--project", "rpccli-test-proj", "--principal-ref", "agent:tree-test",
			"touch", fmt.Sprintf("prio-%d", p), "-t", fmt.Sprintf("Prio %d", p), "--priority", fmt.Sprint(p)})
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("touch prio-%d: %v\n%s", p, err, output.String())
		}
	}
	style.ColorEnabled = true
	out := tree()
	for _, tc := range []struct {
		slug     string
		priority int
		color    string
	}{
		{slug: "rpccli-smoke-task", priority: 1, color: style.ColStateStop},
		{slug: "prio-2", priority: 2, color: style.ColStateOpen},
		{slug: "prio-3", priority: 3, color: style.ColStateWIP},
		{slug: "prio-4", priority: 4, color: style.ColDim},
	} {
		segment := fmt.Sprintf("\033[%smP%d\033[0m", tc.color, tc.priority)
		found := false
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, tc.slug) {
				found = true
				if !strings.Contains(line, segment) {
					t.Errorf("P%d row %q lacks colour segment %q", tc.priority, line, segment)
				}
			}
		}
		if !found {
			t.Errorf("tree has no row for %s:\n%s", tc.slug, out)
		}
	}
}

// Priorities come from a paginated bulk read: a task on the last page and an
// enrolled task from another project (an external backlink, read by its own
// show) still render their Pn.
func TestTreeShowsPriorityPastTheFirstPageAndOnEnrolledForeignTasks(t *testing.T) {
	f := newCampaignCLIFixture(t)
	database := f.openDB(t)
	// The fillers live in a plain directory: campaign residents are also
	// eligible for the per-task fallback, which would hide a lost page.
	plain := createDirectory(t, database, "plain", f.projectAUUID)
	for i := 0; i < treePriorityPageSize; i++ {
		createCampaignFixtureTask(t, database, fmt.Sprintf("filler-%03d", i), fmt.Sprintf("Filler %d", i), plain, "open")
	}
	_ = database.Close()
	f.exec(t, "UPDATE tasks SET priority = 1 WHERE uuid = ?", f.enrolledUUID)
	f.enroll(t, f.campaignAUUID, f.enrolledUUID)

	previousColor := style.ColorEnabled
	style.ColorEnabled = false
	t.Cleanup(func() { style.ColorEnabled = previousColor })
	out := f.mustRun(t, "--project", "campaign-cli-a", "tree", "--pretty")

	taskRow := regexp.MustCompile(`T-\d{5} \S+`)
	withPriority := regexp.MustCompile(`T-\d{5} .* P[1-4] <`)
	rows := 0
	for _, line := range strings.Split(out, "\n") {
		if !taskRow.MatchString(line) {
			continue
		}
		rows++
		if !withPriority.MatchString(line) {
			t.Errorf("task row without a priority: %q", line)
		}
		if strings.Contains(line, "enrolled-member") && (!strings.Contains(line, " P1 <") || !strings.Contains(line, "↗ campaign-cli-b")) {
			t.Errorf("enrolled foreign task row = %q, want P1 and the ↗ campaign-cli-b backlink", line)
		}
	}
	if want := treePriorityPageSize + 2; rows != want {
		t.Fatalf("tree rendered %d task rows, want %d (resident + enrolled + %d fillers)", rows, want, treePriorityPageSize)
	}
}

type treePriorityTransport struct {
	call func(context.Context, string, any) (json.RawMessage, error)
}

func (t *treePriorityTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return t.call(ctx, method, params)
}

func (t *treePriorityTransport) Close() error {
	return nil
}

func TestHydrateTreePrioritiesDegradesGracefully(t *testing.T) {
	var calls []string
	tr := &treePriorityTransport{
		call: func(_ context.Context, method string, _ any) (json.RawMessage, error) {
			calls = append(calls, method)
			return nil, errors.New("transient read failure")
		},
	}
	normal := &treeWireNode{Type: "task", UUID: "normal"}
	external := &treeWireNode{Type: "task", UUID: "external", ExternalPath: "campaign:other"}

	hydrateTreePriorities(context.Background(), tr, "project", []*treeWireNode{normal, external}, false, false)

	if normal.Priority != 0 || external.Priority != 0 {
		t.Fatalf("failed reads assigned priorities: normal=%d external=%d", normal.Priority, external.Priority)
	}
	if got, want := strings.Join(calls, ","), "wrkq.task.list,wrkq.task.show"; got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
}
