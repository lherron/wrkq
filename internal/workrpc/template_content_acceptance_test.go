//go:build wrkq_local

package workrpc_test

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowTemplateRPCUsesCallerContentAndRejectsPathVariant(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	tplPath := p2WorkflowTemplatePath(t)
	body := templateBody(t, tplPath)
	frames := p3Run(t, dbPath,
		mkRPC("validate", "wrkf.workflow.validate", map[string]any{"body": body, "sourceName": tplPath}),
		mkRPC("diff", "wrkf.workflow.diff", map[string]any{"oldBody": body, "newBody": body, "oldSourceName": tplPath, "newSourceName": tplPath}),
		mkRPC("install", "wrkf.workflow.install", map[string]any{"body": body, "sourceName": tplPath, "principal_ref": "agent:installer"}),
		mkRPC("path-deleted", "wrkf.workflow.install", map[string]any{"path": tplPath, "principal_ref": "agent:installer"}),
	)
	validated := p2ResultOrFail(t, frames[1], "content validate")
	if validated["valid"] != true {
		t.Fatalf("content validate result=%#v", validated)
	}
	diff := p2ResultOrFail(t, frames[2], "content diff")
	if diff["sameHash"] != true {
		t.Fatalf("content diff result=%#v", diff)
	}
	old, _ := diff["old"].(map[string]any)
	newer, _ := diff["new"].(map[string]any)
	if old["id"] != "wrkq-code-change" || newer["id"] != "wrkq-code-change" {
		t.Fatalf("content diff summaries=%#v", diff)
	}
	installed := p2ResultOrFail(t, frames[3], "content install")
	if installed["id"] != "wrkq-code-change" {
		t.Fatalf("content install result=%#v", installed)
	}
	if frames[4]["error"] == nil {
		t.Fatalf("deleted path variant unexpectedly succeeded: %#v", frames[4])
	}
}

func TestWorkflowValidateRPCEnforcesSuspensionReasonReferences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	validBody := templateBody(t, filepath.Join("internal", "workflow", "builtins", "wrkq-simple-task-v5.workflow.json"))

	tests := []struct {
		name      string
		mutate    func(map[string]any)
		wantValid bool
		wantError string
	}{
		{
			name:      "used declaration",
			wantValid: true,
		},
		{
			name: "unused declaration",
			mutate: func(document map[string]any) {
				suspensionReasons(document, "operator_required", "never_used")
			},
			wantError: `suspension reason "never_used" is declared but not referenced by any outcome`,
		},
		{
			name: "duplicate declaration",
			mutate: func(document map[string]any) {
				suspensionReasons(document, "operator_required", "operator_required")
			},
			wantError: `duplicate suspension reason "operator_required"`,
		},
		{
			name: "empty declaration",
			mutate: func(document map[string]any) {
				suspensionReasons(document, "operator_required", "")
			},
			wantError: `suspension.reasons[1] must not be empty`,
		},
		{
			name: "referenced but undeclared",
			mutate: func(document map[string]any) {
				suspensionReasons(document, "another_reason")
			},
			wantError: `suspend reason "operator_required" is not declared in suspension.reasons`,
		},
	}

	requests := make([]string, 0, len(tests))
	for _, tc := range tests {
		body := mutateTemplateBody(t, validBody, tc.mutate)
		requests = append(requests, mkRPC(tc.name, "wrkf.workflow.validate", map[string]any{
			"body":       body,
			"sourceName": tc.name + ".workflow.json",
		}))
	}

	frames := p3Run(t, dbPath, requests...)
	for i, tc := range tests {
		result := p2ResultOrFail(t, frames[i+1], tc.name)
		if result["valid"] != tc.wantValid {
			t.Fatalf("%s valid = %v, want %t; result=%#v", tc.name, result["valid"], tc.wantValid, result)
		}
		if tc.wantError == "" {
			continue
		}
		rawErrors, _ := result["errors"].([]any)
		var errors []string
		for _, raw := range rawErrors {
			if value, ok := raw.(string); ok {
				errors = append(errors, value)
			}
		}
		if !strings.Contains(strings.Join(errors, "\n"), tc.wantError) {
			t.Fatalf("%s errors = %v, want substring %q", tc.name, errors, tc.wantError)
		}
	}
}

func mutateTemplateBody(t *testing.T, body string, mutate func(map[string]any)) string {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal([]byte(body), &document); err != nil {
		t.Fatal(err)
	}
	if mutate != nil {
		mutate(document)
	}
	mutated, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return string(mutated)
}

func suspensionReasons(document map[string]any, reasons ...string) {
	values := make([]any, len(reasons))
	for i, reason := range reasons {
		values[i] = reason
	}
	document["suspension"].(map[string]any)["reasons"] = values
}