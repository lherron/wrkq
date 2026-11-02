package db

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestWorkstreamMigrationPreservesSchemaAndRows(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "wrkq.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec("CREATE TABLE schema_migrations(version TEXT PRIMARY KEY,applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%SZ','now')))"); err != nil {
		t.Fatal(err)
	}
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if name >= "000072" {
			break
		}
		raw, err := migrationsFS.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if err := database.applyMigration(name, raw); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	snapshot := func() map[string]string {
		rows, err := database.Query("SELECT type||':'||name,sql FROM sqlite_master WHERE sql IS NOT NULL AND (type IN ('view','trigger') OR (type='index' AND tbl_name='tasks'))")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		result := map[string]string{}
		for rows.Next() {
			var name, sql string
			if err := rows.Scan(&name, &sql); err != nil {
				t.Fatal(err)
			}
			result[name] = sql
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	if _, err := database.Exec(`INSERT INTO containers(uuid,id,slug,title,parent_uuid,kind) VALUES('test-project','P-88888','migration-test','Test','00000000-0000-4000-8000-000000000001','project');
 INSERT INTO tasks(uuid,id,slug,title,project_uuid,state,kind,requester_principal_ref,requester_scope_ref,claim_generation) VALUES('test-owner','T-88888','owner','Owner','test-project','open','task','agent:seed','agent:seed:project:wrkq',3);
 INSERT INTO tasks(uuid,id,slug,title,project_uuid,state,kind,subtask_owner_uuid) VALUES('test-subtask','T-88888.check','check','Child','test-project','open','subtask','test-owner');
 INSERT INTO ext(uuid,task_uuid,ns,kind,id,body,created_at,updated_at) VALUES('test-ext','test-owner','acp','request','main','{"rev":1}','2026-11-02','2026-11-02');`); err != nil {
		t.Fatal(err)
	}
	raw, err := migrationsFS.ReadFile("migrations/000072_workstream.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := database.applyMigration("000072_workstream.sql", raw); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	if len(before) != len(after) {
		t.Fatalf("schema count %d -> %d", len(before), len(after))
	}
	for name, sql := range before {
		if after[name] != sql {
			t.Errorf("schema changed %s: %s -> %s", name, sql, after[name])
		}
	}
	var count, generation int
	var requester string
	if err := database.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&count); err != nil || count != 2 {
		t.Fatalf("rows %d %v", count, err)
	}
	if err := database.QueryRow("SELECT claim_generation,requester_scope_ref FROM tasks WHERE uuid='test-owner'").Scan(&generation, &requester); err != nil || generation != 3 || requester != "agent:seed:project:wrkq" {
		t.Fatalf("new fields lost: %d %s %v", generation, requester, err)
	}
	if _, err := database.Exec(`UPDATE tasks SET kind='workstream',covers='{"container":"P-88888"}' WHERE uuid='test-owner'`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE tasks SET kind='task' WHERE uuid='test-owner'`); err == nil {
		t.Fatal("covers accepted on task")
	}
	rows, err := database.Query("PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		t.Fatal("foreign keys broken")
	}
	var check string
	if err := database.QueryRow("PRAGMA quick_check").Scan(&check); err != nil || check != "ok" {
		t.Fatalf("quick_check: %s %v", check, err)
	}
}
