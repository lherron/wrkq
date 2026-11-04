package db

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMetaClosureMigrationPreservesWrkqValues(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "closure.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO containers(uuid,id,slug,title,parent_uuid,kind) VALUES('meta-project','P-88888','meta-test','Test','00000000-0000-4000-8000-000000000001','project')`); err != nil {
		t.Fatal(err)
	}
	cases := []struct{ id, before, after string }{
		{"T-88881", `{"workflow":{"revision":5,"items":[true,null,"x",1.25]},"scribe":{"slug":"old"}}`, `{"workflow":{"revision":5,"items":[true,null,"x",1.25]}}`},
		{"T-88882", `{"workflow":null,"_wrkq_archive_cascade":false,"wrkf.taskStateMirror":{"version":1}}`, `{"workflow":null,"_wrkq_archive_cascade":false}`},
		{"T-88883", `{"workflow":"text","_wrkq_archive_cascade":[1,2],"unknown":null}`, `{"workflow":"text","_wrkq_archive_cascade":[1,2]}`},
		{"T-88884", `{"workflow":123,"_wrkq_archive_cascade":true,"acp":{"request":{"rev":1}}}`, `{"workflow":123,"_wrkq_archive_cascade":true}`},
		{"T-88885", `{"hcs_scope":"old","dead":false}`, `{}`},
		{"T-88886", `{"workflow":{"revision":6},"_wrkq_archive_cascade":null}`, `{"workflow":{"revision":6},"_wrkq_archive_cascade":null}`},
		{"T-88887", `{}`, `{}`},
	}
	for _, c := range cases {
		if _, err := database.Exec(`INSERT INTO tasks(uuid,id,slug,title,project_uuid,state,meta) VALUES(?,?,?,?, 'meta-project','open',?)`, c.id, c.id, "task-"+c.id[2:], c.id, c.before); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := database.Exec(`INSERT INTO ext(uuid,task_uuid,ns,kind,id,body,created_at,updated_at) VALUES('meta-ext','T-88881','scribe','request','main','{"slug":"old"}','2026-11-03','2026-11-03')`); err != nil {
		t.Fatal(err)
	}
	type row struct {
		Meta    string
		Etag    int64
		Updated string
	}
	snapshot := func() map[string]row {
		t.Helper()
		rows, err := database.Query("SELECT id,meta,etag,updated_at FROM tasks ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = rows.Close() }()
		result := map[string]row{}
		for rows.Next() {
			var id string
			var r row
			if err := rows.Scan(&id, &r.Meta, &r.Etag, &r.Updated); err != nil {
				t.Fatal(err)
			}
			result[id] = r
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := snapshot()
	raw, err := migrationsFS.ReadFile("migrations/000073_task_meta_closure.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	after := snapshot()
	if len(before) != len(after) {
		t.Fatal("task rows changed")
	}
	for _, c := range cases {
		var got, want any
		if err := json.Unmarshal([]byte(after[c.id].Meta), &got); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(c.after), &want); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: got %s, want %s", c.id, after[c.id].Meta, c.after)
		}
		if c.before == c.after {
			if before[c.id] != after[c.id] {
				t.Fatalf("clean row changed: %s", c.id)
			}
		} else if after[c.id].Etag <= before[c.id].Etag {
			t.Fatalf("changed row skipped normal etag trigger: %s", c.id)
		}
	}
	if _, err := database.Exec(string(raw)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, snapshot()) {
		t.Fatal("strip is not idempotent")
	}
	var body string
	var etag int64
	if err := database.QueryRow("SELECT body,etag FROM ext WHERE uuid='meta-ext'").Scan(&body, &etag); err != nil || body != `{"slug":"old"}` || etag != 1 {
		t.Fatalf("extension changed: %s %d %v", body, etag, err)
	}
	var integrity string
	if err := database.QueryRow("PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
		t.Fatalf("integrity: %s %v", integrity, err)
	}
}
