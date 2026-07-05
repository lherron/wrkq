package workflow

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestBuiltinSimpleTaskOperatorResolutionContract(t *testing.T) {
	for _, ref := range []string{BuiltinSimpleTaskV2TemplateRef, BuiltinSimpleTaskV3TemplateRef} {
		t.Run(ref, func(t *testing.T) {
			data, err := builtinTemplateData(ref)
			if err != nil {
				t.Fatalf("builtinTemplateData(%s): %v", ref, err)
			}
			tpl, canonical, err := ParseTemplate(data)
			if err != nil {
				t.Fatalf("ParseTemplate(%s): %v", ref, err)
			}
			if errs := ValidateTemplate(tpl, canonical, nil); len(errs) > 0 {
				t.Fatalf("ValidateTemplate(%s) errors: %v", ref, errs)
			}
			kind := tpl.EvidenceKinds["operator_resolution"]
			if kind.Class != "note" || !containsString(kind.ProducibleBy, "supervisor") {
				t.Fatalf("operator_resolution kind = %+v, want note producible by supervisor", kind)
			}
			if kind.Facts == nil || !containsString(kind.Facts.Required, "resolution") {
				t.Fatalf("operator_resolution facts = %+v, want required resolution", kind.Facts)
			}
			tr := requireTransition(t, tpl, "operator_resolved")
			if tr.From.Status != "waiting" || tr.From.Phase != "operator_required" || !containsString(tr.By, "supervisor") {
				t.Fatalf("operator_resolved transition = %+v", tr)
			}
			if len(tr.Requires) != 1 || tr.Requires[0].Evidence == nil || tr.Requires[0].Evidence.Kind != "operator_resolution" {
				t.Fatalf("operator_resolved requires = %+v, want operator_resolution evidence", tr.Requires)
			}
			assertOperatorOutcome(t, tr, "resume_ready", "active", "ready", "open")
			assertOperatorOutcome(t, tr, "cancelled", "closed", "cancelled", "cancelled")
		})
	}
}

func TestOperatorResolvedNoEvidenceGate(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	driveV2ToOperatorRequired(t, svc, taskUUID)

	_, err := svc.Transition(taskUUID, "operator_resolved", TransitionOptions{PrincipalRef: "agent:supervisor", Role: "supervisor"})
	if err == nil || !strings.Contains(err.Error(), "operator_resolution") {
		t.Fatalf("operator_resolved without evidence error = %v, want operator_resolution blocker", err)
	}
	next, err := svc.Next(taskUUID, "supervisor")
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if len(next.Actions) != 1 || next.Actions[0].Kind != "collect_evidence" || next.Actions[0].Unblocks[0] != "operator_resolved" {
		t.Fatalf("next actions = %+v, want collect_evidence for operator_resolved", next.Actions)
	}
	if len(next.BlockedTransitions) != 1 || next.BlockedTransitions[0].ID != "operator_resolved" {
		t.Fatalf("blocked transitions = %+v, want operator_resolved", next.BlockedTransitions)
	}
}

func TestOperatorResolvedResumeAndCancelPaths(t *testing.T) {
	t.Run("resume_ready", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		attachSimpleTaskV2(t, svc, taskUUID)
		impl := driveV2ToOperatorRequired(t, svc, taskUUID)
		beforeRun, err := svc.ShowRun(impl.Run.RunID)
		if err != nil {
			t.Fatalf("ShowRun before: %v", err)
		}

		ev := addOperatorResolution(t, svc, taskUUID, "resume_ready", "operator fixed dependency")
		out := transitionOperatorResolved(t, svc, taskUUID)
		inst, err := svc.LatestInstance(taskUUID)
		if err != nil {
			t.Fatalf("LatestInstance: %v", err)
		}
		if inst.Status != "active" || inst.Phase != "ready" {
			t.Fatalf("state = %+v, want active/ready", inst.State())
		}
		if got := readTaskState(t, svc, taskUUID); got != "open" {
			t.Fatalf("task state = %q, want open", got)
		}
		afterRun, err := svc.ShowRun(impl.Run.RunID)
		if err != nil {
			t.Fatalf("ShowRun after: %v", err)
		}
		if *afterRun != *beforeRun {
			t.Fatalf("prior run changed: before=%+v after=%+v", beforeRun, afterRun)
		}
		if out["outcome"] != "resume_ready" || ev.PrincipalRef != "agent:supervisor" || ev.Role != "supervisor" {
			t.Fatalf("resolution output/evidence = outcome %#v evidence %+v", out["outcome"], ev)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		attachSimpleTaskV2(t, svc, taskUUID)
		driveV2ToOperatorRequired(t, svc, taskUUID)

		addOperatorResolution(t, svc, taskUUID, "cancelled", "operator cancelled duplicate work")
		out := transitionOperatorResolved(t, svc, taskUUID)
		inst, err := svc.LatestInstance(taskUUID)
		if err != nil {
			t.Fatalf("LatestInstance: %v", err)
		}
		if inst.Status != "closed" || inst.Phase != "cancelled" {
			t.Fatalf("state = %+v, want closed/cancelled", inst.State())
		}
		if got := readTaskState(t, svc, taskUUID); got != "cancelled" {
			t.Fatalf("task state = %q, want cancelled", got)
		}
		if out["outcome"] != "cancelled" {
			t.Fatalf("outcome = %#v, want cancelled", out["outcome"])
		}
	})
}

func TestOperatorResolvedRequiresHumanReason(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	driveV2ToOperatorRequired(t, svc, taskUUID)

	_, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "operator_resolution",
		Ref:          "operator:test",
		Facts:        `{"resolution":"resume_ready"}`,
		PrincipalRef: "agent:supervisor",
		Role:         "supervisor",
	})
	if err == nil || !strings.Contains(err.Error(), "operator_resolution requires") {
		t.Fatalf("AddEvidence without reason error = %v, want reason validation", err)
	}
	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "operator_resolution",
		Ref:          "operator:test",
		Facts:        `{"resolution":"resume_ready","reason":"operator fixed it"}`,
		PrincipalRef: "agent:supervisor",
		Role:         "supervisor",
	}); err != nil {
		t.Fatalf("AddEvidence facts.reason: %v", err)
	}
}

func TestOperatorResolvedExistingWedgeRecoversAfterBuiltinSupersede(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	installOldBuiltinV2WithoutOperatorResolved(t, svc)
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:t"); err != nil {
		t.Fatalf("AttachTask old v2: %v", err)
	}
	wedged, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance wedged: %v", err)
	}
	seedOperatorRequiredWedgeForTest(t, svc, wedged.ID)
	wedged, err = svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance seeded wedge: %v", err)
	}
	oldTpl, _, err := svc.ShowTemplate(BuiltinSimpleTaskV2TemplateRef)
	if err != nil {
		t.Fatalf("ShowTemplate old v2: %v", err)
	}
	if _, err := findTransition(oldTpl, "operator_resolved"); err == nil {
		t.Fatalf("pre-change old v2 unexpectedly has operator_resolved")
	}

	nextAfterSupersede, err := svc.Next(taskUUID, "supervisor")
	if err != nil {
		t.Fatalf("Next after built-in supersede: %v", err)
	}
	if len(nextAfterSupersede.Actions) != 1 || nextAfterSupersede.Actions[0].Kind != "collect_evidence" {
		t.Fatalf("superseded next = %+v, want collect_evidence", nextAfterSupersede.Actions)
	}
	addOperatorResolution(t, svc, taskUUID, "resume_ready", "operator fixed old wedge")
	transitionOperatorResolved(t, svc, taskUUID)
	recovered, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance recovered: %v", err)
	}
	if recovered.ID != wedged.ID {
		t.Fatalf("instance id changed: before=%s after=%s", wedged.ID, recovered.ID)
	}
	if recovered.Status != "active" || recovered.Phase != "ready" {
		t.Fatalf("recovered state = %+v, want active/ready", recovered.State())
	}
}

func TestOperatorResolvedSupervisorEffectCleanupReceipts(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	driveV2ToOperatorRequired(t, svc, taskUUID)
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	pending := insertEffectForTest(t, svc, inst.ID, inst.Revision, "supervisor_call", "pending", "")
	failed := insertEffectForTest(t, svc, inst.ID, inst.Revision, "supervisor_escalation", "failed", "")
	leasedUntil := svc.now().UTC().Add(time.Hour).Format(time.RFC3339)
	leased := insertEffectForTest(t, svc, inst.ID, inst.Revision, "supervisor_call", "leased", leasedUntil)
	unrelated := insertEffectForTest(t, svc, inst.ID, inst.Revision, "request_observer_review", "pending", "")

	ev := addOperatorResolution(t, svc, taskUUID, "resume_ready", "operator cleared supervisor wedge")
	out := transitionOperatorResolved(t, svc, taskUUID)
	eventID, _ := out["eventId"].(string)
	for _, id := range []string{pending, failed, leased} {
		eff, err := svc.ShowEffect(id)
		if err != nil {
			t.Fatalf("ShowEffect %s: %v", id, err)
		}
		if eff.Status != "delivered" || eff.DeliveredAt == "" || len(eff.Receipt) == 0 {
			t.Fatalf("effect %s = %+v, want delivered with receipt", id, eff)
		}
		var receipt map[string]interface{}
		if err := json.Unmarshal(eff.Receipt, &receipt); err != nil {
			t.Fatalf("receipt %s: %v", id, err)
		}
		if receipt["operatorResolutionEvidenceId"] != ev.ID || receipt["transitionEventId"] != eventID {
			t.Fatalf("receipt %s = %+v, want evidence %s event %s", id, receipt, ev.ID, eventID)
		}
		if id == leased && receipt["supervisorOverride"] != true {
			t.Fatalf("leased receipt = %+v, want explicit supervisorOverride", receipt)
		}
	}
	untouched, err := svc.ShowEffect(unrelated)
	if err != nil {
		t.Fatalf("ShowEffect unrelated: %v", err)
	}
	if untouched.Status != "pending" {
		t.Fatalf("unrelated effect = %+v, want pending", untouched)
	}
}

func driveV2ToOperatorRequired(t *testing.T, svc *Service, taskUUID string) *ActionCompleteResult {
	t.Helper()
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	out := startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"operator_required"}`)
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Status != "waiting" || inst.Phase != "operator_required" {
		t.Fatalf("state = %+v, want waiting/operator_required", inst.State())
	}
	return out
}

func addOperatorResolution(t *testing.T, svc *Service, taskUUID, resolution, summary string) *Evidence {
	t.Helper()
	ev, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "operator_resolution",
		Ref:          "operator:test",
		Summary:      summary,
		Facts:        fmt.Sprintf(`{"resolution":%q}`, resolution),
		PrincipalRef: "agent:supervisor",
		Role:         "supervisor",
	})
	if err != nil {
		t.Fatalf("AddEvidence operator_resolution %s: %v", resolution, err)
	}
	return ev
}

func transitionOperatorResolved(t *testing.T, svc *Service, taskUUID string) TransitionResult {
	t.Helper()
	out, err := svc.Transition(taskUUID, "operator_resolved", TransitionOptions{PrincipalRef: "agent:supervisor", Role: "supervisor"})
	if err != nil {
		t.Fatalf("Transition operator_resolved: %v", err)
	}
	return out
}

func requireTransition(t *testing.T, tpl *Template, id string) TransitionSpec {
	t.Helper()
	for _, tr := range tpl.Transitions {
		if tr.ID == id {
			return tr
		}
	}
	t.Fatalf("missing transition %s", id)
	return TransitionSpec{}
}

func assertOperatorOutcome(t *testing.T, tr TransitionSpec, id, status, phase, taskState string) {
	t.Helper()
	for _, out := range tr.Outcomes {
		if out.ID != id {
			continue
		}
		if out.To.Status != status || out.To.Phase != phase {
			t.Fatalf("outcome %s to = %+v, want %s/%s", id, out.To, status, phase)
		}
		for _, eff := range out.Effects {
			if eff.Kind == "set_task_state" && eff.Data["state"] == taskState {
				return
			}
		}
		t.Fatalf("outcome %s missing set_task_state %s: %+v", id, taskState, out.Effects)
	}
	t.Fatalf("missing outcome %s", id)
}

func installOldBuiltinV2WithoutOperatorResolved(t *testing.T, svc *Service) {
	t.Helper()
	var doc map[string]interface{}
	if err := json.Unmarshal(builtinSimpleTaskV2JSON, &doc); err != nil {
		t.Fatalf("unmarshal v2: %v", err)
	}
	evidenceKinds := doc["evidenceKinds"].(map[string]interface{})
	delete(evidenceKinds, "operator_resolution")
	rawTransitions := doc["transitions"].([]interface{})
	transitions := make([]interface{}, 0, len(rawTransitions))
	for _, raw := range rawTransitions {
		tr := raw.(map[string]interface{})
		if tr["id"] == "operator_resolved" {
			continue
		}
		transitions = append(transitions, tr)
	}
	doc["transitions"] = transitions
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal old v2: %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate old v2: %v", err)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:t", nil, true); err != nil {
		t.Fatalf("install old v2: %v", err)
	}
}

func seedOperatorRequiredWedgeForTest(t *testing.T, svc *Service, instanceID string) {
	t.Helper()
	now := svc.now().UTC().Format(time.RFC3339)
	if _, err := svc.db.Exec(`
		UPDATE workflow_instances
		SET status = 'waiting',
		    phase = 'operator_required',
		    outcome = NULL,
		    revision = 2,
		    updated_at = ?,
		    closed_at = NULL
		WHERE id = ?
	`, now, instanceID); err != nil {
		t.Fatalf("seed operator_required wedge: %v", err)
	}
}

func insertEffectForTest(t *testing.T, svc *Service, instanceID string, revision int64, kind, status, leasedUntil string) string {
	t.Helper()
	var id string
	if err := withImmediateTx(svc.db, func(tx *sql.Tx) error {
		var err error
		id, err = nextSeqID(tx, "workflow_effect_seq", "eff")
		if err != nil {
			return err
		}
		seq, err := nextEffectSequenceTx(tx, instanceID)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(map[string]string{"test": kind})
		leaseToken := ""
		leasedBy := ""
		if status == "leased" {
			leaseToken = "lease-test-" + id
			leasedBy = "test-adapter"
		}
		_, err = tx.Exec(`
			INSERT INTO workflow_effects (id, instance_id, revision, sequence, kind, payload_json, status, idempotency_key, semantic_key, leased_by, leased_until, lease_token)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, id, instanceID, revision, seq, kind, string(payload), status, "test:"+id, "test:"+id, nullIfEmpty(leasedBy), nullIfEmpty(leasedUntil), nullIfEmpty(leaseToken))
		return err
	}); err != nil {
		t.Fatalf("insert effect: %v", err)
	}
	return id
}
