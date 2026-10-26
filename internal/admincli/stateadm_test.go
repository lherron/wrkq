package admincli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestStateCommandDBFlagOutranksWrkqDBPathEnv: a wrkqadm state command run
// with --db reads that database even when WRKQ_DB_PATH names another one.
func TestStateCommandDBFlagOutranksWrkqDBPathEnv(t *testing.T) {
	envDB, envPath := migratedDB(t, "env.db")
	seedProject(t, envDB, "env-project", "env-only-project")
	flagDB, flagPath := migratedDB(t, "flag.db")
	seedProject(t, flagDB, "flag-project", "flag-only-project")
	t.Setenv("WRKQ_DB_PATH", envPath)

	snap := filepath.Join(t.TempDir(), "snap.json")
	if out, err := runAdm(t, "state", "export", "--db", flagPath, "--out", snap); err != nil {
		t.Fatalf("wrkqadm state export: %v\n%s", err, out)
	}
	data, err := os.ReadFile(snap)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	if !strings.Contains(string(data), "flag-only-project") {
		t.Fatalf("snapshot of --db %s lacks its project:\n%s", flagPath, data)
	}
	if strings.Contains(string(data), "env-only-project") {
		t.Fatalf("snapshot was taken from WRKQ_DB_PATH %s, not --db", envPath)
	}
}

// TestStateImportDryRunReportsCountsAndWritesNothing: `wrkqadm state import
// --dry-run` validates a snapshot and reports what it would restore, and the
// target ledger is untouched afterwards.
func TestStateImportDryRunReportsCountsAndWritesNothing(t *testing.T) {
	source, sourcePath := migratedDB(t, "source.db")
	proj := seedProject(t, source, "proj-uuid", "proj")
	seedTask(t, source, "task-uuid", "", "task", proj)
	snap := filepath.Join(t.TempDir(), "snap.json")
	if out, err := runAdm(t, "state", "export", "--db", sourcePath, "--out", snap); err != nil {
		t.Fatalf("wrkqadm state export: %v\n%s", err, out)
	}

	target, targetPath := migratedDB(t, "target.db")
	ledger := func() string {
		t.Helper()
		var containers, tasks int
		if err := target.QueryRow(`SELECT (SELECT COUNT(*) FROM containers), (SELECT COUNT(*) FROM tasks)`).Scan(&containers, &tasks); err != nil {
			t.Fatal(err)
		}
		return fmt.Sprintf("containers=%d tasks=%d", containers, tasks)
	}
	before := ledger()

	out, err := runAdm(t, "state", "import", "--dry-run", "--json", "--from", snap, "--db", targetPath)
	if err != nil {
		t.Fatalf("wrkqadm state import --dry-run: %v\n%s", err, out)
	}
	var result struct {
		DryRun     bool `json:"dry_run"`
		Containers int  `json:"containers"`
		Tasks      int  `json:"tasks"`
	}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatalf("decode import result: %v\n%s", err, out)
	}
	if !result.DryRun || result.Containers != 2 || result.Tasks != 1 {
		t.Fatalf("dry-run result = %+v; want dry_run with the snapshot's 2 containers (root + project) and 1 task", result)
	}
	if after := ledger(); after != before {
		t.Fatalf("dry-run import wrote to the target: %s before, %s after", before, after)
	}
}

// TestStateVerifyAcceptsCanonicalExportAndRefusesNonCanonicalOne: `wrkqadm
// state verify` exits 0 on a canonical export and exits 4 on a snapshot whose
// bytes are not the canonical encoding (a --no-canonical export of the same
// ledger), as the command's help promises.
func TestStateVerifyAcceptsCanonicalExportAndRefusesNonCanonicalOne(t *testing.T) {
	source, sourcePath := migratedDB(t, "source.db")
	proj := seedProject(t, source, "proj-uuid", "proj")
	seedTask(t, source, "task-uuid", "", "task", proj)
	dir := t.TempDir()

	canonical := filepath.Join(dir, "canonical.json")
	out, err := runAdm(t, "state", "export", "--db", sourcePath, "--json", "--out", canonical)
	if err != nil {
		t.Fatalf("wrkqadm state export: %v\n%s", err, out)
	}
	var exported struct {
		SnapshotRev string `json:"snapshot_rev"`
	}
	if err := json.Unmarshal([]byte(out), &exported); err != nil || exported.SnapshotRev == "" {
		t.Fatalf("decode export result (%v):\n%s", err, out)
	}
	out, err = runAdm(t, "state", "verify", "--db", sourcePath, "--json", canonical)
	if err != nil {
		t.Fatalf("verify of a canonical export: exit %d (%v)\n%s", ExitCodeForError(err), err, out)
	}
	var verified struct {
		Valid       bool   `json:"valid"`
		SnapshotRev string `json:"snapshot_rev"`
	}
	if err := json.Unmarshal([]byte(out), &verified); err != nil {
		t.Fatalf("decode verify result: %v\n%s", err, out)
	}
	if !verified.Valid || verified.SnapshotRev != exported.SnapshotRev {
		t.Fatalf("verify = %+v; want valid and the export's snapshot_rev %s", verified, exported.SnapshotRev)
	}

	pretty := filepath.Join(dir, "pretty.json")
	if out, err := runAdm(t, "state", "export", "--db", sourcePath, "--no-canonical", "--out", pretty); err != nil {
		t.Fatalf("wrkqadm state export --no-canonical: %v\n%s", err, out)
	}
	out, err = runAdm(t, "state", "verify", "--db", sourcePath, pretty)
	if code := ExitCodeForError(err); code != 4 {
		t.Fatalf("verify of a non-canonical snapshot: exit %d, want 4\n%s", code, out)
	}
	if !strings.Contains(out, "not canonical") {
		t.Fatalf("verify of a non-canonical snapshot does not say why:\n%s", out)
	}
}
