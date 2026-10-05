//go:build wrkq_local

package workrpc_test

// wrkqapi_relation_acceptance_test.go — wrkq.relation.add/list/remove over the
// real JSON-RPC stdio surface (T-04448). Relations are identified by their
// (fromTask, kind, toTask) composite key and carry no "id".

import "testing"

// createTaskPair creates two tasks in one session and returns their ids.
func createTaskPair(t *testing.T, dbPath, titleA, titleB string) (taskA, taskB string) {
	t.Helper()
	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": titleA, "kind": "task"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": titleB, "kind": "task"}),
	)
	taskA, _ = createdTaskIDs(t, cf[1])
	taskB, _ = createdTaskIDs(t, cf[2])
	return taskA, taskB
}

func relationParams(from, kind, to string) map[string]any {
	return map[string]any{"fromTask": from, "kind": kind, "toTask": to}
}

// TestWrkqRelationAdd_ValidKind_Succeeds adds a blocks relation: the result has
// fromTask, kind, toTask and direction, and no "id".
func TestWrkqRelationAdd_ValidKind_Succeeds(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskA, taskB := createTaskPair(t, dbPath, "Relation Task A", "Relation Task B")

	rf := p2Run(t, dbPath, mkRPC("r1", "wrkq.relation.add", relationParams(taskA, "blocks", taskB)))
	result := p2ResultOrFail(t, rf[1], "wrkq.relation.add")

	p2AssertStr(t, result, "fromTask")
	p2AssertStr(t, result, "kind")
	p2AssertStr(t, result, "toTask")
	p2AssertStr(t, result, "direction")
	p2AssertAbsent(t, result, "id")
}

// TestWrkqRelationAdd_Rejections proves each invalid add is a real domain error:
// an unknown kind, a self-relation, a nonexistent fromTask, and a duplicate of
// an existing composite key.
func TestWrkqRelationAdd_Rejections(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	cases := []struct {
		name string
		// params builds the rejected add from two fresh tasks; it may first
		// create state through dbPath.
		params   func(t *testing.T, dbPath, taskA, taskB string) map[string]any
		wantCode string
	}{
		{
			name:     "InvalidKind",
			params:   func(_ *testing.T, _, a, b string) map[string]any { return relationParams(a, "invalid_kind", b) },
			wantCode: "WRKQ_VALIDATION",
		},
		{
			name:     "SelfRelation",
			params:   func(_ *testing.T, _, a, _ string) map[string]any { return relationParams(a, "blocks", a) },
			wantCode: "WRKQ_VALIDATION",
		},
		{
			name:     "NotFound",
			params:   func(_ *testing.T, _, _, b string) map[string]any { return relationParams("T-99999999", "blocks", b) },
			wantCode: "WRKQ_NOT_FOUND",
		},
		{
			name: "Duplicate",
			params: func(t *testing.T, dbPath, a, b string) map[string]any {
				first := p2Run(t, dbPath, mkRPC("r0", "wrkq.relation.add", relationParams(a, "blocks", b)))
				p2ResultOrFail(t, first[1], "first wrkq.relation.add")
				return relationParams(a, "blocks", b)
			},
			wantCode: "WRKQ_CONFLICT",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := migratedDB(t)
			taskA, taskB := createTaskPair(t, dbPath, tc.name+" A", tc.name+" B")
			rf := p2Run(t, dbPath, mkRPC("r1", "wrkq.relation.add", tc.params(t, dbPath, taskA, taskB)))
			p4AssertDomainError(t, rf[1], tc.wantCode, "relation.add "+tc.name)
		})
	}
}

// TestWrkqRelationList_IncludesDirectionField lists the to-task's relations:
// every item carries "direction".
func TestWrkqRelationList_IncludesDirectionField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskA, taskB := createTaskPair(t, dbPath, "Rel List A", "Rel List B")
	p2Run(t, dbPath, mkRPC("r1", "wrkq.relation.add", relationParams(taskA, "blocks", taskB)))

	lf := p2Run(t, dbPath, mkRPC("l1", "wrkq.relation.list", map[string]any{"task": taskB}))
	result := p2ResultOrFail(t, lf[1], "wrkq.relation.list")
	p2AssertHasItems(t, result, "wrkq.relation.list")

	items, _ := result["items"].([]any)
	if len(items) < 1 {
		t.Fatalf("wrkq.relation.list: expected at least 1 item, got %d", len(items))
	}
	for _, item := range items {
		m, _ := item.(map[string]any)
		if _, ok := m["direction"]; !ok {
			t.Errorf("wrkq.relation.list item missing \"direction\" field: %#v", m)
		}
	}
}

// TestWrkqRelationRemove_ByCompositeKey removes a relation by its composite key,
// with no "id" param.
func TestWrkqRelationRemove_ByCompositeKey(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskA, taskB := createTaskPair(t, dbPath, "Rel Remove A", "Rel Remove B")
	p2Run(t, dbPath, mkRPC("r1", "wrkq.relation.add", relationParams(taskA, "blocks", taskB)))

	rmf := p2Run(t, dbPath, mkRPC("rm1", "wrkq.relation.remove", relationParams(taskA, "blocks", taskB)))
	p2ResultOrFail(t, rmf[1], "wrkq.relation.remove by composite key")
}
