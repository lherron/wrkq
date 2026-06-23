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
