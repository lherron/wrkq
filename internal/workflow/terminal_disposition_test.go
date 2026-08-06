//go:build wrkq_local

package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

type terminalDispositionFixture struct {
	svc            *Service
	taskUUID       string
	suspended      *Instance
	claim          *ClaimActionResult
	extraActiveRun *Run
	terminalRun    *Run
}

func setupTerminalDispositionFixture(t *testing.T) terminalDispositionFixture {
	t.Helper()
	svc, taskUUID := actionFixture(t)
	doc := builtinV2Doc(t)
	doc["id"] = "terminal-disposition-test"
	doc["version"] = "1"
	doc["suspension"] = map[string]any{
		"reasons": []any{"operator_required"},
		"effects": map[string]any{
			"resume": []any{map[string]any{"kind": "resume_notice", "role": "supervisor"}},
			"close":  []any{map[string]any{"kind": "set_task_state", "role": "system", "data": map[string]any{"state": "completed"}}},
			"cancel": []any{map[string]any{"kind": "set_task_state", "role": "system", "data": map[string]any{"state": "cancelled"}}},
		},
	}
	doc["transitions"] = append(doc["transitions"].([]any), map[string]any{
		"id":          "park",
		"description": "Park ready work for an operator while its holder remains active.",
		"from":        map[string]any{"status": "active", "phase": "ready"},
		"by":          []any{"supervisor"},
		"outcomes": []any{map[string]any{
			"id":          "needs_operator",
			"description": "Record the operator-required suspension.",
			"when":        map[string]any{"always": true},
			"suspend":     map[string]any{"reason": "operator_required"},
		}},
	})
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal terminal-disposition template: %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate terminal-disposition: %v", err)
	}
	if errs := ValidateTemplate(tpl, canonical, nil); len(errs) > 0 {
		t.Fatalf("ValidateTemplate terminal-disposition: %v", errs)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:test", nil, false); err != nil {
		t.Fatalf("install terminal-disposition template: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "terminal-disposition-test@1", "agent:test"); err != nil {
		t.Fatalf("AttachTask terminal-disposition: %v", err)
	}

	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	claim := claimActionForTest(t, svc, taskUUID, "implement")
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance before auxiliary runs: %v", err)
	}
	extraActive, err := svc.StartRunForSelectors(taskUUID, inst.ID, "observer", "agent:observer", StartRunOptions{
		Action: "observe", LeaseOwner: "observer-seat", LeaseToken: "observer-secret-token",
		LeaseExpiresAt: "2030-01-01T00:00:00Z", HeartbeatAt: "2026-07-22T00:00:00Z",
	})
	if err != nil {
		t.Fatalf("StartRun extra active: %v", err)
	}
	terminal, err := svc.StartRunForSelectors(taskUUID, inst.ID, "auditor", "agent:auditor", StartRunOptions{
		Action: "audit", LeaseOwner: "audit-seat", LeaseToken: "audit-secret-token",
	})
	if err != nil {
		t.Fatalf("StartRun terminal control: %v", err)
	}
	terminal, err = svc.FinishRun(terminal.ID, "completed", "audit already complete")
	if err != nil {
		t.Fatalf("FinishRun terminal control: %v", err)
	}
	if _, err := svc.Transition(taskUUID, "park", TransitionOptions{PrincipalRef: "agent:supervisor", Role: "supervisor"}); err != nil {
		t.Fatalf("Transition park: %v", err)
	}
	suspended, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after park: %v", err)
	}
	if suspended.Suspension == nil {
		t.Fatalf("fixture did not suspend: %+v", suspended)
	}
	return terminalDispositionFixture{svc: svc, taskUUID: taskUUID, suspended: suspended, claim: claim, extraActiveRun: extraActive, terminalRun: terminal}
}

func durableRunSnapshot(t *testing.T, svc *Service, runID string) string {
	t.Helper()
	var snapshot string
	if err := svc.db.QueryRow(`SELECT json_object(
		'status',status,'token',lease_token,'generation',owner_generation,
		'expires',lease_expires_at,'heartbeat',heartbeat_at,
		'completed',completed_at,'terminal',terminal_result
	) FROM workflow_runs WHERE id = ?`, runID).Scan(&snapshot); err != nil {
		t.Fatalf("snapshot run %s: %v", runID, err)
	}
	return snapshot
}

func terminalDispositionMutationSnapshot(t *testing.T, f terminalDispositionFixture) string {
	t.Helper()
	var instance, runs, task string
	if err := f.svc.db.QueryRow(`SELECT json_object(
		'status',status,'phase',phase,'outcome',outcome,'revision',revision,
		'suspensionId',suspension_id,'closedAt',closed_at
	) FROM workflow_instances WHERE id = ?`, f.suspended.ID).Scan(&instance); err != nil {
		t.Fatalf("snapshot instance: %v", err)
	}
	if err := f.svc.db.QueryRow(`SELECT json_group_array(json_object(
		'id',id,'status',status,'token',lease_token,'generation',owner_generation,
		'completed',completed_at,'terminal',terminal_result
	)) FROM (SELECT * FROM workflow_runs WHERE instance_id = ? ORDER BY id)`, f.suspended.ID).Scan(&runs); err != nil {
		t.Fatalf("snapshot runs: %v", err)
	}
	if err := f.svc.db.QueryRow(`SELECT json_object('state',state,'etag',etag,'meta',meta) FROM tasks WHERE uuid = ?`, f.taskUUID).Scan(&task); err != nil {
		t.Fatalf("snapshot task: %v", err)
	}
	var events, effects int
	if err := f.svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_events WHERE instance_id = ?`, f.suspended.ID).Scan(&events); err != nil {
		t.Fatalf("snapshot events: %v", err)
	}
	if err := f.svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_effects WHERE instance_id = ?`, f.suspended.ID).Scan(&effects); err != nil {
		t.Fatalf("snapshot effects: %v", err)
	}
	return strings.Join([]string{instance, runs, task, fmt.Sprintf("events=%d", events), fmt.Sprintf("effects=%d", effects)}, "\n")
}

func TestResolveSuspensionResumePreservesActiveRunAuthorityAndHolderSettles(t *testing.T) {
	f := setupTerminalDispositionFixture(t)
	claimBefore := durableRunSnapshot(t, f.svc, f.claim.Binding.Run.ID)
	extraBefore := durableRunSnapshot(t, f.svc, f.extraActiveRun.ID)
	terminalBefore := durableRunSnapshot(t, f.svc, f.terminalRun.ID)
	revision := f.suspended.Revision

	out, err := f.svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: f.suspended.Suspension.ID, Disposition: DispositionResume,
		ExpectRevision: &revision, PrincipalRef: "agent:supervisor", Role: "supervisor",
	})
	if err != nil {
		t.Fatalf("ResolveSuspension resume with active runs: %v", err)
	}
	resolved, err := f.svc.LatestInstance(f.taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after resume: %v", err)
	}
	if resolved.Suspension != nil || resolved.Revision != revision+1 {
		t.Fatalf("resume instance = suspension %+v revision %d, want cleared revision %d", resolved.Suspension, resolved.Revision, revision+1)
	}
	if got := durableRunSnapshot(t, f.svc, f.claim.Binding.Run.ID); got != claimBefore {
		t.Fatalf("resume mutated claimed run authority:\nbefore %s\nafter  %s", claimBefore, got)
	}
	if got := durableRunSnapshot(t, f.svc, f.extraActiveRun.ID); got != extraBefore {
		t.Fatalf("resume mutated auxiliary active run authority:\nbefore %s\nafter  %s", extraBefore, got)
	}
	if got := durableRunSnapshot(t, f.svc, f.terminalRun.ID); got != terminalBefore {
		t.Fatalf("resume mutated terminal run:\nbefore %s\nafter  %s", terminalBefore, got)
	}
	if terminalized, ok := out["terminalizedRuns"]; ok && terminalized != nil {
		encoded, _ := json.Marshal(terminalized)
		if string(encoded) != "[]" && string(encoded) != "null" {
			t.Fatalf("resume returned terminalized runs: %s", encoded)
		}
	}
	settled := settleClaimForTest(t, f.svc, f.claim,
		`{"result":"done","commit.sha":"resume123","change.id":"change-v1:resume123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`,
		"same holder settled after resume")
	if settled.Run.Status != "completed" {
		t.Fatalf("same holder settle status = %q, want completed", settled.Run.Status)
	}
}

func TestResolveSuspensionTerminalDispositionAtomicallyRevokesAllActiveRuns(t *testing.T) {
	for _, disposition := range []string{DispositionClose, DispositionCancel} {
		t.Run(disposition, func(t *testing.T) {
			f := setupTerminalDispositionFixture(t)
			terminalBefore := durableRunSnapshot(t, f.svc, f.terminalRun.ID)
			originalTokens := []string{f.claim.Binding.Authority.OwnerToken, "observer-secret-token", "audit-secret-token"}
			revision := f.suspended.Revision
			out, err := f.svc.ResolveSuspension(ResolveSuspensionParams{
				SuspensionID: f.suspended.Suspension.ID, Disposition: disposition,
				ExpectRevision: &revision, PrincipalRef: "agent:supervisor", Role: "supervisor",
			})
			if err != nil {
				t.Fatalf("ResolveSuspension %s with active runs: %v", disposition, err)
			}
			eventID, _ := out["eventId"].(string)
			if eventID == "" {
				t.Fatalf("terminal disposition result missing eventId: %#v", out)
			}
			for _, runID := range []string{f.claim.Binding.Run.ID, f.extraActiveRun.ID} {
				var status, token, completedAt, cause string
				if err := f.svc.db.QueryRow(`SELECT status, COALESCE(lease_token,''), COALESCE(completed_at,''), COALESCE(terminal_result,'') FROM workflow_runs WHERE id = ?`, runID).Scan(&status, &token, &completedAt, &cause); err != nil {
					t.Fatalf("read terminalized run %s: %v", runID, err)
				}
				if !isTerminalRunStatus(status) || status == "active" || token != "" || completedAt == "" || !strings.Contains(cause, disposition) || !strings.Contains(cause, eventID) {
					t.Fatalf("terminalized run %s = status %q token %q completed %q cause %q; want terminal, revoked, and cause containing %q/%q", runID, status, token, completedAt, cause, disposition, eventID)
				}
				var finished int
				if err := f.svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_events WHERE instance_id = ? AND run_id = ? AND type = 'workflow.run_finished'`, f.suspended.ID, runID).Scan(&finished); err != nil {
					t.Fatalf("count run_finished for %s: %v", runID, err)
				}
				if finished != 1 {
					t.Fatalf("run_finished events for %s = %d, want 1", runID, finished)
				}
			}
			if got := durableRunSnapshot(t, f.svc, f.terminalRun.ID); got != terminalBefore {
				t.Fatalf("terminal disposition mutated already-terminal run:\nbefore %s\nafter  %s", terminalBefore, got)
			}
			encoded, _ := json.Marshal(out)
			if !strings.Contains(string(encoded), "terminalizedRuns") || !strings.Contains(string(encoded), f.claim.Binding.Run.ID) || !strings.Contains(string(encoded), f.extraActiveRun.ID) {
				t.Fatalf("terminal disposition result lacks run summaries: %s", encoded)
			}
			for _, token := range originalTokens {
				if token != "" && strings.Contains(string(encoded), token) {
					t.Fatalf("terminal disposition result leaked authority token %q: %s", token, encoded)
				}
			}
			_, err = f.svc.SettleAction(SettleActionParams{
				ActionRunID: f.claim.Binding.Run.ID, OwnerToken: f.claim.Binding.Authority.OwnerToken,
				OwnerGeneration: f.claim.Binding.Authority.OwnerGeneration, Result: "completed",
				Evidence: &ActionEvidenceInput{Summary: "late", Facts: `{"result":"done"}`},
			})
			if err == nil {
				t.Fatal("late holder settled after atomic terminalization")
			}
		})
	}
}

func TestResolveSuspensionTerminalizationFailureRollsBackEveryMutation(t *testing.T) {
	f := setupTerminalDispositionFixture(t)
	const triggerName = "test_fail_terminal_run_finished"
	triggerSQL := fmt.Sprintf(`CREATE TRIGGER %s
		BEFORE INSERT ON workflow_events
		WHEN NEW.type = 'workflow.run_finished' AND NEW.run_id = '%s'
		BEGIN
			SELECT RAISE(ABORT, 'injected terminalization event failure');
		END`, triggerName, f.extraActiveRun.ID)
	if _, err := f.svc.db.Exec(triggerSQL); err != nil {
		t.Fatalf("install terminalization failure trigger: %v", err)
	}
	t.Cleanup(func() { _, _ = f.svc.db.Exec("DROP TRIGGER IF EXISTS " + triggerName) })

	before := terminalDispositionMutationSnapshot(t, f)
	revision := f.suspended.Revision
	_, err := f.svc.ResolveSuspension(ResolveSuspensionParams{
		SuspensionID: f.suspended.Suspension.ID, Disposition: DispositionCancel,
		ExpectRevision: &revision, PrincipalRef: "agent:supervisor", Role: "supervisor",
	})
	if err == nil || !strings.Contains(err.Error(), "injected terminalization event failure") {
		t.Fatalf("terminalization failure injection error = %v, want injected event failure", err)
	}
	if after := terminalDispositionMutationSnapshot(t, f); after != before {
		t.Fatalf("failed terminal disposition committed a partial mutation:\nbefore %s\nafter  %s", before, after)
	}
}

func TestResolveSuspensionWrongIDAndStaleRevisionPreserveInstanceRunsAndLedger(t *testing.T) {
	for _, tc := range []struct {
		name string
		call func(terminalDispositionFixture) ResolveSuspensionParams
	}{
		{name: "wrong suspension id", call: func(f terminalDispositionFixture) ResolveSuspensionParams {
			return ResolveSuspensionParams{SuspensionID: "sus_wrong", Disposition: DispositionCancel}
		}},
		{name: "stale revision", call: func(f terminalDispositionFixture) ResolveSuspensionParams {
			stale := f.suspended.Revision - 1
			return ResolveSuspensionParams{SuspensionID: f.suspended.Suspension.ID, Disposition: DispositionCancel, ExpectRevision: &stale}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := setupTerminalDispositionFixture(t)
			before := terminalDispositionMutationSnapshot(t, f)
			if _, err := f.svc.ResolveSuspension(tc.call(f)); err == nil {
				t.Fatal("terminal resolve refusal unexpectedly succeeded")
			}
			if after := terminalDispositionMutationSnapshot(t, f); after != before {
				t.Fatalf("refused terminal resolve mutated durable state:\nbefore %s\nafter  %s", before, after)
			}
		})
	}
}