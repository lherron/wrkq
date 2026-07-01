package workflow

import (
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
	attachSimpleTaskV2(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	impl := startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done","commit.sha":"abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`)
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
	if c.Source.CommitSha != "abc123" {
		t.Fatalf("commitSha = %q, want abc123", c.Source.CommitSha)
	}
	if !strings.Contains(c.SemanticActionKey, impl.Run.RunID) || !strings.Contains(c.SemanticActionKey, "abc123") {
		t.Fatalf("semanticActionKey = %q, want implement run id and commit", c.SemanticActionKey)
	}
	if strings.Contains(c.SemanticActionKey, "wrong-latest") {
		t.Fatalf("semanticActionKey used unrelated latest evidence: %q", c.SemanticActionKey)
	}
}

func TestActionNextBlockedVerifyWhenSourceMissing(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	startAndCompleteAction(t, svc, taskUUID, "triage", `{"result":"ready"}`)
	startAndCompleteAction(t, svc, taskUUID, "implement", `{"result":"done"}`)

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
