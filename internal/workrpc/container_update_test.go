//go:build wrkq_local

package workrpc_test

// container_update_test.go — server protocol/behavior acceptance tests for the
// NEW method wrkq.container.update (T-05112, daedalus hrcchat#10196).
//
// Covers the narrow {slug?, title?} patch contract:
//   - empty-patch validation
//   - server-side slug normalization + validation
//   - title patch
//   - unknown patch-field rejection (overbroad-sink guard)
//   - stale expectEtag → WRKQ_CONFLICT
//   - slug conflict → typed WRKQ_CONFLICT (not a raw store-error leak)
//   - identity retained (uuid/id survive the rename)
//   - children still attached after the rename
//   - path views reflect the new slug
//   - container.updated event payload / etag / attribution
//
// Driven through the real JSON-RPC stdio surface via p2Run / g1* helpers, mirroring
// container_mutations_test.go. Seeding uses direct DB SQL.

import (
	"fmt"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// g1ErrMessage extracts the top-level JSON-RPC error.message from a response frame.
func g1ErrMessage(frame map[string]any) string {
	errObj, _ := frame["error"].(map[string]any)
	msg, _ := errObj["message"].(string)
	return msg
}

// g1ContainerField reads a single column for a container by UUID.
func g1ContainerField(t *testing.T, dbPath, containerUUID, column string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1ContainerField: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var v string
	if err := database.QueryRow("SELECT "+column+" FROM containers WHERE uuid = ?", containerUUID).Scan(&v); err != nil {
		t.Fatalf("g1ContainerField(%s): %v", column, err)
	}
	return v
}

// g1ContainerPath reads the computed path of a container from v_container_paths.
func g1ContainerPath(t *testing.T, dbPath, containerUUID string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1ContainerPath: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var path string
	if err := database.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", containerUUID).Scan(&path); err != nil {
		t.Fatalf("g1ContainerPath: %v", err)
	}
	return path
}

// g1LatestEventPayload returns the most recent event_log payload for the given
// type + resource UUID (and the recorded etag + principal_ref), for attribution
// + payload assertions.
func g1LatestEvent(t *testing.T, dbPath, eventType, resourceUUID string) (payload string, etag int64, principalRef string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("g1LatestEvent: db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var p, pr *string
	var e *int64
	if err := database.QueryRow(
		`SELECT payload, etag, principal_ref FROM event_log
		   WHERE event_type = ? AND resource_uuid = ?
		   ORDER BY id DESC LIMIT 1`, eventType, resourceUUID,
	).Scan(&p, &e, &pr); err != nil {
		t.Fatalf("g1LatestEvent(%s): %v", eventType, err)
	}
	if p != nil {
		payload = *p
	}
	if e != nil {
		etag = *e
	}
	if pr != nil {
		principalRef = *pr
	}
	return payload, etag, principalRef
}

// ─── wrkq.container.update ───────────────────────────────────────────────────

// TestWrkqContainerUpdate_SlugNormalized: the server normalizes the slug
// (e.g. "New_Proj Name" → "new-proj-name") and the returned DTO + DB reflect it.
func TestWrkqContainerUpdate_SlugNormalized(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID := "g1000000-5200-4000-8000-000000000001"
	g1SeedProject(t, dbPath, "g1-upd-norm", projUUID)

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.container.update", map[string]any{
			"container": "g1-upd-norm",
			"patch":     map[string]any{"slug": "New_Proj Name"},
		}),
	)
	result := p2ResultOrFail(t, frames[1], "container.update normalize")
	if got, _ := result["slug"].(string); got != "new-proj-name" {
		t.Errorf("normalized slug: want %q, got %q", "new-proj-name", got)
	}
	if got := g1ContainerField(t, dbPath, projUUID, "slug"); got != "new-proj-name" {
		t.Errorf("DB slug after normalize: want %q, got %q", "new-proj-name", got)
	}
}

// TestWrkqContainerUpdate_TitlePatch: a title-only patch updates the title and
// leaves the slug intact.
func TestWrkqContainerUpdate_TitlePatch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID := "g1000000-5300-4000-8000-000000000001"
	g1SeedProject(t, dbPath, "g1-upd-title", projUUID)

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.container.update", map[string]any{
			"container": "g1-upd-title",
			"patch":     map[string]any{"title": "Fresh Title"},
		}),
	)
	result := p2ResultOrFail(t, frames[1], "container.update title")
	if got, _ := result["title"].(string); got != "Fresh Title" {
		t.Errorf("title patch: want %q, got %q", "Fresh Title", got)
	}
	if got, _ := result["slug"].(string); got != "g1-upd-title" {
		t.Errorf("title patch must not change slug, got %q", got)
	}
}

// TestContainerUpdateRefusesMalformedPatchNamingTheField: every malformed patch —
// absent, empty, an unknown/immutable key (the overbroad-mutation-sink guard), or
// a slug that cannot normalize — is refused with WRKQ_VALIDATION whose
// error.data.field names the offending field, and the container is unchanged.
func TestContainerUpdateRefusesMalformedPatchNamingTheField(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID := "g1000000-5400-4000-8000-000000000001"
	g1SeedProject(t, dbPath, "g1-upd-unknown", projUUID)
	beforeEtag := g1EtagOf(t, dbPath, projUUID)

	type row struct {
		name      string
		params    map[string]any // merged over {"container": "g1-upd-unknown"}
		wantField string
		wantMsg   string // required substring of error.message
	}
	rows := []row{
		{name: "patch absent", params: map[string]any{}, wantField: "patch", wantMsg: "patch is required"},
		{name: "patch empty", params: map[string]any{"patch": map[string]any{}}, wantField: "patch", wantMsg: "at least one field"},
		{name: "invalid slug", params: map[string]any{"patch": map[string]any{"slug": "!!!"}}, wantField: "slug"},
	}
	for _, key := range []string{"kind", "parentUuid", "webhookUrls", "archivedAt", "bogus"} {
		rows = append(rows, row{name: "unknown key " + key, params: map[string]any{"patch": map[string]any{key: "x"}},
			wantField: key, wantMsg: "unknown or immutable patch field"})
	}
	var reqs []string
	for i, r := range rows {
		params := map[string]any{"container": "g1-upd-unknown"}
		for k, v := range r.params {
			params[k] = v
		}
		reqs = append(reqs, mkRPC(fmt.Sprintf("u%d", i), "wrkq.container.update", params))
	}
	frames := p2Run(t, dbPath, reqs...)

	for i, r := range rows {
		frame := frames[1+i]
		if code := p2ErrCode(frame); code != "WRKQ_VALIDATION" {
			t.Errorf("%s: want WRKQ_VALIDATION, got %q (frame=%#v)", r.name, code, frame)
		}
		if field := p2ErrDataField(frame, "field"); field != r.wantField {
			t.Errorf("%s: error.data.field = %#v, want %q", r.name, field, r.wantField)
		}
		if r.wantMsg != "" && !strings.Contains(g1ErrMessage(frame), r.wantMsg) {
			t.Errorf("%s: error.message = %q, want it to contain %q", r.name, g1ErrMessage(frame), r.wantMsg)
		}
	}
	if got := g1ContainerField(t, dbPath, projUUID, "slug"); got != "g1-upd-unknown" {
		t.Errorf("slug must be unchanged after refused patches, got %q", got)
	}
	if got := g1EtagOf(t, dbPath, projUUID); got != beforeEtag {
		t.Errorf("etag must be unchanged after refused patches: want %d, got %d", beforeEtag, got)
	}
}

// TestWrkqContainerUpdate_StaleEtagConflict: a stale expectEtag → WRKQ_CONFLICT, no mutation.
func TestWrkqContainerUpdate_StaleEtagConflict(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID := "g1000000-5500-4000-8000-000000000001"
	g1SeedProject(t, dbPath, "g1-upd-stale", projUUID)
	staleEtag := g1EtagOf(t, dbPath, projUUID) + 99

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.container.update", map[string]any{
			"container":  "g1-upd-stale",
			"patch":      map[string]any{"slug": "g1-upd-stale-new"},
			"expectEtag": staleEtag,
		}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_CONFLICT" {
		t.Errorf("stale etag: want WRKQ_CONFLICT, got %q (frame=%#v)", code, frames[1])
	}
	// The stale-etag conflict message must also be stable + implementation-free —
	// no raw store/SQLite text.
	msg := g1ErrMessage(frames[1])
	for _, banned := range []string{
		"UNIQUE", "constraint", "failed to update container",
		"containers", "parent_uuid", "sql", "SQLITE", "sqlite",
	} {
		if strings.Contains(msg, banned) {
			t.Errorf("stale-etag conflict message leaks store/SQLite text %q: %q", banned, msg)
		}
	}
	if got := g1ContainerField(t, dbPath, projUUID, "slug"); got != "g1-upd-stale" {
		t.Errorf("slug must be unchanged after stale-etag conflict, got %q", got)
	}
}

// TestWrkqContainerUpdate_SlugConflictTyped: renaming onto an existing sibling
// slug → typed WRKQ_CONFLICT (NOT a raw UNIQUE-constraint store leak), no mutation.
func TestWrkqContainerUpdate_SlugConflictTyped(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	aUUID := "g1000000-5600-4000-8000-000000000001"
	bUUID := "g1000000-5600-4000-8000-000000000002"
	g1SeedProject(t, dbPath, "g1-conf-alpha", aUUID)
	g1SeedProject(t, dbPath, "g1-conf-beta", bUUID)

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.container.update", map[string]any{
			"container": "g1-conf-alpha",
			"patch":     map[string]any{"slug": "g1-conf-beta"},
		}),
	)
	code := p2ErrCode(frames[1])
	if code != "WRKQ_CONFLICT" {
		t.Errorf("slug conflict: want WRKQ_CONFLICT, got %q (frame=%#v)", code, frames[1])
	}
	if g1IsMethodNotFound(frames[1]) {
		t.Error("slug conflict: method not registered")
	}
	// The conflict message MUST be a stable, implementation-free string — it must
	// NOT leak any raw SQLite / store text (UNIQUE-constraint wording, the
	// "failed to update container" store wrapper, or table/column identifiers).
	msg := g1ErrMessage(frames[1])
	for _, banned := range []string{
		"UNIQUE", "unique", "constraint", "failed to update container",
		"containers", "parent_uuid", "slug = ", "sql", "SQLITE", "sqlite",
	} {
		if strings.Contains(msg, banned) {
			t.Errorf("slug conflict message leaks store/SQLite text %q: %q", banned, msg)
		}
	}
	if msg == "" {
		t.Error("slug conflict: expected a non-empty conflict message")
	}
	if got := g1ContainerField(t, dbPath, aUUID, "slug"); got != "g1-conf-alpha" {
		t.Errorf("slug must be unchanged after conflict, got %q", got)
	}
}

// TestWrkqContainerUpdate_IdentityRetained: the rename preserves the container's
// UUID + friendly ID (it is an in-place UPDATE, never delete+recreate), and its
// child containers and tasks stay parented to it.
func TestWrkqContainerUpdate_IdentityRetained(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID := "g1000000-5700-4000-8000-000000000001"
	childUUID := "g1000000-5700-4000-8000-000000000002"
	taskUUID := "g1000000-5700-4000-8000-000000000003"
	g1SeedProject(t, dbPath, "g1-upd-identity", projUUID)
	g1SeedDirectory(t, dbPath, "g1-child-dir", childUUID, projUUID)
	g1SeedTask(t, dbPath, "g1-child-task", taskUUID, projUUID)
	beforeID := g1ContainerField(t, dbPath, projUUID, "id")

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.container.update", map[string]any{
			"container": "g1-upd-identity",
			"patch":     map[string]any{"slug": "g1-upd-identity-renamed"},
		}),
	)
	result := p2ResultOrFail(t, frames[1], "container.update identity")
	if got, _ := result["uuid"].(string); got != projUUID {
		t.Errorf("UUID must survive rename: want %q, got %q", projUUID, got)
	}
	if got, _ := result["id"].(string); got != beforeID {
		t.Errorf("friendly ID must survive rename: want %q, got %q", beforeID, got)
	}
	// The same UUID row still exists (not deleted+recreated).
	if !g1RowExists(t, dbPath, "containers", "uuid = ?", projUUID) {
		t.Error("container UUID row must still exist after rename")
	}
	// Children stay parented to the renamed container.
	if !g1RowExists(t, dbPath, "containers", "uuid = ? AND parent_uuid = ?", childUUID, projUUID) {
		t.Error("child container must remain attached to the renamed parent")
	}
	if !g1RowExists(t, dbPath, "tasks", "uuid = ? AND project_uuid = ?", taskUUID, projUUID) {
		t.Error("task must remain attached to the renamed container")
	}
}

// TestWrkqContainerUpdate_PathViewsReflectNewSlug: v_container_paths (and the DTO
// path) reflect the new slug, and descendant paths rebuild under it.
func TestWrkqContainerUpdate_PathViewsReflectNewSlug(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID := "g1000000-5900-4000-8000-000000000001"
	childUUID := "g1000000-5900-4000-8000-000000000002"
	g1SeedProject(t, dbPath, "g1-upd-path", projUUID)
	g1SeedDirectory(t, dbPath, "g1-path-child", childUUID, projUUID)

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.container.update", map[string]any{
			"container": "g1-upd-path",
			"patch":     map[string]any{"slug": "g1-upd-path-new"},
		}),
	)
	result := p2ResultOrFail(t, frames[1], "container.update path")
	if got, _ := result["path"].(string); got != "g1-upd-path-new" {
		t.Errorf("DTO path must reflect new slug: want %q, got %q", "g1-upd-path-new", got)
	}
	if got := g1ContainerPath(t, dbPath, projUUID); got != "g1-upd-path-new" {
		t.Errorf("v_container_paths must reflect new slug: want %q, got %q", "g1-upd-path-new", got)
	}
	// The descendant path rebuilds under the renamed parent.
	if got := g1ContainerPath(t, dbPath, childUUID); got != "g1-upd-path-new/g1-path-child" {
		t.Errorf("descendant path must rebuild under renamed parent: want %q, got %q",
			"g1-upd-path-new/g1-path-child", got)
	}
}

// TestWrkqContainerUpdate_EventPayloadEtagAttribution: the rename logs a
// container.updated event carrying the changed slug/title, the bumped etag, and
// the caller's principal attribution.
func TestWrkqContainerUpdate_EventPayloadEtagAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projUUID := "g1000000-5a00-4000-8000-000000000001"
	g1SeedProject(t, dbPath, "g1-upd-event", projUUID)
	beforeEtag := g1EtagOf(t, dbPath, projUUID)

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.container.update", map[string]any{
			"container": "g1-upd-event",
			"patch":     map[string]any{"slug": "g1-upd-event-new", "title": "Event Title"},
			"actor":     "agent:clod",
		}),
	)
	result := p2ResultOrFail(t, frames[1], "container.update event")

	// etag bumped in the DTO + DB.
	gotEtag, _ := result["etag"].(float64)
	if int64(gotEtag) != beforeEtag+1 {
		t.Errorf("DTO etag: want %d, got %d", beforeEtag+1, int64(gotEtag))
	}

	if g1CountEvents(t, dbPath, "container.updated", projUUID) == 0 {
		t.Fatalf("container.updated event not logged for uuid=%s", projUUID)
	}
	payload, eventEtag, principalRef := g1LatestEvent(t, dbPath, "container.updated", projUUID)
	if eventEtag != beforeEtag+1 {
		t.Errorf("event etag: want %d, got %d", beforeEtag+1, eventEtag)
	}
	if !strings.Contains(payload, "g1-upd-event-new") || !strings.Contains(payload, "Event Title") {
		t.Errorf("event payload must carry the changed slug+title, got %q", payload)
	}
	if principalRef == "" {
		t.Error("event must record principal attribution (principal_ref)")
	}
}

// TestWrkqContainerUpdate_UnknownContainerNotFound: an unresolvable selector →
// WRKQ_NOT_FOUND.
func TestWrkqContainerUpdate_UnknownContainerNotFound(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)

	frames := p2Run(t, dbPath,
		mkRPC("u1", "wrkq.container.update", map[string]any{
			"container": "no-such-container",
			"patch":     map[string]any{"slug": "whatever"},
		}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_NOT_FOUND" {
		t.Errorf("unknown container: want WRKQ_NOT_FOUND, got %q (frame=%#v)", code, frames[1])
	}
}
