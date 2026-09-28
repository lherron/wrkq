package snapshot

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	wrkqdb "github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
)

// T-07498 regression fixtures: a snapshot is the complete task ledger, and a
// fresh `wrkqadm init`-style database restores it column for column.

func openLedgerDB(t *testing.T, name string) (*wrkqdb.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".db")
	database, err := wrkqdb.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database, path
}

// seedInitInbox mirrors `wrkqadm init`: an auto-numbered inbox under root.
func seedInitInbox(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `INSERT INTO containers (id, slug, title, parent_uuid, kind, created_by_principal_ref, updated_by_principal_ref)
		VALUES ('', 'inbox', 'Inbox', ?, 'project', 'agent:init', 'agent:init')`, domain.RootContainerUUID)
}

// seedFullLedger writes every closure shape the mapping pass found on the
// canonical ledger, plus every domain column the old entry structs dropped.
func seedFullLedger(t *testing.T, db *sql.DB) {
	t.Helper()
	root := domain.RootContainerUUID
	stmts := []string{
		// Root mutated (etag/updated_by) the way live traffic does.
		`UPDATE containers SET title = 'ledger root', description = 'root desc', updated_by_principal_ref = 'agent:cody' WHERE uuid = '` + root + `'`,
		// Project with every optional container column; archived child directory
		// holding a LIVE task and a LIVE child container.
		`INSERT INTO containers (uuid, id, slug, title, kind, description, parent_uuid, sort_index, webhook_urls, root, specification, labels, campaign_state,
			etag, created_at, updated_at, created_by_principal_ref, created_by_scope_ref, updated_by_principal_ref, updated_by_scope_ref)
		 VALUES ('c-proj', 'P-00003', 'proj', 'Proj', 'project', 'desc', '` + root + `', 4, '["https://x"]', '/repo', 'spec', '["b","a"]', 'active',
			7, '2025-01-01T00:00:00Z', '2025-01-02T00:00:00Z', 'agent:a', 'agent:a:project:p', 'agent:b', 'agent:b:project:p')`,
		`INSERT INTO containers (uuid, id, slug, title, kind, parent_uuid, archived_at, created_at, updated_at)
		 VALUES ('c-archived', 'P-00004', 'old', 'Old', 'directory', 'c-proj', '2025-02-01T00:00:00Z', '2025-01-01T00:00:00Z', '2025-02-01T00:00:00Z')`,
		`INSERT INTO containers (uuid, id, slug, title, kind, parent_uuid, created_at, updated_at)
		 VALUES ('c-live-child', 'P-00005', 'child', 'Child', 'feature', 'c-archived', '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`,
		// Live task under the archived container, with the full column set
		// (labels stored sorted: a label set's element order is not data).
		`INSERT INTO tasks (uuid, id, slug, title, kind, project_uuid, campaign_uuid, state, priority, assignee_principal_ref, labels, meta, outcome,
			description, specification, etag, created_at, updated_at, created_by_principal_ref, created_by_scope_ref, updated_by_principal_ref, updated_by_scope_ref,
			cp_project_id, cp_run_id, cp_session_id, cp_work_item_id, sdk_session_id, run_status,
			claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at, claim_token_hash, claim_generation)
		 VALUES ('t-live', 'T-00007', 'live', 'Live', 'bug', 'c-archived', 'c-proj', 'in_progress', 1, 'agent:clod', '["a","z"]', '{"k":1}', 'done-ish',
			'd', 's', 9, '2025-01-01T00:00:00Z', '2025-03-01T00:00:00Z', 'agent:a', 'agent:a:project:p', 'agent:b', 'agent:b:project:p',
			'cp', 'run', 'sess', 'wi', 'sdk', 'running',
			'agent:clod', 'agent:clod:project:wrkq', 'max3', '2025-03-01T00:00:00Z', 'hash', 3)`,
		// Archived task, deleted-state task, subtask of the archived task, and a
		// legacy hex-suffixed id that must not drive the task sequence.
		`INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, archived_at, created_at, updated_at)
		 VALUES ('t-archived', 'T-00006', 'arch', 'Arch', 'c-proj', 'archived', 3, '2025-02-01T00:00:00Z', '2025-01-01T00:00:00Z', '2025-02-01T00:00:00Z')`,
		`INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, deleted_at, deleted_by_principal_ref, deleted_by_scope_ref, created_at, updated_at)
		 VALUES ('t-deleted', 'T-00005', 'gone', 'Gone', 'c-proj', 'deleted', 3, '2025-02-01T00:00:00Z', 'agent:a', 'agent:a:project:p', '2025-01-01T00:00:00Z', '2025-02-01T00:00:00Z')`,
		`INSERT INTO tasks (uuid, id, slug, title, kind, project_uuid, parent_task_uuid, state, priority, created_at, updated_at)
		 VALUES ('t-sub', 'T-00008', 'sub', 'Sub', 'subtask', 'c-proj', 't-archived', 'open', 3, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`,
		`INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, created_at, updated_at)
		 VALUES ('t-legacy', 'T-1234567B', 't-1234567b', 'Legacy', 'c-proj', 'completed', 3, '2025-01-01T00:00:00Z', '2025-01-01T00:00:00Z')`,
		// Live comment on the archived task, a soft-deleted comment, a root comment.
		`INSERT INTO comments (uuid, id, task_uuid, kind, created_by_principal_ref, created_by_scope_ref, body, meta, etag, created_at, updated_at)
		 VALUES ('cm-live', 'C-00010', 't-archived', 'decision', 'agent:a', 'agent:a:project:p', 'on archived', '{"m":1}', 2, '2025-02-02T00:00:00Z', '2025-02-03T00:00:00Z')`,
		`INSERT INTO comments (uuid, id, task_uuid, created_by_principal_ref, body, created_at, deleted_at, deleted_by_principal_ref, deleted_by_scope_ref)
		 VALUES ('cm-deleted', 'C-00011', 't-live', 'agent:a', 'gone', '2025-02-02T00:00:00Z', '2025-02-04T00:00:00Z', 'agent:b', 'agent:b:project:p')`,
		`INSERT INTO comments (uuid, id, container_uuid, created_by_principal_ref, body, created_at)
		 VALUES ('cm-root', 'C-00012', '` + root + `', 'agent:a', 'on root', '2025-02-02T00:00:00Z')`,
		// Relations, including one into an archived task.
		`INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, meta, created_at, created_by_principal_ref, created_by_scope_ref)
		 VALUES ('t-live', 't-archived', 'blocks', '{"why":"x"}', '2025-01-05T00:00:00Z', 'agent:a', 'agent:a:project:p')`,
		`INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_at) VALUES ('t-sub', 't-live', 'relates_to', '2025-01-05T00:00:00Z')`,
		// A promise on the archived task.
		`INSERT INTO promises (uuid, id, owner_principal_ref, subject, subject_task_uuid, review_at, created_by_principal_ref, updated_by_principal_ref)
		 VALUES ('pr-1', 'PR-00002', 'agent:a', 'watch', 't-archived', '2099-01-01T00:00:00Z', 'agent:a', 'agent:a')`,
		// High-water marks above the surviving ids (newest rows purged).
		`UPDATE sqlite_sequence SET seq = 50 WHERE name = 'task_seq'`,
		`INSERT INTO sqlite_sequence (name, seq) SELECT 'task_seq', 50 WHERE NOT EXISTS (SELECT 1 FROM sqlite_sequence WHERE name = 'task_seq')`,
		`UPDATE comment_sequences SET value = 40 WHERE name = 'next_comment'`,
	}
	for _, stmt := range stmts {
		mustExec(t, db, stmt)
	}
}

var volatileMeta = regexp.MustCompile(`"(generated_at|snapshot_rev)":"[^"]*"`)

func exportBytes(t *testing.T, db *sql.DB) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "state.json")
	if _, err := Export(db, ExportOptions{OutputPath: path, Canonical: true}); err != nil {
		t.Fatalf("export: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return volatileMeta.ReplaceAll(data, []byte(`"$1":""`))
}

// assertTablesEqual compares every non-legacy column of every modelled row.
func assertTablesEqual(t *testing.T, srcPath, dstPath string) {
	t.Helper()
	conn, err := sql.Open("sqlite3", srcPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	conn.SetMaxOpenConns(1)
	if _, err := conn.Exec(`ATTACH DATABASE ? AS dst`, dstPath); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"containers", "tasks", "comments", "promises", "task_relations"} {
		var cols string
		if err := conn.QueryRow(`SELECT group_concat(name, ',') FROM pragma_table_info(?) WHERE name NOT LIKE '%actor_uuid'`, table).Scan(&cols); err != nil {
			t.Fatal(err)
		}
		for _, dir := range [][2]string{{"main", "dst"}, {"dst", "main"}} {
			rows, err := conn.Query(fmt.Sprintf(`SELECT %s FROM %s.%s EXCEPT SELECT %s FROM %s.%s`, cols, dir[0], table, cols, dir[1], table))
			if err != nil {
				t.Fatal(err)
			}
			var diffs []string
			for rows.Next() {
				values := make([]interface{}, len(strings.Split(cols, ",")))
				ptrs := make([]interface{}, len(values))
				for i := range values {
					ptrs[i] = &values[i]
				}
				if err := rows.Scan(ptrs...); err != nil {
					t.Fatal(err)
				}
				diffs = append(diffs, fmt.Sprintf("%v", values))
			}
			_ = rows.Close()
			if len(diffs) > 0 {
				t.Errorf("%s rows only in %s (cols %s):\n%s", table, dir[0], cols, strings.Join(diffs, "\n"))
			}
		}
	}
}

func TestSnapshotRoundTripsFullLedgerIntoInitDatabase(t *testing.T) {
	source, sourcePath := openLedgerDB(t, "source")
	seedFullLedger(t, source.DB)

	path := filepath.Join(t.TempDir(), "state.json")
	result, err := Export(source.DB, ExportOptions{OutputPath: path, Canonical: true})
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if result.ContainerCount != 4 || result.TaskCount != 5 || result.CommentCount != 3 || result.LinkCount != 2 || result.PromiseCount != 1 {
		t.Fatalf("export counts = %+v, want every row including archived/deleted", result)
	}

	target, targetPath := openLedgerDB(t, "target")
	seedInitInbox(t, target.DB)
	if _, err := Import(target.DB, ImportOptions{InputPath: path}); err != nil {
		t.Fatalf("import into init database: %v", err)
	}

	// Column-for-column: comment inserts must not have bumped task/container
	// etag/updated_at, and the root is restored in place.
	assertTablesEqual(t, sourcePath, targetPath)
	if got, want := string(exportBytes(t, target.DB)), string(exportBytes(t, source.DB)); got != want {
		t.Fatalf("re-export differs from source export:\n got %s\nwant %s", got, want)
	}

	var taskSeq, nextComment int64
	if err := target.QueryRow(`SELECT seq FROM sqlite_sequence WHERE name = 'task_seq'`).Scan(&taskSeq); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(`SELECT value FROM comment_sequences WHERE name = 'next_comment'`).Scan(&nextComment); err != nil {
		t.Fatal(err)
	}
	// 50 is the source high-water; T-1234567B must not read as 1234567.
	if taskSeq != 50 || nextComment != 40 {
		t.Fatalf("restored task_seq=%d next_comment=%d, want 50 and 40", taskSeq, nextComment)
	}
}

func TestSnapshotImportRefusesOccupiedAndCascadingTargets(t *testing.T) {
	source, _ := openLedgerDB(t, "source")
	seedFullLedger(t, source.DB)
	path := filepath.Join(t.TempDir(), "state.json")
	if _, err := Export(source.DB, ExportOptions{OutputPath: path, Canonical: true}); err != nil {
		t.Fatal(err)
	}

	if _, err := Import(source.DB, ImportOptions{InputPath: path}); err == nil || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("import over a populated ledger without --force: err = %v, want not-empty refusal", err)
	}

	// A room bound to a task sits outside the model and would be cascade-deleted.
	mustExec(t, source.DB, `INSERT INTO rooms (kind, task_uuid, opened_by_principal_ref, created_by_principal_ref, updated_by_principal_ref) VALUES ('task', 't-live', 'agent:a', 'agent:a', 'agent:a')`)
	_, err := Import(source.DB, ImportOptions{InputPath: path, Force: true})
	if err == nil || !strings.Contains(err.Error(), "rooms.task_uuid (1)") {
		t.Fatalf("--force with out-of-model dependents: err = %v, want rooms.task_uuid refusal", err)
	}
	if _, err := Import(source.DB, ImportOptions{InputPath: path, Force: true, AllowCascade: true}); err != nil {
		t.Fatalf("--force --allow-cascade: %v", err)
	}
}

// The trigger suspended while the root is restored must survive a failed
// import: DROP and CREATE share the import transaction.
func TestSnapshotFailedImportKeepsRootTouchTrigger(t *testing.T) {
	source, _ := openLedgerDB(t, "source")
	seedFullLedger(t, source.DB)
	snap, _, err := ExportToSnapshot(source.DB, ExportOptions{Canonical: true})
	if err != nil {
		t.Fatal(err)
	}
	root := snap.Containers[domain.RootContainerUUID]
	root.UpdatedByPrincipalRef = "not-a-principal" // fails the CHECK inside the root UPDATE
	snap.Containers[domain.RootContainerUUID] = root
	data, err := CanonicalJSON(snap)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}

	target, _ := openLedgerDB(t, "target")
	if _, err := Import(target.DB, ImportOptions{InputPath: path}); err == nil || !strings.Contains(err.Error(), "restore root container") {
		t.Fatalf("import with invalid root: err = %v, want root restore failure", err)
	}
	var triggers, tasks int
	if err := target.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'trigger' AND name = 'containers_au_touch'`).Scan(&triggers); err != nil {
		t.Fatal(err)
	}
	if err := target.QueryRow(`SELECT COUNT(*) FROM tasks`).Scan(&tasks); err != nil {
		t.Fatal(err)
	}
	if triggers != 1 || tasks != 0 {
		t.Fatalf("after failed import: containers_au_touch count=%d tasks=%d, want 1 and 0", triggers, tasks)
	}
}
