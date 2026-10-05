//go:build wrkq_local

package workrpc_test

// wrkf_dual_selector_acceptance_test.go — the dual-selector guard (§2.5): a
// wrkf method given BOTH task and instanceId must reject them, before any
// mutation, when they resolve to different instances; instanceId alone is a
// sufficient selector.

import "testing"

// TestWrkfDualSelector_MismatchRejected attaches two tasks, then calls
// wrkf.instance.show and wrkf.evidence.add with task=task1 but instanceId of
// task2's instance: each must be WRKF_VALIDATION, not a result.
func TestWrkfDualSelector_MismatchRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	cases := []struct {
		method       string
		uuid1, uuid2 string
		params       func(task1ID, instance2ID string) map[string]any
	}{
		{
			method: "wrkf.instance.show",
			uuid1:  "d2100000-0000-4000-8000-000000000001", uuid2: "d2100000-0000-4000-8000-000000000002",
			params: func(task1ID, instance2ID string) map[string]any {
				return map[string]any{"task": task1ID, "instanceId": instance2ID}
			},
		},
		{
			method: "wrkf.evidence.add",
			uuid1:  "d2100000-0000-4000-8000-000000000011", uuid2: "d2100000-0000-4000-8000-000000000012",
			params: func(task1ID, instance2ID string) map[string]any {
				return redEvidenceParams(task1ID, "test/smokey/p3/dual-ev-mismatch-001", map[string]any{"instanceId": instance2ID})
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.method, func(t *testing.T) {
			dbPath := migratedDB(t)
			task1ID := p2SeedTask(t, dbPath, tc.uuid1, "dual-task-1", "Dual Selector Task 1")
			task2ID := p2SeedTask(t, dbPath, tc.uuid2, "dual-task-2", "Dual Selector Task 2")
			setupFrames := p3Run(t, dbPath,
				installCodeChangeReq(t),
				attachCodeChangeReq("a1", task1ID, ""),
				attachCodeChangeReq("a2", task2ID, ""),
			)
			p2ResultOrFail(t, setupFrames[1], "wrkf.workflow.install")
			p2ResultOrFail(t, setupFrames[2], "wrkq.workflow.attach task1")
			instance2ID := attachedInstanceID(t, setupFrames[3], "wrkq.workflow.attach task2")

			frames := p3Run(t, dbPath, mkRPC("m1", tc.method, tc.params(task1ID, instance2ID)))
			if code := p2ErrCode(frames[1]); code != "WRKF_VALIDATION" {
				if _, hasResult := frames[1]["result"]; hasResult {
					t.Errorf("dual-selector mismatch (%s): expected WRKF_VALIDATION pre-mutation, got a result", tc.method)
				} else {
					t.Errorf("dual-selector mismatch (%s): want WRKF_VALIDATION, got error.data.code=%q", tc.method, code)
				}
			}
		})
	}
}

// TestWrkfDualSelector_InstanceIDOnly_Accepted verifies that wrkf.instance.show
// accepts ONLY instanceId (no task selector): one selector is sufficient.
func TestWrkfDualSelector_InstanceIDOnly_Accepted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "d2100000-0000-4000-8000-000000000021", "dual-id-only-test", "Dual Selector InstanceID Only Test")
	instanceID := p3InstallAndAttach(t, dbPath, p2WorkflowTemplatePath(t), taskID)

	frames := p3Run(t, dbPath, mkRPC("s1", "wrkf.instance.show", map[string]any{"instanceId": instanceID}))
	result := p2ResultOrFail(t, frames[1], "wrkf.instance.show with instanceId-only must return the instance")
	if gotID, _ := result["id"].(string); gotID != instanceID {
		t.Errorf("wrkf.instance.show instanceId-only: want instance id %q, got %q", instanceID, gotID)
	}
}
