package workflow

// suspension_test.go — T-06260. Suspension as a first-class condition on a
// workflow instance, plus the suspended-write gate on both commit paths.
//
// Contract (WRKF_SIMPLIFICATION.md §1, §2):
//   - Suspend records a suspension (id, reason, timestamp, cause pointer) and
//     changes NOTHING else: status/phase/outcome/revision stay exactly as they
//     were. A suspended instance is still "in" its phase.
//   - Exactly one active suspension; a second suspend is rejected.
//   - The suspended-write gate rejects writes at BOTH commit paths
//     (TransitionForSelectors and applyActionTransitionTx). Reads, inspection,
//     and dry-run are unaffected — the gate is the entire fencing story.

import "testing"

// TestSuspendRecordsConditionWithoutStateChange proves park is a record, not a
// state change: the suspension appears; status/phase/outcome/revision are
// untouched; and the suspension is still visible on a fresh read (reads work).
func TestSuspendRecordsConditionWithoutStateChange(t *testing.T) {
	svc, taskUUID, _ := setupCASFixture(t)

	before, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance before: %v", err)
	}
	if before.Suspended() {
		t.Fatalf("fresh instance is suspended: %+v", before.Suspension)
	}

	suspended, err := svc.Suspend(taskUUID, "", "operator_required", "wfe_000042", "agent:op")
	if err != nil {
		t.Fatalf("Suspend: %v", err)
	}
	if !suspended.Suspended() {
		t.Fatalf("Suspend returned a running instance: %+v", suspended)
	}
	sus := suspended.Suspension
	if sus.ID == "" || sus.Reason != "operator_required" || sus.At == "" || sus.CauseRef != "wfe_000042" {
		t.Fatalf("suspension record = %+v, want populated id/reason/at/causeRef", sus)
	}
	if suspended.Status != before.Status || suspended.Phase != before.Phase ||
		suspended.Outcome != before.Outcome || suspended.Revision != before.Revision {
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
		readback.Outcome != before.Outcome || readback.Revision != before.Revision {
		t.Fatalf("readback state drifted: %+v rev=%d", readback.State(), readback.Revision)
	}
}

// TestSuspendRejectsEmptyReason guards the template-declared reason code as
// required input.
func TestSuspendRejectsEmptyReason(t *testing.T) {
	svc, taskUUID, _ := setupCASFixture(t)
	_, err := svc.Suspend(taskUUID, "", "   ", "", "agent:op")
	requireWrkfCode(t, err, wrkfCodeValidation)
}

// TestSuspendedWriteGateDoor1 proves the gate on TransitionForSelectors: an
// otherwise-legal transition (revision still matches, since park does not bump
// it) bounces with WRKF_SUSPENDED, and dry-run inspection still passes.
func TestSuspendedWriteGateDoor1(t *testing.T) {
	svc, taskUUID, _ := setupCASFixture(t)
	if _, err := svc.Suspend(taskUUID, "", "operator_required", "", "agent:op"); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	rev0 := int64(0)
	// Dry-run is a read: it must NOT bounce.
	if _, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		PrincipalRef: "human:test", Role: "coordinator", ExpectRevision: &rev0, DryRun: true,
	}); err != nil {
		t.Fatalf("dry-run transition on suspended instance bounced: %v", err)
	}

	// The real write bounces at the gate.
	_, err := svc.Transition(taskUUID, "complete", TransitionOptions{
		PrincipalRef: "human:test", Role: "coordinator", ExpectRevision: &rev0,
	})
	requireWrkfCode(t, err, wrkfCodeSuspended)

	// No write landed: still active/ready at revision 0.
	after, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after bounce: %v", err)
	}
	if after.Status != "active" || after.Phase != "ready" || after.Revision != 0 {
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
	if _, err := svc.Suspend(taskUUID, "", "operator_required", "", "agent:op"); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

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

// TestDoubleSuspendRejected proves exactly-one-active-suspension: a second
// suspend is rejected and the original suspension is untouched.
func TestDoubleSuspendRejected(t *testing.T) {
	svc, taskUUID, _ := setupCASFixture(t)

	first, err := svc.Suspend(taskUUID, "", "operator_required", "", "agent:op")
	if err != nil {
		t.Fatalf("first Suspend: %v", err)
	}
	_, err = svc.Suspend(taskUUID, "", "needs_input", "", "agent:op")
	requireWrkfCode(t, err, wrkfCodeAlreadySuspended)

	after, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after double-suspend: %v", err)
	}
	if !after.Suspended() || after.Suspension.ID != first.Suspension.ID || after.Suspension.Reason != "operator_required" {
		t.Fatalf("double-suspend mutated the active suspension: %+v", after.Suspension)
	}
}
