package workrpc_test

// handoff_test.go — server-side acceptance for the handoff family (T-05117).
//
// Covers the wrkq.handoff.create/get/listView/acknowledge contract over the REAL
// `wrkq rpc --stdio` boundary (p2Run): create + idempotent replay + payload
// mismatch, get not-found, listView scope filtering, acknowledge etag CAS +
// already-acknowledged mapping + dryRun no-write, and the durable event_log
// payloads (handoff.created / handoff.acknowledged) the byte-snapshot oracle can't
// see. Scope/actor are EXPLICIT params — the server reads no ASP_* env.

import (
	"database/sql"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

const (
	hoScopeRef  = "agent:cody:project:wrkq"
	hoAgentID   = "cody"
	hoProjectID = "wrkq"
)

func hoCreateParams(title, body string, extra map[string]any) map[string]any {
	p := map[string]any{
		"scopeRef":  hoScopeRef,
		"agentId":   hoAgentID,
		"projectId": hoProjectID,
		"title":     title,
		"body":      body,
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func TestHandoffCreate_PersistsAndReturnsDTO(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.handoff.create", hoCreateParams("first", "body one", nil)),
	)
	res := p2ResultOrFail(t, frames[1], "wrkq.handoff.create")
	handoff, _ := res["handoff"].(map[string]any)
	if handoff == nil {
		t.Fatalf("create result missing handoff: %#v", res)
	}
	p2AssertStr(t, handoff, "uuid")
	p2AssertStr(t, handoff, "id")
	p2AssertFieldEq(t, handoff, "scope_ref", hoScopeRef)
	p2AssertFieldEq(t, handoff, "agent_id", hoAgentID)
	p2AssertFieldEq(t, handoff, "project_id", hoProjectID)
	p2AssertFieldEq(t, handoff, "status", "pending")
	p2AssertFieldEq(t, handoff, "title", "first")
	if replay, _ := res["idempotentReplay"].(bool); replay {
		t.Errorf("fresh create must not be an idempotent replay")
	}

	// Durable handoff.created event payload.
	assertHandoffEvent(t, dbPath, handoff["uuid"].(string), "handoff.created")
}

func TestHandoffCreate_IdempotentReplayAndMismatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.handoff.create", hoCreateParams("same", "same body", map[string]any{"idempotencyKey": "k1"})),
		mkRPC("c2", "wrkq.handoff.create", hoCreateParams("same", "same body", map[string]any{"idempotencyKey": "k1"})),
		mkRPC("c3", "wrkq.handoff.create", hoCreateParams("DIFFERENT", "same body", map[string]any{"idempotencyKey": "k1"})),
	)
	first := p2ResultOrFail(t, frames[1], "create#1")["handoff"].(map[string]any)
	second := p2ResultOrFail(t, frames[2], "create#2")
	secondHandoff := second["handoff"].(map[string]any)
	if !second["idempotentReplay"].(bool) {
		t.Errorf("same-key same-payload replay must set idempotentReplay=true")
	}
	if first["id"] != secondHandoff["id"] {
		t.Errorf("replay must return the original handoff id: %v != %v", first["id"], secondHandoff["id"])
	}
	// Same key, different payload → WRKQ_CONFLICT.
	if code := p2ErrCode(frames[3]); code != "WRKQ_CONFLICT" {
		t.Errorf("payload mismatch want WRKQ_CONFLICT, got %q (frame=%#v)", code, frames[3])
	}
}

func TestHandoffGet_NotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("g1", "wrkq.handoff.get", map[string]any{"handoff": "H-99999"}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_NOT_FOUND" {
		t.Errorf("get missing handoff want WRKQ_NOT_FOUND, got %q", code)
	}
}

func TestHandoffListView_ScopeAndStatusFilter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.handoff.create", hoCreateParams("h1", "b1", nil)),
		mkRPC("c2", "wrkq.handoff.create", hoCreateParams("h2", "b2", nil)),
		mkRPC("l1", "wrkq.handoff.listView", map[string]any{"scopeRef": hoScopeRef, "status": "pending", "limit": 50}),
		// A different scope sees nothing.
		mkRPC("l2", "wrkq.handoff.listView", map[string]any{"scopeRef": "agent:other:project:wrkq", "status": "pending", "limit": 50}),
	)
	list := p2ResultOrFail(t, frames[3], "listView")
	items, _ := list["items"].([]any)
	if len(items) != 2 {
		t.Errorf("listView want 2 pending handoffs in scope, got %d", len(items))
	}
	other := p2ResultOrFail(t, frames[4], "listView/other")
	otherItems, _ := other["items"].([]any)
	if len(otherItems) != 0 {
		t.Errorf("listView for a different scope want 0, got %d", len(otherItems))
	}
}

func TestHandoffAcknowledge_TransitionAndEvent(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.handoff.create", hoCreateParams("ack-me", "body", nil)),
	)
	created := p2ResultOrFail(t, frames[1], "create")["handoff"].(map[string]any)
	id := created["id"].(string)

	ackFrames := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.handoff.acknowledge", map[string]any{
			"handoff": id, "actorAgentId": hoAgentID, "principalRef": "agent:" + hoAgentID,
			"scopeRef": hoScopeRef, "note": "loaded",
		}),
		// Acknowledging again → WRKQ_CONFLICT (already acknowledged).
		mkRPC("a2", "wrkq.handoff.acknowledge", map[string]any{
			"handoff": id, "actorAgentId": hoAgentID, "scopeRef": hoScopeRef,
		}),
	)
	acked := p2ResultOrFail(t, ackFrames[1], "acknowledge")
	if acked["status"] != "acknowledged" {
		t.Errorf("acknowledge want status acknowledged, got %v", acked["status"])
	}
	if acked["acknowledgement_note"] != "loaded" {
		t.Errorf("acknowledge note round-trip: got %v", acked["acknowledgement_note"])
	}
	if code := p2ErrCode(ackFrames[2]); code != "WRKQ_CONFLICT" {
		t.Errorf("re-acknowledge want WRKQ_CONFLICT, got %q", code)
	}
	if reason := p2ErrDataField(ackFrames[2], "reason"); reason != "already_acknowledged" {
		t.Errorf("re-acknowledge want reason already_acknowledged, got %v", reason)
	}
	assertHandoffEvent(t, dbPath, created["uuid"].(string), "handoff.acknowledged")
}

func TestHandoffAcknowledge_IfMatchConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.handoff.create", hoCreateParams("cas", "body", nil)),
	)
	id := p2ResultOrFail(t, frames[1], "create")["handoff"].(map[string]any)["id"].(string)

	ackFrames := p2Run(t, dbPath,
		// Wrong ifMatch (current etag is 1) → conflict.
		mkRPC("a1", "wrkq.handoff.acknowledge", map[string]any{
			"handoff": id, "actorAgentId": hoAgentID, "scopeRef": hoScopeRef, "ifMatch": 99,
		}),
	)
	if code := p2ErrCode(ackFrames[1]); code != "WRKQ_CONFLICT" {
		t.Errorf("ifMatch mismatch want WRKQ_CONFLICT, got %q (frame=%#v)", code, ackFrames[1])
	}
}

func TestHandoffAcknowledge_DryRunNoWrite(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	frames := p2Run(t, dbPath,
		mkRPC("c1", "wrkq.handoff.create", hoCreateParams("dry", "body", nil)),
	)
	created := p2ResultOrFail(t, frames[1], "create")["handoff"].(map[string]any)
	id := created["id"].(string)

	ackFrames := p2Run(t, dbPath,
		mkRPC("a1", "wrkq.handoff.acknowledge", map[string]any{
			"handoff": id, "actorAgentId": hoAgentID, "scopeRef": hoScopeRef, "dryRun": true,
		}),
		// Real get must still show pending (dry run wrote nothing).
		mkRPC("g1", "wrkq.handoff.get", map[string]any{"handoff": id}),
	)
	dry := p2ResultOrFail(t, ackFrames[1], "ack/dryRun")
	if dry["status"] != "acknowledged" {
		t.Errorf("dryRun projection should show acknowledged post-state, got %v", dry["status"])
	}
	current := p2ResultOrFail(t, ackFrames[2], "get-after-dryRun")
	if current["status"] != "pending" {
		t.Errorf("dryRun must NOT write: handoff still pending, got %v", current["status"])
	}
	// No handoff.acknowledged event should exist.
	if n := countHandoffEvents(t, dbPath, created["uuid"].(string), "handoff.acknowledged"); n != 0 {
		t.Errorf("dryRun wrote a handoff.acknowledged event (%d); expected 0", n)
	}
}

// ─── event_log helpers ───────────────────────────────────────────────────────

func assertHandoffEvent(t *testing.T, dbPath, resourceUUID, eventType string) {
	t.Helper()
	if n := countHandoffEvents(t, dbPath, resourceUUID, eventType); n == 0 {
		t.Errorf("expected at least one %q event for handoff %s, found none", eventType, resourceUUID)
	}
}

func countHandoffEvents(t *testing.T, dbPath, resourceUUID, eventType string) int {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open db for event check: %v", err)
	}
	defer func() { _ = database.Close() }()
	var n int
	err = database.QueryRow(
		`SELECT COUNT(*) FROM event_log WHERE resource_type = 'handoff' AND resource_uuid = ? AND event_type = ?`,
		resourceUUID, eventType,
	).Scan(&n)
	if err != nil && err != sql.ErrNoRows {
		t.Fatalf("count events: %v", err)
	}
	return n
}
