//go:build wrkq_local

package workflow

// room2box_lifecycle_test.go — acceptance tests for the room-2box@1 builtin
// (T-06556): structural contract, builtin install idempotency and rebuild
// supersede, and the full claim/settle lifecycle through every outcome —
// done, no_product_change, review-fail rewind, every suspension outcome plus
// resume, push_rejected return, closed/done effect delivery, and the cancel
// disposition. Normative contract: agent-loop loops/wrkf-task-loop/2BOX_SPEC.md §1.

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func room2BoxFixture(t *testing.T) (*Service, string) {
	t.Helper()
	svc, taskUUID := actionFixture(t)
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinRoom2BoxTemplateRef, "test-installer"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate(room-2box@1): %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, BuiltinRoom2BoxTemplateRef, "test-installer"); err != nil {
		t.Fatalf("AttachTask(room-2box@1): %v", err)
	}
	return svc, taskUUID
}

func room2BoxState(t *testing.T, svc *Service, taskUUID, wantStatus, wantPhase string) *Instance {
	t.Helper()
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Status != wantStatus || inst.Phase != wantPhase {
		t.Fatalf("instance = %s/%s, want %s/%s", inst.Status, inst.Phase, wantStatus, wantPhase)
	}
	return inst
}

func room2BoxImplementDoneFacts(sha string) string {
	return fmt.Sprintf(
		`{"result":"done","commit.sha":%q,"source_identity":%q,"git.clean":true,"dispatch.base.sha":"base000","tracked.delta.paths":["pkg/fix.go"],"tracked.dirty.paths":[]}`,
		sha, sha)
}

// settleImplementDone claims and settles implement with result=done bound to sha.
func settleImplementDone(t *testing.T, svc *Service, taskUUID, sha string) *SettleActionResult {
	t.Helper()
	claim := claimActionForTest(t, svc, taskUUID, "implement")
	return settleClaimForTest(t, svc, claim, room2BoxImplementDoneFacts(sha), "implemented "+sha)
}

// settleReviewPass claims review, asserts the engine bound it to the expected
// implement evidence, and settles a pass echoing the bound identity/linkage.
func settleReviewPass(t *testing.T, svc *Service, taskUUID, wantIdentity, wantEvidenceID string) *SettleActionResult {
	t.Helper()
	claim := claimActionForTest(t, svc, taskUUID, "review")
	source := claim.Binding.Run.Source
	if source.SourceIdentity != wantIdentity || source.SourceEvidenceID != wantEvidenceID {
		t.Fatalf("review source binding = identity %q evidence %q, want %q / %q",
			source.SourceIdentity, source.SourceEvidenceID, wantIdentity, wantEvidenceID)
	}
	facts := fmt.Sprintf(
		`{"result":"pass","reviewed.head.sha":%q,"source_identity":%q,"source.evidence_id":%q}`,
		wantIdentity, source.SourceIdentity, source.SourceEvidenceID)
	return settleClaimForTest(t, svc, claim, facts, "review pass")
}

func settleLanding(t *testing.T, svc *Service, taskUUID, result string) *SettleActionResult {
	t.Helper()
	claim := claimActionForTest(t, svc, taskUUID, "landing")
	source := claim.Binding.Run.Source
	facts := fmt.Sprintf(
		`{"result":%q,"source_identity":%q,"source.evidence_id":%q,"local.head.sha":%q,"remote.head.sha":%q}`,
		result, source.SourceIdentity, source.SourceEvidenceID, source.SourceIdentity, source.SourceIdentity)
	return settleClaimForTest(t, svc, claim, facts, "landing "+result)
}

func room2BoxTaskState(t *testing.T, svc *Service, taskUUID string) string {
	t.Helper()
	var state string
	if err := svc.db.QueryRow(`SELECT state FROM tasks WHERE uuid = ?`, taskUUID).Scan(&state); err != nil {
		t.Fatalf("query task state: %v", err)
	}
	return state
}

// Structural contract: the embedded template parses, validates, and carries the
// 2BOX_SPEC §1 shape.
func TestBuiltinRoom2BoxTemplateContract(t *testing.T) {
	data, err := builtinTemplateData(BuiltinRoom2BoxTemplateRef)
	if err != nil {
		t.Fatalf("builtinTemplateData(room-2box@1): %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if errs := ValidateTemplate(tpl, canonical, nil); len(errs) > 0 {
		t.Fatalf("ValidateTemplate errors: %v", errs)
	}
	if tpl.ID != "room-2box" || tpl.Version != "1" {
		t.Fatalf("template ref = %s@%s, want room-2box@1", tpl.ID, tpl.Version)
	}
	if len(tpl.Roles) != 3 {
		t.Fatalf("roles = %d, want coordinator/implementer/system only", len(tpl.Roles))
	}
	for _, role := range []string{"coordinator", "implementer", "system"} {
		if _, ok := tpl.Roles[role]; !ok {
			t.Fatalf("missing role %s", role)
		}
	}
	if len(tpl.ExecutableActions) != 3 {
		t.Fatalf("executableActions = %d, want implement/review/landing", len(tpl.ExecutableActions))
	}
	for action, contract := range map[string]string{
		"implement": "praesidium.room-2box.implement@1",
		"review":    "praesidium.room-2box.review@1",
		"landing":   "praesidium.room-2box.direct-land@1",
	} {
		if got := tpl.ExecutableActions[action].HandlerContract; got != contract {
			t.Errorf("%s handlerContract = %q, want %q", action, got, contract)
		}
	}
	implementResult := tpl.EvidenceKinds["implement_result"]
	if implementResult.Facts == nil {
		t.Fatal("implement_result facts contract missing")
	}
	var resultEnum []string
	if err := json.Unmarshal([]byte(mustJSON(t, implementResult.Facts.Properties["result"].Enum)), &resultEnum); err != nil {
		t.Fatalf("implement_result enum: %v", err)
	}
	if containsString(resultEnum, "test_defect") {
		t.Fatalf("implement_result enum %v must not carry test_defect (no tester in this room)", resultEnum)
	}
	for _, action := range []string{"review", "landing"} {
		binding := tpl.ExecutableActions[action].SourceBinding
		if binding == nil || binding.Action != "implement" || binding.BindFields == nil ||
			binding.BindFields.SourceIdentity != "source_identity" {
			t.Errorf("%s sourceBinding = %+v, want previous_action implement / source_identity", action, binding)
		}
	}
	review := tpl.ExecutableActions["review"]
	if review.Role != "coordinator" || review.SettleValidation == nil || len(review.SettleValidation.Rules) != 1 {
		t.Fatalf("review action = %+v, want coordinator with one settle rule", review)
	}
	rule := review.SettleValidation.Rules[0]
	if rule.IdentityFact != "source_identity" || rule.LinkageFact != "source.evidence_id" ||
		!containsString(rule.RequiredFacts, "reviewed.head.sha") {
		t.Fatalf("review pass settle rule = %+v", rule)
	}
	if strings.Contains(string(canonical), "workflow.lane") {
		t.Fatal("room-2box carries a lane fact; the template has no lane concept")
	}
	assertV2EffectState(t, tpl, "direct_land_complete", "landed", "completed")
}

// Builtin install is idempotent, and a rebuilt binary's embedded definition
// supersedes a stale stored definition in place.
func TestRoom2BoxEnsureBuiltinIdempotentAndSupersede(t *testing.T) {
	svc, _ := actionFixture(t)
	id, version, err := svc.EnsureBuiltinTemplate(BuiltinRoom2BoxTemplateRef, "test-installer")
	if err != nil || id != "room-2box" || version != "1" {
		t.Fatalf("EnsureBuiltinTemplate = %s@%s, %v", id, version, err)
	}
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinRoom2BoxTemplateRef, "test-installer"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate replay: %v", err)
	}

	// Simulate a stale stored definition from an older binary: same id@version,
	// different content/hash.
	data, err := builtinTemplateData(BuiltinRoom2BoxTemplateRef)
	if err != nil {
		t.Fatalf("builtinTemplateData: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal builtin: %v", err)
	}
	doc["description"] = "stale definition from an older binary"
	stale, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal stale doc: %v", err)
	}
	staleTpl, staleCanonical, err := ParseTemplate(stale)
	if err != nil {
		t.Fatalf("ParseTemplate(stale): %v", err)
	}
	if _, err := svc.installTemplateCanonical(staleTpl, staleCanonical, Hash(staleCanonical), "old-binary", nil, true); err != nil {
		t.Fatalf("install stale definition: %v", err)
	}
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinRoom2BoxTemplateRef, "test-installer"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate over stale: %v", err)
	}
	_, embeddedCanonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate(embedded): %v", err)
	}
	info, err := svc.ShowTemplateVersion(BuiltinRoom2BoxTemplateRef)
	if err != nil {
		t.Fatalf("ShowTemplateVersion: %v", err)
	}
	if info.Hash != Hash(embeddedCanonical) {
		t.Fatalf("stored hash = %s, want embedded %s (supersede did not overwrite)", info.Hash, Hash(embeddedCanonical))
	}
}

// Happy path: implement done → review pass → landing landed → closed/done with
// the set_task_state completed effect delivered.
func TestRoom2BoxHappyPathLifecycle(t *testing.T) {
	svc, taskUUID := room2BoxFixture(t)
	room2BoxState(t, svc, taskUUID, "active", "implement")

	implemented := settleImplementDone(t, svc, taskUUID, "sha-fix-1")
	room2BoxState(t, svc, taskUUID, "active", "review")

	settleReviewPass(t, svc, taskUUID, "sha-fix-1", implemented.Evidence.ID)
	room2BoxState(t, svc, taskUUID, "active", "land")

	settleLanding(t, svc, taskUUID, "landed")
	room2BoxState(t, svc, taskUUID, "closed", "done")
	assertSetTaskStateDelivered(t, svc, taskUUID, "completed")
	if got := room2BoxTaskState(t, svc, taskUUID); got != "completed" {
		t.Fatalf("task state = %q, want completed", got)
	}
}

// no_product_change advances to review under its stricter settle validation.
func TestRoom2BoxNoProductChangePath(t *testing.T) {
	svc, taskUUID := room2BoxFixture(t)
	claim := claimActionForTest(t, svc, taskUUID, "implement")
	facts := `{"result":"no_product_change","source_identity":"base000","dispatch.base.sha":"base000","tracked.delta.paths":[],"git.clean":true}`
	settleClaimForTest(t, svc, claim, facts, "no product change")
	room2BoxState(t, svc, taskUUID, "active", "review")
}

// Review fail rewinds to implement; the next review binds to the NEW implement
// evidence, and the room still lands.
func TestRoom2BoxReviewFailRewind(t *testing.T) {
	svc, taskUUID := room2BoxFixture(t)
	settleImplementDone(t, svc, taskUUID, "sha-fix-1")

	claim := claimActionForTest(t, svc, taskUUID, "review")
	settleClaimForTest(t, svc, claim,
		`{"result":"fail","reviewed.head.sha":"sha-fix-1","findings.forwarded":true}`, "review fail")
	room2BoxState(t, svc, taskUUID, "active", "implement")

	reworked := settleImplementDone(t, svc, taskUUID, "sha-fix-2")
	room2BoxState(t, svc, taskUUID, "active", "review")
	settleReviewPass(t, svc, taskUUID, "sha-fix-2", reworked.Evidence.ID)
	settleLanding(t, svc, taskUUID, "landed")
	room2BoxState(t, svc, taskUUID, "closed", "done")
}

// push_rejected returns to review for classification, and the room can land
// after a fresh pass.
func TestRoom2BoxPushRejectedReturnsToReview(t *testing.T) {
	svc, taskUUID := room2BoxFixture(t)
	implemented := settleImplementDone(t, svc, taskUUID, "sha-fix-1")
	settleReviewPass(t, svc, taskUUID, "sha-fix-1", implemented.Evidence.ID)

	settleLanding(t, svc, taskUUID, "push_rejected")
	room2BoxState(t, svc, taskUUID, "active", "review")

	settleReviewPass(t, svc, taskUUID, "sha-fix-1", implemented.Evidence.ID)
	settleLanding(t, svc, taskUUID, "landed")
	room2BoxState(t, svc, taskUUID, "closed", "done")
}

// Every suspension outcome parks the instance in place, and resume re-opens the
// same phase for a successor claim.
func TestRoom2BoxSuspensionOutcomesAndResume(t *testing.T) {
	cases := []struct {
		name  string
		phase string
		drive func(t *testing.T, svc *Service, taskUUID string)
		facts string
	}{
		{
			name:  "implement blocked",
			phase: "implement",
			drive: func(*testing.T, *Service, string) {},
			facts: `{"result":"blocked"}`,
		},
		{
			name:  "implement operator_required",
			phase: "implement",
			drive: func(*testing.T, *Service, string) {},
			facts: `{"result":"operator_required"}`,
		},
		{
			name:  "review violation",
			phase: "review",
			drive: func(t *testing.T, svc *Service, taskUUID string) {
				settleImplementDone(t, svc, taskUUID, "sha-fix-1")
			},
			facts: `{"result":"violation","reviewed.head.sha":"sha-fix-1"}`,
		},
		{
			name:  "landing operator_required",
			phase: "landing",
			drive: func(t *testing.T, svc *Service, taskUUID string) {
				implemented := settleImplementDone(t, svc, taskUUID, "sha-fix-1")
				settleReviewPass(t, svc, taskUUID, "sha-fix-1", implemented.Evidence.ID)
			},
			facts: `{"result":"operator_required"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskUUID := room2BoxFixture(t)
			tc.drive(t, svc, taskUUID)
			action := tc.phase
			if tc.phase == "landing" {
				action = "landing"
			}
			before, err := svc.LatestInstance(taskUUID)
			if err != nil {
				t.Fatalf("LatestInstance before park: %v", err)
			}
			claim := claimActionForTest(t, svc, taskUUID, action)
			settleClaimForTest(t, svc, claim, tc.facts, tc.name)

			parked, err := svc.LatestInstance(taskUUID)
			if err != nil {
				t.Fatalf("LatestInstance after park: %v", err)
			}
			if parked.Suspension == nil || parked.Suspension.Reason != "operator_required" {
				t.Fatalf("suspension = %+v, want operator_required", parked.Suspension)
			}
			if parked.Status != before.Status || parked.Phase != before.Phase {
				t.Fatalf("park moved state: %s/%s → %s/%s", before.Status, before.Phase, parked.Status, parked.Phase)
			}

			rev := parked.Revision
			if _, err := svc.ResolveSuspension(ResolveSuspensionParams{
				SuspensionID:   parked.Suspension.ID,
				Disposition:    "resume",
				Explanation:    "operator fixed the blocker",
				ExpectRevision: &rev,
				PrincipalRef:   "agent:operator",
				Role:           "coordinator",
			}); err != nil {
				t.Fatalf("ResolveSuspension resume: %v", err)
			}
			resumed, err := svc.LatestInstance(taskUUID)
			if err != nil {
				t.Fatalf("LatestInstance after resume: %v", err)
			}
			if resumed.Suspension != nil || resumed.Status != before.Status || resumed.Phase != before.Phase {
				t.Fatalf("resume state = %s/%s suspension=%+v, want %s/%s with none",
					resumed.Status, resumed.Phase, resumed.Suspension, before.Status, before.Phase)
			}
			// The parked phase is claimable again by a successor.
			successor := claimActionForTest(t, svc, taskUUID, action)
			if successor.Binding == nil {
				t.Fatal("successor claim after resume returned no binding")
			}
		})
	}
}

// The cancel disposition terminalizes the instance and cancels the task.
func TestRoom2BoxCancelDisposition(t *testing.T) {
	svc, taskUUID := room2BoxFixture(t)
	claim := claimActionForTest(t, svc, taskUUID, "implement")
	settleClaimForTest(t, svc, claim, `{"result":"blocked"}`, "blocked")
	parked, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if parked.Suspension == nil {
		t.Fatal("expected suspension after blocked settle")
	}
	rev := parked.Revision
	if _, err := svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID:   parked.Suspension.ID,
		Disposition:    "cancel",
		Explanation:    "not worth fixing",
		ExpectRevision: &rev,
		PrincipalRef:   "agent:operator",
		Role:           "coordinator",
	}); err != nil {
		t.Fatalf("ResolveSuspension cancel: %v", err)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after cancel: %v", err)
	}
	if inst.Status != "closed" {
		t.Fatalf("instance = %s/%s, want closed", inst.Status, inst.Phase)
	}
	assertSetTaskStateDelivered(t, svc, taskUUID, "cancelled")
	if got := room2BoxTaskState(t, svc, taskUUID); got != "cancelled" {
		t.Fatalf("task state = %q, want cancelled", got)
	}
}