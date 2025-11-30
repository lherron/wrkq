package patch

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/snapshot"
)

func TestDiffSnapshots_AddTask(t *testing.T) {
	base := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks:    map[string]snapshot.TaskEntry{},
		Comments: map[string]snapshot.CommentEntry{},
	}

	target := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "new-task", Title: "New Task", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Comments: map[string]snapshot.CommentEntry{},
	}

	patch := DiffSnapshots(base, target)

	if len(patch) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(patch))
	}

	op := patch[0]
	if op.Op != "add" {
		t.Errorf("expected 'add' op, got '%s'", op.Op)
	}
	if op.Path != "/tasks/task-1" {
		t.Errorf("expected path '/tasks/task-1', got '%s'", op.Path)
	}
}

func TestDiffSnapshots_RemoveTask(t *testing.T) {
	base := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "old-task", Title: "Old Task", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Comments: map[string]snapshot.CommentEntry{},
	}

	target := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks:    map[string]snapshot.TaskEntry{},
		Comments: map[string]snapshot.CommentEntry{},
	}

	patch := DiffSnapshots(base, target)

	if len(patch) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(patch))
	}

	op := patch[0]
	if op.Op != "remove" {
		t.Errorf("expected 'remove' op, got '%s'", op.Op)
	}
	if op.Path != "/tasks/task-1" {
		t.Errorf("expected path '/tasks/task-1', got '%s'", op.Path)
	}
}

func TestDiffSnapshots_ReplaceTask(t *testing.T) {
	base := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "task", Title: "Old Title", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Comments: map[string]snapshot.CommentEntry{},
	}

	target := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "task", Title: "New Title", ProjectUUID: "container-1", State: "completed", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 2, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-02T00:00:00Z"},
		},
		Comments: map[string]snapshot.CommentEntry{},
	}

	patch := DiffSnapshots(base, target)

	if len(patch) != 1 {
		t.Fatalf("expected 1 operation, got %d", len(patch))
	}

	op := patch[0]
	if op.Op != "replace" {
		t.Errorf("expected 'replace' op, got '%s'", op.Op)
	}
	if op.Path != "/tasks/task-1" {
		t.Errorf("expected path '/tasks/task-1', got '%s'", op.Path)
	}
}

func TestDiffSnapshots_NoChanges(t *testing.T) {
	snap := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{},
		Tasks:      map[string]snapshot.TaskEntry{},
		Comments:   map[string]snapshot.CommentEntry{},
	}

	patch := DiffSnapshots(snap, snap)

	if len(patch) != 0 {
		t.Errorf("expected 0 operations for identical snapshots, got %d", len(patch))
	}
}

func TestApplyToSnapshot_Add(t *testing.T) {
	base := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks:    map[string]snapshot.TaskEntry{},
		Comments: map[string]snapshot.CommentEntry{},
	}

	p := Patch{
		{
			Op:   "add",
			Path: "/tasks/task-1",
			Value: map[string]interface{}{
				"id":           "T-00001",
				"slug":         "new-task",
				"title":        "New Task",
				"project_uuid": "container-1",
				"state":        "open",
				"priority":     float64(2),
				"created_by":   "actor-1",
				"updated_by":   "actor-1",
				"etag":         float64(1),
				"created_at":   "2025-01-01T00:00:00Z",
				"updated_at":   "2025-01-01T00:00:00Z",
			},
		},
	}

	result, err := ApplyToSnapshot(base, p)
	if err != nil {
		t.Fatalf("failed to apply patch: %v", err)
	}

	if len(result.Tasks) != 1 {
		t.Errorf("expected 1 task, got %d", len(result.Tasks))
	}

	task, ok := result.Tasks["task-1"]
	if !ok {
		t.Fatal("task-1 not found in result")
	}

	if task.Title != "New Task" {
		t.Errorf("expected title 'New Task', got '%s'", task.Title)
	}
}

func TestApplyToSnapshot_Remove(t *testing.T) {
	base := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "task", Title: "Task", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Comments: map[string]snapshot.CommentEntry{},
	}

	p := Patch{
		{Op: "remove", Path: "/tasks/task-1"},
	}

	result, err := ApplyToSnapshot(base, p)
	if err != nil {
		t.Fatalf("failed to apply patch: %v", err)
	}

	if len(result.Tasks) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(result.Tasks))
	}
}

func TestApplyToSnapshot_RemoveNotFound(t *testing.T) {
	base := &snapshot.Snapshot{
		Meta:       snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors:     map[string]snapshot.ActorEntry{},
		Containers: map[string]snapshot.ContainerEntry{},
		Tasks:      map[string]snapshot.TaskEntry{},
		Comments:   map[string]snapshot.CommentEntry{},
	}

	p := Patch{
		{Op: "remove", Path: "/tasks/nonexistent"},
	}

	_, err := ApplyToSnapshot(base, p)
	if err == nil {
		t.Fatal("expected error for removing nonexistent task")
	}
}

func TestValidateSnapshot_Valid(t *testing.T) {
	snap := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "task", Title: "Task", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Comments: map[string]snapshot.CommentEntry{
			"comment-1": {ID: "C-00001", TaskUUID: "task-1", ActorUUID: "actor-1", Body: "test", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z"},
		},
	}

	err := ValidateSnapshot(snap)
	if err != nil {
		t.Errorf("expected no error for valid snapshot, got: %v", err)
	}
}

func TestValidateSnapshot_InvalidFK(t *testing.T) {
	snap := &snapshot.Snapshot{
		Meta:       snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors:     map[string]snapshot.ActorEntry{},
		Containers: map[string]snapshot.ContainerEntry{},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "task", Title: "Task", ProjectUUID: "nonexistent", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1},
		},
		Comments: map[string]snapshot.CommentEntry{},
	}

	err := ValidateSnapshot(snap)
	if err == nil {
		t.Error("expected error for task with invalid container FK")
	}
}

func TestValidateSnapshot_DuplicateSlug(t *testing.T) {
	snap := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"task-1": {ID: "T-00001", Slug: "same-slug", Title: "Task 1", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
			"task-2": {ID: "T-00002", Slug: "same-slug", Title: "Task 2", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Comments: map[string]snapshot.CommentEntry{},
	}

	err := ValidateSnapshot(snap)
	if err == nil {
		t.Error("expected error for duplicate task slug in same container")
	}
}

func TestValidateSnapshot_ContainerCycle(t *testing.T) {
	snap := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj1", Title: "Project 1", ParentUUID: "container-2", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
			"container-2": {ID: "P-00002", Slug: "proj2", Title: "Project 2", ParentUUID: "container-1", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks:    map[string]snapshot.TaskEntry{},
		Comments: map[string]snapshot.CommentEntry{},
	}

	err := ValidateSnapshot(snap)
	if err == nil {
		t.Error("expected error for container cycle")
	}
}

func TestEscapeJSONPointer(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"with/slash", "with~1slash"},
		{"with~tilde", "with~0tilde"},
		{"both/and~here", "both~1and~0here"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := escapeJSONPointer(tt.input)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestUnescapeJSONPointer(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"simple", "simple"},
		{"with~1slash", "with/slash"},
		{"with~0tilde", "with~tilde"},
		{"both~1and~0here", "both/and~here"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := unescapeJSONPointer(tt.input)
			if result != tt.expected {
				t.Errorf("expected '%s', got '%s'", tt.expected, result)
			}
		})
	}
}

func TestPatchCountOps(t *testing.T) {
	p := Patch{
		{Op: "add", Path: "/tasks/1"},
		{Op: "add", Path: "/tasks/2"},
		{Op: "replace", Path: "/tasks/3"},
		{Op: "remove", Path: "/tasks/4"},
	}

	adds, replaces, removes := p.CountOps()

	if adds != 2 {
		t.Errorf("expected 2 adds, got %d", adds)
	}
	if replaces != 1 {
		t.Errorf("expected 1 replace, got %d", replaces)
	}
	if removes != 1 {
		t.Errorf("expected 1 remove, got %d", removes)
	}
}

func TestPatchSaveAndLoad(t *testing.T) {
	tmpDir := t.TempDir()
	patchPath := filepath.Join(tmpDir, "test.patch")

	original := Patch{
		{Op: "add", Path: "/tasks/1", Value: map[string]string{"id": "T-00001"}},
	}

	if err := original.Save(patchPath); err != nil {
		t.Fatalf("failed to save patch: %v", err)
	}

	loaded, err := LoadPatch(patchPath)
	if err != nil {
		t.Fatalf("failed to load patch: %v", err)
	}

	if len(loaded) != len(original) {
		t.Errorf("length mismatch: %d vs %d", len(loaded), len(original))
	}

	if loaded[0].Op != original[0].Op {
		t.Errorf("op mismatch: %s vs %s", loaded[0].Op, original[0].Op)
	}
}

func TestCreate(t *testing.T) {
	tmpDir := t.TempDir()

	// Create base snapshot
	base := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{},
		Tasks:      map[string]snapshot.TaskEntry{},
		Comments:   map[string]snapshot.CommentEntry{},
	}

	basePath := filepath.Join(tmpDir, "base.json")
	baseData, _ := json.MarshalIndent(base, "", "  ")
	os.WriteFile(basePath, baseData, 0644)

	// Create target snapshot
	target := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
			"actor-2": {ID: "A-00002", Slug: "new-actor", Role: "agent", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{},
		Tasks:      map[string]snapshot.TaskEntry{},
		Comments:   map[string]snapshot.CommentEntry{},
	}

	targetPath := filepath.Join(tmpDir, "target.json")
	targetData, _ := json.MarshalIndent(target, "", "  ")
	os.WriteFile(targetPath, targetData, 0644)

	// Create patch
	patchPath := filepath.Join(tmpDir, "test.patch")
	result, err := Create(CreateOptions{
		FromPath:   basePath,
		ToPath:     targetPath,
		OutputPath: patchPath,
	})

	if err != nil {
		t.Fatalf("failed to create patch: %v", err)
	}

	if result.OpCount != 1 {
		t.Errorf("expected 1 op, got %d", result.OpCount)
	}
	if result.AddCount != 1 {
		t.Errorf("expected 1 add, got %d", result.AddCount)
	}
}

func TestValidatePatchOps(t *testing.T) {
	tests := []struct {
		name      string
		patch     Patch
		wantErrs  int
	}{
		{
			name: "valid patch",
			patch: Patch{
				{Op: "add", Path: "/tasks/1", Value: "test"},
				{Op: "remove", Path: "/tasks/2"},
				{Op: "replace", Path: "/tasks/3", Value: "new"},
			},
			wantErrs: 0,
		},
		{
			name: "add without value",
			patch: Patch{
				{Op: "add", Path: "/tasks/1"},
			},
			wantErrs: 1,
		},
		{
			name: "invalid op",
			patch: Patch{
				{Op: "invalid", Path: "/tasks/1"},
			},
			wantErrs: 1,
		},
		{
			name: "empty path",
			patch: Patch{
				{Op: "add", Path: "", Value: "test"},
			},
			wantErrs: 1,
		},
		{
			name: "path without leading slash",
			patch: Patch{
				{Op: "add", Path: "tasks/1", Value: "test"},
			},
			wantErrs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errors := ValidatePatchOps(tt.patch)
			if len(errors) != tt.wantErrs {
				t.Errorf("expected %d errors, got %d: %v", tt.wantErrs, len(errors), errors)
			}
		})
	}
}

func TestIncrementFriendlyID(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		existing map[string]bool
		strict   bool
		want     string
		wantErr  bool
	}{
		{
			name:     "increment task ID",
			id:       "T-00050",
			existing: map[string]bool{"T-00050": true, "T-00051": true},
			strict:   false,
			want:     "T-00052",
			wantErr:  false,
		},
		{
			name:     "increment with gap",
			id:       "T-00001",
			existing: map[string]bool{"T-00001": true, "T-00005": true},
			strict:   false,
			want:     "T-00006",
			wantErr:  false,
		},
		{
			name:     "preserve width",
			id:       "T-00001",
			existing: map[string]bool{"T-00001": true},
			strict:   false,
			want:     "T-00002",
			wantErr:  false,
		},
		{
			name:     "malformed ID in strict mode",
			id:       "T-abc",
			existing: map[string]bool{},
			strict:   true,
			want:     "",
			wantErr:  true,
		},
		{
			name:     "malformed ID in non-strict mode",
			id:       "T-abc",
			existing: map[string]bool{"T-abc": true},
			strict:   false,
			want:     "T-abc-2",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := incrementFriendlyID(tt.id, tt.existing, tt.strict)
			if (err != nil) != tt.wantErr {
				t.Errorf("incrementFriendlyID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("incrementFriendlyID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFindNewEntities(t *testing.T) {
	branch := []string{"a", "b", "c", "d"}
	base := []string{"a", "b"}

	newEntities := findNewEntities(branch, base)

	if len(newEntities) != 2 {
		t.Fatalf("expected 2 new entities, got %d", len(newEntities))
	}

	// Should be sorted
	if newEntities[0] != "c" || newEntities[1] != "d" {
		t.Errorf("expected ['c', 'd'], got %v", newEntities)
	}
}

func TestRebase_NoCollision(t *testing.T) {
	tmpDir := t.TempDir()

	// Create old-base snapshot
	oldBase := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks:    map[string]snapshot.TaskEntry{},
		Comments: map[string]snapshot.CommentEntry{},
	}

	// New-base is same as old-base
	newBase := oldBase

	// Create patch that adds a task
	p := Patch{
		{
			Op:   "add",
			Path: "/tasks/task-1",
			Value: map[string]interface{}{
				"id":           "T-00001",
				"slug":         "new-task",
				"title":        "New Task",
				"project_uuid": "container-1",
				"state":        "open",
				"priority":     float64(2),
				"created_by":   "actor-1",
				"updated_by":   "actor-1",
				"etag":         float64(1),
				"created_at":   "2025-01-01T00:00:00Z",
				"updated_at":   "2025-01-01T00:00:00Z",
			},
		},
	}

	// Save files
	oldBasePath := filepath.Join(tmpDir, "old-base.json")
	newBasePath := filepath.Join(tmpDir, "new-base.json")
	patchPath := filepath.Join(tmpDir, "patch.json")
	outPath := filepath.Join(tmpDir, "rebased.json")

	oldBaseData, _ := json.MarshalIndent(oldBase, "", "  ")
	os.WriteFile(oldBasePath, oldBaseData, 0644)

	newBaseData, _ := json.MarshalIndent(newBase, "", "  ")
	os.WriteFile(newBasePath, newBaseData, 0644)

	p.Save(patchPath)

	// Rebase
	result, err := Rebase(RebaseOptions{
		PatchPath:   patchPath,
		OldBasePath: oldBasePath,
		NewBasePath: newBasePath,
		OutputPath:  outPath,
	})

	if err != nil {
		t.Fatalf("rebase failed: %v", err)
	}

	// Should have no rewrites since no collision
	if result.CodeRewrites != nil && len(result.CodeRewrites) > 0 {
		t.Errorf("expected no rewrites, got %v", result.CodeRewrites)
	}

	// Should have 1 add operation
	if result.AddCount != 1 {
		t.Errorf("expected 1 add, got %d", result.AddCount)
	}
}

func TestRebase_WithCollision(t *testing.T) {
	tmpDir := t.TempDir()

	// Create old-base snapshot (no tasks)
	oldBase := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks:    map[string]snapshot.TaskEntry{},
		Comments: map[string]snapshot.CommentEntry{},
	}

	// Create new-base with T-00001 already taken
	newBase := &snapshot.Snapshot{
		Meta: snapshot.Meta{SchemaVersion: 1, MachineInterfaceVersion: 1},
		Actors: map[string]snapshot.ActorEntry{
			"actor-1": {ID: "A-00001", Slug: "test", Role: "human", CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Containers: map[string]snapshot.ContainerEntry{
			"container-1": {ID: "P-00001", Slug: "proj", Title: "Project", CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Tasks: map[string]snapshot.TaskEntry{
			"existing-task": {ID: "T-00001", Slug: "existing", Title: "Existing Task", ProjectUUID: "container-1", State: "open", Priority: 2, CreatedBy: "actor-1", UpdatedBy: "actor-1", ETag: 1, CreatedAt: "2025-01-01T00:00:00Z", UpdatedAt: "2025-01-01T00:00:00Z"},
		},
		Comments: map[string]snapshot.CommentEntry{},
	}

	// Create patch that adds a task with T-00001 (will collide)
	p := Patch{
		{
			Op:   "add",
			Path: "/tasks/new-task",
			Value: map[string]interface{}{
				"id":           "T-00001",
				"slug":         "new-task",
				"title":        "New Task",
				"project_uuid": "container-1",
				"state":        "open",
				"priority":     float64(2),
				"created_by":   "actor-1",
				"updated_by":   "actor-1",
				"etag":         float64(1),
				"created_at":   "2025-01-01T00:00:00Z",
				"updated_at":   "2025-01-01T00:00:00Z",
			},
		},
	}

	// Save files
	oldBasePath := filepath.Join(tmpDir, "old-base.json")
	newBasePath := filepath.Join(tmpDir, "new-base.json")
	patchPath := filepath.Join(tmpDir, "patch.json")
	outPath := filepath.Join(tmpDir, "rebased.json")

	oldBaseData, _ := json.MarshalIndent(oldBase, "", "  ")
	os.WriteFile(oldBasePath, oldBaseData, 0644)

	newBaseData, _ := json.MarshalIndent(newBase, "", "  ")
	os.WriteFile(newBasePath, newBaseData, 0644)

	p.Save(patchPath)

	// Rebase
	result, err := Rebase(RebaseOptions{
		PatchPath:   patchPath,
		OldBasePath: oldBasePath,
		NewBasePath: newBasePath,
		OutputPath:  outPath,
	})

	if err != nil {
		t.Fatalf("rebase failed: %v", err)
	}

	// Should have rewrites
	if result.CodeRewrites == nil || len(result.CodeRewrites["tasks"]) == 0 {
		t.Error("expected task rewrites due to collision")
	}

	// Check the rewrite
	if rewrite, ok := result.CodeRewrites["tasks"]["new-task"]; ok {
		if rewrite.From != "T-00001" {
			t.Errorf("expected rewrite from T-00001, got %s", rewrite.From)
		}
		if rewrite.To != "T-00002" {
			t.Errorf("expected rewrite to T-00002, got %s", rewrite.To)
		}
	} else {
		t.Error("expected rewrite for new-task UUID")
	}
}

func TestSummarize_TextFormat(t *testing.T) {
	tmpDir := t.TempDir()

	// Create a patch with multiple operations
	p := Patch{
		{Op: "add", Path: "/tasks/task-1", Value: map[string]interface{}{
			"id": "T-00001", "slug": "task-one", "title": "Task One", "state": "open",
		}},
		{Op: "add", Path: "/tasks/task-2", Value: map[string]interface{}{
			"id": "T-00002", "slug": "task-two", "title": "Task Two", "state": "open",
		}},
		{Op: "replace", Path: "/containers/container-1", Value: map[string]interface{}{
			"id": "P-00001", "slug": "project", "title": "Updated Project",
		}},
	}

	patchPath := filepath.Join(tmpDir, "patch.json")
	p.Save(patchPath)

	result, err := Summarize(SummarizeOptions{
		PatchPath: patchPath,
		Format:    "text",
	})

	if err != nil {
		t.Fatalf("summarize failed: %v", err)
	}

	// Check counts
	if result.Counts.Tasks.Add != 2 {
		t.Errorf("expected 2 tasks added, got %d", result.Counts.Tasks.Add)
	}
	if result.Counts.Containers.Replace != 1 {
		t.Errorf("expected 1 container replaced, got %d", result.Counts.Containers.Replace)
	}

	// Check summary text
	if result.Summary == "" {
		t.Error("expected non-empty summary")
	}
}

func TestSummarize_MarkdownFormat(t *testing.T) {
	tmpDir := t.TempDir()

	p := Patch{
		{Op: "add", Path: "/tasks/task-1", Value: map[string]interface{}{
			"id": "T-00001", "slug": "task-one", "title": "Task One",
		}},
	}

	patchPath := filepath.Join(tmpDir, "patch.json")
	p.Save(patchPath)

	result, err := Summarize(SummarizeOptions{
		PatchPath: patchPath,
		Format:    "markdown",
	})

	if err != nil {
		t.Fatalf("summarize failed: %v", err)
	}

	// Check markdown contains table
	if !contains(result.Summary, "| Entity | Op | ID | Path / Title |") {
		t.Error("expected markdown table header")
	}
	if !contains(result.Summary, "| task | add | T-00001 |") {
		t.Error("expected task row in table")
	}
}

func TestSummarize_JSONFormat(t *testing.T) {
	tmpDir := t.TempDir()

	p := Patch{
		{Op: "remove", Path: "/comments/comment-1"},
	}

	patchPath := filepath.Join(tmpDir, "patch.json")
	p.Save(patchPath)

	result, err := Summarize(SummarizeOptions{
		PatchPath: patchPath,
		Format:    "json",
	})

	if err != nil {
		t.Fatalf("summarize failed: %v", err)
	}

	// Check counts
	if result.Counts.Comments.Remove != 1 {
		t.Errorf("expected 1 comment removed, got %d", result.Counts.Comments.Remove)
	}

	// Check details
	if len(result.Details) != 1 {
		t.Fatalf("expected 1 detail, got %d", len(result.Details))
	}
	if result.Details[0].Entity != "comment" {
		t.Errorf("expected entity 'comment', got '%s'", result.Details[0].Entity)
	}
	if result.Details[0].Op != "remove" {
		t.Errorf("expected op 'remove', got '%s'", result.Details[0].Op)
	}
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
	os.WriteFile(basePath, baseData, 0644)

	// Patch updates the existing task
	p := Patch{
		{Op: "replace", Path: "/tasks/task-1/state", Value: "completed"},
	}

	patchPath := filepath.Join(tmpDir, "patch.json")
	p.Save(patchPath)

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

func TestSummarize_EmptyPatch(t *testing.T) {
	tmpDir := t.TempDir()

	p := Patch{}

	patchPath := filepath.Join(tmpDir, "patch.json")
	p.Save(patchPath)

	result, err := Summarize(SummarizeOptions{
		PatchPath: patchPath,
		Format:    "text",
	})

	if err != nil {
		t.Fatalf("summarize failed: %v", err)
	}

	if result.Summary != "No changes." {
		t.Errorf("expected 'No changes.', got '%s'", result.Summary)
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
	p.Save(patchPath)

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

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
