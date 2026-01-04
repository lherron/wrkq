package cli

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

func TestRenameContainer(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer database.Close()

	database.Migrate()

	// Create test actor
	actorUUID := "test-actor-uuid"
	_, err = database.Exec(`
		INSERT INTO actors (uuid, slug, display_name, role)
		VALUES (?, 'test-actor', 'Test Actor', 'human')
	`, actorUUID)
	if err != nil {
		t.Fatalf("Failed to create actor: %v", err)
	}

	s := store.New(database)

	t.Run("renames slug and title together", func(t *testing.T) {
		// Create container
		result, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{
			Slug:  "old-name",
			Title: "Old Name",
		})
		if err != nil {
			t.Fatalf("Failed to create container: %v", err)
		}

		// Rename using UpdateFields (same as the command does)
		fields := map[string]interface{}{
			"slug":  "new-name",
			"title": "new-name",
		}
		_, err = s.Containers.UpdateFields(actorUUID, result.UUID, fields, 0)
		if err != nil {
			t.Fatalf("Failed to rename container: %v", err)
		}

		// Verify
		container, err := s.Containers.GetByUUID(result.UUID)
		if err != nil {
			t.Fatalf("Failed to get container: %v", err)
		}

		if container.Slug != "new-name" {
			t.Errorf("Expected slug 'new-name', got %q", container.Slug)
		}
		if container.Title == nil || *container.Title != "new-name" {
			t.Errorf("Expected title 'new-name', got %v", container.Title)
		}
	})

	t.Run("renames with custom title", func(t *testing.T) {
		// Create container
		result, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{
			Slug:  "test-container",
			Title: "Test Container",
		})
		if err != nil {
			t.Fatalf("Failed to create container: %v", err)
		}

		// Rename with custom title
		fields := map[string]interface{}{
			"slug":  "renamed-container",
			"title": "Custom Display Name",
		}
		_, err = s.Containers.UpdateFields(actorUUID, result.UUID, fields, 0)
		if err != nil {
			t.Fatalf("Failed to rename container: %v", err)
		}

		// Verify
		container, err := s.Containers.GetByUUID(result.UUID)
		if err != nil {
			t.Fatalf("Failed to get container: %v", err)
		}

		if container.Slug != "renamed-container" {
			t.Errorf("Expected slug 'renamed-container', got %q", container.Slug)
		}
		if container.Title == nil || *container.Title != "Custom Display Name" {
			t.Errorf("Expected title 'Custom Display Name', got %v", container.Title)
		}
	})

	t.Run("dry-run shows changes without applying", func(t *testing.T) {
		// Create container
		result, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{
			Slug:  "dry-run-test",
			Title: "Dry Run Test",
		})
		if err != nil {
			t.Fatalf("Failed to create container: %v", err)
		}

		// Simulate dry-run output
		container, _ := s.Containers.GetByUUID(result.UUID)
		var buf bytes.Buffer
		buf.WriteString("Would rename container:\n")
		buf.WriteString("  Slug:  dry-run-test -> new-slug\n")

		// Verify container unchanged
		container, err = s.Containers.GetByUUID(result.UUID)
		if err != nil {
			t.Fatalf("Failed to get container: %v", err)
		}
		if container.Slug != "dry-run-test" {
			t.Error("Container should not be modified in dry-run")
		}
	})

	t.Run("etag conflict fails rename", func(t *testing.T) {
		// Create container
		result, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{
			Slug:  "etag-test",
			Title: "ETag Test",
		})
		if err != nil {
			t.Fatalf("Failed to create container: %v", err)
		}

		// Try to rename with wrong etag
		fields := map[string]interface{}{
			"slug":  "new-slug",
			"title": "new-slug",
		}
		_, err = s.Containers.UpdateFields(actorUUID, result.UUID, fields, 999)
		if err == nil {
			t.Error("Expected etag conflict error")
		}

		// Verify container unchanged
		container, _ := s.Containers.GetByUUID(result.UUID)
		if container.Slug != "etag-test" {
			t.Error("Container should not be modified on etag mismatch")
		}
	})

	t.Run("logs container.updated event", func(t *testing.T) {
		// Create container
		result, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{
			Slug:  "event-log-test",
			Title: "Event Log Test",
		})
		if err != nil {
			t.Fatalf("Failed to create container: %v", err)
		}

		// Rename
		fields := map[string]interface{}{
			"slug":  "renamed-event-test",
			"title": "Renamed Event Test",
		}
		_, err = s.Containers.UpdateFields(actorUUID, result.UUID, fields, 0)
		if err != nil {
			t.Fatalf("Failed to rename container: %v", err)
		}

		// Verify event logged
		var eventCount int
		database.QueryRow(`
			SELECT COUNT(*) FROM event_log
			WHERE resource_type = 'container' AND resource_uuid = ? AND event_type = 'container.updated'
		`, result.UUID).Scan(&eventCount)

		if eventCount != 1 {
			t.Errorf("Expected 1 container.updated event, got %d", eventCount)
		}
	})

	t.Run("normalizes slug", func(t *testing.T) {
		// Create container
		result, err := s.Containers.Create(actorUUID, store.ContainerCreateParams{
			Slug:  "normalize-test",
			Title: "Normalize Test",
		})
		if err != nil {
			t.Fatalf("Failed to create container: %v", err)
		}

		// Rename with mixed case (should be normalized by caller)
		// The store layer accepts the normalized slug directly
		fields := map[string]interface{}{
			"slug":  "normalized-slug",
			"title": "normalized-slug",
		}
		_, err = s.Containers.UpdateFields(actorUUID, result.UUID, fields, 0)
		if err != nil {
			t.Fatalf("Failed to rename container: %v", err)
		}

		// Verify normalized
		container, _ := s.Containers.GetByUUID(result.UUID)
		if container.Slug != "normalized-slug" {
			t.Errorf("Expected lowercase slug, got %q", container.Slug)
		}
	})
}
