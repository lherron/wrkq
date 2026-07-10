package workflow

import (
	"strings"
	"testing"
)

func TestClaimActionCreatesFencedRunAndReplaysSameRunner(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)

	first, err := svc.ClaimAction(ClaimActionParams{
		Task:     taskUUID,
		RunnerID: "runner-a",
		AgentRef: "agent:cody",
		ScopeRef: "cody@wrkq:T-claim",
		LeaseMs:  300000,
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

	replay, err := svc.ClaimAction(ClaimActionParams{
		Task:     taskUUID,
		RunnerID: "runner-a",
		AgentRef: "agent:cody",
		ScopeRef: "cody@wrkq:T-claim",
		LeaseMs:  300000,
	})
	if err != nil {
		t.Fatalf("ClaimAction replay: %v", err)
	}
	if replay.Binding == nil {
		t.Fatalf("ClaimAction replay returned nil binding")
	}
	if replay.Binding.Run.ID != first.Binding.Run.ID {
		t.Fatalf("replay run id = %q, want %q", replay.Binding.Run.ID, first.Binding.Run.ID)
	}
	if replay.Binding.Authority.OwnerGeneration != 2 {
		t.Fatalf("replay generation = %d, want 2", replay.Binding.Authority.OwnerGeneration)
	}
	if replay.Binding.Authority.OwnerToken == first.Binding.Authority.OwnerToken {
		t.Fatalf("replay should rotate owner token")
	}
	if got := countActiveSemanticRuns(t, svc, first.Binding.Run.InstanceID, first.Binding.Run.SemanticActionKey); got != 1 {
		t.Fatalf("active semantic runs = %d, want 1", got)
	}
}

func TestClaimActionRejectsForeignActiveOwnerAndCapabilityMismatch(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)

	first, err := svc.ClaimAction(ClaimActionParams{
		Task:     taskUUID,
		RunnerID: "runner-a",
		AgentRef: "agent:cody",
		LeaseMs:  300000,
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
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("foreign active owner error = %v, want lease conflict", err)
	}

	_, err = svc.ClaimAction(ClaimActionParams{
		Task:     taskUUID,
		RunnerID: "runner-a",
		AgentRef: "agent:cody",
		Prefer:   ActionClaimPrefer{Action: "triage"},
		Capabilities: []RunnerCapability{{
			Actions: []string{"verify"},
		}},
		LeaseMs: 300000,
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
		LeaseMs: 300000,
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
	if source.SourceRunID != impl.Run.RunID || source.SourceEvidenceID != impl.Evidence.ID || source.CommitSha != "abc123" || source.SourceIdentity != "change-v1:abc123" {
		t.Fatalf("source = %+v, want run %s evidence %s commit abc123 identity change-v1:abc123", source, impl.Run.RunID, impl.Evidence.ID)
	}
	if strings.Contains(claimed.Binding.Run.SemanticActionKey, "wrong-latest") {
		t.Fatalf("semantic key used unrelated latest evidence: %q", claimed.Binding.Run.SemanticActionKey)
	}
	if got := persistedClaimSource(t, svc, claimed.Binding.Run.ID); got != "run="+impl.Run.RunID+" evidence="+impl.Evidence.ID+" commit=abc123 identity=change-v1:abc123" {
		t.Fatalf("persisted source = %q", got)
	}
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
	var sourceRunID, sourceEvidenceID, sourceCommit, sourceIdentity string
	if err := svc.db.QueryRow(`
		SELECT COALESCE(source_run_id,''), COALESCE(source_evidence_id,''), COALESCE(source_commit_sha,''), COALESCE(source_identity,'')
		FROM workflow_runs WHERE id = ?
	`, runID).Scan(&sourceRunID, &sourceEvidenceID, &sourceCommit, &sourceIdentity); err != nil {
		t.Fatalf("query persisted claim source: %v", err)
	}
	return "run=" + sourceRunID + " evidence=" + sourceEvidenceID + " commit=" + sourceCommit + " identity=" + sourceIdentity
}
