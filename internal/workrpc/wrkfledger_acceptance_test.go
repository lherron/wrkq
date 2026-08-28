//go:build wrkq_local

package workrpc_test

import (
	"strings"
	"testing"
)

func TestWrkfLedgerAppendAndListOverRealRPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	t.Setenv("WRKF_PRINCIPAL_REF", "agent:ledger-test")
	dbPath := migratedDB(t)
	taskID := p2SeedTask(t, dbPath, "e4660000-0000-4000-8000-000000000032", "ledger-rpc", "Ledger RPC")
	p3InstallAndAttach(t, dbPath, p2WorkflowTemplatePath(t), taskID)

	frames := p3Run(t, dbPath,
		mkRPC("forged", "wrkf.ledger.append", map[string]any{
			"taskId": taskID, "kind": "strike", "aboutPrincipalRef": "agent:larry",
			"body": map[string]any{"refs": map[string]any{"comments": []string{"C-09173"}}},
			// Caller data must not control immutable writtenBy attribution. Since
			// T-07647 an undeclared key is refused by name rather than dropped.
			"writtenBy": "agent:forged",
		}),
		mkRPC("append", "wrkf.ledger.append", map[string]any{
			"taskId": taskID, "kind": "strike", "aboutPrincipalRef": "agent:larry",
			"body": map[string]any{"refs": map[string]any{"comments": []string{"C-09173"}}},
		}),
		mkRPC("list", "wrkf.ledger.list", map[string]any{"taskId": taskID}),
	)
	if errObj, ok := frames[1]["error"].(map[string]any); !ok {
		t.Fatalf("forged writtenBy must be refused, got %#v", frames[1])
	} else if msg, _ := errObj["message"].(string); !strings.Contains(msg, `unknown field "writtenBy"`) {
		t.Fatalf("forged writtenBy refusal must name the field, got %q", msg)
	}
	frames = frames[1:]
	entry := p2ResultOrFail(t, frames[1], "ledger append")
	if got, _ := entry["instanceId"].(string); len(got) < 4 || got[:4] != "wfi_" {
		t.Fatalf("instanceId = %#v, want real wfi_* id", entry["instanceId"])
	}
	if got, _ := entry["writtenBy"].(string); got != "agent:ledger-test" {
		t.Fatalf("writtenBy = %#v, want explicit engine-stamped caller agent:ledger-test (not forged input)", entry["writtenBy"])
	}
	list := p2ResultOrFail(t, frames[2], "ledger list")
	entries, ok := list["entries"].([]any)
	if !ok || len(entries) != 1 {
		t.Fatalf("ledger list entries = %#v", list["entries"])
	}
}
