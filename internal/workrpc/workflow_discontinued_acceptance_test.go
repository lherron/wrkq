package workrpc_test

import (
	"testing"

	"github.com/lherron/wrkq/internal/workrpc"
)

func TestWorkflowDiscontinueRPCAndAttachOverrideContract(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "00000000-2222-4000-8000-000000000046", "discontinued-rpc", "Discontinued RPC")
	tplPath := p2WorkflowTemplatePath(t)
	frames := p3Run(t, dbPath,
		mkRPC("install", "wrkf.workflow.install", map[string]any{"body": templateBody(t, tplPath)}),
		mkRPC("discontinue", "wrkf.workflow.discontinue", map[string]any{
			"ref":           "wrkq-code-change@1",
			"principal_ref": "agent:curator",
		}),
		mkRPC("show", "wrkf.workflow.show", map[string]any{"ref": "wrkq-code-change@1"}),
		mkRPC("list", "wrkf.workflow.list", map[string]any{}),
		mkRPC("refused", "wrkq.workflow.attach", map[string]any{
			"task": taskID, "workflow": "wrkq-code-change@1",
		}),
		mkRPC("override", "wrkq.workflow.attach", map[string]any{
			"task": taskID, "workflow": "wrkq-code-change@1", "attachDiscontinued": true,
		}),
		mkRPC("reinstate", "wrkf.workflow.reinstate", map[string]any{"ref": "wrkq-code-change@1"}),
	)

	discontinued := p2ResultOrFail(t, frames[2], "wrkf.workflow.discontinue")
	if discontinued["discontinuedAt"] == "" || discontinued["discontinuedBy"] != "agent:curator" {
		t.Fatalf("discontinue result = %#v, want current marker row", discontinued)
	}
	shown := p2ResultOrFail(t, frames[3], "wrkf.workflow.show")
	if shown["discontinuedAt"] != discontinued["discontinuedAt"] || shown["discontinuedBy"] != "agent:curator" {
		t.Fatalf("show result = %#v, want discontinued metadata", shown)
	}
	listed := p2ResultOrFail(t, frames[4], "wrkf.workflow.list")
	templates, _ := listed["templates"].([]any)
	if len(templates) != 1 {
		t.Fatalf("list result = %#v, want one template", listed)
	}
	summary, _ := templates[0].(map[string]any)
	if summary["discontinuedAt"] != discontinued["discontinuedAt"] || summary["discontinuedBy"] != "agent:curator" {
		t.Fatalf("template summary = %#v, want discontinued metadata", summary)
	}
	if got := p2ErrCode(frames[5]); got != "WRKF_VALIDATION" {
		t.Fatalf("non-override attach code = %q, want WRKF_VALIDATION; frame=%#v", got, frames[5])
	}
	if got := p2ErrDataField(frames[5], "field"); got != "workflow" {
		t.Fatalf("non-override attach field = %#v, want workflow", got)
	}
	p2ResultOrFail(t, frames[6], "wrkq.workflow.attach override")
	reinstated := p2ResultOrFail(t, frames[7], "wrkf.workflow.reinstate")
	if _, ok := reinstated["discontinuedAt"]; ok {
		t.Fatalf("reinstate result retained discontinuedAt: %#v", reinstated)
	}
	if _, ok := reinstated["discontinuedBy"]; ok {
		t.Fatalf("reinstate result retained discontinuedBy: %#v", reinstated)
	}
}

func TestWorkflowDiscontinueMethodsAreInRegistry(t *testing.T) {
	registered := map[string]bool{}
	for _, method := range workrpc.NewRegistry(nil, workrpc.RegistryOptions{}).RegisteredMethods() {
		registered[method] = true
	}
	for _, method := range []string{"wrkf.workflow.discontinue", "wrkf.workflow.reinstate"} {
		if !registered[method] {
			t.Errorf("registry missing %s", method)
		}
	}
	for _, forbidden := range []string{"wrkf.workflow.attach", "wrkf.task.attach"} {
		if registered[forbidden] {
			t.Errorf("registry must not split attach ownership with %s", forbidden)
		}
	}
}
