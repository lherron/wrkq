//go:build wrkq_local

package workflow

import (
	"encoding/json"
	"testing"
)

func TestContextFreshnessStaleReprojectsVerify(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV3(t, svc, taskUUID)
	advanceAttachedV3ToAwaitingMergeAtContext(t, svc, taskUUID, "context-v1:a")
	addContextLineageForTest(t, svc, taskUUID, "context-v1:b")

	next, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext after context move: %v", err)
	}
	assertActionCandidates(t, next, "verify")
}

func TestContextFreshnessFreshProjectsLanding(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV3(t, svc, taskUUID)
	advanceAttachedV3ToAwaitingMergeAtContext(t, svc, taskUUID, "context-v1:a")

	next, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext with fresh verdict: %v", err)
	}
	assertActionCandidates(t, next, "landing")
}

func TestContextFreshnessPortableVerdictsProjectLanding(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV3WithPortableVerdicts(t, svc, taskUUID)
	advanceAttachedV3ToAwaitingMergeAtContext(t, svc, taskUUID, "context-v1:a")
	addContextLineageForTest(t, svc, taskUUID, "context-v1:b")

	next, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext with portable verdict: %v", err)
	}
	assertActionCandidates(t, next, "landing")
}

func TestContextFreshnessReverifyAfterMoveSettlesWithoutRefusal(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV3(t, svc, taskUUID)
	advanceAttachedV3ToAwaitingMergeAtContext(t, svc, taskUUID, "context-v1:a")
	addContextLineageForTest(t, svc, taskUUID, "context-v1:b")

	stale, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext stale verdict: %v", err)
	}
	assertActionCandidates(t, stale, "verify")
	reverify := claimActionForTest(t, svc, taskUUID, "verify")
	if reverify.Binding.Run.Source == nil {
		t.Fatalf("reprojected verify source = nil")
	}

	_, err = svc.SettleAction(SettleActionParams{
		ActionRunID:     reverify.Binding.Run.ID,
		OwnerToken:      reverify.Binding.Authority.OwnerToken,
		OwnerGeneration: reverify.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		TransitionMode:  TransitionSkip,
		Evidence: &ActionEvidenceInput{
			Summary: "verified after context move",
			Facts: prVerifiedFactsForSourceAtContext(
				reverify.Binding.Run.Source.SourceEvidenceID,
				reverify.Binding.Run.Source.SourceIdentity,
				"h0", "h1", "bar1", "https://example.test/pr/1", "context-v1:b",
			),
		},
	})
	if err != nil {
		t.Fatalf("settle reverify after context move: %v", err)
	}

	next, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext after fresh reverify: %v", err)
	}
	assertActionCandidates(t, next, "landing")
}

func advanceAttachedV3ToAwaitingMergeAtContext(t *testing.T, svc *Service, taskUUID, contextID string) {
	t.Helper()
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	implement := claimActionForTest(t, svc, taskUUID, "implement")
	settleClaimForTest(t, svc, implement, `{"result":"done","commit.sha":"h0","change.id":"change-v1:h0","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")
	verify := claimActionForTest(t, svc, taskUUID, "verify")
	settleClaimForTest(t, svc, verify, prVerifiedFactsAtContext(verify.Binding.Run.Source.SourceEvidenceID, verify.Binding.Run.Source.SourceIdentity, "h0", "bar0", "https://example.test/pr/1", contextID), "pr verified")
}

func addContextLineageForTest(t *testing.T, svc *Service, taskUUID, contextID string) *Evidence {
	t.Helper()
	evidence, err := svc.AddEvidence(AddEvidenceParams{
		TaskSelector: taskUUID,
		Kind:         "context_lineage",
		Ref:          "context-move:" + contextID,
		Summary:      "context moved",
		Facts:        `{"context.id":"` + contextID + `"}`,
		PrincipalRef: "agent:context",
		Role:         "system",
	})
	if err != nil {
		t.Fatalf("AddEvidence context lineage: %v", err)
	}
	return evidence
}

func attachSimpleTaskV3WithPortableVerdicts(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	data, err := builtinTemplateData(BuiltinSimpleTaskV3TemplateRef)
	if err != nil {
		t.Fatalf("builtinTemplateData(v3): %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal v3: %v", err)
	}
	doc["id"] = "portable-verdict-v3"
	action(doc, "landing")["verdictsPortableAcrossContextMoves"] = true
	canonical, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal portable v3 template: %v", err)
	}
	tpl, canonical, err := ParseTemplate(canonical)
	if err != nil {
		t.Fatalf("ParseTemplate(portable v3): %v", err)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:t", nil, false); err != nil {
		t.Fatalf("install portable v3 template: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "portable-verdict-v3@3", "agent:t"); err != nil {
		t.Fatalf("AttachTask(portable v3): %v", err)
	}
}