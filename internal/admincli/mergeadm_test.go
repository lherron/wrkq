package admincli

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestMergeCommandRefusesLegacyActorMovementWithExit1 asserts that the legacy
// cross-database merge data mover is disabled under principal-only
// attribution: it relied on the actors table and *_actor_uuid remapping,
// which are no longer a write surface.
func TestMergeCommandRefusesLegacyActorMovementWithExit1(t *testing.T) {
	_, source := migratedDB(t, "source.db")
	_, dest := migratedDB(t, "dest.db")

	out, err := runAdm(t, "merge", "--source", source, "--dest", dest, "--project", "proj",
		"--source-attach-dir", filepath.Join(t.TempDir(), "src-att"), "--dest-attach-dir", filepath.Join(t.TempDir(), "dst-att"))
	if err == nil {
		t.Fatalf("wrkqadm merge succeeded; want the legacy-movement refusal\n%s", out)
	}
	if code := ExitCodeForError(err); code != 1 {
		t.Fatalf("exit code = %d; want 1 (err %v)", code, err)
	}
	if !strings.Contains(err.Error(), "legacy actor data movement is no longer supported") {
		t.Fatalf("err = %v; want it to name the retired legacy actor data movement", err)
	}
}
