//go:build wrkq_local

package workrpc_test

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestPromiseRPCOnBehalfNormalizationReadsAndOwnerBoundary(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	createFrames := p2Run(t, dbPath, mkRPC("create", "wrkq.promise.add", map[string]any{
		"ownerPrincipalRef": "lance",
		"onBehalf":          true,
		"subject":           "Check envelope rollout",
		"reviewAt":          "2099-08-24T00:30:00+01:00",
		"principalRef":      "agent:cody",
	}))
	created := p2ResultOrFail(t, createFrames[1], "promise.add")
	id, _ := created["id"].(string)
	uuid, _ := created["uuid"].(string)
	if id == "" || uuid == "" {
		t.Fatalf("created promise identity = %#v", created)
	}
	p2AssertFieldEq(t, created, "ownerPrincipalRef", "agent:lance")
	p2AssertFieldEq(t, created, "createdByPrincipalRef", "agent:cody")
	p2AssertFieldEq(t, created, "reviewAt", "2099-08-23T23:30:00Z")
	p2AssertEtag(t, created)

	frames := p2Run(t, dbPath,
		mkRPC("show", "wrkq.promise.show", map[string]any{"promise": id}),
		mkRPC("list", "wrkq.promise.list", map[string]any{"ownerPrincipalRef": "lance"}),
		mkRPC("ready", "wrkq.promise.ready", map[string]any{"ownerPrincipalRef": "lance"}),
		mkRPC("history", "wrkq.history.listView", map[string]any{"target": id}),
		mkRPC("renew", "wrkq.promise.renew", map[string]any{
			"promise": id, "reviewIn": "7d", "principalRef": "agent:cody",
		}),
		mkRPC("assign", "wrkq.promise.add", map[string]any{
			"ownerPrincipalRef": "lance", "subject": "unsolicited", "reviewIn": "7d", "principalRef": "agent:cody",
		}),
		mkRPC("invalid", "wrkq.promise.add", map[string]any{
			"subject": "invalid time", "reviewAt": "tomorrow", "principalRef": "agent:cody",
		}),
	)
	shown := p2ResultOrFail(t, frames[1], "promise.show")
	if shown["uuid"] != uuid {
		t.Fatalf("show = %#v", shown)
	}
	listed := p2ResultOrFail(t, frames[2], "promise.list")
	items, _ := listed["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("list items = %#v", items)
	}
	ready := p2ResultOrFail(t, frames[3], "promise.ready")
	readyItems, _ := ready["items"].([]any)
	if len(readyItems) != 0 {
		t.Fatalf("future ready items = %#v", readyItems)
	}
	history := p2ResultOrFail(t, frames[4], "promise history")
	historyItems, _ := history["items"].([]any)
	if len(historyItems) != 1 {
		t.Fatalf("history items = %#v", historyItems)
	}
	if code := p2ErrCode(frames[5]); code != "WRKQ_FORBIDDEN" {
		t.Fatalf("non-owner renew code = %q, frame=%#v", code, frames[5])
	}
	if code := p2ErrCode(frames[6]); code != "WRKQ_FORBIDDEN" {
		t.Fatalf("unauthorized assignment code = %q, frame=%#v", code, frames[6])
	}
	if code := p2ErrCode(frames[7]); code != "WRKQ_VALIDATION" {
		t.Fatalf("invalid reviewAt code = %q, frame=%#v", code, frames[7])
	}

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var count int
	var etag int64
	if err := database.QueryRow("SELECT COUNT(*), MAX(etag) FROM promises").Scan(&count, &etag); err != nil {
		t.Fatal(err)
	}
	if count != 1 || etag != 1 {
		t.Fatalf("rejected writes changed promises: count=%d etag=%d", count, etag)
	}
	var payload sql.NullString
	if err := database.QueryRow(`SELECT payload FROM event_log WHERE resource_uuid = ? AND event_type = 'promise.created'`, uuid).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload.String), &event); err != nil {
		t.Fatal(err)
	}
	if event["on_behalf_asserted_by"] != "agent:cody" {
		t.Fatalf("created event = %#v", event)
	}
}
