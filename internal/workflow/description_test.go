//go:build wrkq_local

package workflow

import (
	"strings"
	"testing"
)

// descriptionsTemplate is a minimal valid template that exercises optional
// description fields on a state, a transition, and an outcome.
const descriptionsTemplate = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "desc-fixture",
  "version": "1",
  "kind": "agent_first_workflow",
  "description": "Fixture exercising state, transition, and outcome descriptions.",
  "initial": { "status": "open", "phase": "intake" },
  "roles": { "agent": { "description": "Does the work." } },
  "states": [
    { "status": "open", "phase": "intake", "description": "Work has arrived but not started." },
    { "status": "closed", "phase": "done", "description": "Work is complete." }
  ],
  "transitions": [
    {
      "id": "finish",
      "description": "Complete the intake work and close it out.",
      "from": { "status": "open", "phase": "intake" },
      "by": ["agent"],
      "outcomes": [
        {
          "id": "finished",
          "description": "Everything is done; move to the closed state.",
          "when": { "always": true },
          "to": { "status": "closed", "phase": "done" }
        }
      ]
    }
  ]
}`

func TestParseTemplateDescriptions(t *testing.T) {
	tpl, canonical, err := ParseTemplate([]byte(descriptionsTemplate))
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}

	if errs := ValidateTemplate(tpl, canonical, nil); len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}

	if got := tpl.States[0].Description; got != "Work has arrived but not started." {
		t.Errorf("state description = %q", got)
	}
	if got := tpl.Transitions[0].Description; got != "Complete the intake work and close it out." {
		t.Errorf("transition description = %q", got)
	}
	if got := tpl.Transitions[0].Outcomes[0].Description; got != "Everything is done; move to the closed state." {
		t.Errorf("outcome description = %q", got)
	}

	// Descriptions must survive the canonical (hashed/served) representation so
	// `wrkf workflow show` and the RPC template DTOs render them.
	canon := string(canonical)
	for _, want := range []string{
		"Work has arrived but not started.",
		"Complete the intake work and close it out.",
		"Everything is done; move to the closed state.",
	} {
		if !strings.Contains(canon, want) {
			t.Errorf("canonical template missing description %q", want)
		}
	}
}

// TestParseTemplateWithoutDescriptions guards backward compatibility: templates
// that omit the new optional fields still parse and validate.
func TestParseTemplateWithoutDescriptions(t *testing.T) {
	const noDesc = `{
  "schemaVersion": "wrkf.workflow-template.v0",
  "id": "no-desc-fixture",
  "version": "1",
  "kind": "agent_first_workflow",
  "initial": { "status": "open", "phase": "intake" },
  "roles": { "agent": {} },
  "states": [
    { "status": "open", "phase": "intake" },
    { "status": "closed", "phase": "done" }
  ],
  "transitions": [
    {
      "id": "finish",
      "from": { "status": "open", "phase": "intake" },
      "by": ["agent"],
      "outcomes": [
        { "id": "finished", "when": { "always": true }, "to": { "status": "closed", "phase": "done" } }
      ]
    }
  ]
}`
	tpl, canonical, err := ParseTemplate([]byte(noDesc))
	if err != nil {
		t.Fatalf("ParseTemplate: %v", err)
	}
	if errs := ValidateTemplate(tpl, canonical, nil); len(errs) > 0 {
		t.Fatalf("unexpected validation errors: %v", errs)
	}
	if tpl.States[0].Description != "" || tpl.Transitions[0].Description != "" || tpl.Transitions[0].Outcomes[0].Description != "" {
		t.Errorf("expected empty descriptions, got state=%q transition=%q outcome=%q",
			tpl.States[0].Description, tpl.Transitions[0].Description, tpl.Transitions[0].Outcomes[0].Description)
	}
}