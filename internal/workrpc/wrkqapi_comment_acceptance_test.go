//go:build wrkq_local

package workrpc_test

// wrkqapi_comment_acceptance_test.go — wrkq.comment.add and wrkq.comment.list
// over the real JSON-RPC stdio surface (T-04424 P2; docs/wrkq-wrkf-rpc.md §6.2).

import "testing"

// TestWrkqCommentAdd_ReturnsWrkqComment verifies that wrkq.comment.add returns
// a WrkqComment DTO with required camelCase fields and no DB column leaks.
func TestWrkqCommentAdd_ReturnsWrkqComment(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := createTaskRPC(t, dbPath, "Comment Host Task")

	af := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.comment.add", map[string]any{"task": taskID, "body": "This is a smoke-test comment."}),
	)
	result := p2ResultOrFail(t, af[1], "wrkq.comment.add must return WrkqComment")
	for _, key := range []string{"uuid", "id", "task", "body", "createdAt"} {
		p2AssertStr(t, result, key)
	}
	p2AssertEtag(t, result)
	p2AssertAbsent(t, result, "created_at")
	p2AssertAbsent(t, result, "updated_at")
}

// TestWrkqCommentAdd_UnknownTask_NotFound verifies that commenting on a
// non-existent task returns WRKQ_NOT_FOUND.
func TestWrkqCommentAdd_UnknownTask_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	frames := p2Run(t, migratedDB(t),
		mkRPC("a1", "wrkq.comment.add", map[string]any{"task": "T-99999999", "body": "Comment on ghost task"}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_NOT_FOUND" {
		t.Errorf("comment on unknown task: want WRKQ_NOT_FOUND, got %q", code)
	}
}

// TestWrkqCommentList_ReturnsItems verifies that wrkq.comment.list returns the
// added comments in "items".
func TestWrkqCommentList_ReturnsItems(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	taskID, _ := createTaskRPC(t, dbPath, "Comment List Host")

	frames := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.comment.add", map[string]any{"task": taskID, "body": "Comment 1"}),
		mkRPC("a2", "wrkq.comment.add", map[string]any{"task": taskID, "body": "Comment 2"}),
		mkRPC("l1", "wrkq.comment.list", map[string]any{"task": taskID}),
	)
	result := p2ResultOrFail(t, frames[3], "wrkq.comment.list must return items")
	p2AssertHasItems(t, result, "wrkq.comment.list")
	if items, _ := result["items"].([]any); len(items) < 2 {
		t.Errorf("expected ≥ 2 comments, got %d", len(items))
	}
}
