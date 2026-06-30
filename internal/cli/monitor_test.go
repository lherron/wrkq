package cli

// monitor_test.go — RED acceptance tests for T-04798 Phase 1.
//
// Tests assert the REAL contract for `wrkq monitor watch|wait`. All tests
// FAIL now because the stubs in monitor.go return errMonitorUnimplemented or
// false. They will PASS when Phase 2 (GREEN) implements the real behavior.
//
// Test groups:
//  1. Selector resolution (T-ID and path; invalid → exit 2 before streaming)
//  2. Event filter (task UUIDs + comment UUID/friendly-id payload fallback)
//  3. --state-only (lifecycle changes only; excludes title/body/comment)
//  4. Condition evaluator (already-terminal, state=X, all-terminal, timeout, stall)
//  5. Cursor advancement (monotonic; no duplicates; no rescan-from-zero)
//  6. Raw watch compatibility (machine-compat NDJSON; deprecation on stderr)

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

// ──── Test helpers ────────────────────────────────────────────────────────────

type monitorExecResult struct {
	stdout string
	stderr string
	code   int
	err    error
}

// runMonitorCLI runs a wrkq subcommand in-process, captures stdout/stderr
// separately, then resets cobra state for the next test.
func runMonitorCLI(t *testing.T, dbPath string, args []string) monitorExecResult {
	t.Helper()

	t.Setenv("WRKQ_DB_PATH", dbPath)
	t.Setenv("WRKQ_PRINCIPAL_REF", "agent:test-user")

	var stdout, stderr bytes.Buffer
	rootCmd.SetArgs(args)
	rootCmd.SetOut(&stdout)
	rootCmd.SetErr(&stderr)

	err := rootCmd.Execute()

	rootCmd.SetArgs(nil)
	rootCmd.SetOut(os.Stdout)
	rootCmd.SetErr(os.Stderr)
	_ = rootCmd.PersistentFlags().Set("db", "")

	return monitorExecResult{
		stdout: stdout.String(),
		stderr: stderr.String(),
		code:   ExitCodeForError(err),
		err:    err,
	}
}

// seedMonitorTask inserts a task row for monitor tests.
func seedMonitorTask(t *testing.T, database *db.DB, uuid, id, slug, state string) {
	t.Helper()
	_, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, priority, description, created_at, updated_at, created_by_actor_uuid, updated_by_actor_uuid, etag)
		VALUES (?, ?, ?, ?, '00000000-0000-0000-0000-000000000002', ?, 2, '', datetime('now'), datetime('now'), '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000001', 1)
	`, uuid, id, slug, "Task "+slug, state)
	if err != nil {
		t.Fatalf("seedMonitorTask %s/%s: %v", id, slug, err)
	}
}

// seedMonitorEvent inserts a raw event_log row and returns its auto-assigned ID.
func seedMonitorEvent(t *testing.T, database *db.DB, resourceType, resourceUUID, eventType, payload string) int64 {
	t.Helper()
	res, err := database.Exec(`
		INSERT INTO event_log (resource_type, resource_uuid, event_type, payload)
		VALUES (?, ?, ?, ?)
	`, resourceType, resourceUUID, eventType, payload)
	if err != nil {
		t.Fatalf("seedMonitorEvent %s/%s: %v", eventType, resourceUUID, err)
	}
	id, _ := res.LastInsertId()
	return id
}

// monitorStrPtr returns a pointer to s. Used to populate watchEvent string fields.
func monitorStrPtr(s string) *string { return &s }

// parseMonitorNDJSON parses stdout as NDJSON and returns all decoded objects.
// Fails the test if any line is not valid JSON.
func parseMonitorNDJSON(t *testing.T, stdout string) []map[string]interface{} {
	t.Helper()
	var out []map[string]interface{}
	for _, line := range strings.Split(strings.TrimSpace(stdout), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("non-JSON stdout line: %q", line)
			continue
		}
		out = append(out, obj)
	}
	return out
}

// ─── Test group 1: Selector resolution ───────────────────────────────────────

// TestMonitorWatch_ValidIDSelector verifies that a valid friendly-ID (T-XXXXX)
// selector resolves and an already-satisfied condition exits with a terminal line.
// RED: stub returns errMonitorUnimplemented → no output, exit code = 1, not 0.
func TestMonitorWatch_ValidIDSelector(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "test-task", "open")
	seedMonitorEvent(t, database, "task", "task-uuid-1", "task.updated", `{"state":"in_progress"}`)

	// Request a one-shot watch with --until so the process can terminate cleanly.
	res := runMonitorCLI(t, dbPath, []string{
		"monitor", "watch", "T-00001", "--ndjson", "--until", "state=open", "--timeout", "1s",
	})

	// Contract: valid selector → exit 0 when condition already met.
	if res.code != 0 {
		t.Errorf("valid selector: expected exit code 0, got %d (err=%v)", res.code, res.err)
	}

	// Already-satisfied --until conditions evaluate the authoritative task row
	// before choosing a replay cursor, so no historical event is required.
	objs := parseMonitorNDJSON(t, res.stdout)

	// stdout must contain exactly one wrkq.monitor.terminal line.
	var termCount int
	for _, obj := range objs {
		if obj["type"] == "wrkq.monitor.terminal" {
			termCount++
		}
	}
	if termCount != 1 {
		t.Errorf("expected exactly 1 terminal line, got %d; stdout=%q", termCount, res.stdout)
	}

	// Deprecation noise must NOT appear on stdout.
	if strings.Contains(res.stdout, "deprecated") {
		t.Errorf("deprecation text must not appear on stdout: %q", res.stdout)
	}
}

// TestMonitorWatch_InvalidSelector_ExitsCode2 verifies that an invalid selector
// fails with exit code 2 BEFORE any streaming begins.
// RED: stub returns errMonitorUnimplemented → exit code = 1, not 2.
func TestMonitorWatch_InvalidSelector_ExitsCode2(t *testing.T) {
	_, dbPath := setupTestEnv(t)

	res := runMonitorCLI(t, dbPath, []string{"monitor", "watch", "NOTREAL-9999", "--ndjson"})

	// Contract: invalid selector → exit code 2 (usage/domain failure).
	if res.code != 2 {
		t.Errorf("invalid selector: expected exit code 2, got %d", res.code)
	}

	// No event lines should appear (failure before streaming).
	for _, obj := range parseMonitorNDJSON(t, res.stdout) {
		if obj["type"] == "wrkq.monitor.event" {
			t.Errorf("unexpected event line emitted before selector validation: %v", obj)
		}
	}

	// Human diagnostic must be on stderr, not stdout.
	if res.code == 2 && res.stderr == "" {
		t.Errorf("expected diagnostic on stderr for invalid selector")
	}
}

// TestMonitorResolveSelectors_ValidID tests the resolver directly for a T-ID.
// RED: resolveMonitorSelectors stub returns errMonitorUnimplemented.
func TestMonitorResolveSelectors_ValidID(t *testing.T) {
	database, _ := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "test-task", "open")

	uuids, _, err := resolveMonitorSelectors(database, []string{"T-00001"})
	if err != nil {
		t.Fatalf("valid ID selector should resolve without error; got: %v", err)
	}
	if len(uuids) != 1 || uuids[0] != "task-uuid-1" {
		t.Errorf("expected uuid=[task-uuid-1], got %v", uuids)
	}
}

// TestMonitorResolveSelectors_InvalidSelector tests that an invalid selector
// returns an error with exit code 2.
// RED: stub returns errMonitorUnimplemented (code 1, not 2).
func TestMonitorResolveSelectors_InvalidSelector(t *testing.T) {
	database, _ := setupTestEnv(t)

	_, _, err := resolveMonitorSelectors(database, []string{"NOTREAL-9999"})
	if err == nil {
		t.Fatalf("invalid selector should return error, got nil")
	}
	if ExitCodeForError(err) != 2 {
		t.Errorf("invalid selector should map to exit code 2, got %d (err=%v)",
			ExitCodeForError(err), err)
	}
}

// ─── Test group 2: Event filter (UUID + friendly-id comment fallback) ─────────

// TestMonitorEventFilter_IncludesWatchedTaskEvent verifies that task events for
// watched UUIDs are included by the event filter.
// RED: isEventIncluded stub always returns false.
func TestMonitorEventFilter_IncludesWatchedTaskEvent(t *testing.T) {
	filter := monitorEventFilter{
		TaskUUIDs:       []string{"task-uuid-1"},
		TaskFriendlyIDs: []string{"T-00001"},
	}

	event := watchEvent{
		ID:           1,
		ResourceType: "task",
		ResourceUUID: monitorStrPtr("task-uuid-1"),
		EventType:    "task.updated",
		Payload:      monitorStrPtr(`{"state":"in_progress"}`),
	}
	if !isEventIncluded(event, filter) {
		t.Errorf("task.updated for watched UUID task-uuid-1 should be included")
	}
}

// TestMonitorEventFilter_ExcludesNonWatchedTaskEvent verifies that task events for
// non-watched UUIDs are excluded.
// RED: isEventIncluded stub returns false (happens to match, but the previous
// test already RED-fails, and the filter test below for comment events also fails).
func TestMonitorEventFilter_ExcludesNonWatchedTaskEvent(t *testing.T) {
	filter := monitorEventFilter{
		TaskUUIDs:       []string{"task-uuid-1"},
		TaskFriendlyIDs: []string{"T-00001"},
	}

	event := watchEvent{
		ID:           2,
		ResourceType: "task",
		ResourceUUID: monitorStrPtr("task-uuid-99"),
		EventType:    "task.updated",
		Payload:      monitorStrPtr(`{"state":"completed"}`),
	}
	if isEventIncluded(event, filter) {
		t.Errorf("task.updated for non-watched UUID task-uuid-99 should be excluded")
	}
}

// TestMonitorEventFilter_IncludesCommentByUUIDPayload verifies that comment events
// with the watched task UUID in their payload are included.
// Payload key "task_id" carries the task UUID (from LogCommentCreated path).
// RED: isEventIncluded stub always returns false.
func TestMonitorEventFilter_IncludesCommentByUUIDPayload(t *testing.T) {
	filter := monitorEventFilter{
		TaskUUIDs:       []string{"task-uuid-1"},
		TaskFriendlyIDs: []string{"T-00001"},
	}

	event := watchEvent{
		ID:           3,
		ResourceType: "comment",
		ResourceUUID: monitorStrPtr("comment-uuid-1"),
		EventType:    "comment.created",
		Payload:      monitorStrPtr(`{"task_id":"task-uuid-1","comment_id":"C-00001"}`),
	}
	if !isEventIncluded(event, filter) {
		t.Errorf("comment.created with UUID payload task_id=task-uuid-1 should be included")
	}
}

// TestMonitorEventFilter_IncludesCommentByFriendlyIDPayload verifies that comment
// events with the watched task friendly-id (T-00001) in their payload are included.
// This covers the ≥1 API delete path that writes the friendly id instead of UUID.
// RED: isEventIncluded stub always returns false.
func TestMonitorEventFilter_IncludesCommentByFriendlyIDPayload(t *testing.T) {
	filter := monitorEventFilter{
		TaskUUIDs:       []string{"task-uuid-1"},
		TaskFriendlyIDs: []string{"T-00001"},
	}

	event := watchEvent{
		ID:           4,
		ResourceType: "comment",
		ResourceUUID: monitorStrPtr("comment-uuid-2"),
		EventType:    "comment.purged",
		Payload:      monitorStrPtr(`{"task_id":"T-00001","comment_id":"C-00002"}`),
	}
	if !isEventIncluded(event, filter) {
		t.Errorf("comment.purged with friendly-id payload task_id=T-00001 should be included")
	}
}

// TestMonitorEventFilter_ExcludesCommentForOtherTask verifies that comment events
// for non-watched tasks are excluded.
// RED: stub returns false (passes accidentally, but group is already RED above).
func TestMonitorEventFilter_ExcludesCommentForOtherTask(t *testing.T) {
	filter := monitorEventFilter{
		TaskUUIDs:       []string{"task-uuid-1"},
		TaskFriendlyIDs: []string{"T-00001"},
	}

	event := watchEvent{
		ID:           5,
		ResourceType: "comment",
		ResourceUUID: monitorStrPtr("comment-uuid-3"),
		EventType:    "comment.created",
		Payload:      monitorStrPtr(`{"task_id":"task-uuid-99","comment_id":"C-00003"}`),
	}
	if isEventIncluded(event, filter) {
		t.Errorf("comment event for non-watched task-uuid-99 should be excluded")
	}
}

// ─── Test group 3: --state-only filter ────────────────────────────────────────

// TestMonitorStateOnly_IncludesTaskUpdatedWithStateKey verifies that task.updated
// events whose payload contains the "state" key are included.
// RED: isStateChangeEvent stub always returns false.
func TestMonitorStateOnly_IncludesTaskUpdatedWithStateKey(t *testing.T) {
	event := watchEvent{
		ResourceType: "task",
		EventType:    "task.updated",
		Payload:      monitorStrPtr(`{"state":"completed"}`),
	}
	if !isStateChangeEvent(event) {
		t.Errorf("task.updated with 'state' key should be a state-change event")
	}
}

// TestMonitorStateOnly_IncludesTaskUpdatedWithStateAmongOtherFields verifies
// that task.updated with state plus other fields (e.g. priority) is included.
// RED: isStateChangeEvent stub always returns false.
func TestMonitorStateOnly_IncludesTaskUpdatedWithStateAmongOtherFields(t *testing.T) {
	event := watchEvent{
		ResourceType: "task",
		EventType:    "task.updated",
		Payload:      monitorStrPtr(`{"state":"in_progress","priority":1}`),
	}
	if !isStateChangeEvent(event) {
		t.Errorf("task.updated with 'state' key among other fields should be a state-change event")
	}
}

// TestMonitorStateOnly_IncludesTaskArchived verifies that task.archived is a
// lifecycle state change (not a task.updated payload, but still lifecycle).
// RED: isStateChangeEvent stub always returns false.
func TestMonitorStateOnly_IncludesTaskArchived(t *testing.T) {
	event := watchEvent{ResourceType: "task", EventType: "task.archived"}
	if !isStateChangeEvent(event) {
		t.Errorf("task.archived should be a state-change event")
	}
}

// TestMonitorStateOnly_IncludesTaskDeleted verifies task.deleted is lifecycle.
// RED: isStateChangeEvent stub always returns false.
func TestMonitorStateOnly_IncludesTaskDeleted(t *testing.T) {
	event := watchEvent{ResourceType: "task", EventType: "task.deleted"}
	if !isStateChangeEvent(event) {
		t.Errorf("task.deleted should be a state-change event")
	}
}

// TestMonitorStateOnly_IncludesTaskRestored verifies task.restored is lifecycle.
// RED: isStateChangeEvent stub always returns false.
func TestMonitorStateOnly_IncludesTaskRestored(t *testing.T) {
	event := watchEvent{ResourceType: "task", EventType: "task.restored"}
	if !isStateChangeEvent(event) {
		t.Errorf("task.restored should be a state-change event")
	}
}

// TestMonitorStateOnly_ExcludesTitleOnlyUpdate verifies that task.updated with
// only a title change is excluded from state-only output.
// RED: stub returns false (passes accidentally, but other tests in group RED).
func TestMonitorStateOnly_ExcludesTitleOnlyUpdate(t *testing.T) {
	event := watchEvent{
		ResourceType: "task",
		EventType:    "task.updated",
		Payload:      monitorStrPtr(`{"title":"New title"}`),
	}
	if isStateChangeEvent(event) {
		t.Errorf("task.updated with only title change should NOT be a state-change event")
	}
}

// TestMonitorStateOnly_ExcludesPriorityOnlyUpdate verifies that task.updated with
// only a priority change is excluded.
// RED: stub returns false (passes accidentally).
func TestMonitorStateOnly_ExcludesPriorityOnlyUpdate(t *testing.T) {
	event := watchEvent{
		ResourceType: "task",
		EventType:    "task.updated",
		Payload:      monitorStrPtr(`{"priority":1}`),
	}
	if isStateChangeEvent(event) {
		t.Errorf("task.updated with only priority change should NOT be a state-change event")
	}
}

// TestMonitorStateOnly_ExcludesCommentEvent verifies that comment events are
// excluded from state-only output.
// RED: stub returns false (passes accidentally).
func TestMonitorStateOnly_ExcludesCommentEvent(t *testing.T) {
	event := watchEvent{
		ResourceType: "comment",
		EventType:    "comment.created",
		Payload:      monitorStrPtr(`{"task_id":"task-uuid-1"}`),
	}
	if isStateChangeEvent(event) {
		t.Errorf("comment.created should NOT be a state-change event")
	}
}

// ─── Test group 4: Condition evaluator ────────────────────────────────────────

// TestMonitorEvaluator_AlreadyTerminalExitsImmediately verifies that an already-
// terminal task causes evaluateUntil to exit immediately with result=met.
// RED: stub returns nil, errMonitorUnimplemented → t.Fatalf fires.
func TestMonitorEvaluator_AlreadyTerminalExitsImmediately(t *testing.T) {
	database, _ := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "done-task", "completed")

	result, err := evaluateUntil(database, evaluateUntilOpts{
		TaskUUIDs: []string{"task-uuid-1"},
		Condition: "all-terminal",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("evaluateUntil returned error: %v", err)
	}
	if result.Result != monitorResultMet {
		t.Errorf("already-terminal: expected result=%q, got %q", monitorResultMet, result.Result)
	}
	if result.ExitCode != 0 {
		t.Errorf("already-terminal: expected exit code 0, got %d", result.ExitCode)
	}
	if len(result.Unmet) != 0 {
		t.Errorf("already-terminal: expected empty unmet, got %v", result.Unmet)
	}
}

// TestMonitorEvaluator_StateConditionAlreadySatisfied tests state=completed
// when the task is already in that state → quick exit with result=met.
// RED: stub returns nil, errMonitorUnimplemented.
func TestMonitorEvaluator_StateConditionAlreadySatisfied(t *testing.T) {
	database, _ := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "done-task", "completed")

	result, err := evaluateUntil(database, evaluateUntilOpts{
		TaskUUIDs: []string{"task-uuid-1"},
		Condition: "state=completed",
		Timeout:   5 * time.Second,
	})
	if err != nil {
		t.Fatalf("evaluateUntil returned error: %v", err)
	}
	if result.Result != monitorResultMet {
		t.Errorf("state=completed (already): expected result=met, got %q", result.Result)
	}
	if result.ExitCode != 0 {
		t.Errorf("expected exit code 0, got %d", result.ExitCode)
	}
}

// TestMonitorEvaluator_AllTerminal_IdeaNotTerminal verifies that "idea" state
// does NOT satisfy all-terminal. Terminal set = completed,cancelled,archived,deleted.
// RED: stub returns nil, errMonitorUnimplemented.
func TestMonitorEvaluator_AllTerminal_IdeaNotTerminal(t *testing.T) {
	database, _ := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-idea", "T-00001", "idea-task", "idea")

	result, err := evaluateUntil(database, evaluateUntilOpts{
		TaskUUIDs: []string{"task-uuid-idea"},
		Condition: "all-terminal",
		Timeout:   100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("evaluateUntil returned error: %v", err)
	}
	// "idea" is NOT terminal; timeout must fire.
	if result.Result != monitorResultTimeout {
		t.Errorf("idea state ≠ terminal; expected result=timeout, got %q", result.Result)
	}
	if result.ExitCode != 1 {
		t.Errorf("timeout: expected exit code 1, got %d", result.ExitCode)
	}
	if len(result.Unmet) == 0 {
		t.Errorf("timeout: expected non-empty unmet list")
	}
}

// TestMonitorEvaluator_AllTerminal_RecognisedStates verifies that completed,
// cancelled, archived, deleted each individually satisfy all-terminal.
// RED: stub returns nil, errMonitorUnimplemented.
func TestMonitorEvaluator_AllTerminal_RecognisedStates(t *testing.T) {
	terminalStates := []string{"completed", "cancelled", "archived", "deleted"}
	for _, state := range terminalStates {
		t.Run(state, func(t *testing.T) {
			database, _ := setupTestEnv(t)
			seedMonitorTask(t, database, "task-uuid-t", "T-00001", "term-task", state)

			result, err := evaluateUntil(database, evaluateUntilOpts{
				TaskUUIDs: []string{"task-uuid-t"},
				Condition: "all-terminal",
				Timeout:   5 * time.Second,
			})
			if err != nil {
				t.Fatalf("state %q: evaluateUntil returned error: %v", state, err)
			}
			if result.Result != monitorResultMet {
				t.Errorf("state %q should satisfy all-terminal; got result=%q", state, result.Result)
			}
			if result.ExitCode != 0 {
				t.Errorf("state %q: expected exit code 0, got %d", state, result.ExitCode)
			}
		})
	}
}

// TestMonitorEvaluator_TimeoutExitsCode1 verifies that timeout → exit code 1.
// RED: stub returns nil, errMonitorUnimplemented.
func TestMonitorEvaluator_TimeoutExitsCode1(t *testing.T) {
	database, _ := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "stuck-task", "open")

	result, err := evaluateUntil(database, evaluateUntilOpts{
		TaskUUIDs: []string{"task-uuid-1"},
		Condition: "state=completed",
		Timeout:   100 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("evaluateUntil returned error: %v", err)
	}
	if result.Result != monitorResultTimeout {
		t.Errorf("expected result=timeout, got %q", result.Result)
	}
	if result.ExitCode != 1 {
		t.Errorf("expected exit code 1 for timeout, got %d", result.ExitCode)
	}
	if len(result.Unmet) == 0 {
		t.Errorf("timeout: expected non-empty unmet list")
	}
}

// TestMonitorEvaluator_ExactlyOneTerminalLine verifies that monitor watch --until
// emits exactly one wrkq.monitor.terminal line on stdout with the required schema.
// RED: stub emits nothing on stdout.
func TestMonitorEvaluator_ExactlyOneTerminalLine(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "test-task", "completed")

	res := runMonitorCLI(t, dbPath, []string{
		"monitor", "watch", "T-00001",
		"--until", "state=completed",
		"--timeout", "2s",
		"--ndjson",
	})

	objs := parseMonitorNDJSON(t, res.stdout)

	var terminalLines []map[string]interface{}
	for _, obj := range objs {
		if obj["type"] == "wrkq.monitor.terminal" {
			terminalLines = append(terminalLines, obj)
		}
	}

	if len(terminalLines) != 1 {
		t.Errorf("expected exactly 1 terminal line, got %d; stdout=%q", len(terminalLines), res.stdout)
		return
	}

	term := terminalLines[0]
	if _, ok := term["result"]; !ok {
		t.Errorf("terminal line missing 'result' field: %v", term)
	}
	if _, ok := term["reason"]; !ok {
		t.Errorf("terminal line missing 'reason' field: %v", term)
	}
	if _, ok := term["unmet"]; !ok {
		t.Errorf("terminal line missing 'unmet' field: %v", term)
	}

	// Condition already met → exit 0
	if res.code != 0 {
		t.Errorf("expected exit code 0 (condition met), got %d", res.code)
	}
}

// TestMonitorEvaluator_DiagnosticsToStderr verifies that human diagnostics go
// to stderr; stdout stays parseable NDJSON.
// RED: stub doesn't emit diagnostics to stderr.
func TestMonitorEvaluator_DiagnosticsToStderr(t *testing.T) {
	_, dbPath := setupTestEnv(t)

	// An invalid selector → diagnostics for the error should go to stderr.
	res := runMonitorCLI(t, dbPath, []string{
		"monitor", "watch", "NOTREAL-9999", "--ndjson",
	})

	// stdout must be parseable NDJSON only (no human text).
	for _, line := range strings.Split(strings.TrimSpace(res.stdout), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("stdout must be clean NDJSON; got non-JSON line: %q", line)
		}
	}

	// Exit code 2 → must have a human message on stderr.
	if res.code == 2 && res.stderr == "" {
		t.Errorf("expected diagnostic on stderr for code-2 exit, got empty stderr")
	}
}

// ─── Test group 5: Cursor advancement ─────────────────────────────────────────

// TestMonitorCursorAdvancement_NoDuplicatesOnSecondPoll verifies that re-using the
// returned cursor on the next poll yields no duplicate events.
// RED: pollMonitorEvents stub returns nil, cursorID, errMonitorUnimplemented.
func TestMonitorCursorAdvancement_NoDuplicatesOnSecondPoll(t *testing.T) {
	database, _ := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "test-task", "open")

	// Seed 3 events.
	var lastID int64
	for i := 0; i < 3; i++ {
		lastID = seedMonitorEvent(t, database, "task", "task-uuid-1", "task.updated", `{"state":"in_progress"}`)
	}

	filter := monitorEventFilter{TaskUUIDs: []string{"task-uuid-1"}}

	// First poll from cursor=0 must return 3 events.
	events1, cursor1, err := pollMonitorEvents(database, 0, filter)
	if err != nil {
		t.Fatalf("first poll error: %v", err)
	}
	if len(events1) != 3 {
		t.Errorf("first poll: expected 3 events, got %d", len(events1))
	}
	if cursor1 != lastID {
		t.Errorf("first poll: cursor should advance to %d (last event ID), got %d", lastID, cursor1)
	}

	// Second poll with cursor=cursor1 must return 0 events (no rescan).
	events2, cursor2, err := pollMonitorEvents(database, cursor1, filter)
	if err != nil {
		t.Fatalf("second poll error: %v", err)
	}
	if len(events2) != 0 {
		t.Errorf("second poll: expected 0 events (no duplicates), got %d", len(events2))
	}
	if cursor2 != cursor1 {
		t.Errorf("second poll: cursor should stay at %d, got %d", cursor1, cursor2)
	}
}

// TestMonitorCursorAdvancement_NewEventAfterCursor verifies that a new event
// inserted after the cursor is picked up on the next poll.
// RED: pollMonitorEvents stub returns errMonitorUnimplemented.
func TestMonitorCursorAdvancement_NewEventAfterCursor(t *testing.T) {
	database, _ := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "test-task", "open")

	filter := monitorEventFilter{TaskUUIDs: []string{"task-uuid-1"}}

	// First poll on empty table → 0 events, cursor stays at 0.
	events1, cursor1, err := pollMonitorEvents(database, 0, filter)
	if err != nil {
		t.Fatalf("first poll error: %v", err)
	}
	if len(events1) != 0 {
		t.Errorf("first poll (no events yet): expected 0, got %d", len(events1))
	}

	// Seed one new event.
	newID := seedMonitorEvent(t, database, "task", "task-uuid-1", "task.updated", `{"state":"completed"}`)

	// Second poll from cursor1 must pick up the new event.
	events2, cursor2, err := pollMonitorEvents(database, cursor1, filter)
	if err != nil {
		t.Fatalf("second poll error: %v", err)
	}
	if len(events2) != 1 {
		t.Errorf("second poll: expected 1 new event, got %d", len(events2))
	}
	if cursor2 != newID {
		t.Errorf("second poll: cursor should advance to %d, got %d", newID, cursor2)
	}
}

// TestMonitorCursorAdvancement_FilterExcludesNonWatched verifies that pollMonitorEvents
// excludes events for non-watched tasks; the cursor still advances past them.
// RED: pollMonitorEvents stub returns errMonitorUnimplemented.
func TestMonitorCursorAdvancement_FilterExcludesNonWatched(t *testing.T) {
	database, _ := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "watched", "open")
	seedMonitorTask(t, database, "task-uuid-2", "T-00002", "other", "open")

	// Seed one event for watched and one for non-watched.
	watchedID := seedMonitorEvent(t, database, "task", "task-uuid-1", "task.updated", `{"state":"in_progress"}`)
	otherID := seedMonitorEvent(t, database, "task", "task-uuid-2", "task.updated", `{"state":"completed"}`)

	filter := monitorEventFilter{TaskUUIDs: []string{"task-uuid-1"}}

	events, cursor, err := pollMonitorEvents(database, 0, filter)
	if err != nil {
		t.Fatalf("poll error: %v", err)
	}
	// Only 1 event should be returned (for task-uuid-1).
	if len(events) != 1 {
		t.Errorf("expected 1 event for watched task, got %d", len(events))
	}
	// Cursor must advance past BOTH events (monotonically advancing).
	if cursor != otherID {
		t.Errorf("cursor should advance past all scanned events to %d, got %d", otherID, cursor)
	}
	// Verify the returned event belongs to the watched task.
	if len(events) == 1 && events[0].ID != watchedID {
		t.Errorf("returned event ID should be %d (watched), got %d (monitorEventLine.ID)", watchedID, events[0].ID)
	}
}

// ─── Test group 6: Raw watch compatibility ─────────────────────────────────────

// TestWatchRawCompat_DeprecationOnStderrNotStdout verifies that bare `wrkq watch`
// emits a deprecation notice on STDERR only; stdout stays clean NDJSON.
// RED: deprecation notice not yet implemented.
func TestWatchRawCompat_DeprecationOnStderrNotStdout(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "test-task", "open")
	seedMonitorEvent(t, database, "task", "task-uuid-1", "task.updated", `{"state":"in_progress"}`)

	res := runMonitorCLI(t, dbPath, []string{"watch", "--follow=false", "--ndjson"})

	if res.code != 0 {
		t.Errorf("watch --follow=false should exit 0, got %d (err=%v)", res.code, res.err)
	}

	// stdout must be parseable NDJSON (raw watch output preserved).
	for _, line := range strings.Split(strings.TrimSpace(res.stdout), "\n") {
		if line == "" {
			continue
		}
		var obj map[string]interface{}
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Errorf("stdout must be parseable NDJSON; got: %q", line)
		}
	}

	// Deprecation notice MUST appear on stderr (not yet implemented → RED).
	deprecationMentioned := strings.Contains(res.stderr, "deprecated") ||
		strings.Contains(res.stderr, "monitor watch")
	if !deprecationMentioned {
		t.Errorf("expected deprecation notice on stderr referencing 'monitor watch'; got stderr=%q", res.stderr)
	}

	// Deprecation MUST NOT appear on stdout.
	if strings.Contains(res.stdout, "deprecated") || strings.Contains(res.stdout, "monitor watch") {
		t.Errorf("deprecation text must not contaminate stdout: %q", res.stdout)
	}
}

// TestWatchRawCompat_PositionalArgsIgnored verifies that positional args to bare
// `wrkq watch` remain accepted-and-ignored (NOT reinterpreted as filters).
// All events appear in the global tail regardless of positional args.
func TestWatchRawCompat_PositionalArgsIgnored(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	seedMonitorTask(t, database, "task-uuid-1", "T-00001", "first-task", "open")
	seedMonitorTask(t, database, "task-uuid-2", "T-00002", "second-task", "open")
	seedMonitorEvent(t, database, "task", "task-uuid-1", "task.updated", `{"state":"in_progress"}`)
	seedMonitorEvent(t, database, "task", "task-uuid-2", "task.updated", `{"state":"completed"}`)

	// Pass "T-00001" as a positional arg — the global tail should still emit BOTH events.
	res := runMonitorCLI(t, dbPath, []string{"watch", "--follow=false", "--ndjson", "T-00001"})

	if res.code != 0 {
		t.Errorf("watch --follow=false should exit 0, got %d", res.code)
	}

	// Both events must appear (positional arg did not filter).
	objs := parseMonitorNDJSON(t, res.stdout)
	if len(objs) < 2 {
		t.Errorf("positional arg to bare watch must be ignored; expected ≥2 events, got %d; stdout=%q",
			len(objs), res.stdout)
	}
}
