package workrpc_test

// wrkfapi_acceptance_test.go — RED acceptance tests for T-04428 P3.
//
// Tests define the contract for two net-new wrkf.* behaviors:
//   1. Evidence runId persistence and idempotency (§9.7 of docs/wrkq-wrkf-rpc.md
//      and docs/wrkq-wrkf-rpc-client-forward-spec.md §9.7).
//   2. Dual-selector mismatch guard: wrkf methods that accept both task and
//      instanceId must reject calls where the two selectors resolve to DIFFERENT
//      instances (§2.5).
//
// Tests FAIL now because:
//   - internal/wrkfapi/types.go EvidenceAddParams has no runId or idempotencyKey
//     fields; workflow/service.go INSERT omits run_id; no idempotency enforcement.
//   - wrkf.instance.show ignores instanceId (only uses task); EvidenceAddParams has
//     no instanceId field; no dual-selector pre-mutation guard.
//
// All tests drive through the REAL workrpc server (go run ./cmd/wrkf rpc --stdio)
// exactly as wrkqapi_acceptance_test.go does.
// Do NOT set WRKQ_ACTOR in the environment when running these locally.

import (
	"encoding/json"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// ─── helpers (P3-scoped) ─────────────────────────────────────────────────────

// p3Run runs a wrkf RPC session with the given business requests sandwiched
// between rpc.initialize and rpc.shutdown + rpc.exit (notification, no frame).
// Returns all response frames: init(0), req1(1)..reqN(N), shutdown(N+1).
func p3Run(t *testing.T, dbPath string, reqs ...string) []map[string]any {
	t.Helper()
	seq := []string{
		mkRPC("_init", "rpc.initialize", map[string]any{
			"protocolVersion": "2026-06-30",
			"client":          map[string]any{"name": "p3-smokey", "version": "0.0.1"},
		}),
	}
	seq = append(seq, reqs...)
	seq = append(seq,
		mkRPC("_sd", "rpc.shutdown", map[string]any{}),
		mkRPC("", "rpc.exit", nil), // notification → no frame
	)
	frames := runRPC(t, "wrkf", dbPath, seq)
	want := 2 + len(reqs)
	if len(frames) != want {
		t.Fatalf("p3Run: expected %d frames, got %d\nframes: %#v", want, len(frames), frames)
	}
	return frames
}

// p3InstallAndAttach installs the wrkq-code-change@1 template and attaches it
// to taskID. Returns the instanceId of the newly-attached workflow instance.
func p3InstallAndAttach(t *testing.T, dbPath, tplPath, taskID string) string {
	t.Helper()
	frames := p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     taskID,
			"workflow": "wrkq-code-change@1",
		}),
	)
	// frames: init(0), install(1), attach(2), shutdown(3)
	p2ResultOrFail(t, frames[1], "wrkf.workflow.install")
	attachResult := p2ResultOrFail(t, frames[2], "wrkq.workflow.attach")
	inst, ok := attachResult["instance"].(map[string]any)
	if !ok || inst == nil {
		t.Fatalf("p3InstallAndAttach: attach returned no instance object; result keys: %v", mapKeys(attachResult))
	}
	instanceID, _ := inst["id"].(string)
	if instanceID == "" {
		t.Fatal("p3InstallAndAttach: attach returned empty instance id")
	}
	return instanceID
}

// p3EvidenceItems extracts the evidence items from a wrkf.evidence.list result
// frame. Handles both array results and map results with an "items" key.
func p3EvidenceItems(t *testing.T, frame map[string]any, label string) []map[string]any {
	t.Helper()
	if errObj, ok := frame["error"]; ok {
		t.Fatalf("%s: unexpected error: %v", label, errObj)
	}
	raw := frame["result"]
	var anySliceOut []any
	switch v := raw.(type) {
	case []any:
		anySliceOut = v
	case map[string]any:
		anySliceOut, _ = v["items"].([]any)
		if anySliceOut == nil {
			// Some impls may wrap in a different key; try "evidence"
			anySliceOut, _ = v["evidence"].([]any)
		}
	}
	items := make([]map[string]any, 0, len(anySliceOut))
	for _, item := range anySliceOut {
		if m, ok := item.(map[string]any); ok {
			items = append(items, m)
		}
	}
	return items
}

// p3AddFacts returns a JSON-marshalled facts object for the red_test kind.
// Facts schema requires verdict ∈ ["red","fails_expected"].
func p3RedFacts() json.RawMessage { return json.RawMessage(`{"verdict":"red"}`) }

// ─── Evidence runId persistence (§9.7) ───────────────────────────────────────

// TestWrkfEvidenceAdd_RunIDPersisted verifies that a runId supplied to
// wrkf.evidence.add is persisted and returned by wrkf.evidence.show (§9.7).
//
// RED: EvidenceAddParams has no runId field; workflow/service.go INSERT omits
// run_id; the returned Evidence.RunID is always "".
func TestWrkfEvidenceAdd_RunIDPersisted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"e3100000-0000-4000-8000-000000000001",
		"ev-runid-show-test", "Evidence RunID Show Test")

	_ = p3InstallAndAttach(t, dbPath, tplPath, taskID)

	const wantRunID = "run-smokey-p3-show-001"
	addFrames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":  taskID,
			"kind":  "red_test",
			"ref":   "test/smokey/p3/runid-show-001",
			"role":  "tester",
			"facts": p3RedFacts(),
			"runId": wantRunID,
		}),
	)
	// frames: init(0), add(1), shutdown(2)
	addResult := p2ResultOrFail(t, addFrames[1], "wrkf.evidence.add must succeed")
	evidenceID, _ := addResult["id"].(string)
	if evidenceID == "" {
		t.Fatal("wrkf.evidence.add returned empty id; cannot test runId persistence")
	}

	// Show the evidence and assert runId is returned.
	showFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.evidence.show", map[string]any{"id": evidenceID}),
	)
	showResult := p2ResultOrFail(t, showFrames[1], "wrkf.evidence.show must return evidence")

	// RED: run_id not inserted → always "".
	gotRunID, _ := showResult["runId"].(string)
	if gotRunID != wantRunID {
		t.Errorf("wrkf.evidence.show: runId want %q, got %q (runId not persisted from EvidenceAddParams)", wantRunID, gotRunID)
	}
}

// TestWrkfEvidenceAdd_RunIDInListDTO verifies that wrkf.evidence.list includes
// runId on each returned evidence item (§9.7).
//
// RED: same root cause — run_id INSERT is missing → list items have runId="".
func TestWrkfEvidenceAdd_RunIDInListDTO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"e3100000-0000-4000-8000-000000000002",
		"ev-runid-list-test", "Evidence RunID List Test")

	_ = p3InstallAndAttach(t, dbPath, tplPath, taskID)

	const wantRunID = "run-smokey-p3-list-001"
	frames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":  taskID,
			"kind":  "red_test",
			"ref":   "test/smokey/p3/runid-list-001",
			"role":  "tester",
			"facts": p3RedFacts(),
			"runId": wantRunID,
		}),
		mkRPC("l1", "wrkf.evidence.list", map[string]any{"task": taskID}),
	)
	// frames: init(0), add(1), list(2), shutdown(3)
	p2ResultOrFail(t, frames[1], "wrkf.evidence.add must succeed")

	items := p3EvidenceItems(t, frames[2], "wrkf.evidence.list")
	if len(items) == 0 {
		t.Fatal("wrkf.evidence.list: returned zero items after adding evidence")
	}

	// At least one item must carry the runId we supplied.
	found := false
	for _, item := range items {
		if rid, _ := item["runId"].(string); rid == wantRunID {
			found = true
			break
		}
	}
	// RED: run_id not persisted → no item has the expected runId.
	if !found {
		t.Errorf("wrkf.evidence.list: no item has runId=%q — runId not persisted from evidence.add params", wantRunID)
	}
}

// ─── Evidence idempotency (§9.7) ─────────────────────────────────────────────

// TestWrkfEvidenceAdd_Idempotency_Replay verifies that calling wrkf.evidence.add
// twice with the same idempotencyKey and semantically identical params returns
// the SAME evidence id and creates NO duplicate evidence/event/obligation (§9.7).
//
// RED: EvidenceAddParams has no idempotencyKey field; idempotency is not
// enforced; each call creates a distinct evidence row with a different id.
func TestWrkfEvidenceAdd_Idempotency_Replay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"e3100000-0000-4000-8000-000000000010",
		"ev-idem-replay-test", "Evidence Idempotency Replay Task")

	_ = p3InstallAndAttach(t, dbPath, tplPath, taskID)

	const idemKey = "smokey:p3:ev:idem:replay:001"
	addParams := map[string]any{
		"task":           taskID,
		"kind":           "red_test",
		"ref":            "test/smokey/p3/idem-replay-001",
		"role":           "tester",
		"facts":          p3RedFacts(),
		"idempotencyKey": idemKey,
	}
	frames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", addParams),
		mkRPC("e2", "wrkf.evidence.add", addParams), // identical params + same key
	)
	// frames: init(0), add1(1), add2(2), shutdown(3)
	r1 := p2ResultOrFail(t, frames[1], "first wrkf.evidence.add")
	r2 := p2ResultOrFail(t, frames[2], "second wrkf.evidence.add (replay)")

	id1, _ := r1["id"].(string)
	id2, _ := r2["id"].(string)

	if id1 == "" {
		t.Error("first evidence.add returned empty id")
	}
	// RED: without idempotency, two distinct rows are created → id2 != id1.
	if id1 != id2 {
		t.Errorf("idempotency replay: want same evidence id; first=%q second=%q (idempotency not enforced)", id1, id2)
	}
}

// TestWrkfEvidenceAdd_Idempotency_Mismatch verifies that calling wrkf.evidence.add
// with the same idempotencyKey but DIFFERENT params returns WRKF_IDEMPOTENCY_MISMATCH
// (§9.7). The first call establishes the committed record; the second must be
// rejected before any evidence/event is written.
//
// RED: idempotencyKey is silently ignored → second call succeeds instead of
// returning WRKF_IDEMPOTENCY_MISMATCH.
func TestWrkfEvidenceAdd_Idempotency_Mismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"e3100000-0000-4000-8000-000000000011",
		"ev-idem-mismatch-test", "Evidence Idempotency Mismatch Task")

	_ = p3InstallAndAttach(t, dbPath, tplPath, taskID)

	const idemKey = "smokey:p3:ev:idem:mismatch:001"
	frames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":           taskID,
			"kind":           "red_test",
			"ref":            "test/smokey/p3/idem-mismatch-orig",
			"role":           "tester",
			"facts":          p3RedFacts(),
			"idempotencyKey": idemKey,
		}),
		mkRPC("e2", "wrkf.evidence.add", map[string]any{
			"task":           taskID,
			"kind":           "red_test",
			"ref":            "test/smokey/p3/idem-mismatch-DIFFERENT", // different ref → different canonical hash
			"role":           "tester",
			"facts":          p3RedFacts(),
			"idempotencyKey": idemKey, // same key, different content
		}),
	)
	// frames: init(0), add1(1), add2(2), shutdown(3)
	// First add must succeed.
	p2ResultOrFail(t, frames[1], "first wrkf.evidence.add (original)")

	// Second add same key + different ref must return WRKF_IDEMPOTENCY_MISMATCH.
	// RED: currently succeeds (idempotencyKey ignored) → code is "" → test fails.
	code := p2ErrCode(frames[2])
	if code != "WRKF_IDEMPOTENCY_MISMATCH" {
		t.Errorf("same idempotencyKey + different params: want WRKF_IDEMPOTENCY_MISMATCH, got error.data.code=%q", code)
	}
}

// TestWrkfEvidenceAdd_Idempotency_CanonicalKeyOrder verifies that two calls with
// semantically identical JSON data (same key-value pairs, DIFFERENT key order)
// are treated as the same canonical request hash and the second is a replay,
// not a mismatch (§9.7 canonicalization via shared canonicalizer).
//
// RED: no idempotency enforcement → both calls succeed creating two distinct rows.
func TestWrkfEvidenceAdd_Idempotency_CanonicalKeyOrder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"e3100000-0000-4000-8000-000000000012",
		"ev-idem-canon-test", "Evidence Idempotency Canonical Key Order Task")

	_ = p3InstallAndAttach(t, dbPath, tplPath, taskID)

	const idemKey = "smokey:p3:ev:idem:canon:001"
	// Use the `data` field (free-form JSON, not schema-validated) to exercise
	// canonical key ordering: {"z":3,"a":1,"m":2} and {"a":1,"m":2,"z":3}
	// have the SAME canonical form after round-trip JSON unmarshal+marshal.
	frames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":           taskID,
			"kind":           "red_test",
			"ref":            "test/smokey/p3/idem-canon-001",
			"role":           "tester",
			"facts":          p3RedFacts(),
			"data":           json.RawMessage(`{"z":3,"a":1,"m":2}`), // key order A
			"idempotencyKey": idemKey,
		}),
		mkRPC("e2", "wrkf.evidence.add", map[string]any{
			"task":           taskID,
			"kind":           "red_test",
			"ref":            "test/smokey/p3/idem-canon-001",
			"role":           "tester",
			"facts":          p3RedFacts(),
			"data":           json.RawMessage(`{"a":1,"m":2,"z":3}`), // key order B — same canonical
			"idempotencyKey": idemKey,
		}),
	)
	// frames: init(0), add1(1), add2(2), shutdown(3)
	r1 := p2ResultOrFail(t, frames[1], "first wrkf.evidence.add")
	// Second call must succeed as a replay, not error as mismatch.
	r2 := p2ResultOrFail(t, frames[2], "second wrkf.evidence.add (canonical replay — different key order, same values)")

	id1, _ := r1["id"].(string)
	id2, _ := r2["id"].(string)

	if id1 == "" {
		t.Error("first evidence.add returned empty id")
	}
	// RED: without canonicalization + idempotency, two distinct rows → id2 != id1.
	if id1 != id2 {
		t.Errorf("canonical replay: different JSON key order must replay (same canonical hash); first=%q second=%q", id1, id2)
	}
}

func TestWrkfEvidenceAdd_ProvenanceRoundTripAndIdempotencyMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"e3100000-0000-4000-8000-000000000013",
		"ev-provenance-test", "Evidence Provenance Test")

	_ = p3InstallAndAttach(t, dbPath, tplPath, taskID)

	const idemKey = "smokey:p3:ev:provenance:001"
	const contentHash = "sha256:provenance-001"
	frames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":           taskID,
			"kind":           "red_test",
			"ref":            "test/smokey/p3/provenance-001",
			"role":           "tester",
			"facts":          p3RedFacts(),
			"contentHash":    contentHash,
			"build":          map[string]any{"id": "build-001", "version": "2026.06.15", "env": "ci"},
			"idempotencyKey": idemKey,
		}),
		mkRPC("l1", "wrkf.evidence.list", map[string]any{"task": taskID}),
		mkRPC("e2", "wrkf.evidence.add", map[string]any{
			"task":           taskID,
			"kind":           "red_test",
			"ref":            "test/smokey/p3/provenance-001",
			"role":           "tester",
			"facts":          p3RedFacts(),
			"contentHash":    "sha256:changed",
			"build":          map[string]any{"id": "build-001", "version": "2026.06.15", "env": "ci"},
			"idempotencyKey": idemKey,
		}),
	)
	addResult := p2ResultOrFail(t, frames[1], "wrkf.evidence.add with provenance")
	if got, _ := addResult["contentHash"].(string); got != contentHash {
		t.Fatalf("evidence.add contentHash: want %q, got %q", contentHash, got)
	}
	build, _ := addResult["build"].(map[string]any)
	if build["id"] != "build-001" || build["version"] != "2026.06.15" || build["env"] != "ci" {
		t.Fatalf("evidence.add build provenance mismatch: %#v", build)
	}

	items := p3EvidenceItems(t, frames[2], "wrkf.evidence.list")
	var found bool
	for _, item := range items {
		if got, _ := item["contentHash"].(string); got == contentHash {
			b, _ := item["build"].(map[string]any)
			if b["id"] == "build-001" && b["version"] == "2026.06.15" && b["env"] == "ci" {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("wrkf.evidence.list did not round-trip contentHash/build provenance: %#v", items)
	}
	if code := p2ErrCode(frames[3]); code != "WRKF_IDEMPOTENCY_MISMATCH" {
		t.Fatalf("same idempotencyKey + changed provenance: want WRKF_IDEMPOTENCY_MISMATCH, got %q", code)
	}
}

func TestWrkfRoleBindings_AuthorizeNextAndTransition(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"e3100000-0000-4000-8000-000000000014",
		"role-binding-test", "Role Binding Test")

	setupFrames := p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     taskID,
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	p2ResultOrFail(t, setupFrames[1], "install")
	p2ResultOrFail(t, setupFrames[2], "attach")
	before := p2ResultOrFail(t, setupFrames[3], "task.show before role bind")
	beforeETag, _ := before["etag"].(float64)

	frames := p3Run(t, dbPath,
		mkRPC("b1", "wrkf.role.bind", map[string]any{
			"task":        taskID,
			"role":        "tester",
			"principal_ref":       "agent:alice",
			"deliveryRef": "alice@wrkq:T-role",
			"lane":        "test",
		}),
		mkRPC("l1", "wrkf.role.list", map[string]any{"task": taskID}),
		mkRPC("s2", "wrkq.task.show", map[string]any{"task": taskID}),
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":  taskID,
			"kind":  "red_test",
			"ref":   "test/smokey/p3/role-red-001",
			"principal_ref": "agent:alice",
			"role":  "tester",
			"facts": p3RedFacts(),
		}),
		mkRPC("n1", "wrkf.instance.next", map[string]any{"task": taskID}),
		mkRPC("bad", "wrkf.transition.apply", map[string]any{
			"task":           taskID,
			"transition":     "author_red",
			"principal_ref":          "agent:bob",
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": "role-binding-bad",
		}),
		mkRPC("ok", "wrkf.transition.apply", map[string]any{
			"task":           taskID,
			"transition":     "author_red",
			"principal_ref":          "agent:alice",
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": "role-binding-ok",
		}),
		mkRPC("set", "wrkf.role.set", map[string]any{
			"task":    taskID,
			"roleMap": map[string]any{"tester": "agent:carol"},
		}),
		mkRPC("unbind", "wrkf.role.unbind", map[string]any{
			"task":  taskID,
			"role":  "tester",
			"principal_ref": "agent:carol",
		}),
	)
	bind := p2ResultOrFail(t, frames[1], "wrkf.role.bind")
	if bind["role"] != "tester" || bind["principal_ref"] != "agent:alice" || bind["deliveryRef"] != "alice@wrkq:T-role" {
		t.Fatalf("wrkf.role.bind returned wrong binding: %#v", bind)
	}
	listRaw, _ := frames[2]["result"].([]any)
	if len(listRaw) != 1 {
		t.Fatalf("wrkf.role.list: want one binding, got %#v", frames[2]["result"])
	}
	after := p2ResultOrFail(t, frames[3], "task.show after role bind")
	afterETag, _ := after["etag"].(float64)
	if afterETag != beforeETag {
		t.Fatalf("role binding mutated task etag: before=%v after=%v", beforeETag, afterETag)
	}
	p2ResultOrFail(t, frames[4], "wrkf.evidence.add role evidence")
	next := p2ResultOrFail(t, frames[5], "wrkf.instance.next")
	actions, _ := next["actions"].([]any)
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
	setRaw, _ := frames[8]["result"].([]any)
	if len(setRaw) != 1 {
		t.Fatalf("wrkf.role.set: want replacement binding, got %#v", frames[8]["result"])
	}
	unbindRaw, _ := frames[9]["result"].([]any)
	if len(unbindRaw) != 0 {
		t.Fatalf("wrkf.role.unbind: want no remaining tester bindings, got %#v", frames[9]["result"])
	}
}

func TestWrkfEventQuery_ReplaysTransitionEventsWithFiltersAndCursor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	task1ID := p2SeedTask(t, dbPath,
		"e3200000-0000-4000-8000-000000000001",
		"event-query-test-1", "Event Query Test 1")
	task2ID := p2SeedTask(t, dbPath,
		"e3200000-0000-4000-8000-000000000002",
		"event-query-test-2", "Event Query Test 2")
	task3ID := p2SeedTask(t, dbPath,
		"e3200000-0000-4000-8000-000000000003",
		"event-query-low-risk", "Event Query Low Risk")
	task4UUID := "e3200000-0000-4000-8000-000000000004"
	task4ID := p2SeedTask(t, dbPath,
		task4UUID,
		"event-query-legacy-role", "Event Query Legacy Role")
	seedLegacyTesterAssignment(t, dbPath, task4UUID)

	setupFrames := p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":  task1ID,
			"principal_ref": "agent:smokey",
			"patch": map[string]any{
				"riskClass": "medium",
			},
		}),
		mkRPC("u2", "wrkq.task.update", map[string]any{
			"task":  task2ID,
			"principal_ref": "agent:smokey",
			"patch": map[string]any{
				"riskClass": "high",
			},
		}),
		mkRPC("u3", "wrkq.task.update", map[string]any{
			"task":  task3ID,
			"principal_ref": "agent:smokey",
			"patch": map[string]any{
				"riskClass": "low",
			},
		}),
		mkRPC("u4", "wrkq.task.update", map[string]any{
			"task":  task4ID,
			"principal_ref": "agent:smokey",
			"patch": map[string]any{
				"riskClass": "medium",
			},
		}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     task1ID,
			"principal_ref":    "agent:smokey",
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("a2", "wrkq.workflow.attach", map[string]any{
			"task":     task2ID,
			"principal_ref":    "agent:smokey",
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("a3", "wrkq.workflow.attach", map[string]any{
			"task":     task3ID,
			"principal_ref":    "agent:smokey",
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("a4", "wrkq.workflow.attach", map[string]any{
			"task":     task4ID,
			"principal_ref":    "agent:smokey",
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("b1", "wrkf.role.bind", map[string]any{
			"task":        task1ID,
			"role":        "tester",
			"principal_ref":       "agent:tester-a",
			"deliveryRef": "tester-a@wrkq:" + task1ID,
		}),
		mkRPC("b1b", "wrkf.role.bind", map[string]any{
			"task":        task1ID,
			"role":        "tester",
			"principal_ref":       "agent:tester-aa",
			"deliveryRef": "tester-aa@wrkq:" + task1ID,
		}),
		mkRPC("b2", "wrkf.role.bind", map[string]any{
			"task":        task2ID,
			"role":        "tester",
			"principal_ref":       "agent:tester-b",
			"deliveryRef": "tester-b@wrkq:" + task2ID,
		}),
		mkRPC("b3", "wrkf.role.bind", map[string]any{
			"task":        task3ID,
			"role":        "tester",
			"principal_ref":       "agent:tester-low",
			"deliveryRef": "tester-low@wrkq:" + task3ID,
		}),
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":  task1ID,
			"kind":  "red_test",
			"ref":   "test/smokey/p3/event-query-red-1",
			"principal_ref": "agent:tester-a",
			"role":  "tester",
			"facts": p3RedFacts(),
		}),
		mkRPC("e2", "wrkf.evidence.add", map[string]any{
			"task":  task2ID,
			"kind":  "red_test",
			"ref":   "test/smokey/p3/event-query-red-2",
			"principal_ref": "agent:tester-b",
			"role":  "tester",
			"facts": p3RedFacts(),
		}),
		mkRPC("e3", "wrkf.evidence.add", map[string]any{
			"task":  task3ID,
			"kind":  "red_test",
			"ref":   "test/smokey/p3/event-query-red-low",
			"principal_ref": "agent:tester-low",
			"role":  "tester",
			"facts": p3RedFacts(),
		}),
		mkRPC("e4", "wrkf.evidence.add", map[string]any{
			"task":  task4ID,
			"kind":  "red_test",
			"ref":   "test/smokey/p3/event-query-red-legacy",
			"principal_ref": "agent:legacy",
			"role":  "tester",
			"facts": p3RedFacts(),
		}),
		mkRPC("t1", "wrkf.transition.apply", map[string]any{
			"task":           task1ID,
			"transition":     "author_red",
			"principal_ref":          "agent:tester-a",
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": "event-query-transition-1",
		}),
		mkRPC("t2", "wrkf.transition.apply", map[string]any{
			"task":           task2ID,
			"transition":     "author_red",
			"principal_ref":          "agent:tester-b",
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": "event-query-transition-2",
		}),
		mkRPC("t3", "wrkf.transition.apply", map[string]any{
			"task":           task3ID,
			"transition":     "author_red",
			"principal_ref":          "agent:tester-low",
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": "event-query-transition-3",
		}),
		mkRPC("t4", "wrkf.transition.apply", map[string]any{
			"task":           task4ID,
			"transition":     "author_red",
			"principal_ref":          "agent:legacy",
			"role":           "tester",
			"expectRevision": float64(0),
			"idempotencyKey": "event-query-transition-4",
		}),
	)
	for i, label := range []string{
		"initialize", "install", "risk task1", "risk task2", "risk task3", "risk task4",
		"attach task1", "attach task2", "attach task3", "attach task4",
		"bind task1", "bind task1 second tester", "bind task2", "bind task3",
		"evidence task1", "evidence task2", "evidence task3", "evidence task4",
		"transition task1", "transition task2", "transition task3", "transition task4",
	} {
		if i == 0 {
			continue
		}
		p2ResultOrFail(t, setupFrames[i], label)
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
	if item["id"] == "" || item["transitionedAt"] == "" || item["instanceId"] == "" {
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

// ─── Dual-selector guard (§2.5) ──────────────────────────────────────────────

// TestWrkfDualSelector_InstanceShow_MismatchReject verifies that wrkf.instance.show
// rejects a call where BOTH task and instanceId are supplied but resolve to
// DIFFERENT instances, returning WRKF_VALIDATION pre-mutation (§2.5).
//
// RED: current implementation ignores instanceId (always uses task selector);
// returns task1's instance without validation when instanceId=inst2.
func TestWrkfDualSelector_InstanceShow_MismatchReject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)

	task1ID := p2SeedTask(t, dbPath,
		"d2100000-0000-4000-8000-000000000001",
		"dual-show-task-1", "Dual Selector Show Task 1")
	task2ID := p2SeedTask(t, dbPath,
		"d2100000-0000-4000-8000-000000000002",
		"dual-show-task-2", "Dual Selector Show Task 2")

	// Install template once, attach both tasks; capture instance IDs.
	setupFrames := p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     task1ID,
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("a2", "wrkq.workflow.attach", map[string]any{
			"task":     task2ID,
			"workflow": "wrkq-code-change@1",
		}),
	)
	// setupFrames: init(0), install(1), attach1(2), attach2(3), shutdown(4)
	p2ResultOrFail(t, setupFrames[1], "wrkf.workflow.install")
	p2ResultOrFail(t, setupFrames[2], "wrkq.workflow.attach task1")
	attach2 := p2ResultOrFail(t, setupFrames[3], "wrkq.workflow.attach task2")

	inst2, ok := attach2["instance"].(map[string]any)
	if !ok || inst2 == nil {
		t.Fatalf("task2 attach returned no instance object; result keys: %v", mapKeys(attach2))
	}
	instance2ID, _ := inst2["id"].(string)
	if instance2ID == "" {
		t.Fatal("task2 attach returned empty instance id")
	}

	// Call wrkf.instance.show with task=task1 but instanceId=task2's instance (MISMATCH).
	testFrames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.instance.show", map[string]any{
			"task":       task1ID,     // resolves to instance1
			"instanceId": instance2ID, // resolves to instance2 — MISMATCH
		}),
	)
	// frames: init(0), show(1), shutdown(2)
	// RED: current impl ignores instanceId → returns task1's instance, no error.
	code := p2ErrCode(testFrames[1])
	if code != "WRKF_VALIDATION" {
		if _, hasResult := testFrames[1]["result"]; hasResult {
			t.Errorf("dual-selector mismatch (wrkf.instance.show): expected WRKF_VALIDATION pre-mutation, got result instead (instanceId mismatch not validated)")
		} else {
			t.Errorf("dual-selector mismatch (wrkf.instance.show): want WRKF_VALIDATION, got error.data.code=%q", code)
		}
	}
}

// TestWrkfDualSelector_EvidenceAdd_MismatchReject verifies that wrkf.evidence.add
// rejects a call where BOTH task and instanceId are supplied but resolve to
// DIFFERENT instances, returning WRKF_VALIDATION BEFORE any evidence/event is
// written (§2.5 pre-mutation guard).
//
// RED: EvidenceAddParams has no instanceId field — param is silently ignored;
// evidence is written for task1's instance without dual-selector validation.
func TestWrkfDualSelector_EvidenceAdd_MismatchReject(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)

	task1ID := p2SeedTask(t, dbPath,
		"d2100000-0000-4000-8000-000000000011",
		"dual-ev-task-1", "Dual Selector Evidence Task 1")
	task2ID := p2SeedTask(t, dbPath,
		"d2100000-0000-4000-8000-000000000012",
		"dual-ev-task-2", "Dual Selector Evidence Task 2")

	setupFrames := p3Run(t, dbPath,
		mkRPC("i1", "wrkf.workflow.install", map[string]any{"path": tplPath}),
		mkRPC("a1", "wrkq.workflow.attach", map[string]any{
			"task":     task1ID,
			"workflow": "wrkq-code-change@1",
		}),
		mkRPC("a2", "wrkq.workflow.attach", map[string]any{
			"task":     task2ID,
			"workflow": "wrkq-code-change@1",
		}),
	)
	p2ResultOrFail(t, setupFrames[1], "install")
	p2ResultOrFail(t, setupFrames[2], "attach task1")
	attach2 := p2ResultOrFail(t, setupFrames[3], "attach task2")

	inst2, ok := attach2["instance"].(map[string]any)
	if !ok || inst2 == nil {
		t.Fatalf("task2 attach returned no instance: result keys %v", mapKeys(attach2))
	}
	instance2ID, _ := inst2["id"].(string)
	if instance2ID == "" {
		t.Fatal("task2 attach returned empty instance id")
	}

	// Call wrkf.evidence.add with task=task1 but instanceId=task2's instance (MISMATCH).
	testFrames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", map[string]any{
			"task":       task1ID,     // task1's selector
			"instanceId": instance2ID, // task2's instance — MISMATCH
			"kind":       "red_test",
			"ref":        "test/smokey/p3/dual-ev-mismatch-001",
			"role":       "tester",
			"facts":      p3RedFacts(),
		}),
	)
	// frames: init(0), add(1), shutdown(2)
	// RED: EvidenceAddParams has no instanceId → ignored → evidence written → no error.
	code := p2ErrCode(testFrames[1])
	if code != "WRKF_VALIDATION" {
		if _, hasResult := testFrames[1]["result"]; hasResult {
			t.Errorf("dual-selector mismatch (wrkf.evidence.add): expected WRKF_VALIDATION pre-mutation, got result (instanceId field absent from EvidenceAddParams)")
		} else {
			t.Errorf("dual-selector mismatch (wrkf.evidence.add): want WRKF_VALIDATION, got error.data.code=%q", code)
		}
	}
}

// TestWrkfDualSelector_InstanceIDOnly_Accepted verifies that wrkf.instance.show
// accepts a call with ONLY instanceId (no task selector) per §2.5 — at least
// one selector is sufficient.
//
// RED: current impl requires the task selector and returns
// WRKF_VALIDATION "task selector is required in P1" when instanceId is
// supplied alone.
func TestWrkfDualSelector_InstanceIDOnly_Accepted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	taskID := p2SeedTask(t, dbPath,
		"d2100000-0000-4000-8000-000000000021",
		"dual-id-only-test", "Dual Selector InstanceID Only Test")

	instanceID := p3InstallAndAttach(t, dbPath, tplPath, taskID)

	// Call wrkf.instance.show with ONLY instanceId (no task field).
	frames := p3Run(t, dbPath,
		mkRPC("s1", "wrkf.instance.show", map[string]any{
			"instanceId": instanceID, // no "task" field
		}),
	)
	// frames: init(0), show(1), shutdown(2)
	// RED: current handler returns WRKF_VALIDATION "task selector is required in P1";
	// p2ResultOrFail will t.Fatalf, failing this test for the right reason.
	result := p2ResultOrFail(t, frames[1], "wrkf.instance.show with instanceId-only must return the instance")

	// The returned instance must have the expected id.
	gotID, _ := result["id"].(string)
	if gotID != instanceID {
		t.Errorf("wrkf.instance.show instanceId-only: want instance id %q, got %q", instanceID, gotID)
	}
}
