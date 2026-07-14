package workrpc_test

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
)

type projectRootState struct {
	Root                   *string
	ETag                   int64
	UpdatedByPrincipalRef  string
	ContainerUpdatedEvents int
}

func readProjectRootState(t *testing.T, dbPath, projectUUID string) projectRootState {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()

	var state projectRootState
	var updatedBy sql.NullString
	if err := database.QueryRow(`
		SELECT root, etag, updated_by_principal_ref
		FROM containers WHERE uuid = ?`, projectUUID).
		Scan(&state.Root, &state.ETag, &updatedBy); err != nil {
		t.Fatal(err)
	}
	if updatedBy.Valid {
		state.UpdatedByPrincipalRef = updatedBy.String
	}
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM event_log
		WHERE event_type = 'container.updated' AND resource_uuid = ?`, projectUUID).
		Scan(&state.ContainerUpdatedEvents); err != nil {
		t.Fatal(err)
	}
	return state
}

func TestProjectRootRegistryOverRealRPC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projectUUID := "63000000-6366-4000-8000-000000000001"
	g1SeedProject(t, dbPath, "registry-project", projectUUID)
	taskID := p2SeedTask(t, dbPath, "63000000-6366-4000-8000-000000000002", "registry-task", "Registry task")

	frames := p2Run(t, dbPath,
		mkRPC("set", "wrkq.project.setRoot", map[string]any{
			"project": "registry-project", "root": "~/praesidium/wrkq",
		}),
		mkRPC("list", "wrkq.project.listView", map[string]any{}),
		mkRPC("task", "wrkq.project.setRoot", map[string]any{
			"project": taskID, "root": "~/wrong",
		}),
		mkRPC("clear", "wrkq.project.setRoot", map[string]any{
			"project": "registry-project", "root": "",
		}),
	)

	set := p2ResultOrFail(t, frames[1], "project.setRoot")
	if got := set["root"]; got != "~/praesidium/wrkq" {
		t.Fatalf("setRoot root = %#v, want verbatim tilde path", got)
	}
	listed := p2ResultOrFail(t, frames[2], "project.listView")
	items, _ := listed["items"].([]any)
	found := false
	for _, item := range items {
		row, _ := item.(map[string]any)
		if row["slug"] == "registry-project" {
			found = true
			if row["root"] != "~/praesidium/wrkq" {
				t.Fatalf("listView root = %#v, want stored value", row["root"])
			}
		}
	}
	if !found {
		t.Fatalf("project.listView did not return registry-project: %#v", listed)
	}
	if got := p2ErrCode(frames[3]); got != "WRKQ_VALIDATION" {
		t.Fatalf("task ID setRoot code = %q, want WRKQ_VALIDATION; frame=%#v", got, frames[3])
	}
	if got := g1ErrMessage(frames[3]); got != "--root can only be set on a top-level project; task IDs are not projects" {
		t.Fatalf("task ID setRoot message = %q", got)
	}
	cleared := p2ResultOrFail(t, frames[4], "project.setRoot clear")
	if root, ok := cleared["root"]; !ok || root != nil {
		t.Fatalf("cleared root = %#v (present=%v), want explicit null", root, ok)
	}

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var root *string
	if err := database.QueryRow("SELECT root FROM containers WHERE uuid = ?", projectUUID).Scan(&root); err != nil {
		t.Fatal(err)
	}
	if root != nil {
		t.Fatalf("cleared DB root = %#v, want NULL", root)
	}
}

func TestProjectRootRegistryMethodIsRegistered(t *testing.T) {
	registered := map[string]bool{}
	for _, method := range workrpc.NewRegistry(nil, workrpc.RegistryOptions{}).RegisteredMethods() {
		registered[method] = true
	}
	for _, method := range []string{"wrkq.project.listView", "wrkq.project.setRoot"} {
		if !registered[method] {
			t.Errorf("registry missing %s", method)
		}
	}
}

func TestProjectRootSetRootStaleCASIsAtomic(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projectUUID := "63000000-6366-4000-8000-000000000003"
	g1SeedProject(t, dbPath, "registry-cas", projectUUID)

	seedFrames := p2Run(t, dbPath,
		mkRPC("seed", "wrkq.project.setRoot", map[string]any{
			"project": "registry-cas", "root": "~/before", "actor": "agent:cas-author",
		}),
	)
	p2ResultOrFail(t, seedFrames[1], "project.setRoot CAS seed")
	before := readProjectRootState(t, dbPath, projectUUID)
	if before.Root == nil || *before.Root != "~/before" {
		t.Fatalf("CAS seed root = %#v, want ~/before", before.Root)
	}

	frames := p2Run(t, dbPath,
		mkRPC("stale", "wrkq.project.setRoot", map[string]any{
			"project": "registry-cas", "root": "~/stale", "actor": "agent:stale-author",
			"expectEtag": before.ETag - 1,
		}),
	)
	if code := p2ErrCode(frames[1]); code != "WRKQ_CONFLICT" {
		t.Fatalf("stale setRoot code = %q, want WRKQ_CONFLICT; frame=%#v", code, frames[1])
	}
	after := readProjectRootState(t, dbPath, projectUUID)
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("stale setRoot mutated state:\n before=%#v\n  after=%#v", before, after)
	}
}

func TestProjectRootSetRootCanonicalAttributionAndInvalidIdentityNoOp(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping subprocess in short mode")
	}
	dbPath := migratedDB(t)
	projectUUID := "63000000-6366-4000-8000-000000000004"
	g1SeedProject(t, dbPath, "registry-attribution", projectUUID)

	frames := p2Run(t, dbPath,
		mkRPC("valid", "wrkq.project.setRoot", map[string]any{
			"project": "registry-attribution", "root": "~/attributed", "actor": "agent:registry-author",
		}),
	)
	p2ResultOrFail(t, frames[1], "project.setRoot canonical attribution")
	beforeInvalid := readProjectRootState(t, dbPath, projectUUID)
	if beforeInvalid.Root == nil || *beforeInvalid.Root != "~/attributed" {
		t.Fatalf("attributed root = %#v, want ~/attributed", beforeInvalid.Root)
	}
	if beforeInvalid.UpdatedByPrincipalRef != "agent:registry-author" {
		t.Fatalf("row updated_by_principal_ref = %q, want agent:registry-author", beforeInvalid.UpdatedByPrincipalRef)
	}
	_, _, eventPrincipal := g1LatestEvent(t, dbPath, "container.updated", projectUUID)
	if eventPrincipal != "agent:registry-author" {
		t.Fatalf("event principal_ref = %q, want agent:registry-author", eventPrincipal)
	}

	invalidFrames := p2Run(t, dbPath,
		mkRPC("invalid", "wrkq.project.setRoot", map[string]any{
			"project": "registry-attribution", "root": "~/invalid", "actor": "legacy-bare-actor",
		}),
	)
	if code := p2ErrCode(invalidFrames[1]); code != "WRKQ_VALIDATION" {
		t.Fatalf("invalid identity setRoot code = %q, want WRKQ_VALIDATION; frame=%#v", code, invalidFrames[1])
	}
	afterInvalid := readProjectRootState(t, dbPath, projectUUID)
	if !reflect.DeepEqual(afterInvalid, beforeInvalid) {
		t.Fatalf("invalid identity setRoot mutated state:\n before=%#v\n  after=%#v", beforeInvalid, afterInvalid)
	}
}
