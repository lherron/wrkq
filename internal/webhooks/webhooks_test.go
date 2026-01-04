package webhooks_test

import (
	"encoding/json"
	"path/filepath"
	"reflect"
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
	t.Cleanup(func() { database.Close() })
	return database
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

	root, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{Slug: "root"})
	if err != nil {
		t.Fatalf("failed to create root container: %v", err)
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
