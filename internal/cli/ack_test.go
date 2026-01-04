package cli

import (
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestAckTasks(t *testing.T) {
	database, _ := setupTestEnv(t)

	insertAckTask(t, database, "00000000-0000-0000-0000-000000000101", "T-00101", "ack-one", "completed", nil)
	insertAckTask(t, database, "00000000-0000-0000-0000-000000000102", "T-00102", "ack-two", "completed", "2025-01-01T00:00:00Z")

	counts, err := ackTasks(database, "00000000-0000-0000-0000-000000000001", []string{"T-00101", "T-00102"}, false)
	if err != nil {
		t.Fatalf("ackTasks failed: %v", err)
	}
	if counts.Acknowledged != 1 {
		t.Fatalf("expected 1 acknowledged task, got %d", counts.Acknowledged)
	}
	if counts.Skipped != 1 {
		t.Fatalf("expected 1 skipped task, got %d", counts.Skipped)
	}

	var acknowledgedAt string
	if err := database.QueryRow("SELECT acknowledged_at FROM tasks WHERE id = 'T-00101'").Scan(&acknowledgedAt); err != nil {
		t.Fatalf("failed to read acknowledged_at: %v", err)
	}
	if acknowledgedAt == "" {
		t.Fatalf("expected acknowledged_at to be set")
	}
}

func TestAckTasksRequiresCompleted(t *testing.T) {
	database, _ := setupTestEnv(t)

	insertAckTask(t, database, "00000000-0000-0000-0000-000000000201", "T-00201", "ack-open", "open", nil)

	_, err := ackTasks(database, "00000000-0000-0000-0000-000000000001", []string{"T-00201"}, false)
	if err == nil {
		t.Fatalf("expected error for non-completed task")
	}
}

func TestAckTasksForce(t *testing.T) {
	database, _ := setupTestEnv(t)

	insertAckTask(t, database, "00000000-0000-0000-0000-000000000301", "T-00301", "ack-force", "open", nil)

	counts, err := ackTasks(database, "00000000-0000-0000-0000-000000000001", []string{"T-00301"}, true)
	if err != nil {
		t.Fatalf("ackTasks failed: %v", err)
	}
	if counts.Acknowledged != 1 {
		t.Fatalf("expected 1 acknowledged task, got %d", counts.Acknowledged)
	}
}

func insertAckTask(t *testing.T, database *db.DB, uuid, id, slug, state string, acknowledgedAt interface{}) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, acknowledged_at,
			created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', ?, 3, ?, datetime('now'), datetime('now'),
			'00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`, uuid, id, slug, slug, state, acknowledgedAt)
	if err != nil {
		t.Fatalf("failed to insert task: %v", err)
	}
}
