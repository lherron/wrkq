package workflow

// suspension_resolve_test.go — T-06262. The atomic resolveSuspension command:
// id-only gate, three dispositions, disposition-declared effects, revision bump,
// and the workflow.suspension_resolved event.

import (
	"encoding/json"
	"strings"
	"testing"
)

// parkForResolve parks the suspend-outcome fixture and returns the suspended
// instance so a resolution test can act on its active suspension id.
func parkForResolve(t *testing.T) (*Service, string, *Instance) {
	t.Helper()
	svc, taskUUID, _ := setupSuspendOutcomeFixture(t)
	if _, err := svc.Transition(taskUUID, "park", TransitionOptions{PrincipalRef: "agent:op", Role: "coordinator"}); err != nil {
		t.Fatalf("Transition park: %v", err)
	}
	suspended, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after park: %v", err)
	}
	if !suspended.Suspended() {
		t.Fatalf("fixture did not park: %+v", suspended)
	}
	return svc, taskUUID, suspended
}

// TestResolveSuspensionResumeClearsAndPreservesPhase proves resume clears the
// suspension, bumps revision, keeps status/phase/outcome exactly, applies the
// resume effect, and emits workflow.suspension_resolved.
func TestResolveSuspensionResumeClearsAndPreservesPhase(t *testing.T) {
	svc, taskUUID, suspended := parkForResolve(t)

	out, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: suspended.Suspension.ID,
		Disposition:  DispositionResume,
		Explanation:  "operator cleared the park",
		PrincipalRef: "human:op",
		Role:         "coordinator",
	})
	if err != nil {
		t.Fatalf("ResolveSuspension resume: %v", err)
	}

	resolved, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after resume: %v", err)
	}
	if resolved.Suspended() {
		t.Fatalf("resume left a suspension: %+v", resolved.Suspension)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("marshal resolved instance: %v", err)
	}
	if !strings.Contains(string(encoded), `"suspension":null`) {
		t.Fatalf("resolved instance DTO does not expose null suspension: %s", encoded)
	}
	if resolved.Status != suspended.Status || resolved.Phase != suspended.Phase || resolved.Outcome != suspended.Outcome {
		t.Fatalf("resume changed state: before=%+v after=%+v", suspended.State(), resolved.State())
	}
	if resolved.Revision != suspended.Revision+1 {
		t.Fatalf("resume revision = %d, want %d", resolved.Revision, suspended.Revision+1)
	}
	effects, _ := out["effects"].([]Effect)
	if len(effects) != 1 || effects[0].Kind != "resume_notice" {
		t.Fatalf("resume effects = %+v, want one resume_notice", effects)
	}

	// The workflow.suspension_resolved event is on the instance timeline.
	if !hasSuspensionResolvedEvent(t, svc, resolved.ID, "resume") {
		t.Fatalf("no workflow.suspension_resolved event recorded for resume")
	}
	queried, err := svc.QueryEvents(EventQueryParams{EventType: "workflow.suspension_resolved"})
	if err != nil {
		t.Fatalf("QueryEvents workflow.suspension_resolved: %v", err)
	}
	if len(queried.Items) != 1 || queried.Items[0].Disposition != "resume" || queried.Items[0].Suspension == nil || queried.Items[0].BeforeRevision != suspended.Revision || queried.Items[0].AfterRevision != resolved.Revision {
		t.Fatalf("queried resolution event = %+v", queried.Items)
	}
	var fakeTransitions int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_events WHERE instance_id = ? AND seq > 1 AND type = 'workflow.transitioned'`, resolved.ID).Scan(&fakeTransitions); err != nil {
		t.Fatalf("count fake resolution transitions: %v", err)
	}
	if fakeTransitions != 0 {
		t.Fatalf("park/resolve emitted %d workflow.transitioned events", fakeTransitions)
	}
}

// TestResolveSuspensionCloseTerminalizes proves close lands the instance in
// closed/done and applies the close effect.
func TestResolveSuspensionCloseTerminalizes(t *testing.T) {
	svc, taskUUID, suspended := parkForResolve(t)

	if _, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: suspended.Suspension.ID,
		Disposition:  DispositionClose,
		PrincipalRef: "human:op",
	}); err != nil {
		t.Fatalf("ResolveSuspension close: %v", err)
	}
	resolved, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after close: %v", err)
	}
	if resolved.Suspended() {
		t.Fatalf("close left a suspension: %+v", resolved.Suspension)
	}
	if resolved.Status != "closed" || resolved.Outcome != "done" {
		t.Fatalf("close state = %+v, want closed/done", resolved.State())
	}
	if resolved.Revision != suspended.Revision+1 {
		t.Fatalf("close revision = %d, want %d", resolved.Revision, suspended.Revision+1)
	}
}

// TestResolveSuspensionCancelTerminalizes proves cancel lands the instance in
// closed/cancelled.
func TestResolveSuspensionCancelTerminalizes(t *testing.T) {
	svc, taskUUID, suspended := parkForResolve(t)

	if _, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: suspended.Suspension.ID,
		Disposition:  DispositionCancel,
		PrincipalRef: "human:op",
	}); err != nil {
		t.Fatalf("ResolveSuspension cancel: %v", err)
	}
	resolved, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after cancel: %v", err)
	}
	if resolved.Status != "closed" || resolved.Outcome != "cancelled" || resolved.Suspended() {
		t.Fatalf("cancel state = %+v suspended=%v, want closed/cancelled and cleared", resolved.State(), resolved.Suspended())
	}
}

// TestResolveSuspensionIDIsTheOnlyGate proves a non-matching suspension id is
// refused with WRKF_SUSPENSION_NOT_FOUND and nothing is written.
func TestResolveSuspensionIDIsTheOnlyGate(t *testing.T) {
	svc, taskUUID, suspended := parkForResolve(t)

	_, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: "sus_does_not_exist",
		Disposition:  DispositionResume,
	})
	requireWrkfCode(t, err, wrkfCodeSuspensionNotFound)

	after, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after refused resolve: %v", err)
	}
	if !after.Suspended() || after.Suspension.ID != suspended.Suspension.ID || after.Revision != suspended.Revision {
		t.Fatalf("refused resolve mutated the instance: %+v rev=%d", after.Suspension, after.Revision)
	}
}

// TestResolveSuspensionRejectsUnknownDisposition guards the disposition input.
func TestResolveSuspensionRejectsUnknownDisposition(t *testing.T) {
	svc, _, suspended := parkForResolve(t)
	_, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: suspended.Suspension.ID,
		Disposition:  "retry",
	})
	requireWrkfCode(t, err, wrkfCodeValidation)
}

// TestResolveSuspensionHonorsRevisionCAS proves the ordinary revision CAS
// precondition applies and a stale expectation is rejected without a write.
func TestResolveSuspensionHonorsRevisionCAS(t *testing.T) {
	svc, taskUUID, suspended := parkForResolve(t)
	stale := suspended.Revision - 1
	_, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID:   suspended.Suspension.ID,
		Disposition:    DispositionResume,
		ExpectRevision: &stale,
	})
	requireWrkfCode(t, err, wrkfCodeStaleRevision)

	after, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after stale CAS: %v", err)
	}
	if !after.Suspended() || after.Revision != suspended.Revision {
		t.Fatalf("stale CAS still wrote: suspended=%v rev=%d", after.Suspended(), after.Revision)
	}
}

// TestResolveSuspensionIsIdempotentAfterClear proves a repeat resolution of an
// already-cleared suspension is refused (the id no longer names an active
// suspension) rather than double-applying.
func TestResolveSuspensionIsIdempotentAfterClear(t *testing.T) {
	svc, _, suspended := parkForResolve(t)
	if _, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: suspended.Suspension.ID,
		Disposition:  DispositionResume,
	}); err != nil {
		t.Fatalf("first resolve: %v", err)
	}
	_, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: suspended.Suspension.ID,
		Disposition:  DispositionResume,
	})
	requireWrkfCode(t, err, wrkfCodeSuspensionNotFound)
}

func hasSuspensionResolvedEvent(t *testing.T, svc *Service, instanceID, disposition string) bool {
	t.Helper()
	rows, err := svc.db.Query(`SELECT COALESCE(payload_json,'') FROM workflow_events WHERE instance_id = ? AND type = 'workflow.suspension_resolved'`, instanceID)
	if err != nil {
		t.Fatalf("query suspension_resolved events: %v", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			t.Fatalf("scan event: %v", err)
		}
		if strings.Contains(payload, `"disposition":"`+disposition+`"`) {
			return true
		}
	}
	return false
}
