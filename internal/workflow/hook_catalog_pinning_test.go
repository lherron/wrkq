//go:build wrkq_local

package workflow

import (
	"strings"
	"testing"
)

func TestHookExecutionRequiresConfiguredCatalogToMatchStoredTemplateLaw(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	body, err := builtinTemplateData(BuiltinSimpleTaskV2TemplateRef)
	if err != nil {
		t.Fatal(err)
	}
	tpl, canonical, err := ParseTemplate(body)
	if err != nil {
		t.Fatal(err)
	}
	stored := &HookCatalog{
		SchemaVersion: "wrkf.hook-catalog.v0",
		Hooks: map[string]HookSpec{
			"probe": {Kind: "exec", Argv: []string{"true"}},
		},
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:installer", stored, false); err != nil {
		t.Fatalf("install template with pinned catalog: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:attacher"); err != nil {
		t.Fatalf("attach: %v", err)
	}

	substitute := &HookCatalog{
		SchemaVersion: "wrkf.hook-catalog.v0",
		Hooks: map[string]HookSpec{
			"probe": {Kind: "exec", Argv: []string{"false"}},
		},
	}
	if _, err := svc.RunSingleHook(taskUUID, "triage_complete", "probe", "agent:runner", "coordinator", substitute, ""); err == nil || !strings.Contains(err.Error(), "hook catalog hash mismatch") {
		t.Fatalf("substitute catalog error = %v, want fail-closed hash mismatch", err)
	}

	run, err := svc.RunSingleHook(taskUUID, "triage_complete", "probe", "agent:runner", "coordinator", stored, "")
	if err != nil {
		t.Fatalf("matching deployed catalog: %v", err)
	}
	if run.Verdict != "pass" {
		t.Fatalf("matching pinned hook verdict=%q want pass", run.Verdict)
	}
}