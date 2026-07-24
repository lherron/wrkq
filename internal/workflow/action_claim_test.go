package workflow

import (
	"fmt"
	"strings"
	"testing"
)

func TestClaimActionAcknowledgesNamedPredecessor(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)

	first, err := svc.ClaimAction(ClaimActionParams{
		Task:             taskUUID,
		RunnerID:         "runner-a",
		AgentRef:         "agent:cody",
		ScopeRef:         "cody@wrkq:T-claim",
		LeaseMs:          300000,
		PriorRunProvided: true,
	})
	if err != nil {
		t.Fatalf("ClaimAction first: %v", err)
	}
	if first.Binding == nil {
		t.Fatalf("ClaimAction first returned nil binding")
	}
	if first.Binding.Run.Action != "triage" || first.Binding.Run.Role != "triager" {
		t.Fatalf("claimed run = %+v, want triage/triager", first.Binding.Run)
	}
	if first.Binding.Run.SemanticActionKey == "" || first.Binding.Run.Attempt != 1 {
		t.Fatalf("claimed run missing semantic key/attempt: %+v", first.Binding.Run)
	}
	if first.Binding.Authority.RunnerID != "runner-a" || first.Binding.Authority.OwnerToken == "" || first.Binding.Authority.OwnerGeneration != 1 {
		t.Fatalf("authority = %+v, want runner-a token generation 1", first.Binding.Authority)
	}
	if first.Binding.Run.AgentRef != "agent:cody" || first.Binding.Run.ScopeRef != "cody@wrkq:T-claim" {
		t.Fatalf("run agent/scope = %+v", first.Binding.Run)
	}

	priorRun := first.Binding.Run.ID
	successor, err := svc.ClaimAction(ClaimActionParams{
		Task:             taskUUID,
		RunnerID:         "runner-a",
		AgentRef:         "agent:cody",
		ScopeRef:         "cody@wrkq:T-claim",
		LeaseMs:          300000,
		PriorRun:         &priorRun,
		PriorRunProvided: true,
	})
	if err != nil {
		t.Fatalf("ClaimAction successor: %v", err)
	}
	if successor.Binding == nil {
		t.Fatalf("ClaimAction successor returned nil binding")
	}
	if successor.Binding.Run.ID == first.Binding.Run.ID || successor.Binding.Run.PredecessorRunID != first.Binding.Run.ID {
		t.Fatalf("successor lineage = %+v, want predecessor %q", successor.Binding.Run, first.Binding.Run.ID)
	}
	if successor.Binding.Authority.OwnerGeneration != 1 {
		t.Fatalf("successor generation = %d, want 1", successor.Binding.Authority.OwnerGeneration)
	}
	if got := countActiveSemanticRuns(t, svc, first.Binding.Run.InstanceID, first.Binding.Run.SemanticActionKey); got != 1 {
		t.Fatalf("active semantic runs = %d, want 1", got)
	}
	var predecessorStatus, predecessorToken, supersededBy string
	if err := svc.db.QueryRow(`SELECT status, COALESCE(lease_token,''), COALESCE(superseded_by_run_id,'') FROM workflow_runs WHERE id = ?`, first.Binding.Run.ID).Scan(&predecessorStatus, &predecessorToken, &supersededBy); err != nil {
		t.Fatalf("read predecessor after succession: %v", err)
	}
	if predecessorStatus != "superseded" || predecessorToken != "" || supersededBy != successor.Binding.Run.ID {
		t.Fatalf("predecessor after succession = status %q token %q successor %q", predecessorStatus, predecessorToken, supersededBy)
	}
	var successionEvents int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM ledger_entry WHERE instance_id = ? AND kind = 'workflow.action.succession' AND json_extract(body_json, '$.predecessorRunId') = ? AND json_extract(body_json, '$.successorRunId') = ?`, first.Binding.Run.InstanceID, first.Binding.Run.ID, successor.Binding.Run.ID).Scan(&successionEvents); err != nil {
		t.Fatalf("read succession ledger: %v", err)
	}
	if successionEvents != 1 {
		t.Fatalf("succession ledger entries = %d, want 1", successionEvents)
	}
	_, err = svc.SettleAction(SettleActionParams{
		ActionRunID: first.Binding.Run.ID, OwnerToken: first.Binding.Authority.OwnerToken,
		OwnerGeneration: first.Binding.Authority.OwnerGeneration, Result: "completed",
	})
	if err == nil || !strings.Contains(err.Error(), "superseded by "+successor.Binding.Run.ID) {
		t.Fatalf("late predecessor settle error = %v, want superseded successor", err)
	}
}

func TestClaimActionRefusalCarriesFullPredecessorRecord(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")

	first, err := svc.ClaimAction(ClaimActionParams{
		Task: taskUUID, RunnerID: "runner-implement", AgentRef: "agent:implement",
		Prefer: ActionClaimPrefer{Action: "implement"}, LeaseMs: 300000,
		WorkspaceRoot: "/worktrees/implement", PriorRunProvided: true,
	})
	if err != nil {
		t.Fatalf("first implement claim: %v", err)
	}
	if _, err := svc.BindActionExternal(BindActionExternalParams{ActionRunID: first.Binding.Run.ID, ExternalRunRef: "hrc:run-live"}); err != nil {
		t.Fatalf("bind predecessor external run: %v", err)
	}
	if _, err := svc.db.Exec(`INSERT INTO workflow_evidence (id, instance_id, kind, ref, summary, source_json, actor, role, run_id, produced_at) VALUES ('ev-predecessor', ?, 'implement_result', 'wrkf-action:predecessor', 'partial work', '{}', 'agent:implement', 'implementer', ?, '2026-07-12T12:00:00Z')`, first.Binding.Run.InstanceID, first.Binding.Run.ID); err != nil {
		t.Fatalf("seed predecessor evidence: %v", err)
	}

	wrong := "run-not-the-predecessor"
	_, err = svc.ClaimAction(ClaimActionParams{
		Task: taskUUID, RunnerID: "runner-successor", AgentRef: "agent:successor",
		Prefer: ActionClaimPrefer{Action: "implement"}, LeaseMs: 300000,
		PriorRun: &wrong, PriorRunProvided: true,
	})
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Predecessor == nil {
		t.Fatalf("claim refusal detail = %+v err=%v, want predecessor", detail, err)
	}
	pred := detail.Predecessor
	if pred.RunID != first.Binding.Run.ID || pred.Owner != "runner-implement" || pred.ClaimedAt == "" || pred.HeartbeatAt == "" || pred.ExpiresAt == "" || pred.SettleStatus != "active" || pred.Settled {
		t.Fatalf("predecessor identity/timestamps/status = %+v", pred)
	}
	if len(pred.SideEffectClasses) != 2 || pred.ExternalRunRef != "hrc:run-live" || pred.WorkspaceRef != "/worktrees/implement" {
		t.Fatalf("predecessor declared facts = %+v", pred)
	}
	if len(pred.EvidenceWritten) != 1 || pred.EvidenceWritten[0].ID != "ev-predecessor" {
		t.Fatalf("predecessor evidence = %+v", pred.EvidenceWritten)
	}
}

func TestClaimActionRefusalSettledUsesTerminalStatusPredicate(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")

	first := claimActionForTest(t, svc, taskUUID, "implement")
	const futureTerminalStatus = "future_terminal_status"
	if _, err := svc.db.Exec(
		`UPDATE workflow_runs SET status = ?, completed_at = '2026-07-24T00:00:00Z' WHERE id = ?`,
		futureTerminalStatus,
		first.Binding.Run.ID,
	); err != nil {
		t.Fatalf("seed future terminal predecessor status: %v", err)
	}

	_, err := svc.ClaimAction(ClaimActionParams{
		Task: taskUUID, RunnerID: "runner-successor", AgentRef: "agent:successor",
		Prefer: ActionClaimPrefer{Action: "implement"}, LeaseMs: 300000,
		PriorRunProvided: true,
	})
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Predecessor == nil {
		t.Fatalf("claim refusal detail = %+v err=%v, want predecessor", detail, err)
	}
	if got := detail.Predecessor.SettleStatus; got != futureTerminalStatus {
		t.Fatalf("predecessor settleStatus = %q, want %q", got, futureTerminalStatus)
	}
	if !detail.Predecessor.Settled {
		t.Fatalf("predecessor settled = false for terminal status outside consumer enumerations: %+v", detail.Predecessor)
	}
}

func TestClaimActionRejectsForeignActiveOwnerAndCapabilityMismatch(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)

	first, err := svc.ClaimAction(ClaimActionParams{
		Task:             taskUUID,
		RunnerID:         "runner-a",
		AgentRef:         "agent:cody",
		LeaseMs:          300000,
		PriorRunProvided: true,
	})
	if err != nil {
		t.Fatalf("ClaimAction first: %v", err)
	}
	if first.Binding == nil {
		t.Fatalf("ClaimAction first returned nil binding")
	}

	_, err = svc.ClaimAction(ClaimActionParams{
		Task:     taskUUID,
		RunnerID: "runner-b",
		AgentRef: "agent:larry",
		LeaseMs:  300000,
	})
	if err == nil || !strings.Contains(err.Error(), "claim refused") {
		t.Fatalf("unnamed predecessor error = %v, want claim refused", err)
	}

	_, err = svc.ClaimAction(ClaimActionParams{
		Task:     taskUUID,
		RunnerID: "runner-a",
		AgentRef: "agent:cody",
		Prefer:   ActionClaimPrefer{Action: "triage"},
		Capabilities: []RunnerCapability{{
			Actions: []string{"verify"},
		}},
		LeaseMs:          300000,
		PriorRunProvided: true,
	})
	if err == nil || !strings.Contains(err.Error(), "capabilities") {
		t.Fatalf("capability mismatch error = %v, want capabilities error", err)
	}
	if got := countActiveSemanticRuns(t, svc, first.Binding.Run.InstanceID, first.Binding.Run.SemanticActionKey); got != 1 {
		t.Fatalf("active semantic runs after rejects = %d, want 1", got)
	}
}

func TestClaimActionVerifyCarriesExactSourceBinding(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2WithSourceIdentity(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	impl := startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`)
	if impl.Evidence == nil {
		t.Fatalf("implement completion missing evidence")
	}
	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "implement_result",
		Ref:          "manual:latest",
		Summary:      "unrelated newer evidence",
		Facts:        `{"result":"done","commit.sha":"wrong-latest"}`,
		PrincipalRef: "agent:t",
		Role:         "implementer",
	}); err != nil {
		t.Fatalf("AddEvidence unrelated latest: %v", err)
	}

	claimed, err := svc.ClaimAction(ClaimActionParams{
		Task:     taskUUID,
		RunnerID: "runner-v",
		AgentRef: "agent:verify",
		Prefer:   ActionClaimPrefer{Action: "verify"},
		Capabilities: []RunnerCapability{{
			Actions:         []string{"verify"},
			HandlerContract: "praesidium.wrkq-simple-task.verify@1",
		}},
		LeaseMs:          300000,
		PriorRunProvided: true,
	})
	if err != nil {
		t.Fatalf("ClaimAction verify: %v", err)
	}
	if claimed.Binding == nil {
		t.Fatalf("ClaimAction verify returned nil binding")
	}
	source := claimed.Binding.Run.Source
	if source == nil {
		t.Fatalf("verify claim missing source: %+v", claimed.Binding.Run)
	}
	if source.SourceRunID != impl.Run.RunID || source.SourceEvidenceID != impl.Evidence.ID || source.SourceIdentity != "change-v1:abc123" {
		t.Fatalf("source = %+v, want run %s evidence %s identity change-v1:abc123", source, impl.Run.RunID, impl.Evidence.ID)
	}
	if strings.Contains(claimed.Binding.Run.SemanticActionKey, "wrong-latest") {
		t.Fatalf("semantic key used unrelated latest evidence: %q", claimed.Binding.Run.SemanticActionKey)
	}
	if got := persistedClaimSource(t, svc, claimed.Binding.Run.ID); got != "run="+impl.Run.RunID+" evidence="+impl.Evidence.ID+" identity=change-v1:abc123" {
		t.Fatalf("persisted source = %q", got)
	}
}

func TestActionOccurrenceAttemptNumberingAfterOperationalFailure(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2WithSourceIdentity(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done","commit.sha":"source-one","change.id":"change-v1:source-one","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`)

	first := claimActionForTest(t, svc, taskUUID, "verify")
	if first.Binding.Run.Attempt != 1 {
		t.Fatalf("first attempt = %d, want 1", first.Binding.Run.Attempt)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	wantKey := fmt.Sprintf("verify:%s:r%d", inst.ID, inst.Revision)
	if first.Binding.Run.SemanticActionKey != wantKey {
		t.Fatalf("first semantic key = %q, want action occurrence %q", first.Binding.Run.SemanticActionKey, wantKey)
	}
	if _, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     first.Binding.Run.ID,
		OwnerToken:      first.Binding.Authority.OwnerToken,
		OwnerGeneration: first.Binding.Authority.OwnerGeneration,
		Result:          "operational_failed",
		Evidence: &ActionEvidenceInput{
			Kind:    "failure_result",
			Summary: "retryable verification failure",
			Facts:   `{}`,
		},
	}); err != nil {
		t.Fatalf("settle first verification attempt: %v", err)
	}
	_, err = svc.ClaimAction(ClaimActionParams{
		Task: taskUUID, RunnerID: "runner-review", AgentRef: "agent:review",
		Prefer: ActionClaimPrefer{Action: "verify"}, LeaseMs: 300000, PriorRunProvided: true,
	})
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Predecessor == nil || detail.Predecessor.Owner == "" || detail.Predecessor.HeartbeatAt == "" || detail.Predecessor.ExpiresAt == "" || detail.Predecessor.SettleStatus != "operational_failed" || !detail.Predecessor.Settled {
		t.Fatalf("settled predecessor review record = %+v err=%v", detail.Predecessor, err)
	}

	retry := claimActionForTest(t, svc, taskUUID, "verify")
	if retry.Binding.Run.SemanticActionKey != first.Binding.Run.SemanticActionKey {
		t.Fatalf("retry semantic key = %q, want %q", retry.Binding.Run.SemanticActionKey, first.Binding.Run.SemanticActionKey)
	}
	if retry.Binding.Run.Attempt != 2 {
		t.Fatalf("retry attempt = %d, want 2", retry.Binding.Run.Attempt)
	}
	if retry.Binding.Run.PredecessorRunID != first.Binding.Run.ID {
		t.Fatalf("retry predecessor = %q, want cleanly settled run %q", retry.Binding.Run.PredecessorRunID, first.Binding.Run.ID)
	}
	var settledPredecessorStatus string
	if err := svc.db.QueryRow(`SELECT status FROM workflow_runs WHERE id = ?`, first.Binding.Run.ID).Scan(&settledPredecessorStatus); err != nil {
		t.Fatalf("read settled predecessor: %v", err)
	}
	if settledPredecessorStatus != "superseded" {
		t.Fatalf("settled predecessor status = %q, want superseded", settledPredecessorStatus)
	}
}

func TestActionOccurrenceIdempotencyHistory(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2WithSourceIdentity(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	implemented := startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done","commit.sha":"source-one","change.id":"change-v1:source-one","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`)

	first := claimActionForTest(t, svc, taskUUID, "verify")
	if implemented.Evidence == nil || first.Binding.Run.Source == nil || first.Binding.Run.Source.SourceEvidenceID != implemented.Evidence.ID {
		t.Fatalf("verify source = %+v, want implementation evidence %+v", first.Binding.Run.Source, implemented.Evidence)
	}
	replay := claimActionForTest(t, svc, taskUUID, "verify")
	if replay.Binding.Run.ID == first.Binding.Run.ID || replay.Binding.Run.PredecessorRunID != first.Binding.Run.ID {
		t.Fatalf("acknowledged successor = %+v, want predecessor %q", replay.Binding.Run, first.Binding.Run.ID)
	}

	settle := SettleActionParams{
		ActionRunID:     replay.Binding.Run.ID,
		OwnerToken:      replay.Binding.Authority.OwnerToken,
		OwnerGeneration: replay.Binding.Authority.OwnerGeneration,
		Result:          "operational_failed",
		Evidence: &ActionEvidenceInput{
			Kind:    "failure_result",
			Summary: "retryable verification failure",
			Facts:   `{}`,
		},
	}
	if _, err := svc.SettleAction(settle); err != nil {
		t.Fatalf("settle verification failure: %v", err)
	}
	if replayed, err := svc.SettleAction(settle); err != nil {
		t.Fatalf("idempotent settlement replay: %v", err)
	} else if replayed.Run.ID != replay.Binding.Run.ID {
		t.Fatalf("idempotent settlement run id = %q, want %q", replayed.Run.ID, replay.Binding.Run.ID)
	}
	settle.Evidence.Summary = "different failure payload"
	_, err := svc.SettleAction(settle)
	requireWrkfCode(t, err, "WRKF_IDEMPOTENCY_MISMATCH")
}

func TestClaimActionNewSourceConflictsWithoutOrphaning(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2WithSourceIdentity(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	implemented := startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done","commit.sha":"source-one","change.id":"change-v1:source-one","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`)
	if implemented.Evidence == nil {
		t.Fatal("implementation evidence is required")
	}
	first := claimActionForTest(t, svc, taskUUID, "verify")

	newSource, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "implement_result",
		Ref:          "manual:new-source",
		Summary:      "new implementation source",
		Facts:        `{"result":"done","commit.sha":"source-two","change.id":"change-v1:source-two","git.clean":true,"base.sha":"base001","postcondition":"git_committed_clean","repair.turns":0}`,
		PrincipalRef: "agent:implement",
		Role:         "implementer",
		RunID:        implemented.Run.RunID,
	})
	if err != nil {
		t.Fatalf("add new source evidence: %v", err)
	}

	next, err := svc.ActionNext(ActionNextParams{Task: taskUUID, Filters: ActionNextFilters{Actions: []string{"verify"}}})
	if err != nil {
		t.Fatalf("ActionNext after new source: %v", err)
	}
	assertActionCandidates(t, next, "verify")
	candidate := next.Candidates[0]
	if candidate.Source == nil || candidate.Source.SourceEvidenceID != newSource.ID || candidate.Source.SourceIdentity != "change-v1:source-two" {
		t.Fatalf("new source candidate = %+v, want evidence %s identity change-v1:source-two", candidate.Source, newSource.ID)
	}
	if candidate.SemanticActionKey != first.Binding.Run.SemanticActionKey {
		t.Fatalf("new source key = %q, want existing action occurrence key %q", candidate.SemanticActionKey, first.Binding.Run.SemanticActionKey)
	}

	_, err = svc.ClaimAction(ClaimActionParams{
		Task:             taskUUID,
		RunnerID:         "runner-verify",
		AgentRef:         "agent:verify",
		Prefer:           ActionClaimPrefer{Action: "verify"},
		LeaseMs:          300000,
		PriorRunProvided: true,
	})
	requireWrkfCode(t, err, "WRKF_LEASE_CONFLICT")

	var status string
	if err := svc.db.QueryRow(`SELECT status FROM workflow_runs WHERE id = ?`, first.Binding.Run.ID).Scan(&status); err != nil {
		t.Fatalf("read original active run: %v", err)
	}
	if status != "active" {
		t.Fatalf("original run status = %q, want active", status)
	}
	if got := countActiveSemanticRuns(t, svc, first.Binding.Run.InstanceID, first.Binding.Run.SemanticActionKey); got != 1 {
		t.Fatalf("active runs for action occurrence = %d, want 1", got)
	}
	var activeRuns int
	if err := svc.db.QueryRow(`SELECT COUNT(*) FROM workflow_runs WHERE instance_id = ? AND status = 'active'`, first.Binding.Run.InstanceID).Scan(&activeRuns); err != nil {
		t.Fatalf("count active runs: %v", err)
	}
	if activeRuns != 1 {
		t.Fatalf("active runs after conflicting source claim = %d, want 1", activeRuns)
	}

	settleClaimForTest(t, svc, first, fmt.Sprintf(`{"result":"verified","source.evidence_id":%q,"source.commit.sha":"source-one","verified.commit.sha":"source-one","verified.change.id":%q,"context.id":"context-v1:source-one","git.clean":true}`, implemented.Evidence.ID, first.Binding.Run.Source.SourceIdentity), "verify original source")
}

func countActiveSemanticRuns(t *testing.T, svc *Service, instanceID, semanticKey string) int {
	t.Helper()
	var n int
	if err := svc.db.QueryRow(`
		SELECT COUNT(*) FROM workflow_runs
		WHERE instance_id = ? AND semantic_action_key = ? AND status = 'active'
	`, instanceID, semanticKey).Scan(&n); err != nil {
		t.Fatalf("count active semantic runs: %v", err)
	}
	return n
}

func persistedClaimSource(t *testing.T, svc *Service, runID string) string {
	t.Helper()
	var sourceRunID, sourceEvidenceID, sourceIdentity string
	if err := svc.db.QueryRow(`
		SELECT COALESCE(source_run_id,''), COALESCE(source_evidence_id,''), COALESCE(source_identity,'')
		FROM workflow_runs WHERE id = ?
	`, runID).Scan(&sourceRunID, &sourceEvidenceID, &sourceIdentity); err != nil {
		t.Fatalf("query persisted claim source: %v", err)
	}
	return "run=" + sourceRunID + " evidence=" + sourceEvidenceID + " identity=" + sourceIdentity
}
