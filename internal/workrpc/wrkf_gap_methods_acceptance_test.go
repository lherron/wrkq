package workrpc_test

import "testing"

func TestWrkfGapMethodsOverRealRPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"67580000-0000-4000-8000-000000000001",
		"gap-methods", "Gap Methods")
	_ = p3InstallAndAttach(t, dbPath, tplPath, taskID)

	frames := p3Run(t, dbPath,
		mkRPC("sync", "wrkq.workflow.syncMeta", map[string]any{"task": taskID, "actor": "agent:cody"}),
		mkRPC("schema", "wrkf.evidence.schema", map[string]any{"task": taskID, "kind": "red_test"}),
		mkRPC("call", "wrkf.supervisor.call", map[string]any{"task": taskID, "reason": "needs attention"}),
		mkRPC("escalate", "wrkf.supervisor.escalate", map[string]any{"task": taskID, "reason": "still blocked"}),
		mkRPC("obligation", "wrkf.obligation.create", map[string]any{
			"task": taskID, "kind": "operator_review", "ownerRole": "supervisor", "blocking": true, "reason": "review it",
		}),
		mkRPC("snapshot", "wrkf.watch.snapshot", map[string]any{"selector": taskID, "until": "terminal"}),
		mkRPC("events", "wrkf.watch.events", map[string]any{"selector": taskID, "limit": 100}),
	)

	if got := p2ResultOrFail(t, frames[1], "syncMeta")["synced"]; got != float64(1) {
		t.Fatalf("syncMeta synced = %v, want 1", got)
	}
	if got := p2ResultOrFail(t, frames[2], "evidence.schema")["kind"]; got != "red_test" {
		t.Fatalf("evidence schema kind = %v, want red_test", got)
	}
	if got := p2ResultOrFail(t, frames[3], "supervisor.call")["kind"]; got != "supervisor_call" {
		t.Fatalf("supervisor.call kind = %v", got)
	}
	if got := p2ResultOrFail(t, frames[4], "supervisor.escalate")["kind"]; got != "supervisor_escalation" {
		t.Fatalf("supervisor.escalate kind = %v", got)
	}
	obligation := p2ResultOrFail(t, frames[5], "obligation.create")
	if obligation["kind"] != "operator_review" || obligation["blocking"] != true {
		t.Fatalf("obligation.create = %#v", obligation)
	}
	snapshot := p2ResultOrFail(t, frames[6], "watch.snapshot")
	target, _ := snapshot["target"].(map[string]any)
	if target["kind"] != "task" || target["instanceId"] == "" {
		t.Fatalf("watch.snapshot target = %#v", target)
	}
	events := p2ResultOrFail(t, frames[7], "watch.events")
	items, _ := events["events"].([]any)
	if len(items) == 0 {
		t.Fatalf("watch.events returned no attach event: %#v", events)
	}
	if cursor, _ := events["nextCursor"].(string); cursor == "" {
		t.Fatalf("watch.events returned empty nextCursor: %#v", events)
	}
}

func TestWrkfWatchEventsCursorBindsResolvedIdentity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	firstTask := p2SeedTask(t, dbPath,
		"67580000-0000-4000-8000-000000000011",
		"watch-cursor-first", "Watch Cursor First")
	secondTask := p2SeedTask(t, dbPath,
		"67580000-0000-4000-8000-000000000012",
		"watch-cursor-second", "Watch Cursor Second")
	_ = p3InstallAndAttach(t, dbPath, tplPath, firstTask)
	_ = p3InstallAndAttach(t, dbPath, tplPath, secondTask)

	firstFrames := p3Run(t, dbPath,
		mkRPC("first", "wrkf.watch.events", map[string]any{"selector": firstTask, "limit": 100}),
	)
	first := p2ResultOrFail(t, firstFrames[1], "first watch.events")
	cursor, _ := first["nextCursor"].(string)
	if cursor == "" {
		t.Fatal("first watch.events returned no cursor")
	}

	secondFrames := p3Run(t, dbPath,
		mkRPC("second", "wrkf.watch.events", map[string]any{
			"selector": secondTask, "afterCursor": cursor, "limit": 100,
		}),
	)
	second := p2ResultOrFail(t, secondFrames[1], "second watch.events")
	events, _ := second["events"].([]any)
	if len(events) == 0 {
		t.Fatalf("cursor from predecessor identity suppressed the second target's early events: %#v", second)
	}
	firstEvent, _ := events[0].(map[string]any)
	if firstEvent["seq"] != float64(1) {
		t.Fatalf("second target first seq = %v, want 1", firstEvent["seq"])
	}
}
