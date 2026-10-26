//go:build wrkq_local

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
	"encoding/json"
	"fmt"
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

// TestCommentDeleteDispositionFromParams proves each caller-supplied
// disposition param takes its own branch: mode:"soft" and a current ifMatch
// soft-delete (row kept, deleted_at set, etag 1→2); an unknown mode refuses with
// WRKQ_VALIDATION naming the mode field; a stale ifMatch refuses with
// WRKQ_CONFLICT naming both etags. Refusals leave the row untouched (etag 1).
// (Absent mode and purge are covered by the event-parity tests below.)
func TestCommentDeleteDispositionFromParams(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	type row struct {
		name     string
		params   map[string]any // merged over {"id": <comment uuid>}
		wantCode string         // "" = success
		wantData map[string]any // error.data fields the refusal must carry
		wantSoft bool
	}
	rows := []row{
		{name: "mode soft", params: map[string]any{"mode": "soft"}, wantSoft: true},
		{name: "mode unknown", params: map[string]any{"mode": "incinerate"}, wantCode: "WRKQ_VALIDATION",
			wantData: map[string]any{"field": "mode"}},
		{name: "ifMatch stale", params: map[string]any{"ifMatch": 999}, wantCode: "WRKQ_CONFLICT",
			wantData: map[string]any{"currentEtag": float64(1), "expectedEtag": float64(999)}},
		{name: "ifMatch current", params: map[string]any{"ifMatch": 1}, wantSoft: true},
	}

	dbPath := migratedDB(t)
	_, taskUUID := createTaskRPC(t, dbPath, "comment disposition host")
	var adds []string
	for i, r := range rows {
		adds = append(adds, mkRPC(fmt.Sprintf("ca%d", i), "wrkq.comment.add", map[string]any{"task": taskUUID, "body": r.name}))
	}
	addFrames := p2Run(t, dbPath, adds...)
	uuids := make([]string, len(rows))
	var deletes []string
	for i, r := range rows {
		res := p2ResultOrFail(t, addFrames[1+i], "wrkq.comment.add "+r.name)
		uuids[i], _ = res["uuid"].(string)
		if uuids[i] == "" {
			t.Fatalf("%s: comment.add returned no uuid: %#v", r.name, res)
		}
		params := map[string]any{"id": uuids[i]}
		for k, v := range r.params {
			params[k] = v
		}
		deletes = append(deletes, mkRPC(fmt.Sprintf("d%d", i), "wrkq.comment.delete", params))
	}
	frames := p2Run(t, dbPath, deletes...)

	for i, r := range rows {
		frame := frames[1+i]
		exists, deletedAt, etag := commentRow(t, dbPath, uuids[i])
		if r.wantCode == "" {
			p2ResultOrFail(t, frame, "wrkq.comment.delete "+r.name)
			if !exists || deletedAt == "" || etag != 2 {
				t.Errorf("%s: want soft-deleted row (deleted_at set, etag 2); exists=%v deletedAt=%q etag=%d", r.name, exists, deletedAt, etag)
			}
			continue
		}
		if code := p2ErrCode(frame); code != r.wantCode {
			t.Errorf("%s: want %s, got %q (frame=%#v)", r.name, r.wantCode, code, frame)
		}
		for k, want := range r.wantData {
			if got := p2ErrDataField(frame, k); got != want {
				t.Errorf("%s: error.data.%s = %#v, want %#v (frame=%#v)", r.name, k, got, want, frame)
			}
		}
		if !exists || deletedAt != "" || etag != 1 {
			t.Errorf("%s: refusal must not mutate; exists=%v deletedAt=%q etag=%d", r.name, exists, deletedAt, etag)
		}
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

	// Comment events preserve their fields and stamp production-time task context.
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var containerUUID string
	if err := database.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&containerUUID); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(map[string]any{"task_id": taskUUID, "comment_id": commentID, "deleted_by_principal_ref": "agent:smokey", "soft_delete": true, "campaign_uuid": nil, "container_uuid": containerUUID})
	want := string(expected)
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
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var containerUUID string
	if err := database.QueryRow("SELECT project_uuid FROM tasks WHERE uuid = ?", taskUUID).Scan(&containerUUID); err != nil {
		t.Fatal(err)
	}
	expected, _ := json.Marshal(map[string]any{"task_id": taskUUID, "comment_id": commentID, "hard_delete": true, "campaign_uuid": nil, "container_uuid": containerUUID})
	want := string(expected)
	if payload != want {
		t.Errorf("comment.purged payload mismatch:\n want: %s\n got:  %s", want, payload)
	}
}
