//go:build wrkq_local

package workrpc_test

// wrkf_event_query_acceptance_test.go — wrkf.event.query replaying
// workflow.transitioned events across tasks: phase, risk-class and bound-role
// filters, cursor paging with stable ordering on equal timestamps, project
// filters by slug and id, and matchingRoleBindings (including a legacy
// task_role_assignments row).

import (
	"fmt"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// eventQueryTask is one task in the event-query fixture: its risk class, the
// testers bound to it, and the actor that records evidence and transitions.
type eventQueryTask struct {
	uuid, slug, riskClass string
	testers               []string
	actor                 string
}

func TestWrkfEventQuery_ReplaysTransitionEventsWithFiltersAndCursor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	specs := []eventQueryTask{
		{"e3200000-0000-4000-8000-000000000001", "event-query-test-1", "medium", []string{"agent:tester-a", "agent:tester-aa"}, "agent:tester-a"},
		{"e3200000-0000-4000-8000-000000000002", "event-query-test-2", "high", []string{"agent:tester-b"}, "agent:tester-b"},
		{"e3200000-0000-4000-8000-000000000003", "event-query-low-risk", "low", []string{"agent:tester-low"}, "agent:tester-low"},
		// No wrkf binding: its tester comes from a legacy task_role_assignments row.
		{"e3200000-0000-4000-8000-000000000004", "event-query-legacy-role", "medium", nil, "agent:legacy"},
	}
	ids := make([]string, len(specs))
	for i, spec := range specs {
		ids[i] = p2SeedTask(t, dbPath, spec.uuid, spec.slug, spec.slug)
	}
	seedLegacyTesterAssignment(t, dbPath, specs[3].uuid)
	task1ID, task2ID := ids[0], ids[1]

	// Every step for every task, grouped by step as the original ledger order:
	// risk classes, attaches, bindings, evidence, then transitions.
	setup := []string{installCodeChangeReq(t)}
	for i, spec := range specs {
		setup = append(setup, mkRPC(fmt.Sprintf("u%d", i+1), "wrkq.task.update", map[string]any{
			"task": ids[i], "actor": "agent:smokey", "patch": map[string]any{"riskClass": spec.riskClass},
		}))
	}
	for i := range specs {
		setup = append(setup, mkRPC(fmt.Sprintf("a%d", i+1), "wrkq.workflow.attach", map[string]any{
			"task": ids[i], "actor": "agent:smokey", "workflow": "wrkq-code-change@1",
		}))
	}
	for i, spec := range specs {
		for j, tester := range spec.testers {
			setup = append(setup, mkRPC(fmt.Sprintf("b%d-%d", i+1, j), "wrkf.role.bind", map[string]any{
				"task": ids[i], "role": "tester", "principal_ref": tester,
				"deliveryRef": tester[len("agent:"):] + "@wrkq:" + ids[i],
			}))
		}
	}
	for i, spec := range specs {
		setup = append(setup, mkRPC(fmt.Sprintf("e%d", i+1), "wrkf.evidence.add",
			redEvidenceParams(ids[i], "test/smokey/p3/"+spec.slug+"-red", map[string]any{"principal_ref": spec.actor})))
	}
	for i, spec := range specs {
		setup = append(setup, mkRPC(fmt.Sprintf("t%d", i+1), "wrkf.transition.apply", map[string]any{
			"task":           ids[i],
			"transition":     "author_red",
			"principal_ref":  spec.actor,
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": fmt.Sprintf("event-query-transition-%d", i+1),
		}))
	}
	setupFrames := p3Run(t, dbPath, setup...)
	for i := 1; i <= len(setup); i++ {
		p2ResultOrFail(t, setupFrames[i], "event-query setup step "+fmt.Sprint(i))
	}

	forceSameTransitionTimestamp(t, dbPath)

	page1Frames := p3Run(t, dbPath,
		mkRPC("q1", "wrkf.event.query", map[string]any{
			"eventType":        "workflow.transitioned",
			"fromPhase":        "intake",
			"toPhase":          "red",
			"excludeRiskClass": "low",
			"boundRole":        "tester",
			"limit":            float64(1),
		}),
	)
	page1 := p2ResultOrFail(t, page1Frames[1], "wrkf.event.query page 1")
	items1, _ := page1["items"].([]any)
	if len(items1) != 1 {
		t.Fatalf("page 1: want one item, got %#v", page1["items"])
	}
	nextCursor, _ := page1["nextCursor"].(string)
	if nextCursor == "" || page1["hasMore"] != true {
		t.Fatalf("page 1: expected nextCursor and hasMore=true, got %#v", page1)
	}
	assertTransitionReplayItem(t, items1[0], map[string]bool{task1ID: true, task2ID: true})

	page2Frames := p3Run(t, dbPath,
		mkRPC("q2", "wrkf.event.query", map[string]any{
			"fromPhase":        "intake",
			"toPhase":          "red",
			"excludeRiskClass": "low",
			"boundRole":        "tester",
			"limit":            float64(1),
			"cursor":           nextCursor,
		}),
		mkRPC("all", "wrkf.event.query", map[string]any{
			"fromPhase":        "intake",
			"toPhase":          "red",
			"excludeRiskClass": "low",
			"boundRole":        "tester",
			"limit":            float64(10),
		}),
		mkRPC("wrong", "wrkf.event.query", map[string]any{
			"fromPhase": "verify",
			"toPhase":   "red",
			"boundRole": "tester",
			"limit":     float64(10),
		}),
	)
	page2 := p2ResultOrFail(t, page2Frames[1], "wrkf.event.query page 2")
	items2, _ := page2["items"].([]any)
	if len(items2) != 1 {
		t.Fatalf("page 2: want one item, got %#v", page2["items"])
	}
	assertTransitionReplayItem(t, items2[0], map[string]bool{task1ID: true, task2ID: true})

	first, _ := items1[0].(map[string]any)
	second, _ := items2[0].(map[string]any)
	if first["id"] == second["id"] {
		t.Fatalf("cursor returned duplicate transition event id %q", first["id"])
	}

	all := p2ResultOrFail(t, page2Frames[2], "wrkf.event.query all")
	allItems, _ := all["items"].([]any)
	if len(allItems) != 2 {
		t.Fatalf("event.query all: want two items, got %#v", all["items"])
	}
	assertTaskMatchingBindingCount(t, allItems, task1ID, 2)
	wrong := p2ResultOrFail(t, page2Frames[3], "wrkf.event.query wrong phase")
	wrongItems, _ := wrong["items"].([]any)
	if len(wrongItems) != 0 {
		t.Fatalf("wrong phase filter: want zero items, got %#v", wrong["items"])
	}

	projectID := projectIDFromReplayItem(t, items1[0])
	projectFrames := p3Run(t, dbPath,
		mkRPC("slug", "wrkf.event.query", map[string]any{
			"project":          "p2-test-proj",
			"fromPhase":        "intake",
			"toPhase":          "red",
			"excludeRiskClass": "low",
			"boundRole":        "tester",
			"limit":            float64(10),
		}),
		mkRPC("id", "wrkf.event.query", map[string]any{
			"project":          projectID,
			"fromPhase":        "intake",
			"toPhase":          "red",
			"excludeRiskClass": "low",
			"boundRole":        "tester",
			"limit":            float64(10),
		}),
	)
	bySlug := p2ResultOrFail(t, projectFrames[1], "project slug filter")
	bySlugItems, _ := bySlug["items"].([]any)
	if len(bySlugItems) != 2 {
		t.Fatalf("project slug filter: want two items, got %#v", bySlug["items"])
	}
	byID := p2ResultOrFail(t, projectFrames[2], "project id filter")
	byIDItems, _ := byID["items"].([]any)
	if len(byIDItems) != 2 {
		t.Fatalf("project id filter: want two items, got %#v", byID["items"])
	}
}

func assertTransitionReplayItem(t *testing.T, raw any, allowedTasks map[string]bool) {
	t.Helper()
	item, _ := raw.(map[string]any)
	if item["eventType"] != "workflow.transitioned" {
		t.Fatalf("eventType: want workflow.transitioned, got %#v", item)
	}
	if item["id"] == "" || item["occurredAt"] == "" || item["instanceId"] == "" {
		t.Fatalf("replay item missing durable identity/timestamp/instance: %#v", item)
	}
	if item["transition"] != "author_red" || item["fromPhase"] != "intake" || item["toPhase"] != "red" {
		t.Fatalf("replay item transition fields mismatch: %#v", item)
	}
	if item["role"] != "tester" {
		t.Fatalf("actorRole: want tester, got %#v", item)
	}
	task, _ := item["task"].(map[string]any)
	taskID, _ := task["id"].(string)
	if !allowedTasks[taskID] {
		t.Fatalf("unexpected task in replay item: %#v", task)
	}
	if task["riskClass"] == "low" {
		t.Fatalf("excludeRiskClass=low returned low-risk task: %#v", item)
	}
	if task["projectUuid"] == "" || task["projectId"] == "" || task["projectSlug"] != "p2-test-proj" {
		t.Fatalf("replay item missing normalized project identity: %#v", task)
	}
	bindings, _ := item["matchingRoleBindings"].([]any)
	previousActor := ""
	for _, rawBinding := range bindings {
		binding, _ := rawBinding.(map[string]any)
		if binding["role"] == "tester" && binding["principal_ref"] != "" {
			actor, _ := binding["principal_ref"].(string)
			if previousActor != "" && actor < previousActor {
				t.Fatalf("matchingRoleBindings not sorted by actor: %#v", bindings)
			}
			previousActor = actor
		}
	}
	if previousActor == "" {
		t.Fatalf("replay item missing tester matchingRoleBindings: %#v", item)
	}
}

func assertTaskMatchingBindingCount(t *testing.T, items []any, taskID string, want int) {
	t.Helper()
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		task, _ := item["task"].(map[string]any)
		if task["id"] != taskID {
			continue
		}
		bindings, _ := item["matchingRoleBindings"].([]any)
		if len(bindings) != want {
			t.Fatalf("task %s matchingRoleBindings: want %d, got %#v", taskID, want, bindings)
		}
		return
	}
	t.Fatalf("task %s not found in replay items: %#v", taskID, items)
}

func projectIDFromReplayItem(t *testing.T, raw any) string {
	t.Helper()
	item, _ := raw.(map[string]any)
	task, _ := item["task"].(map[string]any)
	projectID, _ := task["projectId"].(string)
	if projectID == "" {
		t.Fatalf("replay item missing projectId: %#v", item)
	}
	return projectID
}

func seedLegacyTesterAssignment(t *testing.T, dbPath, taskUUID string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("seedLegacyTesterAssignment: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(`
		INSERT INTO task_role_assignments (task_uuid, role, principal_ref)
		VALUES (?, 'tester', 'agent:legacy')
	`, taskUUID); err != nil {
		t.Fatalf("seedLegacyTesterAssignment: insert: %v", err)
	}
}

func forceSameTransitionTimestamp(t *testing.T, dbPath string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("forceSameTransitionTimestamp: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(`
		UPDATE workflow_events
		SET created_at = '2026-06-15T15:00:00Z'
		WHERE type = 'workflow.transitioned'
	`); err != nil {
		t.Fatalf("forceSameTransitionTimestamp: update: %v", err)
	}
}
