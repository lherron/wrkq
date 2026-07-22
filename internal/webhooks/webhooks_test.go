package webhooks_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

func setupTestDB(t *testing.T) *db.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestNoProductionContextFreeDispatchCallSites(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("failed to resolve test path")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	searchRoots := []string{
		filepath.Join(repoRoot, "internal", "rpccli"),
		filepath.Join(repoRoot, "internal", "wrkqapi"),
		filepath.Join(repoRoot, "internal", "wrkqd"),
		filepath.Join(repoRoot, "internal", "store"),
	}

	for _, root := range searchRoots {
		err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			text := string(body)
			if strings.Contains(text, "webhooks.DispatchTask(") || strings.Contains(text, "webhooks.DispatchTaskInfo(") {
				t.Fatalf("context-free webhook dispatch call remains in %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("failed to scan %s: %v", root, err)
		}
	}
}

func TestOriginCausationRefOmittedWhenAbsent(t *testing.T) {
	raw, err := json.Marshal(webhooks.Origin{Actor: "agent:test", Via: "cli"})
	if err != nil {
		t.Fatalf("marshal origin: %v", err)
	}
	if strings.Contains(string(raw), "causation_ref") {
		t.Fatalf("origin without causation ref must omit field, got %s", raw)
	}
}

func setupTestActor(t *testing.T, database *db.DB) string {
	t.Helper()
	result, err := database.Exec(`
		INSERT INTO actors (id, slug, role) VALUES ('', 'test-actor', 'human')
	`)
	if err != nil {
		t.Fatalf("failed to create test actor: %v", err)
	}
	rowID, _ := result.LastInsertId()
	var uuid string
	if err := database.QueryRow("SELECT uuid FROM actors WHERE rowid = ?", rowID).Scan(&uuid); err != nil {
		t.Fatalf("failed to get actor uuid: %v", err)
	}
	return uuid
}

func TestResolveWebhookTargets(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := store.New(database)

	root, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "proj", Kind: "project"})
	if err != nil {
		t.Fatalf("failed to create project container: %v", err)
	}
	child, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "child", ParentUUID: &root.UUID})
	if err != nil {
		t.Fatalf("failed to create child container: %v", err)
	}

	rootURLs := []string{
		"http://example.com/hook/{ticket_id}",
		"ftp://invalid.example.com/hook",
	}
	childURLs := []string{
		"http://example.com/hook/{ticket_id}",
		"http://example.com/other/",
	}

	rootJSON, _ := json.Marshal(rootURLs)
	childJSON, _ := json.Marshal(childURLs)

	if _, err := s.Containers.UpdateFields(actorUUID, root.UUID, map[string]interface{}{"webhook_urls": string(rootJSON)}, 0); err != nil {
		t.Fatalf("failed to set root webhook urls: %v", err)
	}
	if _, err := s.Containers.UpdateFields(actorUUID, child.UUID, map[string]interface{}{"webhook_urls": string(childJSON)}, 0); err != nil {
		t.Fatalf("failed to set child webhook urls: %v", err)
	}

	payload := webhooks.Payload{TicketID: "T-00001", ProjectID: "P-00001"}
	urls, err := webhooks.ResolveWebhookTargets(database, child.UUID, payload)
	if err != nil {
		t.Fatalf("ResolveWebhookTargets failed: %v", err)
	}

	expected := []string{
		"http://example.com/hook/T-00001",
		"http://example.com/other",
	}

	if !reflect.DeepEqual(urls, expected) {
		t.Fatalf("unexpected urls\nexpected: %v\nactual:   %v", expected, urls)
	}
}

// TestResolveWebhookTargets_BareStringDefaultsToAllEvents asserts that a
// legacy/bare URL string with no explicit events receives BOTH task and
// workflow events, while an object-form subscriber can still narrow to
// task-only.
func TestResolveWebhookTargets_BareStringDefaultsToAllEvents(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	s := store.New(database)

	root, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "proj", Kind: "project"})
	if err != nil {
		t.Fatalf("failed to create project container: %v", err)
	}

	// Mixed subscriptions: a bare string (default => all events) and an
	// object-form subscriber narrowed to task-only.
	rootJSON := `[` +
		`"http://example.com/all",` +
		`{"url":"http://example.com/task-only","events":["task.*"]}` +
		`]`
	if _, err := s.Containers.UpdateFields(actorUUID, root.UUID, map[string]interface{}{"webhook_urls": rootJSON}, 0); err != nil {
		t.Fatalf("failed to set root webhook urls: %v", err)
	}

	taskEvent := webhooks.Payload{TicketID: "T-00001", ProjectID: "P-00001", Event: "updated"}
	workflowEvent := webhooks.Payload{TicketID: "T-00001", ProjectID: "P-00001", Event: webhooks.EventWorkflowAttached}

	taskURLs, err := webhooks.ResolveWebhookTargets(database, root.UUID, taskEvent)
	if err != nil {
		t.Fatalf("ResolveWebhookTargets(task) failed: %v", err)
	}
	// Task event reaches both subscribers.
	if want := []string{"http://example.com/all", "http://example.com/task-only"}; !reflect.DeepEqual(taskURLs, want) {
		t.Fatalf("task event targets\nexpected: %v\nactual:   %v", want, taskURLs)
	}

	workflowURLs, err := webhooks.ResolveWebhookTargets(database, root.UUID, workflowEvent)
	if err != nil {
		t.Fatalf("ResolveWebhookTargets(workflow) failed: %v", err)
	}
	// Workflow event reaches ONLY the bare-string (default-all) subscriber;
	// the task-only object subscriber is excluded.
	if want := []string{"http://example.com/all"}; !reflect.DeepEqual(workflowURLs, want) {
		t.Fatalf("workflow event targets\nexpected: %v\nactual:   %v", want, workflowURLs)
	}
}
