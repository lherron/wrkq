package workrpc_test

import "testing"

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
