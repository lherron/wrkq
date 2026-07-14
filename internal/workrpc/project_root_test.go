package workrpc_test

import (
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
)

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
