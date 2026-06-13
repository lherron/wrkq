package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
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
	Kind       string // project, directory, feature, area - defaults to "directory"
}

// ContainerCreateResult contains the result of container creation.
type ContainerCreateResult struct {
	UUID string
	ID   string
	ETag int64
}

// Create creates a new container and logs a container.created event.
func (cs *ContainerStore) Create(actorUUID string, params ContainerCreateParams) (*ContainerCreateResult, error) {
	return cs.CreateWithAttribution(cs.store.attributionFromActorUUID(actorUUID), params)
}

// CreateWithAttribution creates a new container using canonical principal attribution.
func (cs *ContainerStore) CreateWithAttribution(attr attribution.Attribution, params ContainerCreateParams) (*ContainerCreateResult, error) {
	if err := requireAttribution(attr); err != nil {
		return nil, err
	}
	var result *ContainerCreateResult

	// Default title to slug if not provided
	title := params.Title
	if title == "" {
		title = defaultContainerTitle(params.Slug)
	}

	// Default kind to "directory" if not provided
	kind := params.Kind
	if kind == "" {
		kind = string(domain.ContainerKindDirectory)
	}
	if err := domain.ValidateContainerKind(kind); err != nil {
		return nil, err
	}
	rootUUID, err := RootContainerUUID(cs.store.db)
	if err != nil {
		return nil, err
	}
	if kind == string(domain.ContainerKindProject) {
		// Projects are top-level: parent must be the root. A nil parent (the
		// common `mkdir --kind project foo` case) is auto-anchored to the root.
		switch {
		case params.ParentUUID == nil:
			params.ParentUUID = &rootUUID
		case *params.ParentUUID != rootUUID:
			return nil, fmt.Errorf("project containers must be top-level (child of root)")
		}
	} else {
		// Non-project containers must be nested under a project/sub-container,
		// never at the top level (which is reserved for projects).
		if params.ParentUUID == nil || *params.ParentUUID == rootUUID {
			return nil, fmt.Errorf("only project containers can be top-level; %q needs a parent container", kind)
		}
	}

	err = cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		res, err := tx.Exec(`
			INSERT INTO containers (
				id, slug, title, parent_uuid, kind,
				created_by_actor_uuid, updated_by_actor_uuid,
				created_by_principal_ref, updated_by_principal_ref,
				created_by_scope_ref, updated_by_scope_ref
			)
			VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		`, params.Slug, title, params.ParentUUID, kind,
			legacyActorSQL(attr), legacyActorSQL(attr),
			attr.PrincipalRef, attr.PrincipalRef,
			scopeSQL(attr), scopeSQL(attr))
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
			"kind":  kind,
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
			ActorUUID:    eventActorUUID(attr),
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
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

func defaultContainerTitle(slug string) string {
	if slug == "inbox" {
		return "Inbox"
	}
	return slug
}

// UpdateFields updates specified fields on a container and logs a container.updated event.
// Returns the new etag on success.
func (cs *ContainerStore) UpdateFields(actorUUID, containerUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	return cs.UpdateFieldsWithAttribution(cs.store.attributionFromActorUUID(actorUUID), containerUUID, fields, ifMatch)
}

// UpdateFieldsWithAttribution updates a container using canonical principal attribution.
func (cs *ContainerStore) UpdateFieldsWithAttribution(attr attribution.Attribution, containerUUID string, fields map[string]interface{}, ifMatch int64) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	var newETag int64

	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current etag
		var currentETag int64
		var currentKind string
		var currentParentUUID *string
		err := tx.QueryRow("SELECT etag, kind, parent_uuid FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag, &currentKind, &currentParentUUID)
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
		rootUUID, err := RootContainerUUID(tx)
		if err != nil {
			return err
		}
		if err := validateContainerKindUpdate(rootUUID, currentKind, currentParentUUID, fields); err != nil {
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
		setClauses = append(setClauses, "updated_by_principal_ref = ?")
		setClauses = append(setClauses, "updated_by_scope_ref = ?")
		args = append(args, legacyActorSQL(attr), attr.PrincipalRef, scopeSQL(attr))

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
			ActorUUID:    eventActorUUID(attr),
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
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
	return cs.MoveWithAttribution(cs.store.attributionFromActorUUID(actorUUID), containerUUID, newParentUUID, ifMatch)
}

// MoveWithAttribution moves a container using canonical principal attribution.
func (cs *ContainerStore) MoveWithAttribution(attr attribution.Attribution, containerUUID string, newParentUUID *string, ifMatch int64) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
	var newETag int64

	err := cs.store.withTx(func(tx *sql.Tx, ew *events.Writer) error {
		// Get current state
		var currentETag int64
		var oldParentUUID *string
		var kind string
		err := tx.QueryRow("SELECT etag, parent_uuid, kind FROM containers WHERE uuid = ?", containerUUID).Scan(&currentETag, &oldParentUUID, &kind)
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
		rootUUID, err := RootContainerUUID(tx)
		if err != nil {
			return err
		}
		if kind == string(domain.ContainerKindProject) {
			if newParentUUID == nil || *newParentUUID != rootUUID {
				return fmt.Errorf("project containers must remain top-level (child of root)")
			}
		} else if newParentUUID == nil || *newParentUUID == rootUUID {
			return fmt.Errorf("only project containers can be top-level")
		}

		// Update the container
		_, err = tx.Exec(`
			UPDATE containers
			SET parent_uuid = ?,
				etag = etag + 1,
				updated_by_actor_uuid = ?,
				updated_by_principal_ref = ?,
				updated_by_scope_ref = ?
			WHERE uuid = ?
		`, newParentUUID, legacyActorSQL(attr), attr.PrincipalRef, scopeSQL(attr), containerUUID)
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
			ActorUUID:    eventActorUUID(attr),
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
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

func validateContainerKindUpdate(rootUUID, currentKind string, currentParentUUID *string, fields map[string]interface{}) error {
	nextKind := currentKind
	if raw, ok := fields["kind"]; ok {
		value, ok := raw.(string)
		if !ok {
			return fmt.Errorf("container kind must be a string")
		}
		if err := domain.ValidateContainerKind(value); err != nil {
			return err
		}
		nextKind = value
	}

	nextParentUUID := currentParentUUID
	if raw, ok := fields["parent_uuid"]; ok {
		switch value := raw.(type) {
		case nil:
			nextParentUUID = nil
		case *string:
			nextParentUUID = value
		case string:
			nextParentUUID = &value
		default:
			return fmt.Errorf("container parent_uuid must be a string pointer or nil")
		}
	}

	switch {
	case nextKind == string(domain.ContainerKindRoot):
		// The internal root legitimately keeps a null parent; nothing to validate.
	case nextKind == string(domain.ContainerKindProject):
		if nextParentUUID == nil || *nextParentUUID != rootUUID {
			return fmt.Errorf("project containers must be top-level (child of root)")
		}
	default:
		if nextParentUUID == nil || *nextParentUUID == rootUUID {
			return fmt.Errorf("only project containers can be top-level")
		}
	}
	return nil
}

// Archive soft-deletes a container by setting archived_at timestamp.
func (cs *ContainerStore) Archive(actorUUID, containerUUID string, ifMatch int64) (int64, error) {
	return cs.ArchiveWithAttribution(cs.store.attributionFromActorUUID(actorUUID), containerUUID, ifMatch)
}

// ArchiveWithAttribution archives a container using canonical principal attribution.
func (cs *ContainerStore) ArchiveWithAttribution(attr attribution.Attribution, containerUUID string, ifMatch int64) (int64, error) {
	if err := requireAttribution(attr); err != nil {
		return 0, err
	}
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
				updated_by_principal_ref = ?,
				updated_by_scope_ref = ?,
				etag = etag + 1
			WHERE uuid = ?
		`, legacyActorSQL(attr), attr.PrincipalRef, scopeSQL(attr), containerUUID)
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
			ActorUUID:    eventActorUUID(attr),
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
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
	return cs.DeleteWithAttribution(cs.store.attributionFromActorUUID(actorUUID), containerUUID, ifMatch)
}

// DeleteWithAttribution hard-deletes an empty container using canonical attribution.
func (cs *ContainerStore) DeleteWithAttribution(attr attribution.Attribution, containerUUID string, ifMatch int64) error {
	if err := requireAttribution(attr); err != nil {
		return err
	}
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
			"deleted_by": attr.PrincipalRef,
		}
		payloadJSON, _ := json.Marshal(payload)
		payloadStr := string(payloadJSON)

		if err := ew.LogEvent(tx, &domain.Event{
			ActorUUID:    eventActorUUID(attr),
			PrincipalRef: attr.PrincipalRef,
			ScopeRef:     attr.ScopeRef,
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
	var kind string
	var createdByActor, updatedByActor, createdByPrincipal, updatedByPrincipal, createdByScope, updatedByScope sql.NullString

	err := cs.store.db.QueryRow(`
		SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, webhook_urls, etag,
			   created_at, updated_at, archived_at,
			   created_by_actor_uuid, updated_by_actor_uuid,
			   created_by_principal_ref, updated_by_principal_ref,
			   created_by_scope_ref, updated_by_scope_ref
		FROM containers WHERE uuid = ?
	`, uuid).Scan(
		&container.UUID, &container.ID, &container.Slug, &container.Title,
		&container.ParentUUID, &kind, &container.SectionUUID, &container.SortIndex, &container.WebhookURLs, &container.ETag,
		&createdAt, &updatedAt, &archivedAt,
		&createdByActor, &updatedByActor,
		&createdByPrincipal, &updatedByPrincipal,
		&createdByScope, &updatedByScope,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("container not found: %s", uuid)
		}
		return nil, fmt.Errorf("failed to get container: %w", err)
	}
	container.Kind = domain.ContainerKind(kind)
	if createdByActor.Valid {
		container.CreatedByActorUUID = createdByActor.String
	}
	if updatedByActor.Valid {
		container.UpdatedByActorUUID = updatedByActor.String
	}
	if createdByPrincipal.Valid {
		container.CreatedByPrincipalRef = createdByPrincipal.String
	}
	if updatedByPrincipal.Valid {
		container.UpdatedByPrincipalRef = updatedByPrincipal.String
	}
	if createdByScope.Valid {
		container.CreatedByScopeRef = createdByScope.String
	}
	if updatedByScope.Valid {
		container.UpdatedByScopeRef = updatedByScope.String
	}
	return container, nil
}

// LookupBySlugAndParent finds a container by slug within a parent.
func (cs *ContainerStore) LookupBySlugAndParent(slug string, parentUUID *string) (*domain.Container, error) {
	container := &domain.Container{}
	// Use string intermediates for time fields
	var createdAt, updatedAt string
	var archivedAt *string
	var kind string
	var createdByActor, updatedByActor, createdByPrincipal, updatedByPrincipal, createdByScope, updatedByScope sql.NullString
	var query string
	var args []interface{}

	if parentUUID == nil {
		query = `
			SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, webhook_urls, etag,
				   created_at, updated_at, archived_at,
				   created_by_actor_uuid, updated_by_actor_uuid,
				   created_by_principal_ref, updated_by_principal_ref,
				   created_by_scope_ref, updated_by_scope_ref
			FROM containers WHERE slug = ? AND parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')
		`
		args = []interface{}{slug}
	} else {
		query = `
			SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, webhook_urls, etag,
				   created_at, updated_at, archived_at,
				   created_by_actor_uuid, updated_by_actor_uuid,
				   created_by_principal_ref, updated_by_principal_ref,
				   created_by_scope_ref, updated_by_scope_ref
			FROM containers WHERE slug = ? AND parent_uuid = ?
		`
		args = []interface{}{slug, *parentUUID}
	}

	err := cs.store.db.QueryRow(query, args...).Scan(
		&container.UUID, &container.ID, &container.Slug, &container.Title,
		&container.ParentUUID, &kind, &container.SectionUUID, &container.SortIndex, &container.WebhookURLs, &container.ETag,
		&createdAt, &updatedAt, &archivedAt,
		&createdByActor, &updatedByActor,
		&createdByPrincipal, &updatedByPrincipal,
		&createdByScope, &updatedByScope,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("failed to lookup container: %w", err)
	}
	container.Kind = domain.ContainerKind(kind)
	if createdByActor.Valid {
		container.CreatedByActorUUID = createdByActor.String
	}
	if updatedByActor.Valid {
		container.UpdatedByActorUUID = updatedByActor.String
	}
	if createdByPrincipal.Valid {
		container.CreatedByPrincipalRef = createdByPrincipal.String
	}
	if updatedByPrincipal.Valid {
		container.UpdatedByPrincipalRef = updatedByPrincipal.String
	}
	if createdByScope.Valid {
		container.CreatedByScopeRef = createdByScope.String
	}
	if updatedByScope.Valid {
		container.UpdatedByScopeRef = updatedByScope.String
	}
	return container, nil
}

// ListAll returns all non-archived containers (or all containers if includeArchived is true).
func (cs *ContainerStore) ListAll(includeArchived bool) ([]domain.Container, error) {
	query := `
		SELECT uuid, id, slug, title, parent_uuid, kind, section_uuid, sort_index, webhook_urls, etag,
		       created_at, updated_at, archived_at,
		       created_by_actor_uuid, updated_by_actor_uuid,
		       created_by_principal_ref, updated_by_principal_ref,
		       created_by_scope_ref, updated_by_scope_ref
		FROM containers
	`
	if !includeArchived {
		query += " WHERE archived_at IS NULL"
	}

	rows, err := cs.store.db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var containers []domain.Container
	for rows.Next() {
		var c domain.Container
		var createdAt, updatedAt string
		var archivedAt *string
		var kind string
		var createdByActor, updatedByActor, createdByPrincipal, updatedByPrincipal, createdByScope, updatedByScope sql.NullString
		if err := rows.Scan(
			&c.UUID, &c.ID, &c.Slug, &c.Title,
			&c.ParentUUID, &kind, &c.SectionUUID, &c.SortIndex, &c.WebhookURLs, &c.ETag,
			&createdAt, &updatedAt, &archivedAt,
			&createdByActor, &updatedByActor,
			&createdByPrincipal, &updatedByPrincipal,
			&createdByScope, &updatedByScope,
		); err != nil {
			return nil, fmt.Errorf("failed to scan container: %w", err)
		}
		c.Kind = domain.ContainerKind(kind)
		if createdByActor.Valid {
			c.CreatedByActorUUID = createdByActor.String
		}
		if updatedByActor.Valid {
			c.UpdatedByActorUUID = updatedByActor.String
		}
		if createdByPrincipal.Valid {
			c.CreatedByPrincipalRef = createdByPrincipal.String
		}
		if updatedByPrincipal.Valid {
			c.UpdatedByPrincipalRef = updatedByPrincipal.String
		}
		if createdByScope.Valid {
			c.CreatedByScopeRef = createdByScope.String
		}
		if updatedByScope.Valid {
			c.UpdatedByScopeRef = updatedByScope.String
		}
		containers = append(containers, c)
	}
	return containers, rows.Err()
}
