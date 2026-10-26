package admincli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// doctorJSON runs `wrkqadm doctor --json --db <dbPath>` (plus extra argv) and
// decodes the report, returning any non-JSON prefix so callers can reject it.
func doctorJSON(t *testing.T, dbPath string, extra ...string) (doctorReportAdm, map[string]checkResultAdm, string) {
	t.Helper()
	args := append([]string{"doctor", "--json", "--db", dbPath}, extra...)
	out, err := runAdm(t, args...)
	if err != nil {
		t.Fatalf("wrkqadm %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	start := strings.Index(out, "{")
	if start < 0 {
		t.Fatalf("wrkqadm %s printed no JSON report:\n%s", strings.Join(args, " "), out)
	}
	var report doctorReportAdm
	if err := json.Unmarshal([]byte(out[start:]), &report); err != nil {
		t.Fatalf("decode doctor report: %v\n%s", err, out)
	}
	checks := map[string]checkResultAdm{}
	for _, check := range report.Checks {
		// root_container may report twice; keep the worst status.
		if prev, ok := checks[check.Name]; ok && prev.Status != "ok" {
			continue
		}
		checks[check.Name] = check
	}
	return report, checks, out[:start]
}

// execNoFK runs one statement on a single connection with foreign keys off,
// so a seeded row can reference something that does not exist.
func execNoFK(t *testing.T, database *db.DB, query string, args ...interface{}) {
	t.Helper()
	ctx := context.Background()
	conn, err := database.Conn(ctx)
	if err != nil {
		t.Fatalf("conn: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
		t.Fatalf("foreign_keys off: %v", err)
	}
	defer func() { _, _ = conn.ExecContext(ctx, "PRAGMA foreign_keys = ON") }()
	if _, err := conn.ExecContext(ctx, query, args...); err != nil {
		t.Fatalf("seed %q: %v", query, err)
	}
}

// TestDoctorJSONFlagsEachSeededDefect drives `wrkqadm doctor --json` over a
// database seeded with one defect per row and asserts the check that names
// it, its status, and how it rolls up into overall_status.
func TestDoctorJSONFlagsEachSeededDefect(t *testing.T) {
	healthyAttachDir := func(t *testing.T) string {
		dir := filepath.Join(t.TempDir(), "attachments")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	cases := []struct {
		name string
		// seed returns the --db path and the WRKQ_ATTACH_DIR to run under.
		seed        func(t *testing.T) (dbPath, attachDir string)
		wantChecks  map[string]string
		wantOverall string
	}{
		{
			name: "healthy migrated database reports every check ok",
			seed: func(t *testing.T) (string, string) {
				_, path := migratedDB(t, "healthy.db")
				return path, healthyAttachDir(t)
			},
			wantChecks: map[string]string{
				"db_file_exists": "ok", "db_file_permissions": "ok",
				"wal_mode": "ok", "foreign_keys": "ok", "integrity_check": "ok",
				"schema_tables": "ok", "root_container": "ok",
				"orphaned_tasks": "ok", "orphaned_attachments": "ok", "duplicate_slugs": "ok",
				"sequence_drift": "ok", "attach_dir_exists": "ok", "orphaned_files": "ok",
			},
			wantOverall: "ok",
		},
		{
			name: "missing database file",
			seed: func(t *testing.T) (string, string) {
				return filepath.Join(t.TempDir(), "absent.db"), healthyAttachDir(t)
			},
			wantChecks:  map[string]string{"db_file_exists": "error"},
			wantOverall: "error",
		},
		{
			name: "read-only database file",
			seed: func(t *testing.T) (string, string) {
				if os.Geteuid() == 0 {
					t.Skip("permission check is not meaningful as root")
				}
				database, path := migratedDB(t, "readonly.db")
				_ = database.Close()
				if err := os.Chmod(path, 0o444); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(path, 0o644) })
				return path, healthyAttachDir(t)
			},
			wantChecks:  map[string]string{"db_file_exists": "ok", "db_file_permissions": "error"},
			wantOverall: "error",
		},
		{
			name: "unmigrated schema",
			seed: func(t *testing.T) (string, string) {
				path := filepath.Join(t.TempDir(), "unmigrated.db")
				database, err := db.Open(path)
				if err != nil {
					t.Fatal(err)
				}
				_ = database.Close()
				return path, healthyAttachDir(t)
			},
			wantChecks:  map[string]string{"db_file_exists": "ok", "schema_tables": "error"},
			wantOverall: "error",
		},
		{
			name: "task whose project does not exist",
			seed: func(t *testing.T) (string, string) {
				database, path := migratedDB(t, "orphan-task.db")
				execNoFK(t, database, `
					INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, created_by_principal_ref, updated_by_principal_ref)
					VALUES ('orphan-task', '', 'orphan', 'Orphan', 'no-such-project', 'open', 'agent:tester', 'agent:tester')`)
				return path, healthyAttachDir(t)
			},
			wantChecks:  map[string]string{"orphaned_tasks": "warning", "orphaned_attachments": "ok", "duplicate_slugs": "ok", "sequence_drift": "ok"},
			wantOverall: "warning",
		},
		{
			name: "attachment whose task does not exist",
			seed: func(t *testing.T) (string, string) {
				database, path := migratedDB(t, "orphan-attachment.db")
				execNoFK(t, database, `
					INSERT INTO attachments (task_uuid, filename, relative_path, mime_type, size_bytes)
					VALUES ('no-such-task', 'orphan.txt', 'tasks/no-such-task/orphan.txt', 'text/plain', 50)`)
				return path, healthyAttachDir(t)
			},
			wantChecks:  map[string]string{"orphaned_tasks": "ok", "orphaned_attachments": "warning"},
			wantOverall: "warning",
		},
		{
			name: "duplicate task slug in one project",
			seed: func(t *testing.T) (string, string) {
				database, path := migratedDB(t, "duplicate-slug.db")
				proj := seedProject(t, database, "proj-uuid", "proj")
				if _, err := database.Exec(`DROP INDEX tasks_unique_slug_in_container`); err != nil {
					t.Fatal(err)
				}
				seedTask(t, database, "dup-1", "", "duplicate", proj)
				seedTask(t, database, "dup-2", "", "duplicate", proj)
				return path, healthyAttachDir(t)
			},
			wantChecks:  map[string]string{"duplicate_slugs": "error", "orphaned_tasks": "ok", "sequence_drift": "ok"},
			wantOverall: "error",
		},
		{
			name: "attachment directory missing",
			seed: func(t *testing.T) (string, string) {
				_, path := migratedDB(t, "no-attach-dir.db")
				return path, filepath.Join(t.TempDir(), "missing-attachments")
			},
			wantChecks:  map[string]string{"attach_dir_exists": "error"},
			wantOverall: "error",
		},
		{
			name: "attachment directory for a task that does not exist",
			seed: func(t *testing.T) (string, string) {
				_, path := migratedDB(t, "orphan-dir.db")
				attachDir := healthyAttachDir(t)
				if err := os.MkdirAll(filepath.Join(attachDir, "tasks", "no-such-task-uuid"), 0o755); err != nil {
					t.Fatal(err)
				}
				return path, attachDir
			},
			wantChecks:  map[string]string{"attach_dir_exists": "ok", "orphaned_files": "warning"},
			wantOverall: "warning",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dbPath, attachDir := tc.seed(t)
			t.Setenv("WRKQ_ATTACH_DIR", attachDir)

			report, checks, _ := doctorJSON(t, dbPath)
			for name, want := range tc.wantChecks {
				got, ok := checks[name]
				if !ok {
					t.Errorf("check %s missing from report %+v", name, report.Checks)
					continue
				}
				if got.Status != want {
					t.Errorf("check %s = %s (%s); want %s", name, got.Status, got.Message, want)
				}
			}
			if report.OverallStatus != tc.wantOverall {
				t.Errorf("overall_status = %s (errors=%d warnings=%d); want %s; checks %+v", report.OverallStatus, report.Errors, report.Warnings, tc.wantOverall, report.Checks)
			}
			if tc.wantOverall == "ok" && (report.Errors != 0 || report.Warnings != 0) {
				t.Errorf("healthy report counted errors=%d warnings=%d", report.Errors, report.Warnings)
			}
		})
	}
}

// TestDoctorReportsAndFixesSequenceDriftAfterExplicitIDInsert: a task inserted
// with an explicit friendly id leaves task_seq behind the ids in use, so the
// next minted id would collide. doctor reports the drift, --fix repairs it,
// and the next doctor run is clean.
func TestDoctorReportsAndFixesSequenceDriftAfterExplicitIDInsert(t *testing.T) {
	database, dbPath := migratedDB(t, "drift.db")
	proj := seedProject(t, database, "proj-uuid", "proj")
	seedTask(t, database, "drift-task", "T-00042", "drift", proj)
	attachDir := filepath.Join(t.TempDir(), "attachments")
	if err := os.MkdirAll(attachDir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WRKQ_ATTACH_DIR", attachDir)

	_, checks, _ := doctorJSON(t, dbPath)
	drift := checks["sequence_drift"]
	if drift.Status != "error" || !strings.Contains(strings.Join(drift.Details, "\n"), "task_seq (table tasks): sqlite_sequence=0, max_id=42") {
		t.Fatalf("before --fix: sequence_drift = %+v; want error naming task_seq at 0 behind max_id 42", drift)
	}

	report, checks, prefix := doctorJSON(t, dbPath, "--fix")
	if prefix != "" {
		t.Errorf("doctor --fix --json stdout contains non-JSON prefix: %q", prefix)
	}
	if got := checks["sequence_drift"]; got.Status != "ok" || report.Errors != 0 {
		t.Errorf("fix report must reflect repaired state: %+v", report)
	}

	report, checks, _ = doctorJSON(t, dbPath)
	if got := checks["sequence_drift"]; got.Status != "ok" {
		t.Fatalf("after --fix: sequence_drift = %+v; want ok", got)
	}
	if report.OverallStatus != "ok" {
		t.Fatalf("after --fix: overall_status = %s; want ok (%+v)", report.OverallStatus, report.Checks)
	}
}
