package workrpc_test

import "testing"

// TestWrkfInstanceNext_ResolvesByTaskAndInstanceId is a regression test for the
// bug reported by agent-loop (T-04466): wrkf.instance.next returned
// WRKF_NOT_FOUND ("task not found: T-XXXX") both by {task} and by {instanceId},
// while wrkf.instance.show by the same task worked.
//
// Root cause: workflow_instances.task_ref is a project-qualified DISPLAY ref
// (e.g. "wrkq:T-00001"). InstanceNext resolved the instance correctly, then
// re-resolved the task by passing inst.TaskRef to service.Next; selectors.Parse
// leaves the "wrkq:" prefix on the token, so it neither starts with "T-" nor
// parses as a UUID and resolution fails. The fix passes the instance's bare
// TaskUUID (always resolvable) to Next instead of the display ref.
func TestWrkfInstanceNext_ResolvesByTaskAndInstanceId(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"e4660000-0000-4000-8000-000000000001",
		"instance-next-regression", "Instance Next Regression")

	instanceID := p3InstallAndAttach(t, dbPath, tplPath, taskID)

	// by {task} selector
	byTask := p3Run(t, dbPath,
		mkRPC("n1", "wrkf.instance.next", map[string]any{"task": taskID, "role": "tester"}),
	)
	res := p2ResultOrFail(t, byTask[1], "wrkf.instance.next by task must succeed")
	inst, _ := res["instance"].(map[string]any)
	if inst == nil {
		t.Fatalf("wrkf.instance.next by task: result missing instance object; keys=%v", mapKeys(res))
	}
	if gotID, _ := inst["id"].(string); gotID != instanceID {
		t.Errorf("wrkf.instance.next by task: instance.id want %q, got %q", instanceID, gotID)
	}

	// by {instanceId} selector
	byInst := p3Run(t, dbPath,
		mkRPC("n2", "wrkf.instance.next", map[string]any{"instanceId": instanceID, "role": "tester"}),
	)
	res2 := p2ResultOrFail(t, byInst[1], "wrkf.instance.next by instanceId must succeed")
	inst2, _ := res2["instance"].(map[string]any)
	if inst2 == nil {
		t.Fatalf("wrkf.instance.next by instanceId: result missing instance object; keys=%v", mapKeys(res2))
	}
	if gotID, _ := inst2["id"].(string); gotID != instanceID {
		t.Errorf("wrkf.instance.next by instanceId: instance.id want %q, got %q", instanceID, gotID)
	}
}
