package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
)

// ContainerStore handles container persistence operations.
type ContainerStore struct {
	store *Store
}

// ContainerCreateParams contains parameters for creating a new container.
type ContainerCreateParams struct {
	Slug       string
	Title      string // defaults to Slug if empty
	ParentUUID *string
}

// ContainerCreateResult contains the result of container creation.
type ContainerCreateResult struct {
	UUID string
	ID   string
	ETag int64
}

// Create creates a new container and logs a container.created event.
func (cs *ContainerStore) Create(actorUUID string, params ContainerCreateParams) (*ContainerCreateResult, error) {
	var result *ContainerCreateResult

	// Default title to slug if not provided
	title := params.Title
	if title == "" {
		title = params.Slug
	}

	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		res, err := tx.Exec(`
			INSERT INTO containers (id, slug, title, parent_uuid, created_by_actor_uuid, updated_by_actor_uuid)
			VALUES ('', ?, ?, ?, ?, ?)
		`, params.Slug, title, params.ParentUUID, actorUUID, actorUUID)
		if err != nil {
			return fmt.Errorf("failed to create container: %w", err)
		}

		// Get the UUID and ID of the created container
		rowID, err := res.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get last insert ID: %w", err)
		}

		var uuid, id string
		var etag int64
		err = tx.QueryRow("SELECT uuid, id, etag FROM containers WHERE rowid = ?", rowID).Scan(&uuid, &id, &etag)
		if err != nil {
			return fmt.Errorf("failed to get container UUID: %w", err)
		}

		// Log event with structured payload
		payload := map[string]interface{}{
			"slug":  params.Slug,
			"title": title,
		}
		if params.ParentUUID != nil {
			payload["parent_uuid"] = *params.ParentUUID
		}

		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal event payload: %w", err)
		}
		payloadStr := string(payloadJSON)

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "container",
			ResourceUUID: &uuid,
			EventType:    "container.created",
			ETag:         &etag,
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		result = &ContainerCreateResult{
			UUID: uuid,
			ID:   id,
			ETag: etag,
		}
		return nil
	})

	return result, err
}

// UpdateFields updates specified fields on a container and logs a container.updated event.
// Returns the new etag on success.
func (cs *ContainerStore) UpdateFields(actorUUID, containerUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	var newETag int64

	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current etag
		var currentETag int64
		err := tx.QueryRow("SELECT etag FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to get current etag: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Build UPDATE query
		var setClauses []string
		var args []interface{}

		for key, value := range fields {
			setClauses = append(setClauses, fmt.Sprintf("%s = ?", key))
			args = append(args, value)
		}

		// Increment etag and update actor
		setClauses = append(setClauses, "etag = etag + 1")
		setClauses = append(setClauses, "updated_by_actor_uuid = ?")
		args = append(args, actorUUID)

		// Add WHERE clause
		args = append(args, containerUUID)

		query := fmt.Sprintf("UPDATE containers SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
		_, err = tx.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("failed to update container: %w", err)
		}

		// Log event with structured payload
		changesJSON, err := json.Marshal(fields)
		if err != nil {
			return fmt.Errorf("failed to marshal changes: %w", err)
		}
		changesStr := string(changesJSON)
		newETag = currentETag + 1

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "container",
			ResourceUUID: &containerUUID,
			EventType:    "container.updated",
			ETag:         &newETag,
			Payload:      &changesStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		return nil
	})

	return newETag, err
}

// Move moves a container to a different parent and logs a container.moved event.
// Returns the new etag on success.
func (cs *ContainerStore) Move(actorUUID, containerUUID string, newParentUUID *string, ifMatch int64) (int64, error) {
	var newETag int64

	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var oldParentUUID *string
		err := tx.QueryRow("SELECT etag, parent_uuid FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag, &oldParentUUID)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to get container: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Update the container
		_, err = tx.Exec(`
			UPDATE containers
			SET parent_uuid = ?,
				etag = etag + 1,
				updated_by_actor_uuid = ?
			WHERE uuid = ?
		`, newParentUUID, actorUUID, containerUUID)
		if err != nil {
			return fmt.Errorf("failed to move container: %w", err)
		}

		// Log event with structured payload
		payload := map[string]interface{}{}
		if oldParentUUID != nil {
			payload["old_parent_uuid"] = *oldParentUUID
		}
		if newParentUUID != nil {
			payload["new_parent_uuid"] = *newParentUUID
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)
		newETag = currentETag + 1

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "container",
			ResourceUUID: &containerUUID,
			EventType:    "container.moved",
			ETag:         &newETag,
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		return nil
	})

	return newETag, err
}

// Archive soft-deletes a container by setting archived_at timestamp.
func (cs *ContainerStore) Archive(actorUUID, containerUUID string, ifMatch int64) (int64, error) {
	var newETag int64

	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var slug string
		err := tx.QueryRow("SELECT etag, slug FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag, &slug)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to get container: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Soft delete
		_, err = tx.Exec(`
			UPDATE containers
			SET archived_at = strftime('%Y-%m-%dT%H:%M:%SZ','now'),
				updated_by_actor_uuid = ?,
				etag = etag + 1
			WHERE uuid = ?
		`, actorUUID, containerUUID)
		if err != nil {
			return fmt.Errorf("failed to archive container: %w", err)
		}

		// Log event
		payload := map[string]interface{}{
			"slug":        slug,
			"soft_delete": true,
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)
		newETag = currentETag + 1

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "container",
			ResourceUUID: &containerUUID,
			EventType:    "container.archived",
			ETag:         &newETag,
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		return nil
	})

	return newETag, err
}

// Delete hard-deletes an empty container.
func (cs *ContainerStore) Delete(actorUUID, containerUUID string, ifMatch int64) error {
	return cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var slug string
		err := tx.QueryRow("SELECT etag, slug FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag, &slug)
		if err != nil {
			if err == sql.ErrNoRows {
				return fmt.Errorf("container not found: %s", containerUUID)
			}
			return fmt.Errorf("failed to get container: %w", err)
		}

		// Check etag if ifMatch was provided
		if err := checkETag(currentETag, ifMatch); err != nil {
			return err
		}

		// Check for children (tasks or subcontainers)
		var childCount int
		err = tx.QueryRow(`
			SELECT (
				(SELECT COUNT(*) FROM tasks WHERE project_uuid = ?) +
				(SELECT COUNT(*) FROM containers WHERE parent_uuid = ?)
			)
		`, containerUUID, containerUUID).Scan(&childCount)
		if err != nil {
			return fmt.Errorf("failed to check children: %w", err)
		}
		if childCount > 0 {
			return fmt.Errorf("container is not empty: has %d children", childCount)
		}

		// Log event BEFORE deleting
		payload := map[string]interface{}{
			"slug":       slug,
			"deleted_by": actorUUID,
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    &actorUUID,
			ResourceType: "container",
			ResourceUUID: &containerUUID,
			EventType:    "container.deleted",
			Payload:      &payloadStr,
		}); err != nil {
			return fmt.Errorf("failed to log event: %w", err)
		}

		// Hard delete
		_, err = tx.Exec("DELETE FROM containers WHERE uuid = ?", containerUUID)
		if err != nil {
			return fmt.Errorf("failed to delete container: %w", err)
		}

		return nil
	})
}

// GetByUUID retrieves a container by UUID.
func (cs *ContainerStore) GetByUUID(uuid string) (*domain.Container, error) {
	container := &domain.Container{}
	// Use string intermediates for time fields since SQLite stores times as strings
	var createdAt, updatedAt string
	var archivedAt *string

	err := cs.store.db.QueryRow(`
		SELECT uuid, id, slug, title, parent_uuid, etag,
			   created_at, updated_at, archived_at,
			   created_by_actor_uuid, updated_by_actor_uuid
		FROM containers WHERE uuid = ?
	`, uuid).Scan(
		&container.UUID, &container.ID, &container.Slug, &container.Title,
		&container.ParentUUID, &container.ETag,
		&createdAt, &updatedAt, &archivedAt,
		&container.CreatedByActorUUID, &container.UpdatedByActorUUID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("container not found: %s", uuid)
		}
		return nil, fmt.Errorf("failed to get container: %w", err)
	}
	return container, nil
}

// LookupBySlugAndParent finds a container by slug within a parent.
func (cs *ContainerStore) LookupBySlugAndParent(slug string, parentUUID *string) (*domain.Container, error) {
	container := &domain.Container{}
	// Use string intermediates for time fields
	var createdAt, updatedAt string
	var archivedAt *string
	var query string
	var args []interface{}

	if parentUUID == nil {
		query = `
			SELECT uuid, id, slug, title, parent_uuid, etag,
				   created_at, updated_at, archived_at,
				   created_by_actor_uuid, updated_by_actor_uuid
			FROM containers WHERE slug = ? AND parent_uuid IS NULL
		`
		args = []interface{}{slug}
	} else {
		query = `
			SELECT uuid, id, slug, title, parent_uuid, etag,
				   created_at, updated_at, archived_at,
				   created_by_actor_uuid, updated_by_actor_uuid
			FROM containers WHERE slug = ? AND parent_uuid = ?
		`
		args = []interface{}{slug, *parentUUID}
	}

	err := cs.store.db.QueryRow(query, args...).Scan(
		&container.UUID, &container.ID, &container.Slug, &container.Title,
		&container.ParentUUID, &container.ETag,
		&createdAt, &updatedAt, &archivedAt,
		&container.CreatedByActorUUID, &container.UpdatedByActorUUID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("failed to lookup container: %w", err)
	}
	return container, nil
}
