//go:build wrkq_local

package workrpc_test

// wrkf_evidence_acceptance_test.go — wrkf.evidence.add/show/list over the real
// wrkf JSON-RPC stdio surface (T-04428 P3; docs/wrkq-wrkf-rpc.md §9.7): runId
// persistence, instance-only listing, idempotent replay (including canonical
// key order), idempotency mismatch, and provenance round-trip.

import (
	"encoding/json"
	"testing"
)

// attachedCodeChangeTask seeds a task under wrkq-code-change@1 and returns its
// id and instance id.
func attachedCodeChangeTask(t *testing.T, dbPath, uuid, slug string) (taskID, instanceID string) {
	t.Helper()
	taskID = p2SeedTask(t, dbPath, uuid, slug, slug)
	return taskID, p3InstallAndAttach(t, dbPath, p2WorkflowTemplatePath(t), taskID)
}

// TestWrkfEvidenceAdd_RunIDPersisted verifies that a runId supplied to
// wrkf.evidence.add comes back from both wrkf.evidence.show and
// wrkf.evidence.list (§9.7).
func TestWrkfEvidenceAdd_RunIDPersisted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := attachedCodeChangeTask(t, dbPath, "e3100000-0000-4000-8000-000000000001", "ev-runid-test")

	const wantRunID = "run-smokey-p3-001"
	frames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", redEvidenceParams(taskID, "test/smokey/p3/runid-001", map[string]any{"runId": wantRunID})),
		mkRPC("l1", "wrkf.evidence.list", map[string]any{"task": taskID}),
	)
	evidenceID, _ := p2ResultOrFail(t, frames[1], "wrkf.evidence.add must succeed")["id"].(string)
	if evidenceID == "" {
		t.Fatal("wrkf.evidence.add returned empty id; cannot test runId persistence")
	}

	showFrames := p3Run(t, dbPath, mkRPC("s1", "wrkf.evidence.show", map[string]any{"id": evidenceID}))
	if got, _ := p2ResultOrFail(t, showFrames[1], "wrkf.evidence.show must return evidence")["runId"].(string); got != wantRunID {
		t.Errorf("wrkf.evidence.show: runId want %q, got %q", wantRunID, got)
	}

	items := p3EvidenceItems(t, frames[2], "wrkf.evidence.list")
	if len(items) == 0 {
		t.Fatal("wrkf.evidence.list: returned zero items after adding evidence")
	}
	found := false
	for _, item := range items {
		if rid, _ := item["runId"].(string); rid == wantRunID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("wrkf.evidence.list: no item has runId=%q", wantRunID)
	}
}

// TestWrkfEvidenceList_InstanceOnlyEmpty verifies wrkf.evidence.list accepts
// instance-only selection and returns a JSON array ([]), never null, when the
// instance has no evidence (T-06324).
func TestWrkfEvidenceList_InstanceOnlyEmpty(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, instanceID := attachedCodeChangeTask(t, dbPath, "e3100000-0000-4000-8000-000000000042", "ev-instance-only-empty")

	frames := p3Run(t, dbPath,
		mkRPC("l1", "wrkf.evidence.list", map[string]any{"instanceId": instanceID}),
		mkRPC("l2", "wrkf.evidence.list", map[string]any{"task": taskID, "instanceId": instanceID}),
	)
	for i, label := range []string{"instance-only", "task+instanceId"} {
		frame := frames[1+i]
		if errObj, ok := frame["error"]; ok {
			t.Fatalf("wrkf.evidence.list (%s): unexpected error: %v", label, errObj)
		}
		raw, present := frame["result"]
		if !present || raw == nil {
			t.Fatalf("wrkf.evidence.list (%s): result must be a JSON array, got null/absent", label)
		}
		arr, ok := raw.([]any)
		if !ok {
			t.Fatalf("wrkf.evidence.list (%s): result must decode as an array, got %T", label, raw)
		}
		if len(arr) != 0 {
			t.Fatalf("wrkf.evidence.list (%s): expected empty list, got %d items", label, len(arr))
		}
	}
}

// TestWrkfEvidenceAdd_Idempotency_Replay verifies that a second
// wrkf.evidence.add under the same idempotencyKey with the same canonical
// request returns the SAME evidence id (§9.7) — whether the params are
// identical or differ only in the JSON key order of free-form data.
func TestWrkfEvidenceAdd_Idempotency_Replay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	cases := []struct {
		name          string
		uuid          string
		first, second map[string]any
	}{
		{
			name:   "identical params",
			uuid:   "e3100000-0000-4000-8000-000000000010",
			first:  map[string]any{},
			second: map[string]any{},
		},
		{
			// `data` is free-form JSON, not schema-validated; both orders have
			// the same canonical form after round-trip unmarshal+marshal.
			name:   "canonical key order",
			uuid:   "e3100000-0000-4000-8000-000000000012",
			first:  map[string]any{"data": json.RawMessage(`{"z":3,"a":1,"m":2}`)},
			second: map[string]any{"data": json.RawMessage(`{"a":1,"m":2,"z":3}`)},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath := migratedDB(t)
			taskID, _ := attachedCodeChangeTask(t, dbPath, tc.uuid, "ev-idem-replay")
			const idemKey = "smokey:p3:ev:idem:replay:001"
			request := func(extra map[string]any) map[string]any {
				params := redEvidenceParams(taskID, "test/smokey/p3/idem-replay-001", extra)
				params["idempotencyKey"] = idemKey
				return params
			}
			frames := p3Run(t, dbPath,
				mkRPC("e1", "wrkf.evidence.add", request(tc.first)),
				mkRPC("e2", "wrkf.evidence.add", request(tc.second)),
			)
			id1, _ := p2ResultOrFail(t, frames[1], "first wrkf.evidence.add")["id"].(string)
			id2, _ := p2ResultOrFail(t, frames[2], "second wrkf.evidence.add (replay)")["id"].(string)
			if id1 == "" {
				t.Error("first evidence.add returned empty id")
			}
			if id1 != id2 {
				t.Errorf("idempotency replay: want same evidence id; first=%q second=%q", id1, id2)
			}
		})
	}
}

// TestWrkfEvidenceAdd_Idempotency_Mismatch verifies that reusing an
// idempotencyKey with a different ref returns WRKF_IDEMPOTENCY_MISMATCH (§9.7).
func TestWrkfEvidenceAdd_Idempotency_Mismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := attachedCodeChangeTask(t, dbPath, "e3100000-0000-4000-8000-000000000011", "ev-idem-mismatch-test")

	key := map[string]any{"idempotencyKey": "smokey:p3:ev:idem:mismatch:001"}
	frames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", redEvidenceParams(taskID, "test/smokey/p3/idem-mismatch-orig", key)),
		mkRPC("e2", "wrkf.evidence.add", redEvidenceParams(taskID, "test/smokey/p3/idem-mismatch-DIFFERENT", key)),
	)
	p2ResultOrFail(t, frames[1], "first wrkf.evidence.add (original)")
	if code := p2ErrCode(frames[2]); code != "WRKF_IDEMPOTENCY_MISMATCH" {
		t.Errorf("same idempotencyKey + different params: want WRKF_IDEMPOTENCY_MISMATCH, got error.data.code=%q", code)
	}
}

// TestWrkfEvidenceAdd_ProvenanceRoundTripAndIdempotencyMismatch verifies that
// contentHash and build provenance round-trip through add and list, and that
// changing provenance under the same idempotencyKey is a mismatch.
func TestWrkfEvidenceAdd_ProvenanceRoundTripAndIdempotencyMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := attachedCodeChangeTask(t, dbPath, "e3100000-0000-4000-8000-000000000013", "ev-provenance-test")

	const contentHash = "sha256:provenance-001"
	provenance := func(hash string) map[string]any {
		return map[string]any{
			"contentHash":    hash,
			"build":          map[string]any{"id": "build-001", "version": "2026.06.15", "env": "ci"},
			"idempotencyKey": "smokey:p3:ev:provenance:001",
		}
	}
	isBuild001 := func(raw any) bool {
		b, _ := raw.(map[string]any)
		return b["id"] == "build-001" && b["version"] == "2026.06.15" && b["env"] == "ci"
	}
	frames := p3Run(t, dbPath,
		mkRPC("e1", "wrkf.evidence.add", redEvidenceParams(taskID, "test/smokey/p3/provenance-001", provenance(contentHash))),
		mkRPC("l1", "wrkf.evidence.list", map[string]any{"task": taskID}),
		mkRPC("e2", "wrkf.evidence.add", redEvidenceParams(taskID, "test/smokey/p3/provenance-001", provenance("sha256:changed"))),
	)
	addResult := p2ResultOrFail(t, frames[1], "wrkf.evidence.add with provenance")
	if got, _ := addResult["contentHash"].(string); got != contentHash {
		t.Fatalf("evidence.add contentHash: want %q, got %q", contentHash, got)
	}
	if !isBuild001(addResult["build"]) {
		t.Fatalf("evidence.add build provenance mismatch: %#v", addResult["build"])
	}

	items := p3EvidenceItems(t, frames[2], "wrkf.evidence.list")
	var found bool
	for _, item := range items {
		if got, _ := item["contentHash"].(string); got == contentHash && isBuild001(item["build"]) {
			found = true
		}
	}
	if !found {
		t.Fatalf("wrkf.evidence.list did not round-trip contentHash/build provenance: %#v", items)
	}
	if code := p2ErrCode(frames[3]); code != "WRKF_IDEMPOTENCY_MISMATCH" {
		t.Fatalf("same idempotencyKey + changed provenance: want WRKF_IDEMPOTENCY_MISMATCH, got %q", code)
	}
}
