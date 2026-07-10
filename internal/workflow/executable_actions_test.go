package workflow

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuiltinSimpleTaskV2ExecutableActionsValidate(t *testing.T) {
	data, err := builtinTemplateData(BuiltinSimpleTaskV2TemplateRef)
	if err != nil {
		t.Fatalf("builtinTemplateData(v2): %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate(v2): %v", err)
	}
	if errs := ValidateTemplate(tpl, canonical, nil); len(errs) > 0 {
		t.Fatalf("ValidateTemplate(v2) errors: %v", errs)
	}
	if tpl.ID != "wrkq-simple-task" || tpl.Version != "2" {
		t.Fatalf("template ref = %s@%s, want wrkq-simple-task@2", tpl.ID, tpl.Version)
	}
	if len(tpl.ExecutableActions) != 3 {
		t.Fatalf("executableActions len = %d, want 3", len(tpl.ExecutableActions))
	}
	if got := tpl.ExecutableActions["verify"].SourceBinding.Action; got != "implement" {
		t.Fatalf("verify source action = %q, want implement", got)
	}
	assertV2EffectState(t, tpl, "triage_complete", "ready", "open")
	assertV2EffectState(t, tpl, "implement_complete", "blocked", "blocked")
	assertV2EffectState(t, tpl, "verify_complete", "verified", "completed")
	assertV2EffectState(t, tpl, "verify_complete", "failed", "blocked")
	assertV2EffectState(t, tpl, "verify_complete", "blocked", "blocked")

	v1Data, err := builtinTemplateData("wrkq-simple-task")
	if err != nil {
		t.Fatalf("builtinTemplateData(default): %v", err)
	}
	v1, _, err := ParseTemplate(v1Data)
	if err != nil {
		t.Fatalf("ParseTemplate(default): %v", err)
	}
	if v1.Version != "1" {
		t.Fatalf("default built-in version = %q, want 1", v1.Version)
	}
	if len(v1.ExecutableActions) != 0 {
		t.Fatalf("v1 executableActions len = %d, want 0", len(v1.ExecutableActions))
	}
}

func TestBuiltinSimpleTaskV3BatchLandingContract(t *testing.T) {
	data, err := builtinTemplateData(BuiltinSimpleTaskV3TemplateRef)
	if err != nil {
		t.Fatalf("builtinTemplateData(v3): %v", err)
	}
	tpl, canonical, err := ParseTemplate(data)
	if err != nil {
		t.Fatalf("ParseTemplate(v3): %v", err)
	}
	if errs := ValidateTemplate(tpl, canonical, nil); len(errs) > 0 {
		t.Fatalf("ValidateTemplate(v3) errors: %v", errs)
	}
	if tpl.ID != "wrkq-simple-task" || tpl.Version != "3" {
		t.Fatalf("template ref = %s@%s, want wrkq-simple-task@3", tpl.ID, tpl.Version)
	}
	if len(tpl.ExecutableActions) != 4 {
		t.Fatalf("executableActions len = %d, want 4", len(tpl.ExecutableActions))
	}
	verify := tpl.ExecutableActions["verify"]
	if !containsString(verify.SideEffectClasses, "git.push") || !containsString(verify.SideEffectClasses, "github.pr.create") {
		t.Fatalf("v3 verify sideEffectClasses = %v, want git.push and github.pr.create", verify.SideEffectClasses)
	}
	landing := tpl.ExecutableActions["landing"]
	if landing.Role != "release_manager" || landing.ResultEvidenceKind != "landing_result" || landing.SourceBinding == nil {
		t.Fatalf("landing action = %+v", landing)
	}
	if landing.SourceBinding.Action != "verify" || landing.SourceBinding.BindFields == nil ||
		landing.SourceBinding.BindFields.CommitSha != "branch.head.sha" ||
		landing.SourceBinding.BindFields.ArtifactRef != "bar.hash" {
		t.Fatalf("landing source binding = %+v", landing.SourceBinding)
	}
	assertV2EffectState(t, tpl, "verify_complete", "verified", "completed")
	assertNoEffectState(t, tpl, "verify_complete", "pr_verified", "completed")
	assertV2EffectState(t, tpl, "landing_complete", "landed", "completed")
}

func assertV2EffectState(t *testing.T, tpl *Template, transitionID, outcomeID, wantState string) {
	t.Helper()
	for _, tr := range tpl.Transitions {
		if tr.ID != transitionID {
			continue
		}
		for _, outcome := range tr.Outcomes {
			if outcome.ID != outcomeID {
				continue
			}
			for _, effect := range outcome.Effects {
				if effect.Kind != "set_task_state" {
					continue
				}
				got, _ := effect.Data["state"].(string)
				if got != wantState {
					t.Fatalf("%s/%s set_task_state = %q, want %q", transitionID, outcomeID, got, wantState)
				}
				return
			}
			t.Fatalf("%s/%s missing set_task_state effect", transitionID, outcomeID)
		}
		t.Fatalf("%s missing outcome %s", transitionID, outcomeID)
	}
	t.Fatalf("missing transition %s", transitionID)
}

func assertNoEffectState(t *testing.T, tpl *Template, transitionID, outcomeID, unwantedState string) {
	t.Helper()
	for _, tr := range tpl.Transitions {
		if tr.ID != transitionID {
			continue
		}
		for _, outcome := range tr.Outcomes {
			if outcome.ID != outcomeID {
				continue
			}
			for _, effect := range outcome.Effects {
				if effect.Kind != "set_task_state" {
					continue
				}
				if got, _ := effect.Data["state"].(string); got == unwantedState {
					t.Fatalf("%s/%s unexpectedly sets task state %q", transitionID, outcomeID, unwantedState)
				}
			}
			return
		}
	}
	t.Fatalf("missing transition/outcome %s/%s", transitionID, outcomeID)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestValidateExecutableActionsRejectsMalformedReferences(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(map[string]any)
		wantErr string
	}{
		{
			name: "missing role",
			mutate: func(doc map[string]any) {
				action(doc, "triage")["role"] = ""
			},
			wantErr: "executable action triage role is required",
		},
		{
			name: "unknown transition",
			mutate: func(doc map[string]any) {
				action(doc, "triage")["transition"] = "missing_transition"
			},
			wantErr: "executable action triage references unknown transition missing_transition",
		},
		{
			name: "unknown evidence kind",
			mutate: func(doc map[string]any) {
				action(doc, "triage")["resultEvidenceKind"] = "missing_result"
			},
			wantErr: "executable action triage references unknown evidence kind missing_result",
		},
		{
			name: "role not allowed by transition",
			mutate: func(doc map[string]any) {
				action(doc, "implement")["role"] = "tester"
			},
			wantErr: "executable action implement role tester is not allowed by transition implement_complete",
		},
		{
			name: "continuation target missing",
			mutate: func(doc map[string]any) {
				cont := action(doc, "implement")["continuation"].(map[string]any)
				cont["next"] = "review"
			},
			wantErr: "executable action implement continuation targets missing executable action review",
		},
		{
			name: "verify source binding missing",
			mutate: func(doc map[string]any) {
				delete(action(doc, "verify"), "sourceBinding")
			},
			wantErr: "executable action verify sourceBinding is required",
		},
		{
			name: "source required fact impossible",
			mutate: func(doc map[string]any) {
				src := action(doc, "verify")["sourceBinding"].(map[string]any)
				src["requiredFacts"] = []any{"missing.fact"}
			},
			wantErr: "executable action verify sourceBinding required fact missing.fact is not declared on implement_result",
		},
		{
			name: "source identity bind fact impossible",
			mutate: func(doc map[string]any) {
				src := action(doc, "verify")["sourceBinding"].(map[string]any)
				src["bindFields"] = map[string]any{"sourceIdentity": "missing.fact"}
			},
			wantErr: "executable action verify sourceBinding bindFields.sourceIdentity fact missing.fact is not declared on implement_result",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			doc := builtinV2Doc(t)
			tc.mutate(doc)
			data, err := json.Marshal(doc)
			if err != nil {
				t.Fatalf("marshal mutated doc: %v", err)
			}
			tpl, canonical, err := ParseTemplate(data)
			if err != nil {
				t.Fatalf("ParseTemplate(mutated): %v", err)
			}
			errs := ValidateTemplate(tpl, canonical, nil)
			joined := strings.Join(errs, "\n")
			if !strings.Contains(joined, tc.wantErr) {
				t.Fatalf("ValidateTemplate errors = %v, want substring %q", errs, tc.wantErr)
			}
		})
	}
}

func builtinV2Doc(t *testing.T) map[string]any {
	t.Helper()
	data, err := builtinTemplateData(BuiltinSimpleTaskV2TemplateRef)
	if err != nil {
		t.Fatalf("builtinTemplateData(v2): %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("unmarshal v2: %v", err)
	}
	return doc
}

func action(doc map[string]any, id string) map[string]any {
	actions := doc["executableActions"].(map[string]any)
	return actions[id].(map[string]any)
}
