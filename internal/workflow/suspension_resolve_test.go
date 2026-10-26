//go:build wrkq_local

package workflow

// suspension_resolve_test.go — T-06262. The atomic resolveSuspension command:
// id-only gate, three dispositions, disposition-declared effects, revision bump,
// and the workflow.suspension_resolved event.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// parkForResolve parks the suspend-outcome fixture and returns the suspended
// instance so a resolution test can act on its active suspension id.
func parkForResolve(t *testing.T) (*Service, string, *Instance) {
	t.Helper()
	svc, taskUUID, _ := setupSuspendOutcomeFixture(t)
	return parkSuspensionFixture(t, svc, taskUUID)
}

func parkForResolveWithTemplate(t *testing.T, templateID, document string) (*Service, string, *Instance) {
	t.Helper()
	svc, taskUUID, _ := setupCASFixture(t)
	templatePath := filepath.Join(t.TempDir(), templateID+".json")
	if err := os.WriteFile(templatePath, []byte(document), 0o644); err != nil {
		t.Fatalf("write template: %v", err)
	}
	if _, err := svc.InstallTemplate(templatePath, "test-installer", nil); err != nil {
		t.Fatalf("InstallTemplate: %v", err)
	}
	predecessor, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance predecessor: %v", err)
	}
	revision := predecessor.Revision
	if _, err := svc.AttachTask(taskUUID, templateID+"@1", "test-installer", AttachTaskOptions{
		Supersede: true, PredecessorInstanceID: predecessor.ID, PredecessorRevision: &revision,
	}); err != nil {
		t.Fatalf("AttachTask: %v", err)
	}
	return parkSuspensionFixture(t, svc, taskUUID)
}

func parkSuspensionFixture(t *testing.T, svc *Service, taskUUID string) (*Service, string, *Instance) {
	t.Helper()
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
	if effects[0].Status != "pending" || effects[0].Attempts != 0 {
		t.Fatalf("external resume effect = %+v, want pending with zero attempts", effects[0])
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

// TestResolveSuspensionTerminalDispositionClosesInstanceAndSetsTaskState proves
// close and cancel land the instance in closed/<outcome>, clear the suspension,
// bump the revision, and deliver the disposition's set_task_state effect.
func TestResolveSuspensionTerminalDispositionClosesInstanceAndSetsTaskState(t *testing.T) {
	for _, tc := range []struct {
		disposition, outcome, taskState string
	}{
		{disposition: DispositionClose, outcome: "done", taskState: "completed"},
		{disposition: DispositionCancel, outcome: "cancelled", taskState: "cancelled"},
	} {
		t.Run(tc.disposition, func(t *testing.T) {
			disposition := tc.disposition
			svc, taskUUID, suspended := parkForResolve(t)

			out, err := svc.ResolveSuspension(ResolveSuspensionParams{
				SuspensionID: suspended.Suspension.ID,
				Disposition:  disposition,
				PrincipalRef: "human:op",
			})
			if err != nil {
				t.Fatalf("ResolveSuspension %s: %v", disposition, err)
			}
			resolved, err := svc.LatestInstance(taskUUID)
			if err != nil {
				t.Fatalf("LatestInstance after %s: %v", disposition, err)
			}
			if resolved.Status != "closed" || resolved.Outcome != tc.outcome || resolved.Suspended() {
				t.Fatalf("%s state = %+v suspended=%v, want closed/%s and cleared", disposition, resolved.State(), resolved.Suspended(), tc.outcome)
			}
			if resolved.Revision != suspended.Revision+1 {
				t.Fatalf("%s revision = %d, want %d", disposition, resolved.Revision, suspended.Revision+1)
			}

			effects, ok := out["effects"].([]Effect)
			if !ok || len(effects) != 1 {
				t.Fatalf("%s returned effects = %#v, want one effect", disposition, out["effects"])
			}
			assertDeliveredTaskStateEffect(t, effects[0])
			listed, err := svc.ListEffects(taskUUID, true)
			if err != nil {
				t.Fatalf("ListEffects after %s: %v", disposition, err)
			}
			if len(listed) != 1 || listed[0].SemanticKey == "" || listed[0].ID != effects[0].ID {
				t.Fatalf("%s listed effects = %+v, want exactly one semantic-keyed effect", disposition, listed)
			}
			assertDeliveredTaskStateEffect(t, listed[0])

			var state, updatedBy string
			if err := svc.db.QueryRow(`SELECT state, COALESCE(updated_by_principal_ref, '') FROM tasks WHERE uuid = ?`, taskUUID).Scan(&state, &updatedBy); err != nil {
				t.Fatalf("read task after %s: %v", disposition, err)
			}
			if state != tc.taskState || updatedBy != workflowSystemAttribution.PrincipalRef {
				t.Fatalf("task after %s = state %q updated_by %q, want %q by %q", disposition, state, updatedBy, tc.taskState, workflowSystemAttribution.PrincipalRef)
			}
		})
	}
}

func assertDeliveredTaskStateEffect(t *testing.T, effect Effect) {
	t.Helper()
	if effect.Kind != "set_task_state" || effect.Status != "delivered" || effect.Attempts != 1 || len(effect.Receipt) == 0 {
		t.Fatalf("builtin effect = %+v, want delivered set_task_state with one attempt and receipt", effect)
	}
	var receipt map[string]interface{}
	if err := json.Unmarshal(effect.Receipt, &receipt); err != nil {
		t.Fatalf("decode effect receipt: %v", err)
	}
	if receipt["kind"] != "set_task_state.receipt" {
		t.Fatalf("receipt kind = %#v, want set_task_state.receipt", receipt["kind"])
	}
}

func TestResolveSuspensionMixedEffectsDeliversOnlyBuiltin(t *testing.T) {
	document := strings.Replace(suspendOutcomeTemplate,
		`"id": "suspend-outcome-test"`,
		`"id": "suspend-outcome-mixed-test"`, 1)
	document = strings.Replace(document,
		`"close": [{ "kind": "set_task_state", "data": { "state": "completed" } }]`,
		`"close": [
        { "kind": "set_task_state", "data": { "state": "completed" } },
        { "kind": "close_notice", "role": "coordinator" }
      ]`, 1)
	svc, taskUUID, suspended := parkForResolveWithTemplate(t, "suspend-outcome-mixed-test", document)

	out, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: suspended.Suspension.ID,
		Disposition:  DispositionClose,
		PrincipalRef: "human:op",
	})
	if err != nil {
		t.Fatalf("ResolveSuspension mixed close: %v", err)
	}
	effects, ok := out["effects"].([]Effect)
	if !ok || len(effects) != 2 {
		t.Fatalf("mixed returned effects = %#v, want two effects", out["effects"])
	}
	assertDeliveredTaskStateEffect(t, effects[0])
	if effects[1].Kind != "close_notice" || effects[1].Status != "pending" || effects[1].Attempts != 0 {
		t.Fatalf("external close effect = %+v, want pending with zero attempts", effects[1])
	}

	listed, err := svc.ListEffects(taskUUID, true)
	if err != nil {
		t.Fatalf("ListEffects mixed close: %v", err)
	}
	if len(listed) != 2 || listed[0].Status != "delivered" || listed[1].Status != "pending" {
		t.Fatalf("mixed durable effects = %+v, want delivered builtin and pending external", listed)
	}
}

// The close outcome (done) is not a task state; the dynamic payload passes
// registration but still exercises delivery failure and partial commit reporting.
func TestResolveSuspensionBuiltinEffectFailureReturnsCommittedPartialResult(t *testing.T) {
	document := strings.Replace(suspendOutcomeTemplate,
		`"id": "suspend-outcome-test"`,
		`"id": "suspend-outcome-invalid-builtin-test"`, 1)
	document = strings.Replace(document, `"state": "completed"`, `"state": "{outcome}"`, 1)
	svc, taskUUID, suspended := parkForResolveWithTemplate(t, "suspend-outcome-invalid-builtin-test", document)

	result, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: suspended.Suspension.ID,
		Disposition:  DispositionClose,
		PrincipalRef: "human:op",
	})
	if got := wrkfCode(err); got != wrkfCodeEffectDeliveryFailed {
		t.Fatalf("ResolveSuspension error code = %q, want %s (err=%v)", got, wrkfCodeEffectDeliveryFailed, err)
	}
	if result == nil {
		t.Fatal("builtin delivery failure did not return the committed resolution result")
	}
	var deliveryErr *transitionEffectDeliveryError
	if !errors.As(err, &deliveryErr) {
		t.Fatalf("ResolveSuspension error = %T, want *transitionEffectDeliveryError", err)
	}
	if deliveryErr.eventID == "" || deliveryErr.effectID == "" || deliveryErr.kind != "set_task_state" || deliveryErr.status != "failed" {
		t.Fatalf("typed delivery error context = %+v", deliveryErr)
	}
	if eventID, _ := result["eventId"].(string); eventID != deliveryErr.eventID {
		t.Fatalf("partial result eventId = %q, typed error eventId = %q", eventID, deliveryErr.eventID)
	}

	resolved, latestErr := svc.LatestInstance(taskUUID)
	if latestErr != nil {
		t.Fatalf("LatestInstance after delivery failure: %v", latestErr)
	}
	if resolved.Status != "closed" || resolved.Outcome != "done" || resolved.Suspended() {
		t.Fatalf("delivery failure rolled back resolution: state=%+v suspension=%+v", resolved.State(), resolved.Suspension)
	}
	effects, listErr := svc.ListEffects(taskUUID, true)
	if listErr != nil {
		t.Fatalf("ListEffects after delivery failure: %v", listErr)
	}
	if len(effects) != 1 || effects[0].ID != deliveryErr.effectID || effects[0].Status != "failed" || effects[0].Attempts != 1 || effects[0].LastError == "" {
		t.Fatalf("recoverable failed effect = %+v, want one failed attempted effect with error", effects)
	}
	var taskState string
	if err := svc.db.QueryRow(`SELECT state FROM tasks WHERE uuid = ?`, taskUUID).Scan(&taskState); err != nil {
		t.Fatalf("read task after delivery failure: %v", err)
	}
	if taskState != "open" {
		t.Fatalf("invalid builtin payload mutated task state to %q, want open", taskState)
	}
}

// TestSuspensionResolveRefusalsWriteNothing proves every refused resolution
// leaves the instance, its runs (including active holders a terminal
// disposition would revoke), the task, the event ledger and the effect outbox
// exactly as they were. The suspension id is the only gate: a non-matching id
// and an id that no longer names an active suspension (repeat resolve) are both
// WRKF_SUSPENSION_NOT_FOUND, so a resolve is never double-applied.
func TestSuspensionResolveRefusalsWriteNothing(t *testing.T) {
	cases := []struct {
		name string
		// prepare runs before the durable snapshot is taken.
		prepare  func(t *testing.T, f terminalDispositionFixture)
		params   func(f terminalDispositionFixture) ResolveSuspensionParams
		wantCode string
	}{
		{
			name: "unknown suspension id",
			params: func(terminalDispositionFixture) ResolveSuspensionParams {
				return ResolveSuspensionParams{SuspensionID: "sus_does_not_exist", Disposition: DispositionCancel}
			},
			wantCode: wrkfCodeSuspensionNotFound,
		},
		{
			name: "unknown disposition",
			params: func(f terminalDispositionFixture) ResolveSuspensionParams {
				return ResolveSuspensionParams{SuspensionID: f.suspended.Suspension.ID, Disposition: "retry"}
			},
			wantCode: wrkfCodeValidation,
		},
		{
			name: "stale expectRevision",
			params: func(f terminalDispositionFixture) ResolveSuspensionParams {
				stale := f.suspended.Revision - 1
				return ResolveSuspensionParams{SuspensionID: f.suspended.Suspension.ID, Disposition: DispositionCancel, ExpectRevision: &stale}
			},
			wantCode: wrkfCodeStaleRevision,
		},
		{
			name: "repeat resolve after clear",
			prepare: func(t *testing.T, f terminalDispositionFixture) {
				if _, err := f.svc.ResolveSuspension(ResolveSuspensionParams{
					SuspensionID: f.suspended.Suspension.ID, Disposition: DispositionResume,
					PrincipalRef: "agent:supervisor", Role: "supervisor",
				}); err != nil {
					t.Fatalf("first resolve: %v", err)
				}
			},
			params: func(f terminalDispositionFixture) ResolveSuspensionParams {
				return ResolveSuspensionParams{SuspensionID: f.suspended.Suspension.ID, Disposition: DispositionCancel}
			},
			wantCode: wrkfCodeSuspensionNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := setupTerminalDispositionFixture(t)
			if tc.prepare != nil {
				tc.prepare(t, f)
			}
			before := terminalDispositionMutationSnapshot(t, f)
			_, err := f.svc.ResolveSuspension(tc.params(f))
			requireWrkfCode(t, err, tc.wantCode)
			if after := terminalDispositionMutationSnapshot(t, f); after != before {
				t.Fatalf("refused resolve mutated durable state:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
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
