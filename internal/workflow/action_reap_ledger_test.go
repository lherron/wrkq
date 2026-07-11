package workflow

import (
	"encoding/json"
	"testing"
)

// reapLedgerEntry returns the single kind=reap ledger entry for a task, failing
// if there is not exactly one. It reads ONLY through the public ListLedger
// surface — the same view a blind reader has via `wrkf ledger list --task`.
func reapLedgerEntry(t *testing.T, svc *Service, taskUUID string) LedgerEntry {
	t.Helper()
	res, err := svc.ListLedger(ListLedgerParams{TaskID: taskUUID, Kind: "reap"})
	if err != nil {
		t.Fatalf("ListLedger reap: %v", err)
	}
	if len(res.Entries) != 1 {
		t.Fatalf("reap ledger entries = %d, want 1 (%+v)", len(res.Entries), res.Entries)
	}
	return res.Entries[0]
}

func decodeReapBody(t *testing.T, entry LedgerEntry) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(entry.Body, &body); err != nil {
		t.Fatalf("unmarshal reap ledger body: %v", err)
	}
	return body
}

// TestReapTranscribesReapLedgerEntryFailed proves an expired-lease reap with no
// side-effect ambiguity writes a kind=reap ledger entry carrying
// classification=operational_failed and refs.evidence pointing at the reap
// failure evidence, stamped written_by=agent:wrkqd about the reaped run's
// bound principal.
func TestReapTranscribesReapLedgerEntryFailed(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	runID := triage.Binding.Run.ID
	expireActionRunForTest(t, svc, runID)

	reaped, err := svc.ReapActions(ReapActionsParams{Task: taskUUID, Action: "triage", ExpiredBefore: "2026-01-01T00:00:00Z", PrincipalRef: "agent:wrkqd"})
	if err != nil {
		t.Fatalf("ReapActions: %v", err)
	}
	if len(reaped.Items) != 1 || reaped.Items[0].Status != "failed" {
		t.Fatalf("reap = %+v, want one failed run", reaped.Items)
	}

	entry := reapLedgerEntry(t, svc, taskUUID)
	if entry.Kind != "reap" {
		t.Fatalf("kind = %q, want reap", entry.Kind)
	}
	if entry.WrittenBy != "agent:wrkqd" {
		t.Fatalf("writtenBy = %q, want agent:wrkqd", entry.WrittenBy)
	}
	if entry.AboutPrincipalRef != "agent:triage" {
		t.Fatalf("about = %q, want agent:triage (reaped run's bound principal)", entry.AboutPrincipalRef)
	}

	body := decodeReapBody(t, entry)
	if body["classification"] != "operational_failed" {
		t.Fatalf("classification = %#v, want operational_failed", body["classification"])
	}
	if reason, _ := body["reason"].(string); reason == "" {
		t.Fatalf("reason empty; want the reap reason string")
	}

	// refs.evidence must point at the reap failure evidence written in the same tx.
	wantEv := settledFailureEvidenceForRun(t, svc, runID)
	refs, _ := body["refs"].(map[string]any)
	evList, _ := refs["evidence"].([]any)
	if len(evList) != 1 || evList[0] != wantEv.ID {
		t.Fatalf("refs.evidence = %#v, want [%q]", refs["evidence"], wantEv.ID)
	}
}

// TestReapTranscribesReapLedgerEntryOperatorRequired proves the operator_required
// routing outcome is mirrored into the ledger classification.
func TestReapTranscribesReapLedgerEntryOperatorRequired(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")
	runID := impl.Binding.Run.ID
	expireActionRunForTest(t, svc, runID)

	reaped, err := svc.ReapActions(ReapActionsParams{Task: taskUUID, Action: "implement", ExpiredBefore: "2026-01-01T00:00:00Z", PrincipalRef: "agent:wrkqd"})
	if err != nil {
		t.Fatalf("ReapActions: %v", err)
	}
	if len(reaped.Items) != 1 || reaped.Items[0].Status != "operator_required" {
		t.Fatalf("reap = %+v, want one operator_required run", reaped.Items)
	}

	entry := reapLedgerEntry(t, svc, taskUUID)
	body := decodeReapBody(t, entry)
	if body["classification"] != "operator_required" {
		t.Fatalf("classification = %#v, want operator_required", body["classification"])
	}
	wantEv := settledFailureEvidenceForRun(t, svc, runID)
	refs, _ := body["refs"].(map[string]any)
	evList, _ := refs["evidence"].([]any)
	if len(evList) != 1 || evList[0] != wantEv.ID {
		t.Fatalf("refs.evidence = %#v, want [%q]", refs["evidence"], wantEv.ID)
	}
}

// TestReapLedgerBlindReadAnswersTerminalization is the blind-read bar: a reader
// with ONLY the ledger list output can answer "what terminalized this run and
// why" — kind, classification, reason, and the run identity are all decodable
// from the ledger body without consulting engine evidence tables.
func TestReapLedgerBlindReadAnswersTerminalization(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	runID := triage.Binding.Run.ID
	expireActionRunForTest(t, svc, runID)
	if _, err := svc.ReapActions(ReapActionsParams{Task: taskUUID, Action: "triage", ExpiredBefore: "2026-01-01T00:00:00Z", PrincipalRef: "agent:wrkqd"}); err != nil {
		t.Fatalf("ReapActions: %v", err)
	}

	entry := reapLedgerEntry(t, svc, taskUUID)
	body := decodeReapBody(t, entry)
	// what terminalized it: an engine reap.
	if entry.Kind != "reap" || entry.WrittenBy != "agent:wrkqd" {
		t.Fatalf("blind read cannot identify the terminalizer: kind=%q writtenBy=%q", entry.Kind, entry.WrittenBy)
	}
	// why: classification + reason.
	if _, ok := body["classification"].(string); !ok {
		t.Fatalf("blind read cannot classify: %#v", body["classification"])
	}
	if reason, _ := body["reason"].(string); reason == "" {
		t.Fatalf("blind read has no reason string")
	}
	// which run: run identity rides the body.
	runBody, _ := body["run"].(map[string]any)
	if runBody["id"] != runID {
		t.Fatalf("run.id = %#v, want %q", runBody["id"], runID)
	}
}

// TestReapLedgerAppendIsSameTxAsSettle proves atomicity: when the ledger append
// fails, the reap settle does not land. Injecting a hard append failure (the
// ledger table is gone) must leave the run active, with no failure evidence and
// no ledger entry — no terminalized-but-unrecorded window.
func TestReapLedgerAppendIsSameTxAsSettle(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	runID := triage.Binding.Run.ID
	expireActionRunForTest(t, svc, runID)

	// Inject a deterministic append failure at the storage layer.
	if _, err := svc.db.Exec(`DROP TABLE ledger_entry`); err != nil {
		t.Fatalf("drop ledger_entry: %v", err)
	}

	if _, err := svc.ReapActions(ReapActionsParams{Task: taskUUID, Action: "triage", ExpiredBefore: "2026-01-01T00:00:00Z", PrincipalRef: "agent:wrkqd"}); err == nil {
		t.Fatal("ReapActions succeeded despite ledger append failure; settle was not rolled back")
	}

	// The settle must not have landed: run stays active.
	run, err := svc.ShowRun(runID)
	if err != nil {
		t.Fatalf("ShowRun: %v", err)
	}
	if run.Status != "active" {
		t.Fatalf("run status = %q, want active (settle must roll back with the failed append)", run.Status)
	}
	// And no reap failure evidence was committed.
	var evCount int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_evidence WHERE run_id = ? AND ref = ?`, runID, "wrkf-action:"+runID+":reap").Scan(&evCount); err != nil {
		t.Fatalf("count reap evidence: %v", err)
	}
	if evCount != 0 {
		t.Fatalf("reap evidence rows = %d, want 0 (must roll back with the failed append)", evCount)
	}
}
