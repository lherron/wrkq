//go:build wrkq_local

package workrpc_test

// wrkqapi_task_read_acceptance_test.go — wrkq.task.show and wrkq.task.list over
// the real JSON-RPC stdio surface (T-04424 P2): the shown DTO, canonical path,
// list filters and cursor pagination, the summary projection, and the
// trim-aware hasDescription/hasSpecification presence booleans.

import "testing"

// assertBodyPresence checks a task DTO's trim-aware presence booleans.
func assertBodyPresence(t *testing.T, item map[string]any, hasDescription, hasSpecification bool, label string) {
	t.Helper()
	if item["hasDescription"] != hasDescription || item["hasSpecification"] != hasSpecification {
		t.Fatalf("%s presence: want hasDescription=%v hasSpecification=%v, got %v/%v",
			label, hasDescription, hasSpecification, item["hasDescription"], item["hasSpecification"])
	}
}

// assertBodiesOmitted checks that a summary-projection item carries empty bodies.
func assertBodiesOmitted(t *testing.T, item map[string]any, label string) {
	t.Helper()
	if item["description"] != "" || item["specification"] != "" {
		t.Fatalf("%s: summary=true must omit bodies, got description=%q specification=%q", label, item["description"], item["specification"])
	}
}

// listItems returns result["items"] as task maps.
func listItems(result map[string]any) []map[string]any {
	raw, _ := result["items"].([]any)
	items := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		m, _ := item.(map[string]any)
		items = append(items, m)
	}
	return items
}

// ─── wrkq.task.show ──────────────────────────────────────────────────────────

// TestWrkqTaskShow_NotFound verifies that showing an unknown task returns
// WRKQ_NOT_FOUND.
func TestWrkqTaskShow_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	frames := p2Run(t, migratedDB(t), mkRPC("s1", "wrkq.task.show", map[string]any{"task": "T-99999999"}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_NOT_FOUND" {
		t.Errorf("unknown task show: want WRKQ_NOT_FOUND, got %q", code)
	}
}

// TestWrkqTaskShow_ReturnsWrkqTask verifies that wrkq.task.show returns the full
// WrkqTask DTO for a task that exists.
func TestWrkqTaskShow_ReturnsWrkqTask(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	cf := p2Run(t, dbPath, mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Show Test", "kind": "bug"}))
	taskID, _ := createdTaskIDs(t, cf[1])

	sf := p2Run(t, dbPath, mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}))
	result := p2ResultOrFail(t, sf[1], "wrkq.task.show must return WrkqTask")
	assertWrkqTaskDTO(t, result)
	p2AssertFieldEq(t, result, "title", "Show Test")
}

// TestWrkqTaskPath_CreateShowListConsistent verifies that wrkq RPC, not a
// downstream adapter, owns the canonical task path exposed on WrkqTask.
func TestWrkqTaskPath_CreateShowListConsistent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	cf := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Canonical Path Test", "kind": "task", "state": "open"}),
	)
	created := p2ResultOrFail(t, cf[1], "create")
	taskID, _ := created["id"].(string)
	createPath, _ := created["path"].(string)
	if taskID == "" || createPath == "" {
		t.Fatalf("create returned id/path = %q/%q", taskID, createPath)
	}

	frames := p2Run(t, dbPath,
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
		mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open", "limit": 100}),
	)
	if shown := p2ResultOrFail(t, frames[1], "show"); shown["path"] != createPath {
		t.Fatalf("show path mismatch: create=%q show=%q", createPath, shown["path"])
	}
	if listed := p2TaskItemByID(t, p2ResultOrFail(t, frames[2], "list"), taskID); listed["path"] != createPath {
		t.Fatalf("list path mismatch: create=%q list=%q", createPath, listed["path"])
	}
}

// ─── wrkq.task.list filters and pagination ───────────────────────────────────

// TestWrkqTaskList_ReturnsItemsArray verifies that an unfiltered wrkq.task.list
// returns a result with an "items" array.
func TestWrkqTaskList_ReturnsItemsArray(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	frames := p2Run(t, migratedDB(t), mkRPC("l1", "wrkq.task.list", map[string]any{}))
	p2AssertHasItems(t, p2ResultOrFail(t, frames[1], "wrkq.task.list must return a result"), "wrkq.task.list")
}

// TestWrkqTaskList_Filters verifies that state and kind filters return only
// matching tasks.
func TestWrkqTaskList_Filters(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Open Task", "state": "open"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "In-Progress Task", "state": "in_progress"}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "A Bug", "kind": "bug"}),
		mkRPC("c4", "wrkq.task.create", map[string]any{"title": "A Task", "kind": "task"}),
	)
	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open"}),
		mkRPC("l2", "wrkq.task.list", map[string]any{"kind": "bug"}),
	)
	for i, filter := range []struct{ field, want string }{{"state", "open"}, {"kind", "bug"}} {
		result := p2ResultOrFail(t, frames[1+i], "wrkq.task.list with "+filter.field+" filter")
		for _, item := range listItems(result) {
			if got, _ := item[filter.field].(string); got != filter.want {
				t.Errorf("wrkq.task.list with %s=%s returned item with %s=%q", filter.field, filter.want, filter.field, got)
			}
		}
	}
}

// TestWrkqTaskList_CursorPagination verifies that limit+cursor pagination works.
func TestWrkqTaskList_CursorPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Page Task 1"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Page Task 2"}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "Page Task 3"}),
	)

	p1Frames := p2Run(t, dbPath, mkRPC("l1", "wrkq.task.list", map[string]any{"limit": 2}))
	page1 := p2ResultOrFail(t, p1Frames[1], "list page 1")
	items1 := listItems(page1)
	if len(items1) > 2 {
		t.Errorf("limit=2: expected at most 2 items, got %d", len(items1))
	}
	nextCursor, _ := page1["nextCursor"].(string)
	if nextCursor == "" {
		// If we created 3 tasks and got only 2, nextCursor must be set.
		if len(items1) == 2 {
			t.Error("list page 1 with limit=2 and 3 existing tasks: nextCursor must be set")
		}
		return // only 2 or fewer items total; no cursor needed
	}

	p2Frames := p2Run(t, dbPath, mkRPC("l2", "wrkq.task.list", map[string]any{"cursor": nextCursor, "limit": 2}))
	p2ResultOrFail(t, p2Frames[1], "list page 2 via cursor")
}

// ─── summary projection and presence booleans ────────────────────────────────

// TestWrkqTaskList_SummaryProjectionOmitsBodiesReportsPresence verifies that
// summary=true omits bodies while hasDescription/hasSpecification stay
// trim-aware: true for real text, false for empty and whitespace-only bodies.
func TestWrkqTaskList_SummaryProjectionOmitsBodiesReportsPresence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	cases := []struct {
		label                            string
		params                           map[string]any
		hasDescription, hasSpecification bool
	}{
		{"non-empty task", map[string]any{"title": "Summary Bodies", "description": "body text", "specification": "spec text"}, true, true},
		{"empty task", map[string]any{"title": "Summary Empty"}, false, false},
		{"whitespace task", map[string]any{"title": "Summary Whitespace", "description": " \n\t ", "specification": "   "}, false, false},
	}
	dbPath := migratedDB(t)
	reqs := make([]string, len(cases))
	for i, tc := range cases {
		tc.params["state"] = "open"
		reqs[i] = mkRPC("c"+tc.label, "wrkq.task.create", tc.params)
	}
	created := p2Run(t, dbPath, reqs...)

	frames := p2Run(t, dbPath,
		mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open", "limit": 100, "summary": true}),
	)
	result := p2ResultOrFail(t, frames[1], "summary task.list")
	for i, tc := range cases {
		taskID, _ := createdTaskIDs(t, created[1+i])
		item := p2TaskItemByID(t, result, taskID)
		assertBodiesOmitted(t, item, tc.label)
		assertBodyPresence(t, item, tc.hasDescription, tc.hasSpecification, "summary=true "+tc.label)
	}
}

// TestWrkqTaskList_DefaultProjectionKeepsBodiesAndPresence verifies that the
// default projection keeps full bodies alongside the presence booleans.
func TestWrkqTaskList_DefaultProjectionKeepsBodiesAndPresence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	created := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{
			"title":         "Full Bodies",
			"description":   "visible body",
			"specification": "visible spec",
			"state":         "open",
		}),
	)
	taskID, _ := createdTaskIDs(t, created[1])

	frames := p2Run(t, dbPath, mkRPC("l1", "wrkq.task.list", map[string]any{"state": "open", "limit": 100}))
	item := p2TaskItemByID(t, p2ResultOrFail(t, frames[1], "default task.list"), taskID)
	if item["description"] != "visible body" || item["specification"] != "visible spec" {
		t.Fatalf("default task.list must keep full bodies, got description=%q specification=%q", item["description"], item["specification"])
	}
	assertBodyPresence(t, item, true, true, "default task.list")
}

// TestWrkqTaskLoadBackedPresenceBooleans verifies that create, update and show
// DTOs (loaded from the stored row) report the same trim-aware presence.
func TestWrkqTaskLoadBackedPresenceBooleans(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	createdFrames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Load Presence", "specification": "initial spec"}),
	)
	created := p2ResultOrFail(t, createdFrames[1], "create load presence")
	taskID, _ := created["id"].(string)
	assertBodyPresence(t, created, false, true, "create DTO")

	updatedFrames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.task.update", map[string]any{
			"task":  taskID,
			"patch": map[string]any{"description": "now described", "specification": "   "},
		}),
		mkRPC("s1", "wrkq.task.show", map[string]any{"task": taskID}),
	)
	updated := p2ResultOrFail(t, updatedFrames[1], "update load presence")
	if updated["description"] != "now described" || updated["specification"] != "   " {
		t.Fatalf("update DTO must keep full bodies, got description=%q specification=%q", updated["description"], updated["specification"])
	}
	assertBodyPresence(t, updated, true, false, "update DTO (trim-aware)")
	assertBodyPresence(t, p2ResultOrFail(t, updatedFrames[2], "show load presence"), true, false, "show DTO (must match update)")
}

// TestWrkqTaskList_SummaryPagination verifies that the summary projection holds
// across cursor pages.
func TestWrkqTaskList_SummaryPagination(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	p2Run(t, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Summary Page 1", "description": "d1", "specification": "s1"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Summary Page 2", "description": "d2", "specification": "s2"}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "Summary Page 3", "description": "d3", "specification": "s3"}),
	)

	page1Frames := p2Run(t, dbPath, mkRPC("l1", "wrkq.task.list", map[string]any{"limit": 2, "summary": true}))
	page1 := p2ResultOrFail(t, page1Frames[1], "summary list page 1")
	items1 := listItems(page1)
	if len(items1) != 2 {
		t.Fatalf("summary page 1: want 2 items, got %d", len(items1))
	}
	for _, item := range items1 {
		assertBodiesOmitted(t, item, "summary page 1 item")
	}
	nextCursor, _ := page1["nextCursor"].(string)
	if nextCursor == "" {
		t.Fatal("summary page 1 with three tasks and limit=2 must return nextCursor")
	}

	page2Frames := p2Run(t, dbPath, mkRPC("l2", "wrkq.task.list", map[string]any{"limit": 2, "cursor": nextCursor, "summary": true}))
	items2 := listItems(p2ResultOrFail(t, page2Frames[1], "summary list page 2"))
	if len(items2) != 1 {
		t.Fatalf("summary page 2: want 1 item, got %d", len(items2))
	}
	assertBodiesOmitted(t, items2[0], "summary page 2 item")
}
