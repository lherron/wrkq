package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func attachSimpleTaskV2(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskV2TemplateRef, "agent:t"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate(v2): %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:t"); err != nil {
		t.Fatalf("AttachTask(v2): %v", err)
	}
}

func attachSimpleTaskV2WithSourceIdentity(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	doc := builtinV2Doc(t)
	doc["id"] = "source-identity-v2"
	implement := doc["evidenceKinds"].(map[string]any)["implement_result"].(map[string]any)
	implement["facts"].(map[string]any)["properties"].(map[string]any)["change.id"] = map[string]any{"type": "string"}
	action(doc, "verify")["sourceBinding"].(map[string]any)["bindFields"] = map[string]any{"sourceIdentity": "change.id"}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal source-identity v2 template: %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate(source-identity v2): %v", err)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:t", nil, false); err != nil {
		t.Fatalf("install source-identity v2 template: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "source-identity-v2@2", "agent:t"); err != nil {
		t.Fatalf("AttachTask(source-identity v2): %v", err)
	}
}

func attachSimpleTaskV3(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskV3TemplateRef, "agent:t"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate(v3): %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV3TemplateRef, "agent:t"); err != nil {
		t.Fatalf("AttachTask(v3): %v", err)
	}
}

func TestActionNextV2CandidatesByPhaseAndReadOnly(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	before := actionNextMutationCounts(t, svc)

	result, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext intake: %v", err)
	}
	assertActionCandidates(t, result, "triage")
	c := result.Candidates[0]
	if c.RequiredEvidenceKind != "triage_result" || c.Transition != "triage_complete" || c.Role != "triager" {
		t.Fatalf("triage candidate = %+v", c)
	}
	if c.SemanticActionKey == "" || c.InputHash == "" {
		t.Fatalf("triage candidate missing semantic key/input hash: %+v", c)
	}
	after := actionNextMutationCounts(t, svc)
	if before != after {
		t.Fatalf("ActionNext mutated rows: before=%v after=%v", before, after)
	}

	filtered, err := svc.ActionNext(ActionNextParams{
		Task:    taskUUID,
		Filters: ActionNextFilters{Actions: []string{"implement"}},
	})
	if err != nil {
		t.Fatalf("ActionNext filter: %v", err)
	}
	if len(filtered.Candidates) != 0 {
		t.Fatalf("implement filter at intake returned %+v, want empty", filtered.Candidates)
	}

	triage := startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	if triage.Run.Status != "completed" {
		t.Fatalf("triage status = %q", triage.Run.Status)
	}
	ready, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext ready: %v", err)
	}
	assertActionCandidates(t, ready, "implement")
}

func TestActionNextVerifyCandidateUsesExactImplementEvidenceSource(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2WithSourceIdentity(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	impl := startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done","commit.sha":"commit-abc123","change.id":"change-v1:opaque-identity","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`)
	if impl.Evidence == nil {
		t.Fatalf("implement completion missing evidence")
	}

	// A newer implement_result without an implement action run must not become
	// the verify source. This guards against "latest evidence" inference.
	if _, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "implement_result",
		Ref:          "manual:unrelated",
		Summary:      "unrelated latest evidence",
		Facts:        `{"result":"done","commit.sha":"wrong-latest"}`,
		PrincipalRef: "agent:t",
		Role:         "implementer",
	}); err != nil {
		t.Fatalf("AddEvidence unrelated implement_result: %v", err)
	}

	result, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext implemented: %v", err)
	}
	assertActionCandidates(t, result, "verify")
	c := result.Candidates[0]
	if c.Source == nil {
		t.Fatalf("verify candidate missing source: %+v", c)
	}
	if c.Source.SourceRunID != impl.Run.RunID {
		t.Fatalf("sourceRunId = %q, want %q", c.Source.SourceRunID, impl.Run.RunID)
	}
	if c.Source.SourceEvidenceID != impl.Evidence.ID {
		t.Fatalf("sourceEvidenceId = %q, want %q", c.Source.SourceEvidenceID, impl.Evidence.ID)
	}
	if c.Source.SourceIdentity != "change-v1:opaque-identity" {
		t.Fatalf("sourceIdentity = %q, want change-v1:opaque-identity", c.Source.SourceIdentity)
	}
	wantSemanticKey := fmt.Sprintf("verify:%s:r%d", c.InstanceID, c.ExpectedStateRevision)
	if c.SemanticActionKey != wantSemanticKey {
		t.Fatalf("semanticActionKey = %q, want action occurrence %q", c.SemanticActionKey, wantSemanticKey)
	}
	if strings.Contains(c.SemanticActionKey, "wrong-latest") {
		t.Fatalf("semanticActionKey used unrelated latest evidence: %q", c.SemanticActionKey)
	}
}

func TestActionNextBlockedVerifyWhenSourceMissing(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	implemented := startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`)
	if _, err := svc.db.Exec(`UPDATE workflow_evidence SET facts_json = ? WHERE id = ?`, `{"result":"done"}`, implemented.Evidence.ID); err != nil {
		t.Fatalf("remove source facts: %v", err)
	}

	hidden, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext missing source hidden: %v", err)
	}
	if len(hidden.Candidates) != 0 {
		t.Fatalf("missing source without includeBlocked returned %+v, want empty", hidden.Candidates)
	}
	blocked, err := svc.ActionNext(ActionNextParams{Task: taskUUID, Filters: ActionNextFilters{Actions: []string{"verify"}, IncludeBlocked: true}})
	if err != nil {
		t.Fatalf("ActionNext missing source blocked: %v", err)
	}
	assertActionCandidates(t, blocked, "verify")
	if !blocked.Candidates[0].Blocked || blocked.Candidates[0].BlockedReason == "" {
		t.Fatalf("blocked candidate missing reason: %+v", blocked.Candidates[0])
	}
}

func TestActionNextV3LandingCandidateBindsPRVerifiedSource(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV3(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done","commit.sha":"h0","change.id":"change-v1:h0","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`)
	verify := claimActionForTest(t, svc, taskUUID, "verify")
	verified := settleClaimForTest(t, svc, verify, prVerifiedFacts(verify.Binding.Run.Source.SourceEvidenceID, verify.Binding.Run.Source.SourceIdentity, "h0", "bar0", "https://example.test/pr/1"), "pr verified")

	result, err := svc.ActionNext(ActionNextParams{
		Task:    taskUUID,
		Filters: ActionNextFilters{Statuses: []string{"waiting"}, Phases: []string{"awaiting_merge"}},
	})
	if err != nil {
		t.Fatalf("ActionNext awaiting_merge: %v", err)
	}
	assertActionCandidates(t, result, "landing")
	c := result.Candidates[0]
	if c.RequiredEvidenceKind != "landing_result" || c.Role != "release_manager" {
		t.Fatalf("landing candidate = %+v", c)
	}
	if c.Source == nil || c.Source.SourceEvidenceID != verified.Evidence.ID || c.Source.SourceIdentity != "change-v1:h0" || c.Source.ArtifactRef != "bar0" {
		t.Fatalf("landing source = %+v, want evidence %s change-v1:h0 bar0", c.Source, verified.Evidence.ID)
	}
}

func TestActionNextReapAdmissibilityResumesFailedAndOperatorRequiredRuns(t *testing.T) {
	t.Run("failed triage reap", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		attachSimpleTaskV2(t, svc, taskUUID)
		triage := claimActionForTest(t, svc, taskUUID, "triage")

		resumed := reapAndClaimAdmissibleCandidate(t, svc, taskUUID, triage, "failed", "triage")
		settleClaimForTest(t, svc, resumed, `{"result":"ready"}`, "triaged after reap")
		implemented := claimActionForTest(t, svc, taskUUID, "implement")
		implResult := settleClaimForTest(t, svc, implemented, `{"result":"done","commit.sha":"reap-triage","change.id":"change-v1:reap-triage","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented after triage reap")
		settleV2ReapResumptionToDone(t, svc, taskUUID, implResult)
	})

	t.Run("operator-required implement reap", func(t *testing.T) {
		svc, taskUUID := actionFixture(t)
		attachSimpleTaskV2(t, svc, taskUUID)
		triage := claimActionForTest(t, svc, taskUUID, "triage")
		settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
		impl := claimActionForTest(t, svc, taskUUID, "implement")

		resumed := reapAndClaimAdmissibleCandidate(t, svc, taskUUID, impl, "operator_required", "implement")
		implResult := settleClaimForTest(t, svc, resumed, `{"result":"done","commit.sha":"reap-implement","change.id":"change-v1:reap-implement","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented after operator-required reap")
		settleV2ReapResumptionToDone(t, svc, taskUUID, implResult)
	})
}

func reapAndClaimAdmissibleCandidate(t *testing.T, svc *Service, taskUUID string, expired *ClaimActionResult, wantStatus, wantAction string) *ClaimActionResult {
	t.Helper()
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance before reap: %v", err)
	}
	expireActionRunForTest(t, svc, expired.Binding.Run.ID)

	// Claim-over-expired semantics means this 4b proof is about ordinary action
	// discovery, not whether workspace-lease release happened on this reap tick.
	reaped, err := svc.ReapActions(ReapActionsParams{InstanceID: inst.ID, Action: wantAction, ExpiredBefore: "2026-01-01T00:00:00Z", PrincipalRef: "agent:wrkqd"})
	if err != nil {
		t.Fatalf("ReapActions: %v", err)
	}
	if len(reaped.Items) != 1 || reaped.Items[0].Status != wantStatus {
		t.Fatalf("reaped %s = %+v, want one %s run", wantAction, reaped.Items, wantStatus)
	}

	next, err := svc.ActionNext(ActionNextParams{InstanceID: inst.ID})
	if err != nil {
		t.Fatalf("ActionNext reaped instance: %v", err)
	}
	assertActionCandidates(t, next, wantAction)
	candidate := next.Candidates[0]
	if candidate.InstanceID != inst.ID || candidate.Blocked {
		t.Fatalf("reaped candidate = %+v, want an unblocked candidate for instance %s", candidate, inst.ID)
	}

	resumed, err := svc.ClaimAction(ClaimActionParams{
		InstanceID: inst.ID,
		RunnerID:   "runner-resumed-" + wantAction,
		AgentRef:   "agent:resumed-" + wantAction,
		Prefer:     ActionClaimPrefer{SemanticActionKey: candidate.SemanticActionKey},
		LeaseMs:    300000,
	})
	if err != nil {
		t.Fatalf("ClaimAction reprojected %s candidate: %v", wantAction, err)
	}
	if resumed.Binding == nil || resumed.Binding.Run.ID == expired.Binding.Run.ID || resumed.Binding.Run.InstanceID != inst.ID {
		t.Fatalf("resumed %s binding = %+v, want a new run on instance %s", wantAction, resumed.Binding, inst.ID)
	}
	return resumed
}

func settleV2ReapResumptionToDone(t *testing.T, svc *Service, taskUUID string, implemented *SettleActionResult) {
	t.Helper()
	if implemented.Evidence == nil {
		t.Fatal("resumed implement settlement is missing evidence")
	}
	verify := claimActionForTest(t, svc, taskUUID, "verify")
	settleClaimForTest(t, svc, verify, `{"result":"verified","source.evidence_id":"`+implemented.Evidence.ID+`","source.commit.sha":"`+stringFact(evidenceFactsMap(*implemented.Evidence), "commit.sha")+`","verified.commit.sha":"`+stringFact(evidenceFactsMap(*implemented.Evidence), "commit.sha")+`","verified.change.id":"`+stringFact(evidenceFactsMap(*implemented.Evidence), "change.id")+`","context.id":"context-v1:reap-admissibility","git.clean":true}`, "verified after reap resumption")
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance after resumption: %v", err)
	}
	if inst.Status != "closed" || inst.Phase != "done" {
		t.Fatalf("reaped instance state = %+v, want closed/done", inst.State())
	}
}

func startAndCompleteAction(t *testing.T, svc *Service, taskUUID, action, facts string) *ActionCompleteResult {
	t.Helper()
	run, err := svc.StartAction(StartActionParams{Task: taskUUID, Action: action, PrincipalRef: "agent:t"})
	if err != nil {
		t.Fatalf("StartAction %s: %v", action, err)
	}
	out, err := svc.CompleteAction(CompleteActionParams{
		ActionRunID: run.RunID,
		Evidence:    &ActionEvidenceInput{Summary: action, Facts: facts},
	})
	if err != nil {
		t.Fatalf("CompleteAction %s: %v", action, err)
	}
	return out
}

func assertActionCandidates(t *testing.T, result *ActionNextResult, actions ...string) {
	t.Helper()
	if result == nil {
		t.Fatalf("result is nil")
	}
	if len(result.Candidates) != len(actions) {
		t.Fatalf("candidates len = %d, want %d: %+v", len(result.Candidates), len(actions), result.Candidates)
	}
	for i, want := range actions {
		if got := result.Candidates[i].Action; got != want {
			t.Fatalf("candidate[%d].action = %q, want %q: %+v", i, got, want, result.Candidates[i])
		}
	}
}

type actionNextCounts struct {
	Runs      int
	Evidence  int
	Events    int
	Effects   int
	Instances int
}

func actionNextMutationCounts(t *testing.T, svc *Service) actionNextCounts {
	t.Helper()
	return actionNextCounts{
		Runs:      countTable(t, svc, "workflow_runs"),
		Evidence:  countTable(t, svc, "workflow_evidence"),
		Events:    countTable(t, svc, "workflow_events"),
		Effects:   countTable(t, svc, "workflow_effects"),
		Instances: countTable(t, svc, "workflow_instances"),
	}
}

func countTable(t *testing.T, svc *Service, table string) int {
	t.Helper()
	var n int
	if err := svc.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}
