//go:build wrkq_local

package workrpc_test

// wrkqapi_task_workflow_acceptance_test.go — the task-scoped workflow accessors
// on the wrkq surface (T-04424 P2; docs/wrkq-wrkf-rpc.md §8.2, §13):
// wrkq.workflow.attach (typed result + mandatory idempotency),
// wrkq.workflow.inspect, wrkq.workflow.instances and wrkq.workflow.timeline.

import (
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
)

// installCodeChangeReq installs the canonical wrkq-code-change@1 template.
func installCodeChangeReq(t *testing.T) string {
	t.Helper()
	return mkRPC("i1", "wrkf.workflow.install", map[string]any{"body": templateBody(t, p2WorkflowTemplatePath(t))})
}

// attachCodeChangeReq attaches wrkq-code-change@1 to taskID; an empty
// idempotencyKey is omitted.
func attachCodeChangeReq(id, taskID, idempotencyKey string) string {
	params := map[string]any{"task": taskID, "workflow": "wrkq-code-change@1"}
	if idempotencyKey != "" {
		params["idempotencyKey"] = idempotencyKey
	}
	return mkRPC(id, "wrkq.workflow.attach", params)
}

// assertWrkfInstanceDTO checks the camelCase WrkfInstance fields shared by the
// attach and inspect results; contextHash is purged (revision-only CAS).
func assertWrkfInstanceDTO(t *testing.T, inst map[string]any) {
	t.Helper()
	for _, key := range []string{"id", "templateId", "status"} {
		p2AssertStr(t, inst, key)
	}
	for _, key := range []string{"contextHash", "template_id", "context_hash"} {
		p2AssertAbsent(t, inst, key)
	}
}

// TestWrkqWorkflowAttach_ReturnsAttachResult verifies that the first
// wrkq.workflow.attach returns WrkqWorkflowAttachResult{task: WrkqTask,
// instance: WrkfInstance, attached: true}.
func TestWrkqWorkflowAttach_ReturnsAttachResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "a1000000-0000-4000-8000-000000000001", "wf-attach-test", "Workflow Attach Test Task")

	frames := p2Run(t, dbPath,
		installCodeChangeReq(t),
		attachCodeChangeReq("a1", taskID, "p2-smokey:attach:"+taskID+":code-change@1"),
	)
	p2ResultOrFail(t, frames[1], "wrkf.workflow.install")
	attachResult := p2ResultOrFail(t, frames[2], "wrkq.workflow.attach must return WrkqWorkflowAttachResult")

	// task is the embedded WrkqTask DTO object, not a string ID.
	if taskDTO, ok := attachResult["task"].(map[string]any); !ok {
		t.Errorf("wrkq.workflow.attach result field \"task\" must be a WrkqTask DTO object, got: %T=%v", attachResult["task"], attachResult["task"])
	} else {
		for _, key := range []string{"uuid", "id", "slug", "projectUuid"} {
			p2AssertStr(t, taskDTO, key)
		}
		p2AssertEtag(t, taskDTO)
	}
	if attachResult["attached"] != true {
		t.Errorf("first attach: expected attached=true, got %v", attachResult["attached"])
	}

	inst, ok := attachResult["instance"].(map[string]any)
	if !ok || inst == nil {
		t.Fatalf("wrkq.workflow.attach result missing \"instance\" object, got: %T=%v", attachResult["instance"], attachResult["instance"])
	}
	assertWrkfInstanceDTO(t, inst)
	p2AssertStr(t, inst, "templateVersion")
	p2AssertAbsent(t, inst, "template_version")
}

// TestWrkqWorkflowAttach_Idempotency_AttachedFalse verifies that replaying
// wrkq.workflow.attach with the same idempotencyKey returns the SAME instance
// with attached=false (§8.2 mandatory idempotency for wrkq.workflow.attach).
func TestWrkqWorkflowAttach_Idempotency_AttachedFalse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "b1000000-0000-4000-8000-000000000001", "wf-idem-attach-test", "Workflow Idempotency Test Task")
	key := "p2-smokey:attach:" + taskID + ":code-change@1"

	frames := p2Run(t, dbPath,
		installCodeChangeReq(t),
		attachCodeChangeReq("a1", taskID, key),
		attachCodeChangeReq("a2", taskID, key), // same key → must replay
	)
	p2ResultOrFail(t, frames[1], "install")
	r1 := p2ResultOrFail(t, frames[2], "first attach")
	r2 := p2ResultOrFail(t, frames[3], "second attach (replay)")

	inst1, _ := r1["instance"].(map[string]any)
	inst2, _ := r2["instance"].(map[string]any)
	if inst1 == nil || inst2 == nil {
		t.Fatal("both attach calls must return an instance")
	}
	if inst1["id"] != inst2["id"] {
		t.Errorf("idempotency replay: instance ids differ; first=%v second=%v", inst1["id"], inst2["id"])
	}
	if r2["attached"] != false {
		t.Errorf("replay attach: expected attached=false, got %v", r2["attached"])
	}
}

// TestWrkqWorkflowInspect_ReturnsTypedDTO verifies that wrkq.workflow.inspect
// wraps the instance as {"instance": WrkfInstance}.
func TestWrkqWorkflowInspect_ReturnsTypedDTO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "c1000000-0000-4000-8000-000000000001", "wf-inspect-test", "Workflow Inspect Test Task")

	frames := p2Run(t, dbPath,
		installCodeChangeReq(t),
		attachCodeChangeReq("a1", taskID, ""),
		mkRPC("ins1", "wrkq.workflow.inspect", map[string]any{"task": taskID}),
	)
	p2ResultOrFail(t, frames[1], "install")
	p2ResultOrFail(t, frames[2], "attach")
	inspectResult := p2ResultOrFail(t, frames[3], "wrkq.workflow.inspect must return typed DTO")

	inst, ok := inspectResult["instance"].(map[string]any)
	if !ok {
		t.Fatalf("wrkq.workflow.inspect result must be {\"instance\": WrkfInstanceDTO}, got top-level keys: %v", mapKeys(inspectResult))
	}
	assertWrkfInstanceDTO(t, inst)
	p2AssertStr(t, inst, "createdAt")
}

// attachGenerations attaches the builtin simple-task templates directly through
// the workflow service: one instance on oneTaskID, and on multiTaskID a v2
// instance superseded by a v3 successor. Returns (one, first, second).
func attachGenerations(t *testing.T, dbPath, oneTaskID, multiTaskID string) (one, first, second *workflow.Instance) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Fatal(err)
		}
	}()
	svc := workflow.NewService(database)
	for _, ref := range []string{workflow.BuiltinSimpleTaskV2TemplateRef, workflow.BuiltinSimpleTaskV3TemplateRef} {
		if _, _, err := svc.EnsureBuiltinTemplate(ref, "agent:smokey"); err != nil {
			t.Fatalf("EnsureBuiltinTemplate(%s): %v", ref, err)
		}
	}
	if one, err = svc.AttachTask(oneTaskID, workflow.BuiltinSimpleTaskV2TemplateRef, "agent:smokey"); err != nil {
		t.Fatalf("AttachTask(one): %v", err)
	}
	if first, err = svc.AttachTask(multiTaskID, workflow.BuiltinSimpleTaskV2TemplateRef, "agent:smokey"); err != nil {
		t.Fatalf("AttachTask(multi first): %v", err)
	}
	revision := first.Revision
	second, err = svc.AttachTask(multiTaskID, workflow.BuiltinSimpleTaskV3TemplateRef, "agent:smokey", workflow.AttachTaskOptions{
		Supersede:             true,
		PredecessorInstanceID: first.ID,
		PredecessorRevision:   &revision,
	})
	if err != nil {
		t.Fatalf("AttachTask(multi second): %v", err)
	}
	return one, first, second
}

// TestWrkqWorkflowInstances_StableEnvelopeHistoryAndTypedAbsence verifies
// wrkq.workflow.instances: advertised by initialize (and wrkf.task.instances
// is not), {instances: []} for a known task with none, WRKQ_NOT_FOUND for an
// unknown task, and current-first generation history that inspect agrees with.
func TestWrkqWorkflowInstances_StableEnvelopeHistoryAndTypedAbsence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	emptyTaskID := p2SeedTask(t, dbPath, "c2000000-0000-4000-8000-000000000001", "wf-instances-empty", "Workflow Instances Empty Task")
	oneTaskID := p2SeedTask(t, dbPath, "c2000000-0000-4000-8000-000000000002", "wf-instances-one", "Workflow Instances One Task")
	multiTaskID := p2SeedTask(t, dbPath, "c2000000-0000-4000-8000-000000000003", "wf-instances-multi", "Workflow Instances Multi Task")
	one, first, second := attachGenerations(t, dbPath, oneTaskID, multiTaskID)

	frames := p2Run(t, dbPath,
		mkRPC("empty", "wrkq.workflow.instances", map[string]any{"task": emptyTaskID}),
		mkRPC("empty-inspect", "wrkq.workflow.inspect", map[string]any{"task": emptyTaskID}),
		mkRPC("unknown", "wrkq.workflow.instances", map[string]any{"task": "T-99999"}),
		mkRPC("one", "wrkq.workflow.instances", map[string]any{"task": oneTaskID}),
		mkRPC("multi", "wrkq.workflow.instances", map[string]any{"task": multiTaskID}),
		mkRPC("inspect", "wrkq.workflow.inspect", map[string]any{"task": multiTaskID}),
	)

	initResult := p2ResultOrFail(t, frames[0], "initialize")
	if hash, _ := initResult["protocolSchemaHash"].(string); hash == "" {
		t.Fatal("initialize omitted protocolSchemaHash")
	}
	methods, _ := initResult["methods"].([]any)
	var hasInstances, hasForbidden bool
	for _, method := range methods {
		hasInstances = hasInstances || method == "wrkq.workflow.instances"
		hasForbidden = hasForbidden || method == "wrkf.task.instances"
	}
	if !hasInstances || hasForbidden {
		t.Fatalf("initialize methods: has instances=%v, has forbidden wrkf.task.instances=%v", hasInstances, hasForbidden)
	}

	empty := p2ResultOrFail(t, frames[1], "known empty task")
	if emptyInstances, ok := empty["instances"].([]any); !ok || emptyInstances == nil || len(emptyInstances) != 0 {
		t.Fatalf("known empty result = %#v, want {instances:[]}", empty)
	}
	if got := p2ErrCode(frames[2]); got != "WRKF_NOT_FOUND" {
		t.Fatalf("singleton inspect empty error code = %q, want preserved WRKF_NOT_FOUND; frame=%#v", got, frames[2])
	}
	if got := p2ErrCode(frames[3]); got != "WRKQ_NOT_FOUND" {
		t.Fatalf("unknown task error code = %q, want WRKQ_NOT_FOUND; frame=%#v", got, frames[3])
	}

	oneResult := p2ResultOrFail(t, frames[4], "one instance")
	if oneInstances, _ := oneResult["instances"].([]any); len(oneInstances) != 1 || oneInstances[0].(map[string]any)["id"] != one.ID {
		t.Fatalf("one instance result = %#v, want %s", oneResult, one.ID)
	}

	multiResult := p2ResultOrFail(t, frames[5], "multi generation")
	multiInstances, _ := multiResult["instances"].([]any)
	if len(multiInstances) != 2 {
		t.Fatalf("multi generation result = %#v, want two instances", multiResult)
	}
	current := multiInstances[0].(map[string]any)
	history := multiInstances[1].(map[string]any)
	if current["id"] != second.ID || current["status"] == "closed" {
		t.Fatalf("multi current = %#v, want live successor %s", current, second.ID)
	}
	if history["id"] != first.ID || history["status"] != "closed" || history["phase"] != "superseded" {
		t.Fatalf("multi history = %#v, want closed/superseded predecessor %s", history, first.ID)
	}
	inspectInstance, _ := p2ResultOrFail(t, frames[6], "inspect compatibility")["instance"].(map[string]any)
	if inspectInstance["id"] != current["id"] {
		t.Fatalf("inspect instance %v != instances[0] %v", inspectInstance["id"], current["id"])
	}
}

// TestWrkqWorkflowTimeline_ReturnsItems verifies that wrkq.workflow.timeline
// returns an "events" or "items" array after a workflow is attached.
func TestWrkqWorkflowTimeline_ReturnsItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "d1000000-0000-4000-8000-000000000001", "wf-timeline-test", "Workflow Timeline Test Task")

	frames := p2Run(t, dbPath,
		installCodeChangeReq(t),
		attachCodeChangeReq("a1", taskID, ""),
		mkRPC("tl1", "wrkq.workflow.timeline", map[string]any{"task": taskID}),
	)
	p2ResultOrFail(t, frames[1], "install")
	p2ResultOrFail(t, frames[2], "attach")
	timelineResult := p2ResultOrFail(t, frames[3], "wrkq.workflow.timeline must return a result")
	_, hasEvents := timelineResult["events"]
	_, hasItems := timelineResult["items"]
	if !hasEvents && !hasItems {
		t.Error("wrkq.workflow.timeline result must have \"events\" or \"items\" array")
	}
}

// TestWrkqWorkflowTimeline_TransitionEventPayload verifies that a transition
// shows on the timeline as a typed workflow.transitioned event with structured
// from/to states.
func TestWrkqWorkflowTimeline_TransitionEventPayload(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "d1000000-0000-4000-8000-000000000002", "wf-timeline-transition-test", "Workflow Timeline Transition Test Task")

	frames := p2Run(t, dbPath,
		installCodeChangeReq(t),
		attachCodeChangeReq("a1", taskID, ""),
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":          taskID,
			"kind":          "red_test",
			"ref":           "test/smokey/p2/timeline-transition-red",
			"principal_ref": "agent:tester",
			"role":          "tester",
			"facts":         p3RedFacts(),
		}),
		mkRPC("tr1", "wrkf.transition.apply", map[string]any{
			"task":           taskID,
			"transition":     "author_red",
			"principal_ref":  "agent:tester",
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": "timeline-transition-author-red",
		}),
		mkRPC("tl1", "wrkq.workflow.timeline", map[string]any{"task": taskID}),
	)
	for i, label := range []string{"install", "attach", "add evidence", "transition apply"} {
		p2ResultOrFail(t, frames[1+i], label)
	}
	timeline := p2ResultOrFail(t, frames[5], "timeline")

	events, _ := timeline["events"].([]any)
	for _, raw := range events {
		event, _ := raw.(map[string]any)
		if event["type"] != "workflow.transitioned" {
			continue
		}
		payload, _ := event["payload"].(map[string]any)
		if payload["transition"] == "author_red" && payload["outcome"] == "red_recorded" {
			if _, ok := payload["from"].(map[string]any); !ok {
				t.Fatalf("workflow.transitioned payload missing structured from state: %#v", payload)
			}
			if _, ok := payload["to"].(map[string]any); !ok {
				t.Fatalf("workflow.transitioned payload missing structured to state: %#v", payload)
			}
			return
		}
	}
	t.Fatalf("timeline missing typed workflow.transitioned event for author_red: %#v", events)
}
