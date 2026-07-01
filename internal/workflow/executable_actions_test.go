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
