package snapshot

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDeletedOwnerNamedSubtaskForceRoundTrip(t *testing.T) {
	d, _ := openLedgerDB(t, "named")
	seedFullLedger(t, d.DB)
	// Deliberately choose a subtask UUID sorting before its owner UUID.
	mustExec(t, d.DB, `INSERT INTO tasks(uuid,id,slug,title,kind,project_uuid,state,priority,subtask_owner_uuid) VALUES('a-named','T-00005.diagram','diagram','Diagram','task','c-proj','open',3,'t-deleted')`)
	before := exportBytes(t, d.DB)
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if _, err := Export(d.DB, ExportOptions{OutputPath: path, Canonical: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(d.DB, ImportOptions{InputPath: path, Force: true}); err != nil {
		t.Fatal(err)
	}
	after := exportBytes(t, d.DB)
	if !bytes.Equal(before, after) {
		if err := os.WriteFile(filepath.Join(t.TempDir(), "after.json"), after, 0600); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("snapshot changed:\nbefore=%s\nafter=%s", before, after)
	}
}
