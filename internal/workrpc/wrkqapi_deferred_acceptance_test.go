//go:build wrkq_local

package workrpc_test

// wrkqapi_deferred_acceptance_test.go — contract hygiene for the methods T-04448
// moved off the P1 placeholder ("method is registered but not implemented in
// P1"). The behaviour of each method is covered per resource:
//   - task delete/restore/acknowledge  → wrkqapi_task_lifecycle_acceptance_test.go
//   - attachment add/list/remove       → wrkqapi_attachment_acceptance_test.go
//   - relation add/list/remove         → wrkqapi_relation_acceptance_test.go
//   - container show/list              → wrkqapi_container_read_acceptance_test.go
//
// NOTE: Size limit enforcement (WRKQ_ATTACH_MAX_MB) requires env var support;
// implementer should add WRKQ_ATTACH_MAX_MB env var support to config.Load and
// add a dedicated test.

import "testing"

// TestWrkqDeferredMethods_NoneReturnNotImplemented probes every deferred method
// in one session and asserts none answers with the P1 placeholder error.
func TestWrkqDeferredMethods_NoneReturnNotImplemented(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "f0000008-0000-4000-8000-000000000001", "contract-test-task", "Contract Hygiene Task")

	probes := []struct {
		id     string
		method string
		params map[string]any
	}{
		{"p01", "wrkq.task.delete", map[string]any{"task": taskID}},
		{"p02", "wrkq.task.restore", map[string]any{"task": taskID}},
		{"p03", "wrkq.task.acknowledge", map[string]any{"task": taskID, "force": true}},
		{"p04", "wrkq.attachment.add", map[string]any{"task": taskID, "path": "/tmp/x", "filename": "x.txt"}},
		{"p05", "wrkq.attachment.list", map[string]any{"task": taskID}},
		{"p06", "wrkq.attachment.remove", map[string]any{"id": "00000000-0000-0000-0000-000000000000"}},
		{"p07", "wrkq.relation.add", map[string]any{"fromTask": taskID, "kind": "blocks", "toTask": "T-99999999"}},
		{"p08", "wrkq.relation.list", map[string]any{"task": taskID}},
		{"p09", "wrkq.relation.remove", map[string]any{"fromTask": taskID, "kind": "blocks", "toTask": "T-99999999"}},
		{"p10", "wrkq.container.show", map[string]any{"path": "nonexistent"}},
		{"p11", "wrkq.container.list", map[string]any{}},
	}
	reqs := make([]string, len(probes))
	for i, p := range probes {
		reqs[i] = mkRPC(p.id, p.method, p.params)
	}

	extraEnv := []string{"WRKQ_ATTACH_DIR=" + t.TempDir()}
	frames := runRPCWithEnv(t, "wrkq", dbPath, rpcSession("p4-contract", reqs...), extraEnv)
	if want := 2 + len(probes); len(frames) != want {
		t.Fatalf("contract hygiene: expected %d frames, got %d", want, len(frames))
	}
	for i, p := range probes {
		if p4IsStubError(frames[1+i]) { // offset by the init frame
			t.Errorf("method %s returned stub error 'not implemented in P1'; must be implemented", p.method)
		}
	}
}
