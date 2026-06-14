package store

import (
	"testing"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/paths"
)

// TestCreateParams_StateRequiresDomainState is the compile-time proof that
// store.CreateParams.State has been migrated from `string` to `domain.State`.
//
// In Go, a typed constant of type domain.State cannot be assigned to a `string`
// field without explicit conversion. If this file compiles, the migration is done.
// If it doesn't, that compile failure IS the expected red.
//
// Daedalus requirement (2): mutation-path params require domain.State.
func TestCreateParams_StateRequiresDomainState(t *testing.T) {
	// domain.StateOpen is type domain.State. Assigning it to CreateParams.State
	// proves the field type is domain.State (not string).
	_ = CreateParams{
		State: domain.StateOpen,
	}
}

// TestNewSlug_CompileProof pins that paths.NewSlug exists and returns paths.Slug.
// The Slug type is distinct from string — impl must expose it from the paths package.
func TestNewSlug_CompileProof(t *testing.T) {
	slug, err := paths.NewSlug("test-slug")
	if err != nil {
		t.Fatalf("paths.NewSlug(\"test-slug\") unexpected error: %v", err)
	}
	if string(slug) != "test-slug" {
		t.Errorf("string(slug) = %q, want \"test-slug\"", string(slug))
	}
	// Type assertion: slug must be paths.Slug (not an alias of string).
	var _ paths.Slug = slug
}

// TestDBRoundTrip_StateTextColumn verifies that a task created with domain.StateOpen
// is stored in the SQLite TEXT column as the bare string "open" and reads back as
// domain.StateOpen through GetByUUID.
//
// Daedalus requirement (4): DB column stays TEXT, NO schema migration.
// The domain.State bare named type has no custom Scan/Value, so sqlx/database/sql
// must handle it via the underlying string — this test confirms that works.
func TestDBRoundTrip_StateTextColumn(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	containerUUID := setupTestContainer(t, database, actorUUID)
	s := New(database)

	result, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug:        "db-roundtrip-state",
		Title:       "DB Round-Trip State Test",
		ProjectUUID: containerUUID,
		State:       domain.StateOpen,
		Priority:    2,
	})
	if err != nil {
		t.Fatalf("Create failed: %v", err)
	}

	// Direct DB read: TEXT column must store the bare string "open".
	var rawState string
	if err := database.QueryRow("SELECT state FROM tasks WHERE uuid = ?", result.UUID).Scan(&rawState); err != nil {
		t.Fatalf("failed to query state from DB: %v", err)
	}
	if rawState != "open" {
		t.Errorf("DB state TEXT = %q, want \"open\" (no schema change; bare named type must serialize as string)", rawState)
	}

	// Store read: GetByUUID must return a Task whose State == domain.StateOpen.
	// This proves domain.Task.State has been migrated to domain.State.
	task, err := s.Tasks.GetByUUID(result.UUID)
	if err != nil {
		t.Fatalf("GetByUUID failed: %v", err)
	}
	if task.State != domain.StateOpen {
		t.Errorf("task.State = %v, want domain.StateOpen (%v)", task.State, domain.StateOpen)
	}
}

// TestUpdateBoundary_CompletionAndDeleteCascadeStillFire is the regression guard for
// daedalus's ripple trap: after domain.State migration, the internal type assertions
// in UpdateFieldsWithViaAttribution (fields["state"].(string), == "deleted") must
// still work — completion side-effects must fire and cascade-delete must still cascade.
//
// Daedalus requirement (5): fields["state"] normalization regression.
func TestUpdateBoundary_CompletionAndDeleteCascadeStillFire(t *testing.T) {
	t.Run("completion transition fires update event", func(t *testing.T) {
		database := setupTestDB(t)
		actorUUID := setupTestActor(t, database)
		containerUUID := setupTestContainer(t, database, actorUUID)
		s := New(database)

		task, err := s.Tasks.Create(actorUUID, CreateParams{
			Slug:        "boundary-complete",
			Title:       "Boundary Complete",
			ProjectUUID: containerUUID,
			State:       domain.StateOpen,
			Priority:    2,
		})
		if err != nil {
			t.Fatalf("Create failed: %v", err)
		}

		// Transition to completed via the string path that UpdateFieldsWithViaAttribution
		// currently uses internally (fields["state"].(string)). After migration, the impl
		// must accept string(domain.State) or domain.State values here.
		_, err = s.Tasks.UpdateFields(actorUUID, task.UUID, map[string]interface{}{
			"state": string(domain.StateCompleted),
		}, 0)
		if err != nil {
			t.Fatalf("UpdateFields to completed failed: %v", err)
		}

		// Verify DB stores "completed".
		var rawState string
		if err := database.QueryRow("SELECT state FROM tasks WHERE uuid = ?", task.UUID).Scan(&rawState); err != nil {
			t.Fatalf("query state: %v", err)
		}
		if rawState != "completed" {
			t.Errorf("DB state = %q after completion, want \"completed\"", rawState)
		}

		// Verify the task.updated event was logged (completion path must still fire).
		var eventCount int
		_ = database.QueryRow(
			"SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.updated'",
			task.UUID,
		).Scan(&eventCount)
		if eventCount == 0 {
			t.Error("expected at least one task.updated event for completion transition, got 0")
		}
	})

	t.Run("deleted cascades to subtasks", func(t *testing.T) {
		database := setupTestDB(t)
		actorUUID := setupTestActor(t, database)
		containerUUID := setupTestContainer(t, database, actorUUID)
		s := New(database)

		// Create parent task.
		parent, err := s.Tasks.Create(actorUUID, CreateParams{
			Slug:        "boundary-parent",
			Title:       "Parent Task",
			ProjectUUID: containerUUID,
			State:       domain.StateOpen,
			Priority:    2,
		})
		if err != nil {
			t.Fatalf("Create parent failed: %v", err)
		}

		// Create subtask under parent.
		subtask, err := s.Tasks.Create(actorUUID, CreateParams{
			Slug:           "boundary-subtask",
			Title:          "Subtask",
			ProjectUUID:    containerUUID,
			State:          domain.StateOpen,
			Priority:       2,
			ParentTaskUUID: &parent.UUID,
		})
		if err != nil {
			t.Fatalf("Create subtask failed: %v", err)
		}

		// Delete parent — the "deleted" == "deleted" check in UpdateFieldsWithViaAttribution
		// must still trigger cascadeDeleteSubtasks after type migration.
		_, err = s.Tasks.UpdateFields(actorUUID, parent.UUID, map[string]interface{}{
			"state": string(domain.StateDeleted),
		}, 0)
		if err != nil {
			t.Fatalf("UpdateFields to deleted failed: %v", err)
		}

		// Verify subtask was cascade-deleted.
		var subtaskState string
		if err := database.QueryRow("SELECT state FROM tasks WHERE uuid = ?", subtask.UUID).Scan(&subtaskState); err != nil {
			t.Fatalf("query subtask state: %v", err)
		}
		if subtaskState != "deleted" {
			t.Errorf("subtask state = %q after parent deleted, want \"deleted\" (cascade must still fire)", subtaskState)
		}

		// Verify the cascade task.deleted event was logged for the subtask.
		var cascadeEvents int
		_ = database.QueryRow(
			"SELECT COUNT(*) FROM event_log WHERE resource_uuid = ? AND event_type = 'task.deleted'",
			subtask.UUID,
		).Scan(&cascadeEvents)
		if cascadeEvents == 0 {
			t.Error("expected task.deleted event for cascade-deleted subtask, got 0 (cascade side-effect must still fire)")
		}
	})
}
