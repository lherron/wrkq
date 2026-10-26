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

// suspendOutcomeRPCTemplate is a minimal template with one suspend outcome
// (transition park → needs_operator) and a resume/close/cancel effect policy.
const suspendOutcomeRPCTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "suspend-outcome-test",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "active", "phase": "ready" },
  "roles": { "coordinator": {} },
  "states": [
    { "status": "active", "phase": "ready" },
    { "status": "closed", "outcome": "done" },
    { "status": "closed", "outcome": "cancelled" }
  ],
  "suspension": {
    "reasons": ["operator_required"],
    "effects": {
      "resume": [{ "kind": "resume_notice", "role": "coordinator" }],
      "close": [{ "kind": "set_task_state", "data": { "state": "completed" } }],
      "cancel": [{ "kind": "set_task_state", "data": { "state": "cancelled" } }]
    }
  },
  "transitions": [
    {
      "id": "park",
      "from": { "status": "active", "phase": "ready" },
      "by": ["coordinator"],
      "outcomes": [{
        "id": "needs_operator",
        "when": { "always": true },
        "suspend": { "reason": "operator_required" }
      }]
    },
    {
      "id": "complete",
      "from": { "status": "active", "phase": "ready" },
      "by": ["coordinator"],
      "outcomes": [{
        "id": "done",
        "when": { "always": true },
        "to": { "status": "closed", "outcome": "done" }
      }]
    }
  ]
}`

func TestWorkflowValidateRPCEnforcesSuspensionReasonReferences(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	validBody := templateBody(t, filepath.Join("internal", "workflow", "builtins", "wrkq-simple-task-v5.workflow.json"))
	suspendBody := func(old, replacement string) string {
		t.Helper()
		if !strings.Contains(suspendOutcomeRPCTemplate, old) {
			t.Fatalf("suspend outcome template lacks %q", old)
		}
		return strings.Replace(suspendOutcomeRPCTemplate, old, replacement, 1)
	}
	const suspendClause = `"suspend": { "reason": "operator_required" }`

	// Each row lists the EXACT validation errors (order and count).
	tests := []struct {
		name       string
		body       string // raw template body; empty means validBody after mutate
		mutate     func(map[string]any)
		wantValid  bool
		wantErrors []string
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
			wantErrors: []string{`suspension reason "never_used" is declared but not referenced by any outcome`},
		},
		{
			name: "duplicate declaration",
			mutate: func(document map[string]any) {
				suspensionReasons(document, "operator_required", "operator_required")
			},
			wantErrors: []string{`duplicate suspension reason "operator_required"`},
		},
		{
			name: "empty declaration",
			mutate: func(document map[string]any) {
				suspensionReasons(document, "operator_required", "")
			},
			wantErrors: []string{`suspension.reasons[1] must not be empty`},
		},
		{
			name: "referenced but undeclared",
			mutate: func(document map[string]any) {
				suspensionReasons(document, "another_reason")
			},
			wantErrors: []string{
				`transition test_complete outcome blocked suspend reason "operator_required" is not declared in suspension.reasons`,
				`transition test_complete outcome operator_required suspend reason "operator_required" is not declared in suspension.reasons`,
				`transition implement_complete outcome operator_required suspend reason "operator_required" is not declared in suspension.reasons`,
				`transition test_fix_complete outcome operator_required suspend reason "operator_required" is not declared in suspension.reasons`,
				`transition verify_complete outcome blocked suspend reason "operator_required" is not declared in suspension.reasons`,
				`transition verify_complete outcome other suspend reason "operator_required" is not declared in suspension.reasons`,
				`transition gate_complete outcome violation suspend reason "operator_required" is not declared in suspension.reasons`,
				`transition direct_land_complete outcome operator_required suspend reason "operator_required" is not declared in suspension.reasons`,
				`suspension reason "another_reason" is declared but not referenced by any outcome`,
			},
		},
		{
			name:      "minimal suspend outcome",
			body:      suspendOutcomeRPCTemplate,
			wantValid: true,
		},
		{
			name: "referenced but undeclared (minimal)",
			body: suspendBody(`"reason": "operator_required"`, `"reason": "unlisted_reason"`),
			wantErrors: []string{
				`transition park outcome needs_operator suspend reason "unlisted_reason" is not declared in suspension.reasons`,
				`suspension reason "operator_required" is declared but not referenced by any outcome`,
			},
		},
		{
			name: "outcome declares both to and suspend",
			body: suspendBody(suspendClause, `"to": { "status": "closed", "outcome": "done" }, `+suspendClause),
			wantErrors: []string{
				`transition park outcome needs_operator must declare exactly one of to or suspend`,
			},
		},
		{
			name: "outcome declares neither to nor suspend",
			body: suspendBody(suspendClause, `"description": "missing target"`),
			wantErrors: []string{
				`transition park outcome needs_operator must declare exactly one of to or suspend`,
				`suspension is declared but no outcome uses suspend`,
				`suspension reason "operator_required" is declared but not referenced by any outcome`,
			},
		},
		{
			name: "suspend from a closed state",
			body: suspendBody(`"from": { "status": "active", "phase": "ready" }`, `"from": { "status": "closed", "outcome": "done" }`),
			wantErrors: []string{
				`transition park outcome needs_operator cannot suspend from a closed state`,
			},
		},
		{
			name: "suspension declared but no outcome suspends",
			body: suspendBody(suspendClause, `"to": { "status": "closed", "outcome": "done" }`),
			wantErrors: []string{
				`suspension is declared but no outcome uses suspend`,
				`suspension reason "operator_required" is declared but not referenced by any outcome`,
			},
		},
		{
			name: "unknown disposition in suspension effects",
			body: suspendBody(`"resume": [{ "kind": "resume_notice", "role": "coordinator" }]`, `"retry": [{ "kind": "resume_notice", "role": "coordinator" }]`),
			wantErrors: []string{
				`suspension.effects contains unknown disposition "retry"`,
			},
		},
	}

	requests := make([]string, 0, len(tests))
	for _, tc := range tests {
		body := tc.body
		if body == "" {
			body = mutateTemplateBody(t, validBody, tc.mutate)
		}
		requests = append(requests, mkRPC(tc.name, "wrkf.workflow.validate", map[string]any{
			"body":       body,
			"sourceName": tc.name + ".workflow.json",
		}))
	}

	frames := p3Run(t, dbPath, requests...)
	for i, tc := range tests {
		result := p2ResultOrFail(t, frames[i+1], tc.name)
		if result["valid"] != tc.wantValid {
			t.Errorf("%s valid = %v, want %t; result=%#v", tc.name, result["valid"], tc.wantValid, result)
		}
		rawErrors, _ := result["errors"].([]any)
		errors := []string{}
		for _, raw := range rawErrors {
			if value, ok := raw.(string); ok {
				errors = append(errors, value)
			}
		}
		want := tc.wantErrors
		if want == nil {
			want = []string{}
		}
		if strings.Join(errors, "\n") != strings.Join(want, "\n") || len(errors) != len(want) {
			t.Errorf("%s errors = %q, want exactly %q", tc.name, errors, want)
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
