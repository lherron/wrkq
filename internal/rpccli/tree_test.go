package rpccli

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/style"
)

func TestTreeHumanDisplaysTaskPriority(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	previousColor := style.ColorEnabled
	style.ColorEnabled = false
	t.Cleanup(func() { style.ColorEnabled = previousColor })

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{"--db", dbPath, "--project", "rpccli-test-proj", "tree", "--pretty", "--all"})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("tree --pretty --all: %v\n%s", err, output.String())
	}

	want := fmt.Sprintf("%s rpccli smoke ✓ \"task\" P1 <completed>", taskID)
	if !strings.Contains(output.String(), want) {
		t.Fatalf("human tree missing task priority %q:\n%s", want, output.String())
	}
}

func TestFormatTreeHumanNodeColorCodesPriority(t *testing.T) {
	previousColor := style.ColorEnabled
	style.ColorEnabled = true
	t.Cleanup(func() { style.ColorEnabled = previousColor })

	tests := []struct {
		priority int
		color    string
	}{
		{priority: 1, color: style.ColStateStop},
		{priority: 2, color: style.ColStateOpen},
		{priority: 3, color: style.ColStateWIP},
		{priority: 4, color: style.ColDim},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("P%d", tt.priority), func(t *testing.T) {
			got := formatTreeHumanNode(&treeWireNode{
				Type:     "task",
				ID:       "T-00001",
				Title:    "Task",
				Priority: tt.priority,
			})
			want := fmt.Sprintf("\033[%smP%d\033[0m", tt.color, tt.priority)
			if !strings.Contains(got, want) {
				t.Fatalf("priority color = %q, want segment %q", got, want)
			}
		})
	}
}
