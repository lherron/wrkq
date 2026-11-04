//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestReservedMetaCreateAndUpdate(t *testing.T) {
	ctx := context.Background()
	for _, key := range []string{"acp", "scribe", "wrkf.taskStateMirror", "hcs_scope", "deltaId", "unknown"} {
		for _, raw := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/raw=%t", key, raw), func(t *testing.T) {
				a := newAttributionAPI(t, "agent:seed")
				meta := map[string]any{key: nil}
				encoded, _ := json.Marshal(meta)
				requireReserved := func(err error) {
					t.Helper()
					if err == nil || claimErrorCode(t, err) != "WRKQ_META_KEY_RESERVED" || !strings.Contains(err.Error(), "wrkq ext put") || !strings.Contains(err.Error(), "meta."+key) {
						t.Fatalf("reserved key: %v", err)
					}
				}
				create := TaskCreateParams{Title: "foreign"}
				if raw {
					create.MetaRaw = string(encoded)
				} else {
					create.Meta = meta
				}
				_, err := a.TaskCreate(ctx, create)
				requireReserved(err)
				var tasks int
				if err := a.db.QueryRow("SELECT count(*) FROM tasks").Scan(&tasks); err != nil || tasks != 0 {
					t.Fatalf("failed create left %d tasks: %v", tasks, err)
				}
				task, err := a.TaskCreate(ctx, TaskCreateParams{Title: "clean"})
				if err != nil {
					t.Fatal(err)
				}
				for _, withExt := range []bool{false, true} {
					if withExt {
						if _, err := a.ExtPut(ctx, ExtPutParams{Subject: ExtSubject{Task: task.ID}, NS: "acp", Kind: "request", ID: "main", Body: map[string]any{"rev": 1}}); err != nil {
							t.Fatal(err)
						}
					}
					patch := TaskPatch{}
					if raw {
						text := string(encoded)
						patch.MetaRaw = &text
					} else {
						patch.Meta = &meta
					}
					_, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: patch})
					requireReserved(err)
					shown, err := a.TaskShow(ctx, TaskShowParams{Task: task.ID})
					if err != nil {
						t.Fatal(err)
					}
					if shown.ETag != task.ETag || shown.UpdatedAt != task.UpdatedAt {
						t.Fatal("refusal mutated task")
					}
				}
			})
		}
	}
}

func TestReservedMetaUnchangedLegacyAndWrkqCarry(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	ctx := context.Background()
	allowed := map[string]any{"workflow": map[string]any{"revision": 3, "state": map[string]any{"status": "active"}}, "_wrkq_archive_cascade": nil}
	task, err := a.TaskCreate(ctx, TaskCreateParams{Title: "workflow", Meta: allowed})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Meta: &allowed}}); err != nil {
		t.Fatalf("wrkq read-modify-write: %v", err)
	}
	// Seed pre-migration data through SQL; the public create/update paths must no longer admit it.
	legacy := map[string]any{"workflow": allowed["workflow"], "hcs_scope": "old"}
	encoded, _ := json.Marshal(legacy)
	if _, err = a.db.Exec("UPDATE tasks SET meta=? WHERE id=?", string(encoded), task.ID); err != nil {
		t.Fatal(err)
	}
	title := "unrelated edit"
	if _, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Title: &title}}); err != nil {
		t.Fatalf("title-only edit: %v", err)
	}
	if _, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Meta: &legacy}}); err == nil || claimErrorCode(t, err) != "WRKQ_META_KEY_RESERVED" {
		t.Fatalf("unchanged foreign key: %v", err)
	}
	for _, raw := range []string{`{}`, `null`, `{"workflow":{"revision":4},"_wrkq_archive_cascade":true}`} {
		if _, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{MetaRaw: &raw}}); err != nil {
			t.Fatalf("allowed %s: %v", raw, err)
		}
	}
}
