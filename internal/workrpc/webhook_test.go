package workrpc_test

// webhook_test.go — server protocol/behavior acceptance tests for the DEDICATED
// global-webhook family wrkq.webhook.add / wrkq.webhook.remove / wrkq.webhook.listView
// (T-05119, daedalus #10211).
//
// Covers:
//   - root resolution server-side (URLs land on the singleton root container)
//   - add idempotency (duplicate add → no-change)
//   - remove idempotency (missing remove → no-change)
//   - invalid add URL → WRKQ_VALIDATION
//   - MAP-ALPHABETICAL mutation result key order ({changed,count,target,webhook_urls} /
//     {changed,webhook_urls})
//   - listView legacy {url:<value>} row shape
//   - container.updated event attribution/payload on a real change
//   - OPTIONAL expectEtag CAS conflict (mirror never sends it; legacy no-CAS preserved)
//
// Driven through the real JSON-RPC stdio surface via p2Run / mkRPC, mirroring
// container_update_test.go.

import (
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// g1RootUUID resolves the singleton root container's UUID.
func g1RootUUID(t *testing.T, dbPath string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1RootUUID: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var uuid string
	if err := database.QueryRow("SELECT uuid FROM containers WHERE kind = 'root'").Scan(&uuid); err != nil {
		t.Fatalf("g1RootUUID: %v", err)
	}
	return uuid
}

// g1RootWebhookURLs reads the decoded webhook_urls slice off the root container.
func g1RootWebhookURLs(t *testing.T, dbPath string) []string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1RootWebhookURLs: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var raw sql.NullString
	if err := database.QueryRow("SELECT webhook_urls FROM containers WHERE kind = 'root'").Scan(&raw); err != nil {
		t.Fatalf("g1RootWebhookURLs: %v", err)
	}
	if !raw.Valid || strings.TrimSpace(raw.String) == "" {
		return nil
	}
	var urls []string
	if err := json.Unmarshal([]byte(raw.String), &urls); err != nil {
		t.Fatalf("g1RootWebhookURLs unmarshal: %v", err)
	}
	return urls
}

// frameResultRaw re-marshals a frame's "result" so map-key ORDER can be asserted
// (the generic decode in resultMap loses key order; re-marshaling a map sorts keys
// alphabetically, which is exactly the contract we are pinning).
func frameResultRaw(t *testing.T, frame map[string]any) string {
	t.Helper()
	if errObj, ok := frame["error"]; ok {
		t.Fatalf("expected result, got error: %v", errObj)
	}
	b, err := json.Marshal(frame["result"])
	if err != nil {
		t.Fatalf("frameResultRaw marshal: %v", err)
	}
	return string(b)
}

// ─── wrkq.webhook.add / remove / listView ────────────────────────────────────

// TestWrkqWebhookAdd_RootResolutionAndResult: add resolves the root server-side,
// writes webhook_urls onto it, and returns the MAP-ALPHABETICAL changed result.
func TestWrkqWebhookAdd_RootResolutionAndResult(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	rootUUID := g1RootUUID(t, dbPath)

	frames := p2Run(t, dbPath,
		mkRPC("w1", "wrkq.webhook.add", map[string]any{"url": "https://hook.test/wrkq"}),
	)
	got := frameResultRaw(t, frames[1])
	const want = `{"changed":true,"count":1,"target":"https://hook.test/wrkq","webhook_urls":["https://hook.test/wrkq"]}`
	if got != want {
		t.Errorf("add changed result key order/shape drifted:\n got: %s\nwant: %s", got, want)
	}
	// The URL landed on the ROOT container (server-side resolution).
	urls := g1RootWebhookURLs(t, dbPath)
	if len(urls) != 1 || urls[0] != "https://hook.test/wrkq" {
		t.Errorf("root webhook_urls: want [https://hook.test/wrkq], got %v", urls)
	}
	if rootUUID == "" {
		t.Error("root UUID must resolve")
	}
}

// TestWrkqWebhookAdd_DuplicateNoChange: adding an already-present URL is idempotent
// → no-change result {changed,webhook_urls}, no event, no etag bump.
func TestWrkqWebhookAdd_DuplicateNoChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	rootUUID := g1RootUUID(t, dbPath)

	frames := p2Run(t, dbPath,
		mkRPC("w1", "wrkq.webhook.add", map[string]any{"url": "https://dup.test/wrkq"}),
	)
	p2ResultOrFail(t, frames[1], "webhook.add seed")
	etagAfterFirst := g1EtagOf(t, dbPath, rootUUID)
	eventsAfterFirst := g1CountEvents(t, dbPath, "container.updated", rootUUID)

	frames = p2Run(t, dbPath,
		mkRPC("w2", "wrkq.webhook.add", map[string]any{"url": "https://dup.test/wrkq"}),
	)
	got := frameResultRaw(t, frames[1])
	const want = `{"changed":false,"webhook_urls":["https://dup.test/wrkq"]}`
	if got != want {
		t.Errorf("duplicate add no-change result drifted:\n got: %s\nwant: %s", got, want)
	}
	if etag := g1EtagOf(t, dbPath, rootUUID); etag != etagAfterFirst {
		t.Errorf("duplicate add must not bump etag: want %d, got %d", etagAfterFirst, etag)
	}
	if ev := g1CountEvents(t, dbPath, "container.updated", rootUUID); ev != eventsAfterFirst {
		t.Errorf("duplicate add must not log a new event: want %d, got %d", eventsAfterFirst, ev)
	}
}

// TestWrkqWebhookRemove_MissingNoChange: removing an absent URL is idempotent →
// no-change result {changed,webhook_urls}.
func TestWrkqWebhookRemove_MissingNoChange(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	frames := p2Run(t, dbPath,
		mkRPC("w1", "wrkq.webhook.remove", map[string]any{"url": "https://absent.test/wrkq"}),
	)
	got := frameResultRaw(t, frames[1])
	const want = `{"changed":false,"webhook_urls":[]}`
	if got != want {
		t.Errorf("missing remove no-change result drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestWrkqWebhookRemove_Changed: removing an existing URL returns the changed
// MAP-ALPHABETICAL result and clears the durable webhook_urls.
func TestWrkqWebhookRemove_Changed(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	p2Run(t, dbPath, mkRPC("w1", "wrkq.webhook.add", map[string]any{"url": "https://rm.test/wrkq"}))
	frames := p2Run(t, dbPath,
		mkRPC("w2", "wrkq.webhook.remove", map[string]any{"url": "https://rm.test/wrkq"}),
	)
	got := frameResultRaw(t, frames[1])
	const want = `{"changed":true,"count":0,"target":"https://rm.test/wrkq","webhook_urls":[]}`
	if got != want {
		t.Errorf("remove changed result drifted:\n got: %s\nwant: %s", got, want)
	}
	if urls := g1RootWebhookURLs(t, dbPath); len(urls) != 0 {
		t.Errorf("root webhook_urls must be empty after remove, got %v", urls)
	}
}

// TestWrkqWebhookAdd_InvalidURLValidation: a non-http(s)/hostless URL → WRKQ_VALIDATION,
// no mutation. The message keeps the legacy wording (no domain-code prefix is part
// of the error.message — the prefix lives in the typed code).
func TestWrkqWebhookAdd_InvalidURLValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	frames := p2Run(t, dbPath,
		mkRPC("w1", "wrkq.webhook.add", map[string]any{"url": "not-a-url"}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_VALIDATION" {
		t.Errorf("invalid url: want WRKQ_VALIDATION, got %q (frame=%#v)", code, frames[1])
	}
	if urls := g1RootWebhookURLs(t, dbPath); len(urls) != 0 {
		t.Errorf("invalid url must not mutate webhook_urls, got %v", urls)
	}
}

// TestWrkqWebhookListView_RowShape: listView returns legacy {url:<value>} rows in
// stored order.
func TestWrkqWebhookListView_RowShape(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	p2Run(t, dbPath, mkRPC("w1", "wrkq.webhook.add", map[string]any{"url": "https://a.test/wrkq"}))
	p2Run(t, dbPath, mkRPC("w2", "wrkq.webhook.add", map[string]any{"url": "https://b.test/wrkq"}))
	frames := p2Run(t, dbPath, mkRPC("w3", "wrkq.webhook.listView", map[string]any{}))
	got := frameResultRaw(t, frames[1])
	const want = `[{"url":"https://a.test/wrkq"},{"url":"https://b.test/wrkq"}]`
	if got != want {
		t.Errorf("listView row shape/order drifted:\n got: %s\nwant: %s", got, want)
	}
}

// TestWrkqWebhookAdd_EventAttribution: a real add logs a container.updated event on
// the ROOT container carrying the new webhook_urls, the bumped etag, and the
// caller's principal attribution.
func TestWrkqWebhookAdd_EventAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	rootUUID := g1RootUUID(t, dbPath)
	beforeEtag := g1EtagOf(t, dbPath, rootUUID)

	frames := p2Run(t, dbPath,
		mkRPC("w1", "wrkq.webhook.add", map[string]any{"url": "https://evt.test/wrkq", "actor": "clod"}),
	)
	p2ResultOrFail(t, frames[1], "webhook.add event")

	if g1CountEvents(t, dbPath, "container.updated", rootUUID) == 0 {
		t.Fatalf("container.updated event not logged for root uuid=%s", rootUUID)
	}
	payload, eventEtag, principalRef := g1LatestEvent(t, dbPath, "container.updated", rootUUID)
	if eventEtag != beforeEtag+1 {
		t.Errorf("event etag: want %d, got %d", beforeEtag+1, eventEtag)
	}
	if !strings.Contains(payload, "https://evt.test/wrkq") {
		t.Errorf("event payload must carry the new webhook url, got %q", payload)
	}
	if principalRef == "" {
		t.Error("event must record principal attribution (principal_ref)")
	}
}

// TestWrkqWebhookAdd_StaleEtagConflict: the OPTIONAL expectEtag CAS rejects a stale
// etag with WRKQ_CONFLICT (the CLI mirror never sends it, so legacy no-CAS
// last-writer-wins is preserved; raw RPC callers may opt in to reduce that risk).
func TestWrkqWebhookAdd_StaleEtagConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	rootUUID := g1RootUUID(t, dbPath)
	staleEtag := g1EtagOf(t, dbPath, rootUUID) + 99

	frames := p2Run(t, dbPath,
		mkRPC("w1", "wrkq.webhook.add", map[string]any{
			"url":        "https://cas.test/wrkq",
			"expectEtag": staleEtag,
		}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_CONFLICT" {
		t.Errorf("stale etag: want WRKQ_CONFLICT, got %q (frame=%#v)", code, frames[1])
	}
	// The conflict message must be stable + implementation-free (no SQLite/store leak).
	errObj, _ := frames[1]["error"].(map[string]any)
	msg, _ := errObj["message"].(string)
	for _, banned := range []string{"UNIQUE", "constraint", "sqlite", "SQLITE", "containers", "webhook_urls"} {
		if strings.Contains(msg, banned) {
			t.Errorf("stale-etag conflict message leaks store/SQLite text %q: %q", banned, msg)
		}
	}
	if urls := g1RootWebhookURLs(t, dbPath); len(urls) != 0 {
		t.Errorf("stale-etag conflict must not mutate webhook_urls, got %v", urls)
	}
}
