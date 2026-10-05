//go:build wrkq_local

package workrpc_test

// wrkqapi_container_read_acceptance_test.go — wrkq.container.show/list over the
// real JSON-RPC stdio surface (T-04448).

import "testing"

// TestWrkqContainerShow_NotFound shows a nonexistent path: a real WRKQ_NOT_FOUND.
func TestWrkqContainerShow_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	cf := p2Run(t, dbPath, mkRPC("cs1", "wrkq.container.show", map[string]any{"path": "nonexistent/path/xyz"}))
	p4AssertDomainError(t, cf[1], "WRKQ_NOT_FOUND", "container.show nonexistent")
}

// TestWrkqContainerShow_CamelCaseDTO_IncludesPath shows the project container
// seeded by p2SeedTask (slug "p2-test-proj"): camelCase fields including path,
// and no DB column leaks.
func TestWrkqContainerShow_CamelCaseDTO_IncludesPath(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	p2SeedTask(t, dbPath, "f0000009-0000-4000-8000-000000000001", "p4-container-show", "Container Show Task")

	cf := p2Run(t, dbPath, mkRPC("cs1", "wrkq.container.show", map[string]any{"path": "p2-test-proj"}))
	result := p2ResultOrFail(t, cf[1], "wrkq.container.show known project")

	for _, key := range []string{"uuid", "slug", "title", "kind", "path"} {
		p2AssertStr(t, result, key)
	}
	for _, column := range []string{"project_uuid", "parent_uuid", "created_at", "updated_at"} {
		p2AssertAbsent(t, result, column)
	}
}

// TestWrkqContainerList_ReturnsItems lists containers after seeding a project:
// a non-empty items array.
func TestWrkqContainerList_ReturnsItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	p2SeedTask(t, dbPath, "f000000a-0000-4000-8000-000000000001", "p4-container-list", "Container List Task")

	cf := p2Run(t, dbPath, mkRPC("cl1", "wrkq.container.list", map[string]any{}))
	result := p2ResultOrFail(t, cf[1], "wrkq.container.list")
	p2AssertHasItems(t, result, "wrkq.container.list")
	if items, _ := result["items"].([]any); len(items) < 1 {
		t.Errorf("wrkq.container.list: expected at least 1 item, got %d", len(items))
	}
}
