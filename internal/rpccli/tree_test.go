package rpccli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
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

type treePriorityTransport struct {
	call func(context.Context, string, any) (json.RawMessage, error)
}

func (t *treePriorityTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return t.call(ctx, method, params)
}

func (t *treePriorityTransport) Close() error {
	return nil
}

func TestHydrateTreePrioritiesUsesPaginatedBulkReadAndExternalFallback(t *testing.T) {
	var calls []string
	tr := &treePriorityTransport{
		call: func(_ context.Context, method string, rawParams any) (json.RawMessage, error) {
			calls = append(calls, method)
			switch method {
			case "wrkq.task.list":
				params, ok := rawParams.(map[string]any)
				if !ok {
					t.Fatalf("%s params type = %T, want map[string]any", method, rawParams)
				}
				if params["path"] != "project" || params["recursive"] != true ||
					params["summary"] != true || params["limit"] != treePriorityPageSize {
					t.Fatalf("bulk params = %#v", params)
				}
				cursor, _ := params["cursor"].(string)
				switch cursor {
				case "":
					return json.RawMessage(`{"items":[{"uuid":"in-path-1","priority":2}],"nextCursor":"page-2"}`), nil
				case "page-2":
					return json.RawMessage(`{"items":[{"uuid":"in-path-2","priority":3}]}`), nil
				default:
					t.Fatalf("unexpected cursor %q", cursor)
				}
			case "wrkq.task.show":
				params, ok := rawParams.(map[string]string)
				if !ok {
					t.Fatalf("%s params type = %T, want map[string]string", method, rawParams)
				}
				if params["task"] != "external" {
					t.Fatalf("fallback params = %#v", params)
				}
				return json.RawMessage(`{"uuid":"external","priority":1}`), nil
			default:
				t.Fatalf("unexpected method %q", method)
			}
			return nil, nil
		},
	}
	external := &treeWireNode{Type: "task", UUID: "external", ExternalBacklink: true}
	nodes := []*treeWireNode{
		{Type: "task", UUID: "in-path-1"},
		{Type: "container", Children: []*treeWireNode{{Type: "task", UUID: "in-path-2"}}, ExternalChildren: []*treeWireNode{external}},
	}

	hydrateTreePriorities(context.Background(), tr, "project", nodes, false, false)

	if got := nodes[0].Priority; got != 2 {
		t.Errorf("first-page priority = %d, want 2", got)
	}
	if got := nodes[1].Children[0].Priority; got != 3 {
		t.Errorf("second-page priority = %d, want 3", got)
	}
	if got := external.Priority; got != 1 {
		t.Errorf("external fallback priority = %d, want 1", got)
	}
	if got, want := strings.Join(calls, ","), "wrkq.task.list,wrkq.task.list,wrkq.task.show"; got != want {
		t.Errorf("calls = %q, want %q", got, want)
	}
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
