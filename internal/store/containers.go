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
				created_by_principal_ref, updated_by_principal_ref,
				created_by_scope_ref, updated_by_scope_ref
			)
			VALUES ('', ?, ?, ?, ?, ?, ?, ?, ?)
		`, params.Slug, title, params.ParentUUID, kind,
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
		if err := validateEffectiveMembershipTx(tx, campaignValidation{containers: true}); err != nil {
			return err
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

		if err := logContainerEvent(tx, ew, attr, uuid, "container.created", &etag, payload); err != nil {
			return err
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

		// Increment etag and update attribution
		setClauses = append(setClauses, "etag = etag + 1")
		setClauses = append(setClauses, "updated_by_principal_ref = ?")
		setClauses = append(setClauses, "updated_by_scope_ref = ?")
		args = append(args, attr.PrincipalRef, scopeSQL(attr))

		// Add WHERE clause
		args = append(args, containerUUID)

		query := fmt.Sprintf("UPDATE containers SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
		_, err = tx.Exec(query, args...)
		if err != nil {
			return fmt.Errorf("failed to update container: %w", err)
		}
		if _, ok := fields["campaign_state"]; ok {
			if err := validateEffectiveMembershipTx(tx, campaignValidation{containers: true}); err != nil {
				return err
			}
		}

		// Log event with structured payload. Container content edits retain a
		// mechanical snapshot of BOTH full bodies, even when only one changed.
		// Curated iteration remains a kind=decision comment; this event is the
		// byte-preserving history channel.
		eventFields := fields
		_, descriptionChanged := fields["description"]
		_, specificationChanged := fields["specification"]
		_, labelsChanged := fields["labels"]
		if descriptionChanged || specificationChanged || labelsChanged {
			if eventFields, err = containerContentSnapshot(tx, containerUUID, fields); err != nil {
				return err
			}
		}
		newETag = currentETag + 1
		if err := logContainerEvent(tx, ew, attr, containerUUID, "container.updated", &newETag, eventFields); err != nil {
			return err
		}

		return nil
	})

	return newETag, err
}

func containerContentSnapshot(tx *sql.Tx, containerUUID string, fields map[string]interface{}) (map[string]interface{}, error) {
	var description string
	var specification, labels sql.NullString
	if err := tx.QueryRow(
		"SELECT description, specification, labels FROM containers WHERE uuid = ?", containerUUID,
	).Scan(&description, &specification, &labels); err != nil {
		return nil, fmt.Errorf("failed to snapshot container content: %w", err)
	}
	snapshot := make(map[string]interface{}, len(fields)+2)
	for key, value := range fields {
		snapshot[key] = value
	}
	snapshot["description"] = description
	if specification.Valid {
		snapshot["specification"] = specification.String
	} else {
		snapshot["specification"] = nil
	}
	if _, labelsChanged := fields["labels"]; labelsChanged {
		var labelValues []string
		if labels.Valid && strings.TrimSpace(labels.String) != "" {
			_ = json.Unmarshal([]byte(labels.String), &labelValues)
		}
		if labelValues == nil {
			labelValues = []string{}
		}
		snapshot["labels"] = labelValues
	}
	return snapshot, nil
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
				updated_by_principal_ref = ?,
				updated_by_scope_ref = ?
			WHERE uuid = ?
		`, newParentUUID, attr.PrincipalRef, scopeSQL(attr), containerUUID)
		if err != nil {
			return fmt.Errorf("failed to move container: %w", err)
		}
		if err := validateEffectiveMembershipTx(tx, campaignValidation{containers: true}); err != nil {
			return err
		}

		// Log event with structured payload
		payload := map[string]interface{}{}
		if oldParentUUID != nil {
			payload["old_parent_uuid"] = *oldParentUUID
		}
		if newParentUUID != nil {
			payload["new_parent_uuid"] = *newParentUUID
		}
		newETag = currentETag + 1
		if err := logContainerEvent(tx, ew, attr, containerUUID, "container.moved", &newETag, payload); err != nil {
			return err
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

// containerColumns is the SELECT list scanContainer reads, in scan order.
// `root` is selected separately: ListAll has never returned it.
const containerColumns = `uuid, id, slug, title, parent_uuid, kind, sort_index, webhook_urls, etag,
	created_at, updated_at, archived_at,
	created_by_principal_ref, updated_by_principal_ref,
	created_by_scope_ref, updated_by_scope_ref`

// containerWithRootColumns adds the root marker the single-container reads return.
const containerWithRootColumns = containerColumns + `, root`

// scanContainer reads containerColumns, plus root when withRoot.
func scanContainer(scanner rowScanner, withRoot bool) (*domain.Container, error) {
	c := &domain.Container{}
	var createdAt, updatedAt string
	var archivedAt *string
	var kind string
	var createdByPrincipal, updatedByPrincipal, createdByScope, updatedByScope sql.NullString
	dest := []interface{}{
		&c.UUID, &c.ID, &c.Slug, &c.Title,
		&c.ParentUUID, &kind, &c.SortIndex, &c.WebhookURLs, &c.ETag,
		&createdAt, &updatedAt, &archivedAt,
		&createdByPrincipal, &updatedByPrincipal,
		&createdByScope, &updatedByScope,
	}
	if withRoot {
		dest = append(dest, &c.Root)
	}
	if err := scanner.Scan(dest...); err != nil {
		return nil, err
	}
	c.Kind = domain.ContainerKind(kind)
	c.CreatedByPrincipalRef = createdByPrincipal.String
	c.UpdatedByPrincipalRef = updatedByPrincipal.String
	c.CreatedByScopeRef = createdByScope.String
	c.UpdatedByScopeRef = updatedByScope.String
	return c, nil
}

// GetByUUID retrieves a container by UUID.
func (cs *ContainerStore) GetByUUID(uuid string) (*domain.Container, error) {
	container, err := scanContainer(cs.store.db.QueryRow(`SELECT `+containerWithRootColumns+` FROM containers WHERE uuid = ?`, uuid), true)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("container not found: %s", uuid)
		}
		return nil, fmt.Errorf("failed to get container: %w", err)
	}
	return container, nil
}

// LookupBySlugAndParent finds a container by slug within a parent; a nil
// parent means the root.
func (cs *ContainerStore) LookupBySlugAndParent(slug string, parentUUID *string) (*domain.Container, error) {
	query := `SELECT ` + containerWithRootColumns + ` FROM containers WHERE slug = ? AND parent_uuid = (SELECT uuid FROM containers WHERE kind = 'root')`
	args := []interface{}{slug}
	if parentUUID != nil {
		query = `SELECT ` + containerWithRootColumns + ` FROM containers WHERE slug = ? AND parent_uuid = ?`
		args = append(args, *parentUUID)
	}
	container, err := scanContainer(cs.store.db.QueryRow(query, args...), true)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // Not found
		}
		return nil, fmt.Errorf("failed to lookup container: %w", err)
	}
	return container, nil
}

// ListAll returns all non-archived containers (or all containers if includeArchived is true).
func (cs *ContainerStore) ListAll(includeArchived bool) ([]domain.Container, error) {
	query := `SELECT ` + containerColumns + ` FROM containers`
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
		c, err := scanContainer(rows, false)
		if err != nil {
			return nil, fmt.Errorf("failed to scan container: %w", err)
		}
		containers = append(containers, *c)
	}
	return containers, rows.Err()
}

// containerSubtreeCTE binds subtree(uuid) to the container named by the
// first ? and every container beneath it.
const containerSubtreeCTE = `WITH RECURSIVE subtree(uuid) AS (
			SELECT uuid FROM containers WHERE uuid = ?
			UNION ALL
			SELECT c.uuid FROM containers c JOIN subtree s ON c.parent_uuid = s.uuid
		)`
