//go:build wrkq_local

package workrpc_test

// wrkf_role_binding_acceptance_test.go — wrkf.role.bind/list/set/unbind over the
// real wrkf JSON-RPC stdio surface: a binding never touches the task row, it
// routes wrkf.instance.next work to the bound actor, and it authorizes
// wrkf.transition.apply (an unbound actor is WRKF_ROLE_DENIED).

import "testing"

func TestWrkfRoleBindings_AuthorizeNextAndTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "e3100000-0000-4000-8000-000000000014", "role-binding-test", "Role Binding Test")

	setupFrames := p3Run(t, dbPath,
		installCodeChangeReq(t),
		attachCodeChangeReq("a1", taskID, ""),
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	p2ResultOrFail(t, setupFrames[1], "install")
	p2ResultOrFail(t, setupFrames[2], "attach")
	beforeETag, _ := p2ResultOrFail(t, setupFrames[3], "task.show before role bind")["etag"].(float64)

	authorRed := func(id, principal, key string) string {
		return mkRPC(id, "wrkf.transition.apply", map[string]any{
			"task":           taskID,
			"transition":     "author_red",
			"principal_ref":  principal,
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": key,
		})
	}
	frames := p3Run(t, dbPath,
		mkRPC("b1", "wrkf.role.bind", map[string]any{
			"task":          taskID,
			"role":          "tester",
			"principal_ref": "agent:alice",
			"deliveryRef":   "alice@wrkq:T-role",
			"lane":          "test",
		}),
		mkRPC("l1", "wrkf.role.list", map[string]any{"task": taskID}),
		mkRPC("s2", "wrkq.task.show", map[string]any{"task": taskID}),
		mkRPC("e1", "wrkf.evidence.add", redEvidenceParams(taskID, "test/smokey/p3/role-red-001", map[string]any{"principal_ref": "agent:alice"})),
		mkRPC("n1", "wrkf.instance.next", map[string]any{"task": taskID}),
		authorRed("bad", "agent:bob", "role-binding-bad"),
		authorRed("ok", "agent:alice", "role-binding-ok"),
		mkRPC("set", "wrkf.role.set", map[string]any{
			"task":    taskID,
			"roleMap": map[string]any{"tester": "agent:carol"},
		}),
		mkRPC("unbind", "wrkf.role.unbind", map[string]any{
			"task":          taskID,
			"role":          "tester",
			"principal_ref": "agent:carol",
		}),
	)
	bind := p2ResultOrFail(t, frames[1], "wrkf.role.bind")
	if bind["role"] != "tester" || bind["principal_ref"] != "agent:alice" || bind["deliveryRef"] != "alice@wrkq:T-role" {
		t.Fatalf("wrkf.role.bind returned wrong binding: %#v", bind)
	}
	if listRaw, _ := frames[2]["result"].([]any); len(listRaw) != 1 {
		t.Fatalf("wrkf.role.list: want one binding, got %#v", frames[2]["result"])
	}
	if afterETag, _ := p2ResultOrFail(t, frames[3], "task.show after role bind")["etag"].(float64); afterETag != beforeETag {
		t.Fatalf("role binding mutated task etag: before=%v after=%v", beforeETag, afterETag)
	}
	p2ResultOrFail(t, frames[4], "wrkf.evidence.add role evidence")
	actions, _ := p2ResultOrFail(t, frames[5], "wrkf.instance.next")["actions"].([]any)
	var owned bool
	for _, raw := range actions {
		action, _ := raw.(map[string]any)
		owner, _ := action["owner"].(map[string]any)
		if owner["role"] == "tester" && owner["principal_ref"] == "agent:alice" {
			owned = true
			break
		}
	}
	if !owned {
		t.Fatalf("wrkf.instance.next did not assign tester work to bound actor: %#v", actions)
	}
	if code := p2ErrCode(frames[6]); code != "WRKF_ROLE_DENIED" {
		t.Fatalf("transition by unbound actor: want WRKF_ROLE_DENIED, got %q", code)
	}
	p2ResultOrFail(t, frames[7], "transition by bound actor")
	if setRaw, _ := frames[8]["result"].([]any); len(setRaw) != 1 {
		t.Fatalf("wrkf.role.set: want replacement binding, got %#v", frames[8]["result"])
	}
	if unbindRaw, _ := frames[9]["result"].([]any); len(unbindRaw) != 0 {
		t.Fatalf("wrkf.role.unbind: want no remaining tester bindings, got %#v", frames[9]["result"])
	}
}
