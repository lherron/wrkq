package workflow

import (
	"strings"
	"testing"
)

func TestSettleActionCompletedIdempotentAndProjection(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	setTaskSpecAndState(t, svc, taskUUID, "Shaped spec.", "in_progress")
	attachSimpleTaskV2(t, svc, taskUUID)
	claim := claimActionForTest(t, svc, taskUUID, "triage")

	settle := settleClaimForTest(t, svc, claim, `{"result":"ready"}`, "triaged")
	if settle.Run.Status != "completed" {
		t.Fatalf("settled run status = %q, want completed", settle.Run.Status)
	}
	if settle.Evidence == nil || settle.Evidence.Kind != "triage_result" || settle.Evidence.RunID != claim.Binding.Run.ID {
		t.Fatalf("settlement evidence = %+v", settle.Evidence)
	}
	if settle.Transition == nil {
		t.Fatalf("settlement missing transition")
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Phase != "ready" || inst.Revision != 1 {
		t.Fatalf("instance state = %+v, want active/ready rev 1", inst.State())
	}
	if got := readTaskState(t, svc, taskUUID); got != "open" {
		t.Fatalf("task state after v2 triage = %q, want open", got)
	}
	shown, err := svc.ShowRun(claim.Binding.Run.ID)
	if err != nil {
		t.Fatalf("ShowRun: %v", err)
	}
	if shown.LeaseToken != "" || shown.LeaseOwner != "" {
		t.Fatalf("settlement did not clear ownership: %+v", shown)
	}

	beforeEvidence := countTable(t, svc, "workflow_evidence")
	beforeEvents := countTable(t, svc, "workflow_events")
	replay := settleClaimForTest(t, svc, claim, `{"result":"ready"}`, "triaged")
	if replay.Evidence == nil || replay.Evidence.ID != settle.Evidence.ID {
		t.Fatalf("replay evidence = %+v, want %s", replay.Evidence, settle.Evidence.ID)
	}
	if got := countTable(t, svc, "workflow_evidence"); got != beforeEvidence {
		t.Fatalf("evidence rows after replay = %d, want %d", got, beforeEvidence)
	}
	if got := countTable(t, svc, "workflow_events"); got != beforeEvents {
		t.Fatalf("event rows after replay = %d, want %d", got, beforeEvents)
	}

	_, err = svc.SettleAction(SettleActionParams{
		ActionRunID:     claim.Binding.Run.ID,
		OwnerToken:      claim.Binding.Authority.OwnerToken,
		OwnerGeneration: claim.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence:        &ActionEvidenceInput{Summary: "different", Facts: `{"result":"ready"}`},
	})
	if err == nil || !strings.Contains(err.Error(), "idempotency") {
		t.Fatalf("conflicting terminal replay error = %v, want idempotency conflict", err)
	}
}

func TestSettleActionRejectsWrongOwnershipWithoutMutation(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	claim := claimActionForTest(t, svc, taskUUID, "triage")
	before := actionNextMutationCounts(t, svc)

	_, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     claim.Binding.Run.ID,
		OwnerToken:      "wrong-token",
		OwnerGeneration: claim.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence:        &ActionEvidenceInput{Summary: "triaged", Facts: `{"result":"ready"}`},
	})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("wrong owner error = %v, want lease conflict", err)
	}
	after := actionNextMutationCounts(t, svc)
	if after != before {
		t.Fatalf("wrong owner mutated rows: before=%+v after=%+v", before, after)
	}
	run, err := svc.ShowRun(claim.Binding.Run.ID)
	if err != nil {
		t.Fatalf("ShowRun: %v", err)
	}
	if run.Status != "active" {
		t.Fatalf("run status after wrong owner = %q, want active", run.Status)
	}
}

func TestSettleActionImplementRequiresGitCommittedCleanFacts(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")
	before := actionNextMutationCounts(t, svc)

	_, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     impl.Binding.Run.ID,
		OwnerToken:      impl.Binding.Authority.OwnerToken,
		OwnerGeneration: impl.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence: &ActionEvidenceInput{
			Summary: "implemented",
			Facts:   `{"result":"done","commit.sha":"abc123","git.clean":false,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "git.clean") {
		t.Fatalf("dirty implement settlement error = %v, want git.clean validation", err)
	}
	if after := actionNextMutationCounts(t, svc); after != before {
		t.Fatalf("dirty implement mutated rows: before=%+v after=%+v", before, after)
	}

	out := settleClaimForTest(t, svc, impl, `{"result":"done","commit.sha":"abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")
	if out.Evidence == nil {
		t.Fatalf("implement settlement missing evidence")
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Phase != "implemented" {
		t.Fatalf("instance phase = %q, want implemented", inst.Phase)
	}
}

func TestSettleActionVerifyRequiresClaimedSourceCommit(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")
	settleClaimForTest(t, svc, impl, `{"result":"done","commit.sha":"abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")
	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "implement_result",
		Ref:          "manual:latest-head",
		Summary:      "unrelated latest evidence",
		Facts:        `{"result":"done","commit.sha":"wrong-latest"}`,
		PrincipalRef: "agent:t",
		Role:         "implementer",
	}); err != nil {
		t.Fatalf("AddEvidence unrelated latest: %v", err)
	}

	verify := claimActionForTest(t, svc, taskUUID, "verify")
	if verify.Binding.Run.Source == nil || verify.Binding.Run.Source.CommitSha != "abc123" {
		t.Fatalf("verify claim source = %+v, want abc123", verify.Binding.Run.Source)
	}
	before := actionNextMutationCounts(t, svc)
	_, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     verify.Binding.Run.ID,
		OwnerToken:      verify.Binding.Authority.OwnerToken,
		OwnerGeneration: verify.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence: &ActionEvidenceInput{
			Summary: "verified wrong latest",
			Facts:   `{"result":"verified","source.commit.sha":"wrong-latest","verified.commit.sha":"wrong-latest","git.clean":true}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "source commit") {
		t.Fatalf("wrong source verify error = %v, want source commit validation", err)
	}
	if after := actionNextMutationCounts(t, svc); after != before {
		t.Fatalf("wrong source verify mutated rows: before=%+v after=%+v", before, after)
	}

	out := settleClaimForTest(t, svc, verify, `{"result":"verified","source.commit.sha":"abc123","verified.commit.sha":"abc123","git.clean":true}`, "verified")
	if out.Transition == nil {
		t.Fatalf("verify settlement missing transition")
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Status != "closed" || inst.Phase != "done" {
		t.Fatalf("instance state = %+v, want closed/done", inst.State())
	}
	if got := readTaskState(t, svc, taskUUID); got != "completed" {
		t.Fatalf("task state after v2 verify = %q, want completed", got)
	}
}

func TestSettleActionV2BlockerEffectsSetTaskBlocked(t *testing.T) {
	t.Run("implement_blocked", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		attachSimpleTaskV2(t, svc, taskUUID)
		triage := claimActionForTest(t, svc, taskUUID, "triage")
		settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")

		impl := claimActionForTest(t, svc, taskUUID, "implement")
		settleClaimForTest(t, svc, impl, `{"result":"blocked"}`, "blocked")
		inst, err := svc.LatestInstance(taskUUID)
		if err != nil {
			t.Fatalf("LatestInstance: %v", err)
		}
		if inst.Status != "active" || inst.Phase != "ready" {
			t.Fatalf("instance state = %+v, want active/ready", inst.State())
		}
		if got := readTaskState(t, svc, taskUUID); got != "blocked" {
			t.Fatalf("task state after v2 implement blocked = %q, want blocked", got)
		}
	})

	for _, c := range []struct {
		name  string
		facts string
	}{
		{
			name:  "verify_failed",
			facts: `{"result":"failed","source.commit.sha":"abc123","verified.commit.sha":"abc123","git.clean":true}`,
		},
		{
			name:  "verify_blocked",
			facts: `{"result":"blocked","source.commit.sha":"abc123","verified.commit.sha":"abc123","git.clean":true}`,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, taskUUID := actionFixture(t)
			attachSimpleTaskV2(t, svc, taskUUID)
			triage := claimActionForTest(t, svc, taskUUID, "triage")
			settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
			impl := claimActionForTest(t, svc, taskUUID, "implement")
			settleClaimForTest(t, svc, impl, `{"result":"done","commit.sha":"abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")

			verify := claimActionForTest(t, svc, taskUUID, "verify")
			settleClaimForTest(t, svc, verify, c.facts, c.name)
			inst, err := svc.LatestInstance(taskUUID)
			if err != nil {
				t.Fatalf("LatestInstance: %v", err)
			}
			if inst.Status != "active" || inst.Phase != "ready" {
				t.Fatalf("instance state = %+v, want active/ready", inst.State())
			}
			if got := readTaskState(t, svc, taskUUID); got != "blocked" {
				t.Fatalf("task state after %s = %q, want blocked", c.name, got)
			}
		})
	}
}

func TestActionReapV2RecoveryDistinguishesSideEffectAmbiguity(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	expireActionRunForTest(t, svc, triage.Binding.Run.ID)
	reapedTriage, err := svc.ReapActions(ReapActionsParams{Task: taskUUID, Action: "triage", ExpiredBefore: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("ReapActions triage: %v", err)
	}
	if len(reapedTriage.Items) != 1 || reapedTriage.Items[0].Status != "failed" {
		t.Fatalf("triage reap = %+v, want failed", reapedTriage.Items)
	}

	// Reset with a fresh task and advance to implementation, whose executable
	// action declares worktree/git side effects. Reaping must not pretend the
	// implementation failed semantically or safely succeeded.
	svc, taskUUID = actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage = claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")
	expireActionRunForTest(t, svc, impl.Binding.Run.ID)
	beforeEvents := countTable(t, svc, "workflow_events")
	reapedImpl, err := svc.ReapActions(ReapActionsParams{Task: taskUUID, Action: "implement", ExpiredBefore: "2026-01-01T00:00:00Z"})
	if err != nil {
		t.Fatalf("ReapActions implement: %v", err)
	}
	if len(reapedImpl.Items) != 1 || reapedImpl.Items[0].Status != "operator_required" {
		t.Fatalf("implement reap = %+v, want operator_required", reapedImpl.Items)
	}
	if got := countTable(t, svc, "workflow_events"); got != beforeEvents {
		t.Fatalf("reap emitted transition events = %d, want %d", got, beforeEvents)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Phase != "ready" {
		t.Fatalf("reap changed workflow phase = %q, want ready", inst.Phase)
	}
	run, err := svc.ShowRun(impl.Binding.Run.ID)
	if err != nil {
		t.Fatalf("ShowRun: %v", err)
	}
	if run.LeaseToken != "" || run.LeaseOwner != "" || run.Status != "operator_required" {
		t.Fatalf("reaped implement run = %+v", run)
	}
}

func claimActionForTest(t *testing.T, svc *Service, taskUUID, action string) *ClaimActionResult {
	t.Helper()
	out, err := svc.ClaimAction(ClaimActionParams{
		Task:     taskUUID,
		RunnerID: "runner-" + action,
		AgentRef: "agent:" + action,
		Prefer:   ActionClaimPrefer{Action: action},
		LeaseMs:  300000,
	})
	if err != nil {
		t.Fatalf("ClaimAction %s: %v", action, err)
	}
	if out.Binding == nil {
		t.Fatalf("ClaimAction %s returned nil binding", action)
	}
	return out
}

func expireActionRunForTest(t *testing.T, svc *Service, runID string) {
	t.Helper()
	if _, err := svc.db.Exec(`UPDATE workflow_runs SET lease_expires_at = '2000-01-01T00:00:00Z' WHERE id = ?`, runID); err != nil {
		t.Fatalf("expire action run: %v", err)
	}
}

func settleClaimForTest(t *testing.T, svc *Service, claim *ClaimActionResult, facts, summary string) *SettleActionResult {
	t.Helper()
	out, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     claim.Binding.Run.ID,
		OwnerToken:      claim.Binding.Authority.OwnerToken,
		OwnerGeneration: claim.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence:        &ActionEvidenceInput{Summary: summary, Facts: facts},
	})
	if err != nil {
		t.Fatalf("SettleAction %s: %v", claim.Binding.Run.Action, err)
	}
	return out
}
