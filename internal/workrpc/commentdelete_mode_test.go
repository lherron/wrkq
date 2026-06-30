package workrpc_test

// commentdelete_mode_test.go — acceptance tests for the caller-owned-confirmation
// seam's server half for `comment rm` (T-05100 Tranche B).
//
// wrkq.comment.delete gains an EXPLICIT `mode` param + an `ifMatch` precondition:
//   - absent/"soft" → soft-delete (sets deleted_at + bumps etag), row preserved.
//   - "purge"       → hard-delete the comment row.
//   - invalid       → WRKQ_VALIDATION.
//   - ifMatch mismatch → WRKQ_CONFLICT (machine-checkable CAS, NOT a prompt).
//
// These run through the real JSON-RPC dispatch surface, which is itself the
// server-non-interactivity proof: the server resolves every disposition from the
// params alone and never prompts, inspects a TTY, or reads a confirmation.

import (
	"database/sql"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// TestCommentAdd_ExactAttribution is the NON-webhook guard for the shared
// attributionFor fix (daedalus #10261, T-05119): a canonical --as principal with
// no legacy actor row must be recorded exactly (created_by_principal_ref) on a
// comment.add — proving the fix is in the shared helper, not webhook-specific.
func TestCommentAdd_ExactAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	_, taskUUID := createTaskRPC(t, dbPath, "attribution host")
	frames := p2Run(t, dbPath,
		mkRPC("ca", "wrkq.comment.add", map[string]any{"task": taskUUID, "body": "hi", "actor": "agent:flag-principal"}),
	)
	res := p2ResultOrFail(t, frames[1], "wrkq.comment.add attribution")
	commentUUID, _ := res["uuid"].(string)
	if commentUUID == "" {
		t.Fatalf("comment.add returned no uuid: %#v", res)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = database.Close() }()
	var pr string
	if err := database.QueryRow(
		`SELECT created_by_principal_ref FROM comments WHERE uuid = ?`, commentUUID,
	).Scan(&pr); err != nil {
		t.Fatalf("read comment attribution: %v", err)
	}
	if pr != "agent:flag-principal" {
		t.Errorf("comment created_by_principal_ref: want agent:flag-principal, got %q", pr)
	}
}

// commentRow reads (exists, deletedAt, etag) for a comment UUID, or exists=false
// when the row is gone (purge).
func commentRow(t *testing.T, dbPath, uuid string) (exists bool, deletedAt string, etag int64) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("commentRow: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var dl sql.NullString
	err = database.QueryRow("SELECT deleted_at, etag FROM comments WHERE uuid = ?", uuid).Scan(&dl, &etag)
	if err == sql.ErrNoRows {
		return false, "", 0
	}
	if err != nil {
		t.Fatalf("commentRow query: %v", err)
	}
	return true, dl.String, etag
}

// createCommentRPC creates a task + comment via RPC and returns the comment uuid.
// The host task title is derived from body so distinct comments don't collide on
// the project_uuid+slug uniqueness constraint.
func createCommentRPC(t *testing.T, dbPath, body string) (commentUUID string) {
	t.Helper()
	_, taskUUID := createTaskRPC(t, dbPath, "comment-host: "+body)
	frames := p2Run(t, dbPath,
		mkRPC("ca", "wrkq.comment.add", map[string]any{"task": taskUUID, "body": body}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.comment.add")
	commentUUID, _ = result["uuid"].(string)
	if commentUUID == "" {
		t.Fatalf("createCommentRPC: empty uuid: %#v", result)
	}
	return commentUUID
}

// TestCommentDelete_SoftDefault proves absent/"soft" mode soft-deletes (sets
// deleted_at + bumps etag) and PRESERVES the row.
func TestCommentDelete_SoftDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	uuid := createCommentRPC(t, dbPath, "soft me")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.comment.delete", map[string]any{"id": uuid}))
	p2ResultOrFail(t, frames[1], "wrkq.comment.delete (soft default)")

	exists, deletedAt, etag := commentRow(t, dbPath, uuid)
	if !exists {
		t.Fatal("soft-delete must NOT purge the row")
	}
	if deletedAt == "" {
		t.Error("soft-delete must set deleted_at")
	}
	if etag != 2 {
		t.Errorf("soft-delete must bump etag 1→2, got %d", etag)
	}
}

// TestCommentDelete_SoftExplicit proves mode:"soft" behaves identically to absent.
func TestCommentDelete_SoftExplicit(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	uuid := createCommentRPC(t, dbPath, "soft explicit")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.comment.delete", map[string]any{"id": uuid, "mode": "soft"}))
	p2ResultOrFail(t, frames[1], "wrkq.comment.delete (mode soft)")

	exists, deletedAt, _ := commentRow(t, dbPath, uuid)
	if !exists || deletedAt == "" {
		t.Errorf("mode:soft must soft-delete; exists=%v deletedAt=%q", exists, deletedAt)
	}
}

// TestCommentDelete_PurgeMode proves mode:purge hard-deletes the comment row.
func TestCommentDelete_PurgeMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	uuid := createCommentRPC(t, dbPath, "purge me")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.comment.delete", map[string]any{"id": uuid, "mode": "purge"}))
	p2ResultOrFail(t, frames[1], "wrkq.comment.delete (purge)")

	if exists, _, _ := commentRow(t, dbPath, uuid); exists {
		t.Error("purge must hard-delete the comment row")
	}
}

// TestCommentDelete_InvalidMode proves an unknown mode → WRKQ_VALIDATION and
// leaves the comment untouched.
func TestCommentDelete_InvalidMode(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	uuid := createCommentRPC(t, dbPath, "keep me")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.comment.delete", map[string]any{"id": uuid, "mode": "incinerate"}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_VALIDATION" {
		t.Errorf("invalid mode: want WRKQ_VALIDATION, got %q (frame=%#v)", code, frames[1])
	}
	if exists, deletedAt, _ := commentRow(t, dbPath, uuid); !exists || deletedAt != "" {
		t.Errorf("invalid mode must not mutate; exists=%v deletedAt=%q", exists, deletedAt)
	}
}

// TestCommentDelete_IfMatchMismatch proves a stale ifMatch → WRKQ_CONFLICT with no
// mutation (a machine-checkable CAS, never a prompt).
func TestCommentDelete_IfMatchMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	uuid := createCommentRPC(t, dbPath, "cas me") // fresh etag = 1

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.comment.delete", map[string]any{"id": uuid, "ifMatch": 999}))
	if code := p2ErrCode(frames[1]); code != "WRKQ_CONFLICT" {
		t.Errorf("ifMatch mismatch: want WRKQ_CONFLICT, got %q (frame=%#v)", code, frames[1])
	}
	if exists, deletedAt, etag := commentRow(t, dbPath, uuid); !exists || deletedAt != "" || etag != 1 {
		t.Errorf("ifMatch mismatch must not mutate; exists=%v deletedAt=%q etag=%d", exists, deletedAt, etag)
	}
}

// TestCommentDelete_IfMatchMatch proves a matching ifMatch lets the soft-delete
// proceed.
func TestCommentDelete_IfMatchMatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	uuid := createCommentRPC(t, dbPath, "match me") // fresh etag = 1

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.comment.delete", map[string]any{"id": uuid, "ifMatch": 1}))
	p2ResultOrFail(t, frames[1], "wrkq.comment.delete (ifMatch match)")

	if exists, deletedAt, _ := commentRow(t, dbPath, uuid); !exists || deletedAt == "" {
		t.Errorf("ifMatch match must soft-delete; exists=%v deletedAt=%q", exists, deletedAt)
	}
}

// TestCommentDelete_NonInteractive_FromParamsAlone proves the server resolves a
// soft-delete then a purge purely from framed JSON-RPC params with NO prompt — if
// it tried to read a confirmation it would block until the subprocess timeout.
func TestCommentDelete_NonInteractive_FromParamsAlone(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	soft := createCommentRPC(t, dbPath, "soft non-interactive")
	purge := createCommentRPC(t, dbPath, "purge non-interactive")

	frames := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.comment.delete", map[string]any{"id": soft, "mode": "soft"}),
		mkRPC("p1", "wrkq.comment.delete", map[string]any{"id": purge, "mode": "purge"}),
	)
	p2ResultOrFail(t, frames[1], "soft from params alone")
	p2ResultOrFail(t, frames[2], "purge from params alone")

	if exists, deletedAt, _ := commentRow(t, dbPath, soft); !exists || deletedAt == "" {
		t.Errorf("soft from params: exists=%v deletedAt=%q", exists, deletedAt)
	}
	if exists, _, _ := commentRow(t, dbPath, purge); exists {
		t.Error("purge from params alone must hard-delete the row")
	}
}

// createCommentRPCFull creates a task + comment and returns the comment UUID, the
// FRIENDLY comment id, and the task UUID (the latter two are what the legacy event
// payload embeds).
func createCommentRPCFull(t *testing.T, dbPath, body string) (commentUUID, commentID, taskUUID string) {
	t.Helper()
	_, taskUUID = createTaskRPC(t, dbPath, "comment-host: "+body)
	frames := p2Run(t, dbPath,
		mkRPC("ca", "wrkq.comment.add", map[string]any{"task": taskUUID, "body": body}),
	)
	result := p2ResultOrFail(t, frames[1], "wrkq.comment.add")
	commentUUID, _ = result["uuid"].(string)
	commentID, _ = result["id"].(string)
	if commentUUID == "" || commentID == "" {
		t.Fatalf("createCommentRPCFull: empty uuid/id: %#v", result)
	}
	return commentUUID, commentID, taskUUID
}

// latestCommentEvent reads the most recent event_log row for a comment by
// event_type, returning the durable (etag, payload). etag is nil-presence-aware:
// hasEtag is false when the column is NULL.
func latestCommentEvent(t *testing.T, dbPath, commentUUID, eventType string) (hasEtag bool, etag int64, payload string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("latestCommentEvent: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var et sql.NullInt64
	var pl sql.NullString
	err = database.QueryRow(
		`SELECT etag, payload FROM event_log
		 WHERE resource_type = 'comment' AND resource_uuid = ? AND event_type = ?
		 ORDER BY id DESC LIMIT 1`,
		commentUUID, eventType,
	).Scan(&et, &pl)
	if err == sql.ErrNoRows {
		t.Fatalf("no %s event for comment %s", eventType, commentUUID)
	}
	if err != nil {
		t.Fatalf("latestCommentEvent query: %v", err)
	}
	return et.Valid, et.Int64, pl.String
}

// TestCommentDelete_SoftEventParity proves the soft-delete durable EVENT matches
// legacy byte-for-byte where the row-snapshot oracle cannot: the comment.deleted
// event payload shape (task_id = task UUID, comment_id = FRIENDLY comment id,
// deleted_by_principal_ref present, soft_delete:true) AND the event etag = the NEW
// (post-bump) comment etag. This is the regression guard for the HIGH-severity
// event-log drift fixed in hrcchat#10198.
func TestCommentDelete_SoftEventParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	commentUUID, commentID, taskUUID := createCommentRPCFull(t, dbPath, "soft event parity") // fresh etag=1

	// Pass an EXPLICIT principal so the recorded principal is deterministic
	// regardless of the ambient default principal (principal-only attribution
	// requires an exact agent:<id> ref).
	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.comment.delete", map[string]any{"id": commentUUID, "actor": "agent:smokey"}))
	p2ResultOrFail(t, frames[1], "wrkq.comment.delete (soft event parity)")

	// Row preserved + etag bumped 1→2.
	exists, deletedAt, rowEtag := commentRow(t, dbPath, commentUUID)
	if !exists || deletedAt == "" {
		t.Fatalf("soft-delete must preserve the row + set deleted_at; exists=%v deletedAt=%q", exists, deletedAt)
	}
	if rowEtag != 2 {
		t.Fatalf("soft-delete row etag: want 2, got %d", rowEtag)
	}

	hasEtag, evtEtag, payload := latestCommentEvent(t, dbPath, commentUUID, "comment.deleted")
	if !hasEtag {
		t.Error("comment.deleted event MUST carry an etag (legacy sets it to the new comment etag)")
	} else if evtEtag != 2 {
		t.Errorf("comment.deleted event etag: want 2 (new comment etag), got %d", evtEtag)
	}

	// Legacy payload is a fixed key order: task_id, comment_id, deleted_by_principal_ref, soft_delete.
	want := `{"task_id":"` + taskUUID + `","comment_id":"` + commentID +
		`","deleted_by_principal_ref":"agent:smokey","soft_delete":true}`
	if payload != want {
		t.Errorf("comment.deleted payload mismatch:\n want: %s\n got:  %s", want, payload)
	}
}

// TestCommentDelete_PurgeEventParity proves the purge event payload byte-matches
// legacy: comment.purged with {task_id = task UUID, comment_id = FRIENDLY id,
// hard_delete:true} and NO event etag (legacy omits it for purge).
func TestCommentDelete_PurgeEventParity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	commentUUID, commentID, taskUUID := createCommentRPCFull(t, dbPath, "purge event parity")

	frames := p2Run(t, dbPath, mkRPC("d1", "wrkq.comment.delete", map[string]any{"id": commentUUID, "mode": "purge"}))
	p2ResultOrFail(t, frames[1], "wrkq.comment.delete (purge event parity)")

	if exists, _, _ := commentRow(t, dbPath, commentUUID); exists {
		t.Fatal("purge must hard-delete the comment row")
	}

	hasEtag, _, payload := latestCommentEvent(t, dbPath, commentUUID, "comment.purged")
	if hasEtag {
		t.Error("comment.purged event must NOT carry an etag (legacy omits it for purge)")
	}
	want := `{"task_id":"` + taskUUID + `","comment_id":"` + commentID + `","hard_delete":true}`
	if payload != want {
		t.Errorf("comment.purged payload mismatch:\n want: %s\n got:  %s", want, payload)
	}
}
