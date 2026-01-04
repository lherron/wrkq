package store

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// setupTestDB creates a temporary test database with migrations applied.
func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	return database
}

// setupTestActor creates a test actor and returns its UUID.
func setupTestActor(t *testing.T, database *db.DB) string {
	t.Helper()
	result, err := database.Exec(`
		INSERT INTO actors (id, slug, role) VALUES ('', 'test-actor', 'human')
	`)
	if err != nil {
		t.Fatalf("failed to create test actor: %v", err)
	}
	rowID, _ := result.LastInsertId()
	var uuid string
	if err := database.QueryRow("SELECT uuid FROM actors WHERE rowid = ?", rowID).Scan(&uuid); err != nil {
		t.Fatalf("failed to get actor uuid: %v", err)
	}
	return uuid
}

// setupTestContainer creates a root container and returns its UUID.
func setupTestContainer(t *testing.T, database *db.DB, actorUUID string) string {
	t.Helper()
	s := New(database)
	result, err := s.Containers.Create(actorUUID, ContainerCreateParams{
		Slug: "test-project",
	})
	if err != nil {
		t.Fatalf("failed to create test container: %v", err)
	}
	return result.UUID
}

func TestTaskStore_Create(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	containerUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)

	result, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:                 "test-task",
		Title:                "Test Task",
		Description:          "A test task",
		ProjectUUID:          containerUUID,
		State:                "open",
		Priority:             2,
		RequestedByProjectID: strPtr("agent-spaces"),
		AssignedProjectID:    strPtr("rex"),
		Resolution:           strPtr("done"),
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if result.UUID == "" {
		t.Error("expected UUID to be set")
	}
	if result.ID == "" {
		t.Error("expected ID to be set")
	}
	// ETag is 2 because: INSERT sets etag=1, then tasks_ai_friendly trigger runs
	// to set the ID, which triggers tasks_au_etag to increment etag to 2
	if result.ETag != 2 {
		t.Errorf("expected etag 2, got %d", result.ETag)
	}

	// Verify task was created
	task, err := s.Tasks.GetByUUID(result.UUID)
	if err != nil {
		t.Fatalf("GetByUUID failed: %v", err)
	}
	if task.Slug != "test-task" {
		t.Errorf("expected slug 'test-task', got %q", task.Slug)
	}
	if task.Title != "Test Task" {
		t.Errorf("expected title 'Test Task', got %q", task.Title)
	}
	if task.RequestedByProjectID == nil || *task.RequestedByProjectID != "agent-spaces" {
		t.Errorf("expected requested_by_project_id 'agent-spaces', got %v", task.RequestedByProjectID)
	}
	if task.AssignedProjectID == nil || *task.AssignedProjectID != "rex" {
		t.Errorf("expected assigned_project_id 'rex', got %v", task.AssignedProjectID)
	}
	if task.Resolution == nil || *task.Resolution != "done" {
		t.Errorf("expected resolution 'done', got %v", task.Resolution)
	}

	// Verify event was logged
	var eventCount int
	database.QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.created'", result.UUID).Scan(&eventCount)
	if eventCount != 1 {
		t.Errorf("expected 1 task.created event, got %d", eventCount)
	}
}

func TestTaskStore_UpdateFields(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	containerUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)

	// Create a task first
	createResult, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "update-test",
		Title:       "Update Test",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Update the task
	fields := map[string]interface{}{
		"state":    "in_progress",
		"priority": 1,
	}
	newETag, err := s.Tasks.UpdateFields(actorUUID, createResult.UUID, fields, 0)
	if err != nil {
		t.Fatalf("UpdateFields failed: %v", err)
	}
	// After create (etag=2) + update = etag 3
	if newETag != 3 {
		t.Errorf("expected etag 3, got %d", newETag)
	}

	// Verify update
	task, _ := s.Tasks.GetByUUID(createResult.UUID)
	if task.State != "in_progress" {
		t.Errorf("expected state 'in_progress', got %q", task.State)
	}
	if task.Priority != 1 {
		t.Errorf("expected priority 1, got %d", task.Priority)
	}

	// Verify event
	var eventCount int
	database.QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.updated'", createResult.UUID).Scan(&eventCount)
	if eventCount != 1 {
		t.Errorf("expected 1 task.updated event, got %d", eventCount)
	}
}

func TestTaskStore_UpdateFields_ETagMismatch(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	containerUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)

	createResult, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "etag-test",
		Title:       "ETag Test",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    3,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Try to update with wrong etag
	_, err = s.Tasks.UpdateFields(actorUUID, createResult.UUID, map[string]interface{}{"state": "completed"}, 999)
	if err == nil {
		t.Error("expected error for etag mismatch")
	}
}

func TestTaskStore_Move(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)

	// Create two containers
	container1, _ := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project-1"})
	container2, _ := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "project-2"})

	// Create a task in container1
	taskResult, _ := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "move-test",
		Title:       "Move Test",
		ProjectUUID: container1.UUID,
		State:       "open",
		Priority:    3,
	})

	// Move to container2
	newETag, err := s.Tasks.Move(actorUUID, taskResult.UUID, container2.UUID, 0)
	if err != nil {
		t.Fatalf("Move failed: %v", err)
	}
	// After create (etag=2) + move = etag 3
	if newETag != 3 {
		t.Errorf("expected etag 3, got %d", newETag)
	}

	// Verify move
	task, _ := s.Tasks.GetByUUID(taskResult.UUID)
	if task.ProjectUUID != container2.UUID {
		t.Errorf("expected project_uuid %q, got %q", container2.UUID, task.ProjectUUID)
	}
}

func TestTaskStore_Archive(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	containerUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)

	taskResult, _ := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "archive-test",
		Title:       "Archive Test",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    3,
	})

	result, err := s.Tasks.Archive(actorUUID, taskResult.UUID, 0)
	if err != nil {
		t.Fatalf("Archive failed: %v", err)
	}
	// After create (etag=2) + archive = etag 3
	if result.ETag != 3 {
		t.Errorf("expected etag 3, got %d", result.ETag)
	}

	// Verify archive
	task, _ := s.Tasks.GetByUUID(taskResult.UUID)
	if task.State != "archived" {
		t.Errorf("expected state 'archived', got %q", task.State)
	}

	// Verify archived_at is set in DB (since GetByUUID doesn't parse times)
	var archivedAt *string
	database.QueryRow("SELECT archived_at FROM tasks WHERE uuid = ?", taskResult.UUID).Scan(&archivedAt)
	if archivedAt == nil {
		t.Error("expected archived_at to be set in database")
	}

	// Verify event was logged
	var eventCount int
	database.QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.archived'", taskResult.UUID).Scan(&eventCount)
	if eventCount != 1 {
		t.Errorf("expected 1 task.archived event, got %d", eventCount)
	}
}

func strPtr(value string) *string {
	return &value
}

func TestTaskStore_Purge(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	containerUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)

	taskResult, _ := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "purge-test",
		Title:       "Purge Test",
		ProjectUUID: containerUUID,
		State:       "open",
		Priority:    3,
	})

	_, err := s.Tasks.Purge(actorUUID, taskResult.UUID, 0)
	if err != nil {
		t.Fatalf("Purge failed: %v", err)
	}

	// Verify task is gone
	_, err = s.Tasks.GetByUUID(taskResult.UUID)
	if err == nil {
		t.Error("expected error for purged task")
	}

	// Verify purge event was logged
	var eventCount int
	database.QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.purged'", taskResult.UUID).Scan(&eventCount)
	if eventCount != 1 {
		t.Errorf("expected 1 task.purged event, got %d", eventCount)
	}
}

func TestContainerStore_Create(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)

	result, err := s.Containers.Create(actorUUID, ContainerCreateParams{
		Slug:  "new-project",
		Title: "New Project",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	if result.UUID == "" {
		t.Error("expected UUID to be set")
	}
	if result.ETag != 1 {
		t.Errorf("expected etag 1, got %d", result.ETag)
	}

	// Verify container
	container, _ := s.Containers.GetByUUID(result.UUID)
	if container.Slug != "new-project" {
		t.Errorf("expected slug 'new-project', got %q", container.Slug)
	}
}

func TestContainerStore_Create_DefaultInboxTitle(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)

	result, err := s.Containers.Create(actorUUID, ContainerCreateParams{
		Slug: "inbox",
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	container, err := s.Containers.GetByUUID(result.UUID)
	if err != nil {
		t.Fatalf("GetByUUID failed: %v", err)
	}
	if container.Title == nil || *container.Title != "Inbox" {
		t.Errorf("expected title 'Inbox', got %v", container.Title)
	}
}

func TestContainerStore_UpdateFields(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)

	createResult, _ := s.Containers.Create(actorUUID, ContainerCreateParams{
		Slug: "update-container",
	})

	newETag, err := s.Containers.UpdateFields(actorUUID, createResult.UUID, map[string]interface{}{
		"slug": "renamed-container",
	}, 0)
	if err != nil {
		t.Fatalf("UpdateFields failed: %v", err)
	}
	if newETag != 2 {
		t.Errorf("expected etag 2, got %d", newETag)
	}

	container, _ := s.Containers.GetByUUID(createResult.UUID)
	if container.Slug != "renamed-container" {
		t.Errorf("expected slug 'renamed-container', got %q", container.Slug)
	}
}

func TestContainerStore_Move(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)

	parent, _ := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "parent"})
	child, _ := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "child"})

	// Move child under parent
	_, err := s.Containers.Move(actorUUID, child.UUID, &parent.UUID, 0)
	if err != nil {
		t.Fatalf("Move failed: %v", err)
	}

	container, _ := s.Containers.GetByUUID(child.UUID)
	if container.ParentUUID == nil || *container.ParentUUID != parent.UUID {
		t.Error("expected child to be under parent")
	}
}

func TestContainerStore_Delete(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)

	result, _ := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "to-delete"})

	err := s.Containers.Delete(actorUUID, result.UUID, 0)
	if err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	_, err = s.Containers.GetByUUID(result.UUID)
	if err == nil {
		t.Error("expected error for deleted container")
	}
}

func TestContainerStore_DeleteNonEmpty(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := New(database)

	container, _ := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "non-empty"})

	// Create a task in the container
	s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "child-task",
		Title:       "Child Task",
		ProjectUUID: container.UUID,
		State:       "open",
		Priority:    3,
	})

	// Try to delete non-empty container
	err := s.Containers.Delete(actorUUID, container.UUID, 0)
	if err == nil {
		t.Error("expected error for non-empty container")
	}
}
