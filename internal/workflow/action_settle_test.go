package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func attachSimpleTaskV2WithGenericSettleContracts(t *testing.T, svc *Service, taskUUID string) {
	t.Helper()
	doc := builtinV2Doc(t)
	doc["id"] = "generic-settle-v2"
	implement := doc["evidenceKinds"].(map[string]any)["implement_result"].(map[string]any)
	implement["facts"].(map[string]any)["properties"].(map[string]any)["change.id"] = map[string]any{"type": "string"}
	verify := doc["evidenceKinds"].(map[string]any)["verify_result"].(map[string]any)
	verify["facts"].(map[string]any)["properties"].(map[string]any)["source.evidence_id"] = map[string]any{"type": "string"}
	verify["facts"].(map[string]any)["properties"].(map[string]any)["verified.change.id"] = map[string]any{"type": "string"}
	verifyAction := action(doc, "verify")
	verifyAction["sourceBinding"].(map[string]any)["bindFields"] = map[string]any{"sourceIdentity": "change.id"}
	verifyAction["settleValidation"] = map[string]any{
		"rules": []any{map[string]any{
			"identityFact":  "verified.change.id",
			"linkageFact":   "source.evidence_id",
			"requiredFacts": []any{"source.evidence_id", "source.commit.sha", "verified.change.id"},
			"echoFields": []any{
				map[string]any{"fact": "source.commit.sha", "sourceFact": "commit.sha"},
			},
		}},
	}

	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal generic-settle v2 template: %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate(generic-settle v2): %v", err)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:t", nil, false); err != nil {
		t.Fatalf("install generic-settle v2 template: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, "generic-settle-v2@2", "agent:t"); err != nil {
		t.Fatalf("AttachTask(generic-settle v2): %v", err)
	}
}

func TestSettleActionTemplateIdentityRejectsMismatch(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2WithGenericSettleContracts(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	implement := claimActionForTest(t, svc, taskUUID, "implement")
	impl := settleClaimForTest(t, svc, implement, `{"result":"done","commit.sha":"abc123","change.id":"change-v1:expected","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")
	verify := claimActionForTest(t, svc, taskUUID, "verify")
	if verify.Binding.Run.Source == nil || verify.Binding.Run.Source.SourceIdentity != "change-v1:expected" {
		t.Fatalf("verify source = %+v, want change-v1:expected", verify.Binding.Run.Source)
	}

	before := actionNextMutationCounts(t, svc)
	_, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     verify.Binding.Run.ID,
		OwnerToken:      verify.Binding.Authority.OwnerToken,
		OwnerGeneration: verify.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence: &ActionEvidenceInput{
			Summary: "wrong identity",
			Facts:   `{"result":"verified","source.commit.sha":"abc123","verified.commit.sha":"abc123","source.evidence_id":"` + impl.Evidence.ID + `","verified.change.id":"change-v1:foreign","git.clean":true}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wrong source identity error = %v, want identity validation", err)
	}
	if after := actionNextMutationCounts(t, svc); after != before {
		t.Fatalf("wrong source identity mutated rows: before=%+v after=%+v", before, after)
	}
}

func TestSettleActionTemplateContractsRejectMissingLinkageAndEcho(t *testing.T) {
	cases := []struct {
		name               string
		facts              func(string) string
		foreignSource      bool
		replaceClaimSource bool
		wantError          string
	}{
		{
			name: "missing required fact",
			facts: func(_ string) string {
				return `{"result":"verified","source.commit.sha":"abc123","verified.change.id":"change-v1:expected"}`
			},
			wantError: "required facts",
		},
		{
			name: "foreign wrong-kind linkage",
			facts: func(sourceEvidenceID string) string {
				return `{"result":"verified","source.evidence_id":"` + sourceEvidenceID + `","source.commit.sha":"abc123","verified.change.id":"change-v1:expected"}`
			},
			foreignSource: true,
			wantError:     "linkage",
		},
		{
			name: "wrong kind claimed source",
			facts: func(sourceEvidenceID string) string {
				return `{"result":"verified","source.evidence_id":"` + sourceEvidenceID + `","source.commit.sha":"abc123","verified.change.id":"change-v1:expected"}`
			},
			foreignSource:      true,
			replaceClaimSource: true,
			wantError:          "wrong declared kind",
		},
		{
			name: "echo mismatch",
			facts: func(sourceEvidenceID string) string {
				return `{"result":"verified","source.evidence_id":"` + sourceEvidenceID + `","source.commit.sha":"wrong","verified.change.id":"change-v1:expected"}`
			},
			wantError: "echo",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskUUID := actionFixture(t)
			attachSimpleTaskV2WithGenericSettleContracts(t, svc, taskUUID)
			triage := claimActionForTest(t, svc, taskUUID, "triage")
			settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
			implement := claimActionForTest(t, svc, taskUUID, "implement")
			impl := settleClaimForTest(t, svc, implement, `{"result":"done","commit.sha":"abc123","change.id":"change-v1:expected","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")
			verify := claimActionForTest(t, svc, taskUUID, "verify")
			sourceEvidenceID := impl.Evidence.ID
			if tc.foreignSource {
				foreign, err := svc.AddEvidence(AddEvidenceParams{
					TaskSelector: taskUUID,
					Kind:         "triage_result",
					Summary:      "wrong kind",
					Facts:        `{"result":"ready"}`,
					PrincipalRef: "agent:t",
					Role:         "triager",
				})
				if err != nil {
					t.Fatalf("AddEvidence foreign source: %v", err)
				}
				sourceEvidenceID = foreign.ID
				if tc.replaceClaimSource {
					if _, err := svc.db.Exec(`UPDATE workflow_runs SET source_evidence_id = ? WHERE id = ?`, foreign.ID, verify.Binding.Run.ID); err != nil {
						t.Fatalf("replace claim source: %v", err)
					}
				}
			}

			before := actionNextMutationCounts(t, svc)
			_, err := svc.SettleAction(SettleActionParams{
				ActionRunID:     verify.Binding.Run.ID,
				OwnerToken:      verify.Binding.Authority.OwnerToken,
				OwnerGeneration: verify.Binding.Authority.OwnerGeneration,
				Result:          "completed",
				Evidence:        &ActionEvidenceInput{Summary: tc.name, Facts: tc.facts(sourceEvidenceID)},
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("SettleAction error = %v, want %q", err, tc.wantError)
			}
			if after := actionNextMutationCounts(t, svc); after != before {
				t.Fatalf("invalid settlement mutated rows: before=%+v after=%+v", before, after)
			}
		})
	}
}

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

func TestSettleActionExpiredSuccessLeavesDowngradeLane(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")

	root := t.TempDir()
	impl, err := svc.ClaimAction(ClaimActionParams{
		Task:          taskUUID,
		RunnerID:      "runner-impl",
		AgentRef:      "agent:impl",
		Prefer:        ActionClaimPrefer{Action: "implement"},
		LeaseMs:       300000,
		WorkspaceRoot: root,
	})
	if err != nil {
		t.Fatalf("ClaimAction implement: %v", err)
	}
	expireActionRunForTest(t, svc, impl.Binding.Run.ID)
	expireWorkspaceLeaseForTest(t, svc, root)

	beforeEvents := countTable(t, svc, "workflow_events")
	_, err = svc.SettleAction(SettleActionParams{
		ActionRunID:         impl.Binding.Run.ID,
		OwnerToken:          impl.Binding.Authority.OwnerToken,
		OwnerGeneration:     impl.Binding.Authority.OwnerGeneration,
		WorkspaceToken:      impl.Binding.Workspace.LeaseToken,
		WorkspaceGeneration: impl.Binding.Workspace.OwnerGeneration,
		Result:              "completed",
		Evidence:            &ActionEvidenceInput{Summary: "implemented", Facts: `{"result":"done","commit.sha":"a75cfb1","change.id":"change-v1:a75cfb1","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`},
	})
	if err == nil || !strings.Contains(err.Error(), "lease conflict") {
		t.Fatalf("expired success settle error = %v, want lease conflict", err)
	}

	out, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     impl.Binding.Run.ID,
		OwnerToken:      impl.Binding.Authority.OwnerToken,
		OwnerGeneration: impl.Binding.Authority.OwnerGeneration,
		Result:          "operator_required",
		Evidence: &ActionEvidenceInput{
			Summary: "completed settle was refused after lease expiry; commit a75cfb1 requires resumed verification",
			Facts:   `{"reason":"completed settle refused after lease expiry","commit.sha":"a75cfb1"}`,
		},
	})
	if err != nil {
		t.Fatalf("downgrade settle after refused success: %v", err)
	}
	if out.Run.Status != "operator_required" || out.Evidence == nil || out.Evidence.Kind != "failure_result" {
		t.Fatalf("downgrade output = %+v evidence=%+v, want operator_required failure_result", out.Run, out.Evidence)
	}
	if out.Transition != nil || len(out.Effects) != 0 || len(out.Obligations) != 0 {
		t.Fatalf("downgrade settlement applied transition side effects: %+v", out)
	}
	if got := countTable(t, svc, "workflow_events"); got != beforeEvents {
		t.Fatalf("downgrade emitted transition events = %d, want %d", got, beforeEvents)
	}
	next, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext after downgrade: %v", err)
	}
	assertActionCandidates(t, next, "implement")
}

func TestSettleActionRecoveredInstanceRunsNextAction(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")
	settleClaimForTest(t, svc, impl, `{"result":"operator_required"}`, "needs operator")

	addOperatorResolution(t, svc, taskUUID, "resume_ready", "operator cleared side-effect ambiguity")
	transitionOperatorResolved(t, svc, taskUUID)
	next, err := svc.ActionNext(ActionNextParams{Task: taskUUID})
	if err != nil {
		t.Fatalf("ActionNext recovered: %v", err)
	}
	assertActionCandidates(t, next, "implement")

	recovered := claimActionForTest(t, svc, taskUUID, "implement")
	out := settleClaimForTest(t, svc, recovered, `{"result":"done","commit.sha":"a75cfb1","change.id":"change-v1:a75cfb1","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented after recovery")
	if out.Run.Status != "completed" || out.Transition == nil {
		t.Fatalf("recovered implement settle = %+v, transition=%+v", out.Run, out.Transition)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Status != "active" || inst.Phase != "implemented" {
		t.Fatalf("recovered instance state = %+v, want active/implemented", inst.State())
	}
}

func TestSettleActionOperatorResolutionReceiptsDoNotCollideWithNextActionEffects(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")
	settleClaimForTest(t, svc, impl, `{"result":"operator_required"}`, "needs operator")
	addOperatorResolution(t, svc, taskUUID, "resume_ready", "operator cleared side-effect ambiguity")
	transitionOperatorResolved(t, svc, taskUUID)

	recovered := claimActionForTest(t, svc, taskUUID, "implement")
	out := settleClaimForTest(t, svc, recovered, `{"result":"blocked"}`, "blocked after recovery")
	if len(out.Effects) != 1 || out.Effects[0].Kind != "set_task_state" || out.Effects[0].Status != "delivered" {
		t.Fatalf("recovered blocked effects = %+v, want delivered set_task_state", out.Effects)
	}

	effects, err := svc.ListEffects(taskUUID, true)
	if err != nil {
		t.Fatalf("ListEffects: %v", err)
	}
	var deliveredOpen, deliveredBlocked int
	seenSemanticKeys := map[string]bool{}
	for _, eff := range effects {
		if eff.Kind != "set_task_state" {
			continue
		}
		if seenSemanticKeys[eff.SemanticKey] {
			t.Fatalf("duplicate set_task_state semantic key after recovery: %q effects=%+v", eff.SemanticKey, effects)
		}
		seenSemanticKeys[eff.SemanticKey] = true
		if eff.Status != "delivered" || len(eff.Receipt) == 0 {
			t.Fatalf("set_task_state effect = %+v, want delivered with receipt", eff)
		}
		switch {
		case strings.HasSuffix(eff.SemanticKey, ":open"):
			deliveredOpen++
		case strings.HasSuffix(eff.SemanticKey, ":blocked"):
			deliveredBlocked++
		}
	}
	if deliveredOpen < 2 || deliveredBlocked != 1 {
		t.Fatalf("set_task_state effects open=%d blocked=%d all=%+v", deliveredOpen, deliveredBlocked, effects)
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
			Facts:   `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":false,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "value constraint") {
		t.Fatalf("dirty implement settlement error = %v, want declared value validation", err)
	}
	if after := actionNextMutationCounts(t, svc); after != before {
		t.Fatalf("dirty implement mutated rows: before=%+v after=%+v", before, after)
	}

	out := settleClaimForTest(t, svc, impl, `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")
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

func TestSettleActionVerifyRequiresBoundSourceIdentity(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	attachSimpleTaskV2(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")
	implemented := settleClaimForTest(t, svc, impl, `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")
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
	if verify.Binding.Run.Source == nil || verify.Binding.Run.Source.SourceIdentity != "change-v1:abc123" {
		t.Fatalf("verify claim source = %+v, want change-v1:abc123", verify.Binding.Run.Source)
	}
	before := actionNextMutationCounts(t, svc)
	_, err := svc.SettleAction(SettleActionParams{
		ActionRunID:     verify.Binding.Run.ID,
		OwnerToken:      verify.Binding.Authority.OwnerToken,
		OwnerGeneration: verify.Binding.Authority.OwnerGeneration,
		Result:          "completed",
		Evidence: &ActionEvidenceInput{
			Summary: "verified wrong latest",
			Facts:   `{"result":"verified","source.evidence_id":"` + implemented.Evidence.ID + `","source.commit.sha":"abc123","verified.commit.sha":"abc123","verified.change.id":"change-v1:wrong-latest","git.clean":true}`,
		},
	})
	if err == nil || !strings.Contains(err.Error(), "identity") {
		t.Fatalf("wrong source verify error = %v, want source identity validation", err)
	}
	if after := actionNextMutationCounts(t, svc); after != before {
		t.Fatalf("wrong source verify mutated rows: before=%+v after=%+v", before, after)
	}

	out := settleClaimForTest(t, svc, verify, `{"result":"verified","source.evidence_id":"`+implemented.Evidence.ID+`","source.commit.sha":"abc123","verified.commit.sha":"abc123","verified.change.id":"change-v1:abc123","git.clean":true}`, "verified")
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
		name   string
		result string
	}{
		{
			name:   "verify_failed",
			result: "failed",
		},
		{
			name:   "verify_blocked",
			result: "blocked",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, taskUUID := actionFixture(t)
			attachSimpleTaskV2(t, svc, taskUUID)
			triage := claimActionForTest(t, svc, taskUUID, "triage")
			settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
			impl := claimActionForTest(t, svc, taskUUID, "implement")
			implemented := settleClaimForTest(t, svc, impl, `{"result":"done","commit.sha":"abc123","change.id":"change-v1:abc123","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")

			verify := claimActionForTest(t, svc, taskUUID, "verify")
			facts := `{"result":"` + c.result + `","source.evidence_id":"` + implemented.Evidence.ID + `","source.commit.sha":"abc123","verified.commit.sha":"abc123","verified.change.id":"change-v1:abc123","git.clean":true}`
			settleClaimForTest(t, svc, verify, facts, c.name)
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

func TestSettleActionV3PRVerifiedAwaitingMergeQueue(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	advanceV3ToImplemented(t, svc, taskUUID, "h0")

	verify := claimActionForTest(t, svc, taskUUID, "verify")
	out := settleClaimForTest(t, svc, verify, prVerifiedFacts(verify.Binding.Run.Source.SourceEvidenceID, verify.Binding.Run.Source.SourceIdentity, "h0", "bar0", "https://example.test/pr/1"), "pr verified")
	if out.Transition == nil {
		t.Fatalf("pr_verified settlement missing transition")
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Status != "waiting" || inst.Phase != "awaiting_merge" {
		t.Fatalf("instance state = %+v, want waiting/awaiting_merge", inst.State())
	}
	if got := readTaskState(t, svc, taskUUID); got == "completed" {
		t.Fatalf("task state after pr_verified = %q, want not completed", got)
	}
	queue, err := svc.QueryEvents(EventQueryParams{ToPhase: "awaiting_merge"})
	if err != nil {
		t.Fatalf("QueryEvents awaiting_merge: %v", err)
	}
	if len(queue.Items) != 1 || queue.Items[0].Task.UUID != taskUUID || queue.Items[0].ToPhase != "awaiting_merge" {
		t.Fatalf("awaiting_merge queue = %+v, want task %s", queue.Items, taskUUID)
	}
}

func TestSettleActionV3LandingGateMatrix(t *testing.T) {
	cases := []struct {
		name        string
		clearSource bool
		mutate      func(map[string]interface{})
		wantError   string
	}{
		{
			name:        "missing_source_verify_evidence",
			clearSource: true,
			wantError:   "claimed source evidence",
		},
		{
			name: "missing_pr_sha_binding",
			mutate: func(f map[string]interface{}) {
				f["source.branch.head.sha"] = "wrong-h0"
			},
			wantError: "source evidence echo",
		},
		{
			name: "failed_bar_rerun",
			mutate: func(f map[string]interface{}) {
				f["frozen_bar.result"] = "failed"
			},
			wantError: "value constraint",
		},
		{
			name: "command_hash_drift",
			mutate: func(f map[string]interface{}) {
				f["frozen_bar.command_hash"] = "cmd-drift"
			},
			wantError: "value constraint",
		},
		{
			name: "content_hash_drift",
			mutate: func(f map[string]interface{}) {
				f["frozen_bar.content_hash"] = "content-drift"
			},
			wantError: "value constraint",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, taskUUID := actionFixture(t)
			verify := prepareV3AwaitingMerge(t, svc, taskUUID)
			landing := claimActionForTest(t, svc, taskUUID, "landing")
			if tc.clearSource {
				if _, err := svc.db.Exec(`UPDATE workflow_runs SET source_evidence_id = NULL WHERE id = ?`, landing.Binding.Run.ID); err != nil {
					t.Fatalf("clear landing source: %v", err)
				}
			}
			facts := validLandingFactsMap(verify.Evidence.ID)
			if tc.mutate != nil {
				tc.mutate(facts)
			}
			before := actionNextMutationCounts(t, svc)
			_, err := svc.SettleAction(SettleActionParams{
				ActionRunID:     landing.Binding.Run.ID,
				OwnerToken:      landing.Binding.Authority.OwnerToken,
				OwnerGeneration: landing.Binding.Authority.OwnerGeneration,
				Result:          "completed",
				Evidence:        &ActionEvidenceInput{Summary: "landing", Facts: mustJSON(t, facts)},
			})
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("SettleAction error = %v, want %q", err, tc.wantError)
			}
			if after := actionNextMutationCounts(t, svc); after != before {
				t.Fatalf("failed landing mutated rows: before=%+v after=%+v", before, after)
			}
			inst, err := svc.LatestInstance(taskUUID)
			if err != nil {
				t.Fatalf("LatestInstance: %v", err)
			}
			if inst.Status != "waiting" || inst.Phase != "awaiting_merge" {
				t.Fatalf("instance state after failed landing = %+v, want waiting/awaiting_merge", inst.State())
			}
			if got := readTaskState(t, svc, taskUUID); got == "completed" {
				t.Fatalf("task state after failed landing = %q, want not completed", got)
			}
		})
	}
}

func TestSettleActionV3LandingResultClosesWithEvidenceChain(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	verify := prepareV3AwaitingMerge(t, svc, taskUUID)
	landing := claimActionForTest(t, svc, taskUUID, "landing")
	out := settleClaimForTest(t, svc, landing, mustJSON(t, validLandingFactsMap(verify.Evidence.ID)), "landed")
	if out.Evidence == nil || out.Evidence.Kind != "landing_result" {
		t.Fatalf("landing evidence = %+v", out.Evidence)
	}
	inst, err := svc.LatestInstance(taskUUID)
	if err != nil {
		t.Fatalf("LatestInstance: %v", err)
	}
	if inst.Status != "closed" || inst.Phase != "done" {
		t.Fatalf("instance state = %+v, want closed/done", inst.State())
	}
	if got := readTaskState(t, svc, taskUUID); got != "completed" {
		t.Fatalf("task state after landing = %q, want completed", got)
	}
	ev, err := svc.ListEvidence(taskUUID)
	if err != nil {
		t.Fatalf("ListEvidence: %v", err)
	}
	for _, e := range ev {
		if e.Kind == "verify_result" && strings.Contains(string(e.Facts), `"verified.commit.sha":"h1"`) {
			t.Fatalf("rebased head H1 was recorded as exact-source verified: %+v", e)
		}
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

func advanceV3ToImplemented(t *testing.T, svc *Service, taskUUID, commit string) {
	t.Helper()
	attachSimpleTaskV3(t, svc, taskUUID)
	triage := claimActionForTest(t, svc, taskUUID, "triage")
	settleClaimForTest(t, svc, triage, `{"result":"ready"}`, "triaged")
	impl := claimActionForTest(t, svc, taskUUID, "implement")
	settleClaimForTest(t, svc, impl, `{"result":"done","commit.sha":"`+commit+`","change.id":"change-v1:`+commit+`","git.clean":true,"base.sha":"base000","postcondition":"git_committed_clean","repair.turns":0}`, "implemented")
}

func prepareV3AwaitingMerge(t *testing.T, svc *Service, taskUUID string) *SettleActionResult {
	t.Helper()
	advanceV3ToImplemented(t, svc, taskUUID, "h0")
	verify := claimActionForTest(t, svc, taskUUID, "verify")
	return settleClaimForTest(t, svc, verify, prVerifiedFacts(verify.Binding.Run.Source.SourceEvidenceID, verify.Binding.Run.Source.SourceIdentity, "h0", "bar0", "https://example.test/pr/1"), "pr verified")
}

func prVerifiedFacts(sourceEvidenceID, identity, head, bar, prURL string) string {
	return `{"result":"pr_verified","source.evidence_id":"` + sourceEvidenceID + `","source.commit.sha":"` + head + `","verified.commit.sha":"` + head + `","verified.change.id":"` + identity + `","branch.head.sha":"` + head + `","bar.hash":"` + bar + `","pr.url":"` + prURL + `","git.clean":true}`
}

func validLandingFactsMap(sourceEvidenceID string) map[string]interface{} {
	return map[string]interface{}{
		"result":                           "landed",
		"source.evidence_id":               sourceEvidenceID,
		"source.change.id":                 "change-v1:h0",
		"source.branch.head.sha":           "h0",
		"source.bar.hash":                  "bar0",
		"source.pr.url":                    "https://example.test/pr/1",
		"rebased.head.sha":                 "h1",
		"merged.main.sha":                  "main1",
		"pr.url":                           "https://example.test/pr/1",
		"rebase.range":                     "h0..h1",
		"frozen_bar.result":                "passed",
		"frozen_bar.command_hash":          "cmd0",
		"frozen_bar.expected_command_hash": "cmd0",
		"frozen_bar.content_hash":          "content0",
		"frozen_bar.expected_content_hash": "content0",
		"bar.changed":                      false,
	}
}

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return string(b)
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
