//go:build wrkq_local

package workflow

// suspension_test.go — T-06260. Suspension as a first-class condition on a
// workflow instance, plus the suspended-write gate on all three write paths.
//
// Contract (WRKF_SIMPLIFICATION.md §1–§3):
//   - A template suspend outcome records a suspension (id, reason, timestamp,
//     cause pointer) without changing status/phase/outcome. The normal
//     transition still advances revision.
//   - Exactly one active suspension; a second suspend is rejected.
//   - The suspended-write gate rejects writes at all three doors
//     (TransitionForSelectors, applyActionTransitionTx, and ClaimAction). Reads,
//     inspection, and dry-run are unaffected — the gate is the entire fencing
//     story.

import (
	"database/sql"
	"testing"
)

// TestSuspendRecordsConditionWithoutStateChange proves park is a record, not a
// state change: the suspension appears; status/phase/outcome are untouched;
// the transition advances revision; and fresh reads expose the suspension.
func TestSuspendRecordsConditionWithoutStateChange(t *testing.T) {
	svc, taskUUID, before := setupSuspendOutcomeFixture(t)
	if before.Suspended() {
		t.Fatalf("fresh instance is suspended: %+v", before.Suspension)
	}

	result, err := svc.Transition(taskUUID, "park", TransitionOptions{PrincipalRef: "agent:op", Role: "coordinator"})
	if err != nil {
		t.Fatalf("Transition park: %v", err)
	}
	suspended, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after park: %v", err)
	}
	if !suspended.Suspended() {
		t.Fatalf("Suspend returned a running instance: %+v", suspended)
	}
	sus := suspended.Suspension
	eventID, _ := result["eventId"].(string)
	if sus.ID == "" || sus.Reason != "operator_required" || sus.At == "" || sus.CauseRef != eventID || eventID == "" {
		t.Fatalf("suspension record = %+v, want populated id/reason/at/causeRef", sus)
	}
	if suspended.Status != before.Status || suspended.Phase != before.Phase ||
		suspended.Outcome != before.Outcome || suspended.Revision != before.Revision+1 {
		t.Fatalf("park changed state: before=%+v rev=%d after=%+v rev=%d",
			before.State(), before.Revision, suspended.State(), suspended.Revision)
	}

	// Reads/inspection are unaffected and surface the suspension.
	readback, err := svc.InspectTask(taskUUID)
	if err != nil {
		t.Fatalf("InspectTask while suspended: %v", err)
	}
	if !readback.Suspended() || readback.Suspension.ID != sus.ID {
		t.Fatalf("readback lost suspension: %+v", readback.Suspension)
	}
	if readback.Status != before.Status || readback.Phase != before.Phase ||
		readback.Outcome != before.Outcome || readback.Revision != before.Revision+1 {
		t.Fatalf("readback state drifted: %+v rev=%d", readback.State(), readback.Revision)
	}
}

// TestSuspendRejectsEmptyReason guards the template-declared reason code as
// required input.
func TestSuspendRejectsEmptyReason(t *testing.T) {
	svc, _, _ := setupSuspendOutcomeFixture(t)
	err := withImmediateTx(svc.db, func(tx *sql.Tx) error {
		return applySuspendOutcomeTx(tx, &Instance{Status: "active"}, &SuspendSpec{Reason: "   "}, "wfe_test", svc.now().Format("2006-01-02T15:04:05Z07:00"))
	})
	requireWrkfCode(t, err, wrkfCodeValidation)
}

// TestSuspendedWriteGateDoor1 proves the gate on TransitionForSelectors: an
// otherwise-legal transition at the post-park revision bounces with
// WRKF_SUSPENDED, and dry-run inspection still passes.
func TestSuspendedWriteGateDoor1(t *testing.T) {
	svc, taskUUID, _ := setupSuspendOutcomeFixture(t)
	if _, err := svc.Transition(taskUUID, "park", TransitionOptions{PrincipalRef: "agent:op", Role: "coordinator"}); err != nil {
		t.Fatalf("Transition park: %v", err)
	}

	rev1 := int64(1)
	// Dry-run is a read: it must NOT bounce.
	if _, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		PrincipalRef: "human:test", Role: "coordinator", ExpectRevision: &rev1, DryRun: true,
	}); err != nil {
		t.Fatalf("dry-run transition on suspended instance bounced: %v", err)
	}

	// The real write bounces at the gate.
	_, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		PrincipalRef: "human:test", Role: "coordinator", ExpectRevision: &rev1,
	})
	requireWrkfCode(t, err, wrkfCodeSuspended)

	// No write landed: still active/ready at revision 0.
	after, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after bounce: %v", err)
	}
	if after.Status != "active" || after.Phase != "ready" || after.Revision != 1 {
		t.Fatalf("gate let a write through: %+v rev=%d", after.State(), after.Revision)
	}
}

// TestSuspendedWriteGateDoor2 proves the gate on applyActionTransitionTx: a
// worker that claimed before the park settles after it and bounces with
// WRKF_SUSPENDED.
func TestSuspendedWriteGateDoor2(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)

	// A worker claims triage before the park.
	triage := claimActionForTest(t, svc, taskUUID, "triage")

	before, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance before park: %v", err)
	}
	parkInstanceForTest(t, svc, taskUUID)

	// The pre-park worker settles after the park — it bounces at the gate.
	_, err = svc.SettleAction(SettleActionParams{
		ActionRunID:     triage.Binding.Run.ID,
		OwnerToken:      triage.Binding.Authority.OwnerToken,
		OwnerGeneration: triage.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence:        &ActionEvidenceInput{Summary: "triaged", Facts: `{"result":"ready"}`},
	})
	requireWrkfCode(t, err, wrkfCodeSuspended)

	// The settle wrote nothing to the instance.
	after, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after bounce: %v", err)
	}
	if after.Status != before.Status || after.Phase != before.Phase ||
		after.Outcome != before.Outcome || after.Revision != before.Revision {
		t.Fatalf("door-2 gate let a write through: before=%+v rev=%d after=%+v rev=%d",
			before.State(), before.Revision, after.State(), after.Revision)
	}
	if !after.Suspended() {
		t.Fatalf("suspension vanished after bounced settle: %+v", after)
	}
}

// TestSuspendedWriteGateDoor3 proves the claim gate rejects a new run before
// issuing work authority, carries only the active suspension record, and opens
// normally once that suspension is resolved.
func TestSuspendedWriteGateDoor3(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	parkInstanceForTest(t, svc, taskUUID)

	parked, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after park: %v", err)
	}
	_, err = svc.ClaimAction(ClaimActionParams{
		Task: taskUUID, RunnerID: "runner-a", AgentRef: "agent:cody",
		Prefer: ActionClaimPrefer{Action: "triage"}, LeaseMs: 300000,
		PriorRunProvided: true,
	})
	requireWrkfCode(t, err, wrkfCodeSuspended)
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Suspension == nil {
		t.Fatalf("claim refusal detail = %+v err=%v, want active suspension", detail, err)
	}
	if *detail.Suspension != *parked.Suspension {
		t.Fatalf("claim refusal suspension = %+v, want %+v", detail.Suspension, parked.Suspension)
	}
	if detail.Predecessor != nil {
		t.Fatalf("claim refusal leaked predecessor dossier: %+v", detail.Predecessor)
	}
	var runs int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_runs WHERE instance_id = ?`, parked.ID).Scan(&runs); err != nil {
		t.Fatalf("count runs after suspended claim: %v", err)
	}
	if runs != 0 {
		t.Fatalf("runs after suspended claim = %d, want 0", runs)
	}

	if _, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: parked.Suspension.ID, Disposition: "resume", PrincipalRef: "agent:op",
	}); err != nil {
		t.Fatalf("ResolveSuspension resume: %v", err)
	}
	claim, err := svc.ClaimAction(ClaimActionParams{
		Task: taskUUID, RunnerID: "runner-a", AgentRef: "agent:cody",
		Prefer: ActionClaimPrefer{Action: "triage"}, LeaseMs: 300000,
		PriorRunProvided: true,
	})
	if err != nil {
		t.Fatalf("ClaimAction after resume: %v", err)
	}
	if claim.Binding == nil || claim.Binding.Run.Action != "triage" {
		t.Fatalf("claim after resume = %+v, want triage binding", claim)
	}
}

// TestSuspendedClaimGatePrecedesExpiredPredecessor proves suspension wins over
// succession: a contestable predecessor remains untouched and its dossier is
// not consulted or returned until the suspension is resolved.
func TestSuspendedClaimGatePrecedesExpiredPredecessor(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	first := claimActionForTest(t, svc, taskUUID, "triage")
	if _, err := svc.db.Exec(`UPDATE workflow_runs SET lease_expires_at = '2000-01-01T00:00:00Z' WHERE id = ?`, first.Binding.Run.ID); err != nil {
		t.Fatalf("expire predecessor lease: %v", err)
	}
	parkInstanceForTest(t, svc, taskUUID)
	parked, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after park: %v", err)
	}

	wrong := "run-not-the-predecessor"
	_, err = svc.ClaimAction(ClaimActionParams{
		Task: taskUUID, RunnerID: "runner-successor", AgentRef: "agent:successor",
		Prefer: ActionClaimPrefer{Action: "triage"}, LeaseMs: 300000,
		PriorRun: &wrong, PriorRunProvided: true,
	})
	requireWrkfCode(t, err, wrkfCodeSuspended)
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Suspension == nil || detail.Suspension.ID != parked.Suspension.ID {
		t.Fatalf("precedence refusal detail = %+v err=%v, want suspension %s", detail, err, parked.Suspension.ID)
	}
	if detail.Predecessor != nil {
		t.Fatalf("precedence refusal leaked predecessor dossier: %+v", detail.Predecessor)
	}
	var status, supersededBy string
	if err := svc.db.QueryRow(`SELECT status, COALESCE(superseded_by_run_id, '') FROM workflow_runs WHERE id = ?`, first.Binding.Run.ID).Scan(&status, &supersededBy); err != nil {
		t.Fatalf("read predecessor after refusal: %v", err)
	}
	if status != "active" || supersededBy != "" {
		t.Fatalf("suspended claim touched predecessor: status=%q supersededBy=%q", status, supersededBy)
	}
}

// TestDoubleSuspendRejected proves exactly-one-active-suspension: a second
// suspend is rejected and the original suspension is untouched.
func TestDoubleSuspendRejected(t *testing.T) {
	svc, taskUUID, _ := setupSuspendOutcomeFixture(t)
	if _, err := svc.Transition(taskUUID, "park", TransitionOptions{PrincipalRef: "agent:op", Role: "coordinator"}); err != nil {
		t.Fatalf("Transition park: %v", err)
	}
	first, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	err = withImmediateTx(svc.db, func(tx *sql.Tx) error {
		current, err := resolveInstanceSelectors(tx, taskUUID, "")
		if err != nil {
			return err
		}
		return applySuspendOutcomeTx(tx, current, &SuspendSpec{Reason: "operator_required"}, "wfe_test", svc.now().Format("2006-01-02T15:04:05Z07:00"))
	})
	requireWrkfCode(t, err, wrkfCodeAlreadySuspended)

	after, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after double-suspend: %v", err)
	}
	if !after.Suspended() || after.Suspension.ID != first.Suspension.ID || after.Suspension.Reason != "operator_required" {
		t.Fatalf("double-suspend mutated the active suspension: %+v", after.Suspension)
	}
}

// parkInstanceForTest exercises the internal storage primitive directly only
// where the test needs a worker claim from a template without suspend outcomes.
// Production suspension entry remains template-transition-only.
func parkInstanceForTest(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	err := withImmediateTx(svc.db, func(tx *sql.Tx) error {
		inst, err := resolveInstanceSelectors(tx, taskUUID, "")
		if err != nil {
			return err
		}
		now := svc.now().UTC().Format("2006-01-02T15:04:05Z07:00")
		if err := applySuspendOutcomeTx(tx, inst, &SuspendSpec{Reason: "operator_required"}, "wfe_test", now); err != nil {
			return err
		}
		_, err = tx.Exec(`
			UPDATE workflow_instances
			SET suspension_id = ?, suspension_reason = ?, suspension_at = ?, suspension_cause_ref = ?, updated_at = ?
			WHERE id = ? AND suspension_id IS NULL
		`, inst.Suspension.ID, inst.Suspension.Reason, inst.Suspension.At, inst.Suspension.CauseRef, now, inst.ID)
		return err
	})
	if err != nil {
		t.Fatalf("park instance internally: %v", err)
	}
}