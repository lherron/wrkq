package cli

import (
	"sort"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestFindFiltersForRoundtripFields(t *testing.T) {
	database, _ := setupTestEnv(t)

	insertFindTask(t, database, "00000000-0000-0000-0000-000000000401", "T-00401", "rt-1", "completed", "proj-a", "proj-b", nil)
	insertFindTask(t, database, "00000000-0000-0000-0000-000000000402", "T-00402", "rt-2", "completed", "proj-a", "proj-c", "2025-01-01T00:00:00Z")
	insertFindTask(t, database, "00000000-0000-0000-0000-000000000403", "T-00403", "rt-3", "cancelled", "proj-b", "proj-b", nil)
	insertFindTask(t, database, "00000000-0000-0000-0000-000000000404", "T-00404", "rt-4", "open", "", "proj-b", nil)

	results, _, err := findTasks(database, findOptions{requestedByProjectID: "proj-a"}, true)
	if err != nil {
		t.Fatalf("findTasks failed: %v", err)
	}
	assertIDs(t, results, []string{"T-00401", "T-00402"})

	results, _, err = findTasks(database, findOptions{assignedProjectID: "proj-b"}, true)
	if err != nil {
		t.Fatalf("findTasks failed: %v", err)
	}
	assertIDs(t, results, []string{"T-00401", "T-00403", "T-00404"})

	results, _, err = findTasks(database, findOptions{ackPending: true}, true)
	if err != nil {
		t.Fatalf("findTasks failed: %v", err)
	}
	assertIDs(t, results, []string{"T-00401", "T-00403"})
}

func insertFindTask(t *testing.T, database *db.DB, uuid, id, slug, state, requestedBy, assignedProject string, acknowledgedAt interface{}) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, requested_by_project_id,
			assigned_project_id, acknowledged_at, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', ?, 3, ?, ?, ?, datetime('now'), datetime('now'),
			'00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`, uuid, id, slug, slug, state, nullableStringValue(requestedBy), nullableStringValue(assignedProject), acknowledgedAt)
	if err != nil {
		t.Fatalf("failed to insert task: %v", err)
	}
}

func nullableStringValue(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func assertIDs(t *testing.T, results []findResult, expected []string) {
	t.Helper()
	ids := make([]string, 0, len(results))
	for _, result := range results {
		ids = append(ids, result.ID)
	}
	sort.Strings(ids)
	sort.Strings(expected)
	if len(ids) != len(expected) {
		t.Fatalf("expected %d results, got %d (%v)", len(expected), len(ids), ids)
	}
	for i := range expected {
		if ids[i] != expected[i] {
			t.Fatalf("expected ids %v, got %v", expected, ids)
		}
	}
}
