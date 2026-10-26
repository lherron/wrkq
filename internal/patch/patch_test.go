package patch

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	wrkqdb "github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/snapshot"
)

const at = "2025-01-01T00:00:00Z"

func containerEntry(id, slug, parent string) snapshot.ContainerEntry {
	return snapshot.ContainerEntry{ID: id, Slug: slug, Title: slug, Kind: "project", ParentUUID: parent, CreatedByPrincipalRef: "agent:tester", UpdatedByPrincipalRef: "agent:tester", ETag: 1, CreatedAt: at, UpdatedAt: at}
}

func taskEntry(id, slug, projectUUID string) snapshot.TaskEntry {
	return snapshot.TaskEntry{ID: id, Slug: slug, Title: slug, ProjectUUID: projectUUID, State: "open", Kind: "task", Priority: 2, CreatedByPrincipalRef: "agent:tester", UpdatedByPrincipalRef: "agent:tester", ETag: 1, CreatedAt: at, UpdatedAt: at}
}

// taskValue is a task entry as a patch "add" value.
func taskValue(id, slug, projectUUID string) map[string]interface{} {
	return map[string]interface{}{
		"id": id, "slug": slug, "title": slug, "project_uuid": projectUUID, "state": "open", "kind": "task",
		"priority": float64(2), "created_by_principal_ref": "agent:tester", "updated_by_principal_ref": "agent:tester",
		"etag": float64(1), "created_at": at, "updated_at": at,
	}
}

func ledger(containers map[string]snapshot.ContainerEntry, tasks map[string]snapshot.TaskEntry) *snapshot.Snapshot {
	return &snapshot.Snapshot{
		Meta:       snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Containers: containers,
		Tasks:      tasks,
		Comments:   map[string]snapshot.CommentEntry{},
	}
}

func writeJSONFile(t *testing.T, dir, name string, v interface{}) string {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func savePatch(t *testing.T, dir string, p Patch) string {
	t.Helper()
	path := filepath.Join(dir, "patch.json")
	if err := p.Save(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func opsOf(p Patch) []string {
	ops := make([]string, len(p))
	for i, op := range p {
		ops[i] = op.Op + " " + op.Path
	}
	return ops
}

// TestPatchCreateWritesOneRFC6902OpPerChangedEntity drives Create (the
// engine of `wrkqadm patch create`: two snapshot files in, a patch file out)
// and reads the saved patch back.
func TestPatchCreateWritesOneRFC6902OpPerChangedEntity(t *testing.T) {
	proj := map[string]snapshot.ContainerEntry{"container-1": containerEntry("P-00001", "proj", "")}
	retitled := taskEntry("T-00001", "task", "container-1")
	retitled.Title, retitled.State, retitled.ETag, retitled.UpdatedAt = "New Title", "completed", 2, "2025-01-02T00:00:00Z"

	tests := []struct {
		name                        string
		base, target                *snapshot.Snapshot
		wantOps                     []string
		wantAdd, wantRepl, wantRemv int
	}{
		{
			name:    "added task",
			base:    ledger(proj, map[string]snapshot.TaskEntry{}),
			target:  ledger(proj, map[string]snapshot.TaskEntry{"task-1": taskEntry("T-00001", "new-task", "container-1")}),
			wantOps: []string{"add /tasks/task-1"}, wantAdd: 1,
		},
		{
			name:    "removed task",
			base:    ledger(proj, map[string]snapshot.TaskEntry{"task-1": taskEntry("T-00001", "old-task", "container-1")}),
			target:  ledger(proj, map[string]snapshot.TaskEntry{}),
			wantOps: []string{"remove /tasks/task-1"}, wantRemv: 1,
		},
		{
			name:    "changed task is one whole-entity replace",
			base:    ledger(proj, map[string]snapshot.TaskEntry{"task-1": taskEntry("T-00001", "task", "container-1")}),
			target:  ledger(proj, map[string]snapshot.TaskEntry{"task-1": retitled}),
			wantOps: []string{"replace /tasks/task-1"}, wantRepl: 1,
		},
		{
			name: "added container plus mixed task changes are counted per op",
			base: ledger(proj, map[string]snapshot.TaskEntry{
				"task-1": taskEntry("T-00001", "task", "container-1"),
				"task-2": taskEntry("T-00002", "gone", "container-1"),
			}),
			target: ledger(map[string]snapshot.ContainerEntry{
				"container-1": containerEntry("P-00001", "proj", ""),
				"container-2": containerEntry("P-00002", "other", ""),
			}, map[string]snapshot.TaskEntry{
				"task-1": retitled,
				"task-3": taskEntry("T-00003", "new", "container-1"),
			}),
			wantOps: []string{"add /containers/container-2", "replace /tasks/task-1", "remove /tasks/task-2", "add /tasks/task-3"},
			wantAdd: 2, wantRepl: 1, wantRemv: 1,
		},
		{
			name:    "identical snapshots write an empty patch",
			base:    ledger(proj, map[string]snapshot.TaskEntry{"task-1": taskEntry("T-00001", "task", "container-1")}),
			target:  ledger(proj, map[string]snapshot.TaskEntry{"task-1": taskEntry("T-00001", "task", "container-1")}),
			wantOps: []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "out", "change.patch")
			result, err := Create(CreateOptions{
				FromPath:   writeJSONFile(t, dir, "base.json", tt.base),
				ToPath:     writeJSONFile(t, dir, "target.json", tt.target),
				OutputPath: out,
			})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			saved, err := LoadPatch(out)
			if err != nil {
				t.Fatalf("load saved patch: %v", err)
			}
			got := opsOf(saved)
			if strings.Join(got, "\n") != strings.Join(tt.wantOps, "\n") {
				t.Fatalf("saved patch ops = %q; want %q", got, tt.wantOps)
			}
			if result.OpCount != len(tt.wantOps) || result.AddCount != tt.wantAdd || result.ReplaceCount != tt.wantRepl || result.RemoveCount != tt.wantRemv {
				t.Fatalf("counts = op %d add %d replace %d remove %d; want op %d add %d replace %d remove %d",
					result.OpCount, result.AddCount, result.ReplaceCount, result.RemoveCount,
					len(tt.wantOps), tt.wantAdd, tt.wantRepl, tt.wantRemv)
			}
		})
	}
}

// TestPatchApplyWritesAddsAndRemovesAndRefusesMissingTargets drives Apply (the
// engine of `wrkqadm patch apply`) against a real migrated database: an add
// lands a task, a remove deletes one, and a patch that targets a missing
// task or a stale base rev is refused with the database unchanged.
func TestPatchApplyWritesAddsAndRemovesAndRefusesMissingTargets(t *testing.T) {
	newLedgerDB := func(t *testing.T) *sql.DB {
		t.Helper()
		database, err := wrkqdb.Open(filepath.Join(t.TempDir(), "wrkq.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		if err := database.Migrate(); err != nil {
			t.Fatal(err)
		}
		for _, stmt := range []string{
			`INSERT INTO containers (uuid, id, slug, title, kind, parent_uuid, etag, created_at, updated_at, created_by_principal_ref, updated_by_principal_ref)
			 VALUES ('container-1', 'P-00001', 'proj', 'proj', 'project', '` + domain.RootContainerUUID + `', 1, '` + at + `', '` + at + `', 'agent:tester', 'agent:tester')`,
			`INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, etag, created_at, updated_at, created_by_principal_ref, updated_by_principal_ref)
			 VALUES ('task-old', 'T-00001', 'old', 'old', 'container-1', 'open', 2, 1, '` + at + `', '` + at + `', 'agent:tester', 'agent:tester')`,
		} {
			if _, err := database.Exec(stmt); err != nil {
				t.Fatalf("seed: %v", err)
			}
		}
		return database.DB
	}
	taskUUIDs := func(t *testing.T, db *sql.DB) string {
		t.Helper()
		var uuids sql.NullString
		if err := db.QueryRow(`SELECT group_concat(uuid, ',') FROM (SELECT uuid FROM tasks ORDER BY uuid)`).Scan(&uuids); err != nil {
			t.Fatal(err)
		}
		return uuids.String
	}

	tests := []struct {
		name      string
		patch     Patch
		ifMatch   string
		wantTasks string
		wantErr   string
	}{
		{"add lands the task", Patch{{Op: "add", Path: "/tasks/task-new", Value: taskValue("T-00002", "new", "container-1")}}, "", "task-new,task-old", ""},
		{"remove deletes the task", Patch{{Op: "remove", Path: "/tasks/task-old"}}, "", "", ""},
		{"remove of a missing task is refused", Patch{
			{Op: "add", Path: "/tasks/task-new", Value: taskValue("T-00002", "new", "container-1")},
			{Op: "remove", Path: "/tasks/nonexistent"},
		}, "", "task-old", "task not found: nonexistent"},
		{"stale base rev is refused", Patch{{Op: "add", Path: "/tasks/task-new", Value: taskValue("T-00002", "new", "container-1")}}, "sha256:stale", "task-old", "snapshot_rev mismatch: expected sha256:stale"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := newLedgerDB(t)
			result, err := Apply(db, ApplyOptions{PatchPath: savePatch(t, t.TempDir(), tt.patch), IfMatch: tt.ifMatch})
			if tt.wantErr == "" {
				if err != nil || !result.Applied {
					t.Fatalf("Apply: result %+v err %v; want applied", result, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Apply err = %v; want %q", err, tt.wantErr)
			}
			if got := taskUUIDs(t, db); got != tt.wantTasks {
				t.Fatalf("tasks after apply = %q; want %q", got, tt.wantTasks)
			}
		})
	}
}

// TestPatchValidateRejectsResultsThatBreakLedgerInvariants drives Validate
// (the engine of `wrkqadm patch validate`: base snapshot + patch) and asserts
// the named violation for each broken ledger invariant.
func TestPatchValidateRejectsResultsThatBreakLedgerInvariants(t *testing.T) {
	base := ledger(map[string]snapshot.ContainerEntry{
		"container-1": containerEntry("P-00001", "proj", ""),
		"container-2": containerEntry("P-00002", "child", "container-1"),
	}, map[string]snapshot.TaskEntry{"task-1": taskEntry("T-00001", "task", "container-1")})

	cycled := containerEntry("P-00001", "proj", "container-2")
	tests := []struct {
		name      string
		patch     Patch
		wantError string // empty: the result is valid
	}{
		{"valid result", Patch{{Op: "add", Path: "/tasks/task-2", Value: taskValue("T-00002", "other", "container-1")}}, ""},
		{"task in an unknown container", Patch{{Op: "add", Path: "/tasks/task-2", Value: taskValue("T-00002", "other", "nonexistent")}},
			"task task-2 references unknown container nonexistent"},
		{"duplicate task slug in one container", Patch{{Op: "add", Path: "/tasks/task-2", Value: taskValue("T-00002", "task", "container-1")}},
			"duplicate task slug 'task' in container container-1"},
		{"container parent cycle", Patch{{Op: "replace", Path: "/containers/container-1", Value: cycled}},
			"cycle detected in container hierarchy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			result, err := Validate(ValidateOptions{BasePath: writeJSONFile(t, dir, "base.json", base), PatchPath: savePatch(t, dir, tt.patch)})
			if err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if tt.wantError == "" {
				if !result.Valid || len(result.Errors) != 0 {
					t.Fatalf("valid result reported %+v", result)
				}
				return
			}
			if result.Valid || !strings.Contains(strings.Join(result.Errors, "\n"), tt.wantError) {
				t.Fatalf("result = %+v; want invalid naming %q", result, tt.wantError)
			}
		})
	}
}

// TestPatchRebaseRenumbersCollidingFriendlyIDs drives Rebase (the engine of
// `wrkqadm patch rebase`: patch + old base + new base in, rebased patch out).
func TestPatchRebaseRenumbersCollidingFriendlyIDs(t *testing.T) {
	proj := map[string]snapshot.ContainerEntry{"container-1": containerEntry("P-00001", "proj", "")}
	oldBase := ledger(proj, map[string]snapshot.TaskEntry{})
	newBaseWith := func(ids ...string) *snapshot.Snapshot {
		tasks := map[string]snapshot.TaskEntry{}
		for i, id := range ids {
			tasks["existing-"+string(rune('a'+i))] = taskEntry(id, "existing-"+string(rune('a'+i)), "container-1")
		}
		return ledger(proj, tasks)
	}
	add := func(uuid, id string) Operation {
		return Operation{Op: "add", Path: "/tasks/" + uuid, Value: taskValue(id, uuid, "container-1")}
	}

	tests := []struct {
		name      string
		newBase   *snapshot.Snapshot
		patch     Patch
		strictIDs bool
		// wantIDs maps each added task uuid to its friendly id in the rebased patch.
		wantIDs map[string]string
		// wantRewrites maps renumbered task uuids to "from->to".
		wantRewrites map[string]string
		wantErr      string
	}{
		{name: "no collision keeps the id", newBase: newBaseWith(), patch: Patch{add("task-new", "T-00001")},
			wantIDs: map[string]string{"task-new": "T-00001"}},
		{name: "collision takes the next id", newBase: newBaseWith("T-00001"), patch: Patch{add("task-new", "T-00001")},
			wantIDs: map[string]string{"task-new": "T-00002"}, wantRewrites: map[string]string{"task-new": "T-00001->T-00002"}},
		{name: "collision past a gap takes max plus one", newBase: newBaseWith("T-00001", "T-00005"), patch: Patch{add("task-new", "T-00001")},
			wantIDs: map[string]string{"task-new": "T-00006"}, wantRewrites: map[string]string{"task-new": "T-00001->T-00006"}},
		{name: "several new tasks renumber in uuid order", newBase: newBaseWith("T-00001"), patch: Patch{add("task-b", "T-00001"), add("task-a", "T-00001")},
			wantIDs:      map[string]string{"task-a": "T-00002", "task-b": "T-00003"},
			wantRewrites: map[string]string{"task-a": "T-00001->T-00002", "task-b": "T-00001->T-00003"}},
		{name: "malformed colliding id is refused with --strict-ids", newBase: newBaseWith("T-abc"), patch: Patch{add("task-new", "T-abc")}, strictIDs: true,
			wantErr: "task task-new: malformed friendly ID: T-abc"},
		{name: "malformed colliding id gets a -2 suffix without --strict-ids", newBase: newBaseWith("T-abc"), patch: Patch{add("task-new", "T-abc")},
			wantIDs: map[string]string{"task-new": "T-abc-2"}, wantRewrites: map[string]string{"task-new": "T-abc->T-abc-2"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "rebased.json")
			result, err := Rebase(RebaseOptions{
				PatchPath:   savePatch(t, dir, tt.patch),
				OldBasePath: writeJSONFile(t, dir, "old-base.json", oldBase),
				NewBasePath: writeJSONFile(t, dir, "new-base.json", tt.newBase),
				OutputPath:  out,
				StrictIDs:   tt.strictIDs,
			})
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Rebase err = %v; want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Rebase: %v", err)
			}

			gotRewrites := map[string]string{}
			for uuid, rw := range result.CodeRewrites["tasks"] {
				gotRewrites[uuid] = rw.From + "->" + rw.To
			}
			if len(gotRewrites) != len(tt.wantRewrites) {
				t.Fatalf("task rewrites = %v; want %v", gotRewrites, tt.wantRewrites)
			}
			for uuid, want := range tt.wantRewrites {
				if gotRewrites[uuid] != want {
					t.Fatalf("task rewrites = %v; want %v", gotRewrites, tt.wantRewrites)
				}
			}

			rebased, err := LoadPatch(out)
			if err != nil {
				t.Fatalf("load rebased patch: %v", err)
			}
			// The rebased patch carries only the patch's own changes: tasks the
			// new base gained since the old base are never removed.
			if len(rebased) != len(tt.wantIDs) || result.RemoveCount != 0 || result.ReplaceCount != 0 {
				t.Fatalf("rebased patch = %q; want only the %d patch adds", opsOf(rebased), len(tt.wantIDs))
			}
			adds := 0
			for _, op := range rebased {
				if op.Op != "add" {
					continue
				}
				adds++
				uuid := strings.TrimPrefix(op.Path, "/tasks/")
				value, _ := op.Value.(map[string]interface{})
				if want, ok := tt.wantIDs[uuid]; !ok || value["id"] != want {
					t.Fatalf("rebased add %s id=%v; want id %q", op.Path, value["id"], tt.wantIDs[uuid])
				}
			}
			if adds != len(tt.wantIDs) || result.AddCount != len(tt.wantIDs) {
				t.Fatalf("rebased patch has %d adds (AddCount %d); want %d: %q", adds, result.AddCount, len(tt.wantIDs), opsOf(rebased))
			}
		})
	}
}

// A patch that edits a task the new base has since deleted is a conflict,
// never a silent drop or a resurrection.
func TestPatchRebaseRefusesEditToTaskTheNewBaseDeleted(t *testing.T) {
	proj := map[string]snapshot.ContainerEntry{"container-1": containerEntry("P-00001", "proj", "")}
	oldBase := ledger(proj, map[string]snapshot.TaskEntry{"task-x": taskEntry("T-00001", "task-x", "container-1")})
	newBase := ledger(proj, map[string]snapshot.TaskEntry{})
	edited := taskValue("T-00001", "task-x", "container-1")
	edited["title"] = "edited"
	dir := t.TempDir()
	_, err := Rebase(RebaseOptions{
		PatchPath:   savePatch(t, dir, Patch{{Op: "replace", Path: "/tasks/task-x", Value: edited}}),
		OldBasePath: writeJSONFile(t, dir, "old-base.json", oldBase),
		NewBasePath: writeJSONFile(t, dir, "new-base.json", newBase),
		OutputPath:  filepath.Join(dir, "rebased.json"),
	})
	if err == nil || !strings.Contains(err.Error(), "patch conflicts with new base") {
		t.Fatalf("Rebase err = %v; want a conflict", err)
	}
}

// TestPatchSummarizeRendersEachFormat drives Summarize (the engine of
// `wrkqadm patch summarize`) over one patch per output format.
func TestPatchSummarizeRendersEachFormat(t *testing.T) {
	t.Run("text counts each entity's ops in one sentence", func(t *testing.T) {
		result, err := Summarize(SummarizeOptions{PatchPath: savePatch(t, t.TempDir(), Patch{
			{Op: "add", Path: "/tasks/task-1", Value: map[string]interface{}{"id": "T-00001", "slug": "task-one", "title": "Task One", "state": "open"}},
			{Op: "add", Path: "/tasks/task-2", Value: map[string]interface{}{"id": "T-00002", "slug": "task-two", "title": "Task Two", "state": "open"}},
			{Op: "replace", Path: "/containers/container-1", Value: map[string]interface{}{"id": "P-00001", "slug": "project", "title": "Updated Project"}},
		}), Format: "text"})
		if err != nil {
			t.Fatalf("summarize failed: %v", err)
		}
		if result.Counts.Tasks.Add != 2 || result.Counts.Containers.Replace != 1 {
			t.Errorf("counts = %+v; want 2 tasks added and 1 container replaced", result.Counts)
		}
		if result.Summary != "2 tasks added, 1 container updated." {
			t.Errorf("summary = %q", result.Summary)
		}
	})

	t.Run("markdown renders a row per op", func(t *testing.T) {
		result, err := Summarize(SummarizeOptions{PatchPath: savePatch(t, t.TempDir(), Patch{
			{Op: "add", Path: "/tasks/task-1", Value: map[string]interface{}{"id": "T-00001", "slug": "task-one", "title": "Task One"}},
		}), Format: "markdown"})
		if err != nil {
			t.Fatalf("summarize failed: %v", err)
		}
		if !strings.Contains(result.Summary, "| Entity | Op | ID | Path / Title |") {
			t.Errorf("expected markdown table header in %q", result.Summary)
		}
		if !strings.Contains(result.Summary, "| task | add | T-00001 |") {
			t.Errorf("expected task row in %q", result.Summary)
		}
	})

	t.Run("json details name the entity and op", func(t *testing.T) {
		result, err := Summarize(SummarizeOptions{PatchPath: savePatch(t, t.TempDir(), Patch{
			{Op: "remove", Path: "/comments/comment-1"},
		}), Format: "json"})
		if err != nil {
			t.Fatalf("summarize failed: %v", err)
		}
		if result.Counts.Comments.Remove != 1 {
			t.Errorf("expected 1 comment removed, got %d", result.Counts.Comments.Remove)
		}
		if len(result.Details) != 1 || result.Details[0].Entity != "comment" || result.Details[0].Op != "remove" {
			t.Fatalf("details = %+v; want one comment remove", result.Details)
		}
	})

	t.Run("empty patch reads No changes", func(t *testing.T) {
		result, err := Summarize(SummarizeOptions{PatchPath: savePatch(t, t.TempDir(), Patch{}), Format: "text"})
		if err != nil {
			t.Fatalf("summarize failed: %v", err)
		}
		if result.Summary != "No changes." {
			t.Errorf("expected 'No changes.', got '%s'", result.Summary)
		}
	})
}

func TestSummarize_WithBase(t *testing.T) {
	tmpDir := t.TempDir()

	// Create base snapshot with context
	base := &snapshot.Snapshot{
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "project", Title: "My Project"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "existing-task", Title: "Existing Task", ProjectUUID: "container-1"},
		},
	}

	basePath := filepath.Join(tmpDir, "base.json")
	baseData, _ := json.MarshalIndent(base, "", "  ")
	_ = os.WriteFile(basePath, baseData, 0644)

	// Patch updates the existing task
	p := Patch{
		{Op: "replace", Path: "/tasks/task-1/state", Value: "completed"},
	}

	patchPath := filepath.Join(tmpDir, "patch.json")
	_ = p.Save(patchPath)

	result, err := Summarize(SummarizeOptions{
		PatchPath: patchPath,
		BasePath:  basePath,
		Format:    "markdown",
	})

	if err != nil {
		t.Fatalf("summarize failed: %v", err)
	}

	// Should show ID from base
	found := false
	for _, d := range result.Details {
		if d.ID == "T-00001" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected to find T-00001 from base snapshot context")
	}
}

func TestSummarize_DeterministicOrder(t *testing.T) {
	tmpDir := t.TempDir()

	// Create patch with multiple operations that could be ordered differently
	p := Patch{
		{Op: "add", Path: "/tasks/zzz-task", Value: map[string]interface{}{"id": "T-00003"}},
		{Op: "add", Path: "/tasks/aaa-task", Value: map[string]interface{}{"id": "T-00001"}},
		{Op: "add", Path: "/containers/bbb-container", Value: map[string]interface{}{"id": "P-00001"}},
	}

	patchPath := filepath.Join(tmpDir, "patch.json")
	_ = p.Save(patchPath)

	// Run twice and compare
	result1, _ := Summarize(SummarizeOptions{PatchPath: patchPath, Format: "json"})
	result2, _ := Summarize(SummarizeOptions{PatchPath: patchPath, Format: "json"})

	if len(result1.Details) != len(result2.Details) {
		t.Fatal("detail counts differ between runs")
	}

	for i := range result1.Details {
		if result1.Details[i].UUID != result2.Details[i].UUID {
			t.Errorf("detail order differs at index %d", i)
		}
	}

	// Verify sorted order: containers before tasks, then by UUID
	if result1.Details[0].Entity != "container" {
		t.Error("expected containers to come before tasks (alphabetically)")
	}
	if result1.Details[1].UUID != "aaa-task" {
		t.Error("expected tasks sorted by UUID")
	}
}
