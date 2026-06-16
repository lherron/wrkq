package workrpc_test

// tasklist_sortrecursive_test.go — RED acceptance tests for T-04851 (gap4).
//
// Covers the additive wrkq.task.list params: sort, direction, recursive (default
// false), plus cursor identity (cursor encodes the (sort,direction) tuple; cross-
// direction/cross-sort cursor reuse must be rejected).
//
// All tests COMPILE cleanly and fail RED at runtime because:
//   - TaskListParams has no sort/direction/recursive fields → params silently dropped
//   - recursive=true behaves identically to the default (subtree not queried)
//   - sort/direction whitelist is not enforced → invalid values succeed instead of WRKQ_VALIDATION
//   - cursor does not encode direction → cross-direction reuse is not caught
//   - cursor sort-field encoding is undetectable (sort param ignored, always created_at)
//
// Tests are driven through the real JSON-RPC dispatch surface via p2Run
// (go run ./cmd/wrkq rpc --stdio) — NO direct references to not-yet-existent
// Go DTO symbols or struct fields.

import (
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

// ─── gap4 seeding helpers ────────────────────────────────────────────────────

// g4SeedHierarchy seeds a parent container, a child container nested inside it,
// and one task in each container.  Returns (parentSlug, taskInParentID, taskInChildID).
//
// UUIDs use a "g4000000-…" prefix so they never collide with other test seeds.
func g4SeedHierarchy(t *testing.T, dbPath string) (parentSlug, taskInParentID, taskInChildID string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g4SeedHierarchy: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()

	const actorUUID = "00000000-0000-4000-8000-0000000000a0" // wrkq-system actor (migration 000024)

	parentSlug = "g4-parent"
	childSlug := "g4-child"
	parentUUID := "g4000000-1111-4000-8000-000000000001"
	childUUID := "g4000000-2222-4000-8000-000000000002"
	taskAUUID := "g4000000-3333-4000-8000-000000000003"
	taskBUUID := "g4000000-4444-4000-8000-000000000004"

	// Parent container — direct child of root.
	if _, err := database.Exec(`
		INSERT OR IGNORE INTO containers
			(uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, 'G4 Parent Project',
		        (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)`,
		parentUUID, parentSlug, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("g4SeedHierarchy: insert parent container: %v", err)
	}

	// Child container — nested inside parent.  Must use kind='directory' (or
	// feature/area): only kind='project' is allowed directly under root; the
	// parent/kind consistency trigger rejects a second-level 'project'.
	if _, err := database.Exec(`
		INSERT OR IGNORE INTO containers
			(uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, ?, 'G4 Child Directory', ?, 'directory', ?, ?)`,
		childUUID, childSlug, parentUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("g4SeedHierarchy: insert child container: %v", err)
	}

	// Task in the parent container.
	if _, err := database.Exec(`
		INSERT OR IGNORE INTO tasks
			(uuid, slug, title, description, project_uuid, state, priority, kind,
			 created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, 'g4-task-parent', 'G4 Parent Task', '', ?, 'open', 2, 'task', ?, ?)`,
		taskAUUID, parentUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("g4SeedHierarchy: insert parent task: %v", err)
	}

	// Task in the child (nested) container.
	if _, err := database.Exec(`
		INSERT OR IGNORE INTO tasks
			(uuid, slug, title, description, project_uuid, state, priority, kind,
			 created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, 'g4-task-child', 'G4 Child Task', '', ?, 'open', 2, 'task', ?, ?)`,
		taskBUUID, childUUID, actorUUID, actorUUID,
	); err != nil {
		t.Fatalf("g4SeedHierarchy: insert child task: %v", err)
	}

	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", taskAUUID).Scan(&taskInParentID); err != nil {
		t.Fatalf("g4SeedHierarchy: fetch parent task id: %v", err)
	}
	if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", taskBUUID).Scan(&taskInChildID); err != nil {
		t.Fatalf("g4SeedHierarchy: fetch child task id: %v", err)
	}
	return
}

// g4ItemIDs extracts the "id" field from every item in a wrkq.task.list result.
func g4ItemIDs(result map[string]any) []string {
	items, _ := result["items"].([]any)
	ids := make([]string, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if id, _ := item["id"].(string); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}

// g4ContainsID returns true if id appears in ids.
func g4ContainsID(ids []string, id string) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

// g4ItemPriorities extracts the "priority" field (as int) from every item.
func g4ItemPriorities(result map[string]any) []int {
	items, _ := result["items"].([]any)
	priorities := make([]int, 0, len(items))
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if p, ok := item["priority"].(float64); ok {
			priorities = append(priorities, int(p))
		}
	}
	return priorities
}

// ─── recursive tests ─────────────────────────────────────────────────────────

// TestWrkqTaskList_RecursiveDefault_DirectContainerOnly is a REGRESSION test:
// with no recursive param (default false), the path filter must remain direct-
// container-only — tasks in nested containers must NOT appear.
//
// EXPECTED GREEN (existing invariant preserved): the current path filter uses
// t.project_uuid = containerUUID, which is already direct-only.
func TestWrkqTaskList_RecursiveDefault_DirectContainerOnly(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	parentSlug, taskInParentID, taskInChildID := g4SeedHierarchy(t, dbPath)

	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{
			"path": parentSlug,
			// no "recursive" key — default must be direct-only
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.list without recursive")
	ids := g4ItemIDs(result)

	if !g4ContainsID(ids, taskInParentID) {
		t.Errorf("default (non-recursive) list: task in PARENT container (%s) must appear; got ids=%v", taskInParentID, ids)
	}
	if g4ContainsID(ids, taskInChildID) {
		t.Errorf("default (non-recursive) list: task in CHILD container (%s) must NOT appear; got ids=%v", taskInChildID, ids)
	}
}

// TestWrkqTaskList_RecursiveTrueIncludesNested verifies that recursive=true causes
// wrkq.task.list to include tasks in nested (child) containers of the target path.
//
// RED: the "recursive" param is not in TaskListParams and is silently dropped; the
// handler uses a direct-container filter, so the child task never appears.
func TestWrkqTaskList_RecursiveTrueIncludesNested(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	parentSlug, taskInParentID, taskInChildID := g4SeedHierarchy(t, dbPath)

	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{
			"path":      parentSlug,
			"recursive": true,
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.list with recursive=true")
	ids := g4ItemIDs(result)

	if !g4ContainsID(ids, taskInParentID) {
		t.Errorf("recursive=true list: task in PARENT container (%s) must appear; got ids=%v", taskInParentID, ids)
	}
	// RED: child task is not returned because recursive param is ignored.
	if !g4ContainsID(ids, taskInChildID) {
		t.Errorf("recursive=true list: task in CHILD container (%s) must also appear; got ids=%v", taskInChildID, ids)
	}
}

// ─── sort / direction tests ──────────────────────────────────────────────────

// TestWrkqTaskList_SortPriorityAsc verifies that sort=priority + direction=asc
// returns tasks in ascending priority order.
//
// RED: sort and direction params are silently ignored (not in TaskListParams);
// items are returned in default created_at ASC order instead of priority ASC.
func TestWrkqTaskList_SortPriorityAsc(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create tasks with priorities 3, 1, 2 (deliberately out of priority order).
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "G4 Sort P3", "priority": 3}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "G4 Sort P1", "priority": 1}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "G4 Sort P2", "priority": 2}),
	)

	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{
			"sort":      "priority",
			"direction": "asc",
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.list sort=priority direction=asc")
	priorities := g4ItemPriorities(result)

	if len(priorities) < 3 {
		t.Fatalf("sort=priority,asc: expected ≥ 3 items, got %d", len(priorities))
	}
	// Verify ascending priority order.
	for i := 1; i < len(priorities); i++ {
		if priorities[i] < priorities[i-1] {
			t.Errorf("sort=priority,asc: item %d has priority %d < item %d priority %d (not ascending); full order: %v",
				i, priorities[i], i-1, priorities[i-1], priorities)
			break
		}
	}
	// Explicitly check: first item must have lowest priority (1), not 3 (creation order).
	if priorities[0] != 1 {
		t.Errorf("sort=priority,asc: first item must have priority 1, got %d; full order: %v", priorities[0], priorities)
	}
}

// TestWrkqTaskList_SortPriorityDesc verifies that sort=priority + direction=desc
// returns tasks in descending priority order.
//
// RED: sort/direction params silently ignored; order is created_at ASC instead.
func TestWrkqTaskList_SortPriorityDesc(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create tasks with priorities 1, 3, 2 (deliberately out of order).
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "G4 Desc P1", "priority": 1}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "G4 Desc P3", "priority": 3}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "G4 Desc P2", "priority": 2}),
	)

	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{
			"sort":      "priority",
			"direction": "desc",
		}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.task.list sort=priority direction=desc")
	priorities := g4ItemPriorities(result)

	if len(priorities) < 3 {
		t.Fatalf("sort=priority,desc: expected ≥ 3 items, got %d", len(priorities))
	}
	// Verify descending priority order.
	for i := 1; i < len(priorities); i++ {
		if priorities[i] > priorities[i-1] {
			t.Errorf("sort=priority,desc: item %d has priority %d > item %d priority %d (not descending); full order: %v",
				i, priorities[i], i-1, priorities[i-1], priorities)
			break
		}
	}
	// Explicitly check: first item must have highest priority (3), not 1 (creation order).
	if priorities[0] != 3 {
		t.Errorf("sort=priority,desc: first item must have priority 3, got %d; full order: %v", priorities[0], priorities)
	}
}

// TestWrkqTaskList_SortUpdatedAtAsc verifies sort=updated_at + direction=asc.
// This is a whitelist membership test: updated_at is a valid sort field.
//
// RED: sort param ignored → result may differ from expected ordering; more
// critically, no whitelist enforcement means invalid fields also succeed (see
// TestWrkqTaskList_InvalidSortFieldRejected).
func TestWrkqTaskList_SortUpdatedAtAsc(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create two tasks, then update the first one so its updated_at is newer.
	createFrames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "G4 UpdAt First"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "G4 UpdAt Second"}),
	)
	r1 := p2ResultOrFail(t, createFrames[1], "create first task")
	task1ID, _ := r1["id"].(string)
	if task1ID == "" {
		t.Fatal("create first task returned empty id")
	}
	// The tasks_au_touch trigger (migration 000010) writes updated_at with
	// SECOND granularity (strftime('%Y-%m-%dT%H:%M:%SZ','now')). Both creates
	// and the update below would otherwise land in the same wall-clock second,
	// yielding identical updated_at values and a tie broken by id ASC — making
	// "the updated task sorts last" impossible. Wait past the second boundary so
	// task1's updated_at is strictly greater than task2's.
	time.Sleep(1100 * time.Millisecond)

	// Update task1 so its updated_at is newer than task2.
	p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":  task1ID,
			"patch": map[string]any{"title": "G4 UpdAt First (updated)"},
		}),
	)

	// List with sort=updated_at, direction=asc.
	// Task2 has an earlier updated_at, task1 has a later one; asc order = task2 first.
	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{
			"sort":      "updated_at",
			"direction": "asc",
		}),
	)
	// Must not be an error (updated_at is a valid sort field).
	result := p2ResultOrFail(t, frames[1], "wrkq.task.list sort=updated_at direction=asc")
	p2AssertHasItems(t, result, "sort=updated_at,asc")

	// Ordering: task1 (updated) should appear LAST (largest updated_at → last in asc).
	ids := g4ItemIDs(result)
	if len(ids) < 2 {
		t.Fatalf("expected ≥ 2 items, got %d", len(ids))
	}
	lastID := ids[len(ids)-1]
	if lastID != task1ID {
		t.Errorf("sort=updated_at,asc: most recently updated task (%s) must be LAST; got last=%s, all ids=%v",
			task1ID, lastID, ids)
	}
}

// ─── whitelist rejection tests ───────────────────────────────────────────────

// TestWrkqTaskList_InvalidSortFieldRejected verifies that a sort value not in the
// whitelist (created_at, updated_at, priority, id, path) returns WRKQ_VALIDATION.
//
// RED: sort param is silently dropped (not in TaskListParams); invalid values
// are never validated; the handler returns a normal items list instead of an error.
func TestWrkqTaskList_InvalidSortFieldRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	nonWhitelistedFields := []string{
		"bogus_field",
		"title",
		"description",
		"state",
		"; DROP TABLE tasks;--",
		"t.created_at", // table-qualified name must be rejected (SQL injection guard)
	}

	for _, badSort := range nonWhitelistedFields {
		badSort := badSort
		t.Run(badSort, func(t *testing.T) {
			frames := p2Run(t, dbPath,
				mkRPC("l1", "wrkq.task.list", map[string]any{
					"sort": badSort,
				}),
			)
			code := p2ErrCode(frames[1])
			if code != "WRKQ_VALIDATION" {
				t.Errorf("sort=%q: want WRKQ_VALIDATION, got error.data.code=%q (frame=%#v)",
					badSort, code, frames[1])
			}
		})
	}
}

// TestWrkqTaskList_ValidSortFields verifies that all whitelisted sort values are
// accepted (no error).
//
// EXPECTED GREEN for this sub-test's pass condition (no error returned), but the
// actual field validates that valid sorts DON'T erroneously fail.  Combined with
// TestWrkqTaskList_InvalidSortFieldRejected, the pair pins the exact whitelist.
func TestWrkqTaskList_ValidSortFields(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Whitelist from task spec and normalizeFindSort in cli/find.go.
	validFields := []string{"created_at", "updated_at", "priority", "id", "path"}

	for _, field := range validFields {
		field := field
		t.Run(field, func(t *testing.T) {
			frames := p2Run(t, dbPath,
				mkRPC("l1", "wrkq.task.list", map[string]any{
					"sort": field,
				}),
			)
			// Valid sort must not return an error.
			if errObj, ok := frames[1]["error"]; ok {
				t.Errorf("sort=%q is a valid field but returned error: %v", field, errObj)
			}
		})
	}
}

// TestWrkqTaskList_InvalidDirectionRejected verifies that a direction value other
// than "asc" or "desc" returns WRKQ_VALIDATION.
//
// RED: direction param is silently dropped (not in TaskListParams); invalid values
// succeed instead of being rejected.
func TestWrkqTaskList_InvalidDirectionRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Empty string is NOT included: "" means "not specified" (use default), not invalid.
	badDirections := []string{"sideways", "up", "DESC", "ASC", "1"}

	for _, bad := range badDirections {
		bad := bad
		t.Run("direction="+bad, func(t *testing.T) {
			frames := p2Run(t, dbPath,
				mkRPC("l1", "wrkq.task.list", map[string]any{
					"direction": bad,
				}),
			)
			code := p2ErrCode(frames[1])
			if code != "WRKQ_VALIDATION" {
				t.Errorf("direction=%q: want WRKQ_VALIDATION, got error.data.code=%q",
					bad, code)
			}
		})
	}
}

// ─── pagination with sort+direction ─────────────────────────────────────────

// TestWrkqTaskList_PaginationNoDupesNoSkips_SortPriority verifies that full
// cursor pagination with sort=priority, direction=asc produces:
//   - all created tasks appear exactly once across all pages
//   - items across pages are in strict ascending priority order
//
// RED: sort/direction params are ignored; items appear in created_at ASC order
// (5,4,3,2,1 priority) rather than priority ASC order (1,2,3,4,5).
func TestWrkqTaskList_PaginationNoDupesNoSkips_SortPriority(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create 5 tasks with priorities 5,4,3,2,1 (reversed creation order vs
	// desired sort order) so created_at ASC and priority ASC produce opposite results.
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "G4 Pag P5", "priority": 4}), // priority 4 (wrkq clamps 5→4 max)
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "G4 Pag P4", "priority": 4}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "G4 Pag P3", "priority": 3}),
		mkRPC("c4", "wrkq.task.create", map[string]any{"title": "G4 Pag P2", "priority": 2}),
		mkRPC("c5", "wrkq.task.create", map[string]any{"title": "G4 Pag P1", "priority": 1}),
	)

	// Page through all results with limit=2, sort=priority, direction=asc.
	var allIDs []string
	var allPriorities []int
	cursor := ""
	pageCount := 0
	maxPages := 10 // guard against infinite loop in buggy impl

	for pageCount < maxPages {
		params := map[string]any{
			"sort":      "priority",
			"direction": "asc",
			"limit":     2,
		}
		if cursor != "" {
			params["cursor"] = cursor
		}

		frames := p2Run(t, dbPath,
			mkRPC("page", "wrkq.task.list", params),
		)
		result := p2ResultOrFail(t, frames[1], "wrkq.task.list page")
		pageIDs := g4ItemIDs(result)
		pagePriorities := g4ItemPriorities(result)

		allIDs = append(allIDs, pageIDs...)
		allPriorities = append(allPriorities, pagePriorities...)
		pageCount++

		nextCursor, _ := result["nextCursor"].(string)
		if nextCursor == "" {
			break
		}
		cursor = nextCursor
	}

	// Must have collected all 5 tasks.
	if len(allIDs) != 5 {
		t.Errorf("pagination: expected 5 total tasks, got %d across %d pages; ids=%v",
			len(allIDs), pageCount, allIDs)
	}

	// No duplicates.
	seen := make(map[string]int)
	for i, id := range allIDs {
		if prev, ok := seen[id]; ok {
			t.Errorf("pagination: duplicate id %q at positions %d and %d", id, prev, i)
		}
		seen[id] = i
	}

	// Priorities must be in ascending order across all pages.
	for i := 1; i < len(allPriorities); i++ {
		if allPriorities[i] < allPriorities[i-1] {
			t.Errorf("pagination sort=priority,asc: priority went %d → %d at page boundary (not ascending); full priority sequence: %v",
				allPriorities[i-1], allPriorities[i], allPriorities)
			break
		}
	}
	// The last task created (priority 1) must appear first in priority ASC ordering.
	if len(allPriorities) > 0 && allPriorities[0] != 1 {
		t.Errorf("pagination sort=priority,asc: first result must have priority 1, got %d; full: %v",
			allPriorities[0], allPriorities)
	}
}

// ─── cursor identity: direction mismatch ─────────────────────────────────────

// TestWrkqTaskList_CursorDirectionMismatchRejected verifies that a cursor produced
// under direction=asc must be REJECTED (WRKQ_VALIDATION) when submitted with
// direction=desc.
//
// RED: direction param is not in TaskListParams and is silently dropped; both the
// cursor-producing call and the consuming call use the same underlying created_at
// ASC ordering.  Because the cursor does not encode direction, the mismatch goes
// undetected and the second call succeeds instead of returning WRKQ_VALIDATION.
func TestWrkqTaskList_CursorDirectionMismatchRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create 3 tasks to ensure a two-item page produces a nextCursor.
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "G4 CurDir T1"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "G4 CurDir T2"}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "G4 CurDir T3"}),
	)

	// Page 1: sort=created_at, direction=asc, limit=2 → produces nextCursor.
	p1Frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{
			"sort":      "created_at",
			"direction": "asc",
			"limit":     2,
		}),
	)
	p1Result := p2ResultOrFail(t, p1Frames[1], "page 1 direction=asc")
	nextCursor, _ := p1Result["nextCursor"].(string)
	if nextCursor == "" {
		t.Skip("cursor identity test requires nextCursor; got empty (need ≥ 3 tasks)")
	}

	// Page 2: SAME sort field, but OPPOSITE direction → cursor must be rejected.
	p2Frames := p2Run(t, dbPath,
		mkRPC("l2", "wrkq.task.list", map[string]any{
			"sort":      "created_at",
			"direction": "desc",   // opposite direction from the cursor's origin
			"cursor":    nextCursor,
		}),
	)
	// Expect WRKQ_VALIDATION; a cursor from direction=asc encodes a position that
	// is semantically invalid for a direction=desc traversal.
	code := p2ErrCode(p2Frames[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("cursor from direction=asc reused with direction=desc: want WRKQ_VALIDATION, got error.data.code=%q (frame=%#v)",
			code, p2Frames[1])
	}
}

// TestWrkqTaskList_CursorSortFieldMismatchRejected verifies that a cursor produced
// under sort=priority must be REJECTED (WRKQ_VALIDATION) when submitted with a
// different sort field (sort=created_at).
//
// RED: sort param is not in TaskListParams and is silently dropped; both the
// cursor-producing call and the consuming call use the same underlying created_at
// ordering.  Because the cursor does not encode the active sort field, the mismatch
// goes undetected and the second call succeeds instead of returning WRKQ_VALIDATION.
func TestWrkqTaskList_CursorSortFieldMismatchRejected(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	// Create 3 tasks.
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "G4 CurSort T1", "priority": 2}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "G4 CurSort T2", "priority": 1}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "G4 CurSort T3", "priority": 3}),
	)

	// Page 1: sort=priority, direction=asc, limit=2.
	// After implementation: cursor encodes sort=priority position.
	// Before implementation (now): sort ignored, cursor encodes created_at position.
	p1Frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{
			"sort":      "priority",
			"direction": "asc",
			"limit":     2,
		}),
	)
	p1Result := p2ResultOrFail(t, p1Frames[1], "page 1 sort=priority")
	nextCursor, _ := p1Result["nextCursor"].(string)
	if nextCursor == "" {
		t.Skip("cursor sort-mismatch test requires nextCursor; got empty")
	}

	// Page 2: DIFFERENT sort field — cursor from sort=priority is invalid for sort=created_at.
	p2Frames := p2Run(t, dbPath,
		mkRPC("l2", "wrkq.task.list", map[string]any{
			"sort":      "created_at", // different sort from cursor origin
			"direction": "asc",
			"cursor":    nextCursor,
		}),
	)
	// Expect WRKQ_VALIDATION; a cursor produced under sort=priority cannot be applied
	// to a sort=created_at traversal without risk of duplicates or skipped items.
	code := p2ErrCode(p2Frames[1])
	if code != "WRKQ_VALIDATION" {
		t.Errorf("cursor from sort=priority reused with sort=created_at: want WRKQ_VALIDATION, got error.data.code=%q (frame=%#v)",
			code, p2Frames[1])
	}
}

// TestWrkqTaskList_CursorValidDirectionReuse verifies the POSITIVE case: a cursor
// produced under (sort=created_at, direction=asc) is VALID when the next page
// uses the SAME (sort=created_at, direction=asc).
//
// EXPECTED GREEN: once cursor identity is implemented this must continue to work.
// Also tests the no-error path so implementers know which direction is safe.
func TestWrkqTaskList_CursorValidDirectionReuse(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "G4 CV T1"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "G4 CV T2"}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "G4 CV T3"}),
	)

	// Page 1.
	p1Frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{
			"sort":      "created_at",
			"direction": "asc",
			"limit":     2,
		}),
	)
	p1Result := p2ResultOrFail(t, p1Frames[1], "page 1")
	nextCursor, _ := p1Result["nextCursor"].(string)
	if nextCursor == "" {
		t.Skip("valid cursor reuse test requires nextCursor; got empty")
	}

	// Page 2 — same sort+direction as cursor origin → must succeed.
	p2Frames := p2Run(t, dbPath,
		mkRPC("l2", "wrkq.task.list", map[string]any{
			"sort":      "created_at",
			"direction": "asc",
			"cursor":    nextCursor,
		}),
	)
	p2ResultOrFail(t, p2Frames[1], "page 2 with matching sort+direction (must succeed)")
}
