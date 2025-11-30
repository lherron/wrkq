package snapshot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// Export reads the database and produces a canonical snapshot.
func Export(db *sql.DB, opts ExportOptions) (*ExportResult, error) {
	if opts.OutputPath == "" {
		opts.OutputPath = DefaultOutputPath
	}

	// Build snapshot from database
	snap, err := buildSnapshot(db, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to build snapshot: %w", err)
	}

	// Generate canonical JSON
	var data []byte
	if opts.Canonical {
		data, err = CanonicalJSON(snap)
		if err != nil {
			return nil, fmt.Errorf("failed to generate canonical JSON: %w", err)
		}
	} else {
		data, err = PrettyJSON(snap)
		if err != nil {
			return nil, fmt.Errorf("failed to generate JSON: %w", err)
		}
	}

	// Compute snapshot_rev from canonical bytes
	snapshotRev := ComputeSnapshotRev(data)

	// Update snapshot metadata with computed rev
	snap.Meta.SnapshotRev = snapshotRev

	// Re-generate with updated snapshot_rev
	if opts.Canonical {
		data, err = CanonicalJSON(snap)
		if err != nil {
			return nil, fmt.Errorf("failed to regenerate canonical JSON: %w", err)
		}
	} else {
		data, err = PrettyJSON(snap)
		if err != nil {
			return nil, fmt.Errorf("failed to regenerate JSON: %w", err)
		}
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(opts.OutputPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create output directory: %w", err)
	}

	// Write snapshot file
	if err := os.WriteFile(opts.OutputPath, data, 0644); err != nil {
		return nil, fmt.Errorf("failed to write snapshot: %w", err)
	}

	result := &ExportResult{
		OutputPath:     opts.OutputPath,
		SnapshotRev:    snapshotRev,
		ActorCount:     len(snap.Actors),
		ContainerCount: len(snap.Containers),
		TaskCount:      len(snap.Tasks),
		CommentCount:   len(snap.Comments),
		LinkCount:      len(snap.Links),
		EventCount:     len(snap.Events),
	}

	return result, nil
}

// ExportToSnapshot reads the database and returns a Snapshot struct (for use in verify, etc.)
func ExportToSnapshot(db *sql.DB, opts ExportOptions) (*Snapshot, []byte, error) {
	snap, err := buildSnapshot(db, opts)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to build snapshot: %w", err)
	}

	// Generate canonical JSON
	var data []byte
	if opts.Canonical {
		data, err = CanonicalJSON(snap)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to generate canonical JSON: %w", err)
		}
	} else {
		data, err = PrettyJSON(snap)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to generate JSON: %w", err)
		}
	}

	// Compute and set snapshot_rev
	snapshotRev := ComputeSnapshotRev(data)
	snap.Meta.SnapshotRev = snapshotRev

	// Re-generate with updated snapshot_rev
	if opts.Canonical {
		data, err = CanonicalJSON(snap)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to regenerate canonical JSON: %w", err)
		}
	} else {
		data, err = PrettyJSON(snap)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to regenerate JSON: %w", err)
		}
	}

	return snap, data, nil
}

func buildSnapshot(db *sql.DB, opts ExportOptions) (*Snapshot, error) {
	snap := &Snapshot{
		Meta: Meta{
			SchemaVersion:           1,
			MachineInterfaceVersion: 1,
			GeneratedAt:             FormatTimestamp(time.Now()),
		},
		Actors:     make(map[string]ActorEntry),
		Containers: make(map[string]ContainerEntry),
		Tasks:      make(map[string]TaskEntry),
		Comments:   make(map[string]CommentEntry),
		Links:      make(map[string]LinkEntry),
	}

	// Export actors
	if err := exportActors(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export actors: %w", err)
	}

	// Export containers
	if err := exportContainers(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export containers: %w", err)
	}

	// Export tasks
	if err := exportTasks(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export tasks: %w", err)
	}

	// Export comments
	if err := exportComments(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export comments: %w", err)
	}

	// Export events if requested
	if opts.IncludeEvents {
		if err := exportEvents(db, snap); err != nil {
			return nil, fmt.Errorf("failed to export events: %w", err)
		}
	}

	return snap, nil
}

func exportActors(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, slug, display_name, role, meta, created_at, updated_at
		FROM actors
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var uuid, id, slug, role, createdAt, updatedAt string
		var displayName, meta sql.NullString

		if err := rows.Scan(&uuid, &id, &slug, &displayName, &role, &meta, &createdAt, &updatedAt); err != nil {
			return err
		}

		entry := ActorEntry{
			ID:        id,
			Slug:      slug,
			Role:      role,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}

		if displayName.Valid {
			entry.DisplayName = displayName.String
		}
		if meta.Valid {
			entry.Meta = meta.String
		}

		snap.Actors[uuid] = entry
	}

	return rows.Err()
}

func exportContainers(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, slug, title, parent_uuid, etag,
		       created_at, updated_at, archived_at,
		       created_by_actor_uuid, updated_by_actor_uuid
		FROM containers
		WHERE archived_at IS NULL
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var uuid, id, slug, title, createdAt, updatedAt string
		var parentUUID, archivedAt sql.NullString
		var createdBy, updatedBy string
		var etag int64

		if err := rows.Scan(&uuid, &id, &slug, &title, &parentUUID, &etag,
			&createdAt, &updatedAt, &archivedAt,
			&createdBy, &updatedBy); err != nil {
			return err
		}

		entry := ContainerEntry{
			ID:        id,
			Slug:      slug,
			Title:     title,
			ETag:      etag,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
			CreatedBy: createdBy,
			UpdatedBy: updatedBy,
		}

		if parentUUID.Valid {
			entry.ParentUUID = parentUUID.String
		}
		if archivedAt.Valid {
			entry.ArchivedAt = archivedAt.String
		}

		snap.Containers[uuid] = entry
	}

	return rows.Err()
}

func exportTasks(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, slug, title, project_uuid, state, priority,
		       start_at, due_at, labels, description, etag,
		       created_at, updated_at, completed_at, archived_at,
		       created_by_actor_uuid, updated_by_actor_uuid
		FROM tasks
		WHERE archived_at IS NULL
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var uuid, id, slug, title, projectUUID, state, createdAt, updatedAt string
		var description string
		var startAt, dueAt, labels, completedAt, archivedAt sql.NullString
		var createdBy, updatedBy string
		var priority int
		var etag int64

		if err := rows.Scan(&uuid, &id, &slug, &title, &projectUUID, &state, &priority,
			&startAt, &dueAt, &labels, &description, &etag,
			&createdAt, &updatedAt, &completedAt, &archivedAt,
			&createdBy, &updatedBy); err != nil {
			return err
		}

		entry := TaskEntry{
			ID:          id,
			Slug:        slug,
			Title:       title,
			ProjectUUID: projectUUID,
			State:       state,
			Priority:    priority,
			ETag:        etag,
			CreatedAt:   createdAt,
			UpdatedAt:   updatedAt,
			CreatedBy:   createdBy,
			UpdatedBy:   updatedBy,
		}

		if description != "" {
			entry.Description = description
		}
		if startAt.Valid {
			entry.StartAt = startAt.String
		}
		if dueAt.Valid {
			entry.DueAt = dueAt.String
		}
		if labels.Valid && labels.String != "" && labels.String != "[]" {
			// Parse JSON array of labels
			var labelSlice []string
			if err := json.Unmarshal([]byte(labels.String), &labelSlice); err == nil && len(labelSlice) > 0 {
				// Sort labels for determinism
				sort.Strings(labelSlice)
				entry.Labels = labelSlice
			}
		}
		if completedAt.Valid {
			entry.CompletedAt = completedAt.String
		}
		if archivedAt.Valid {
			entry.ArchivedAt = archivedAt.String
		}

		snap.Tasks[uuid] = entry
	}

	return rows.Err()
}

func exportComments(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, task_uuid, actor_uuid, body, meta, etag,
		       created_at, updated_at, deleted_at, deleted_by_actor_uuid
		FROM comments
		WHERE deleted_at IS NULL
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var uuid, id, taskUUID, actorUUID, body, createdAt string
		var meta, updatedAt, deletedAt, deletedBy sql.NullString
		var etag int64

		if err := rows.Scan(&uuid, &id, &taskUUID, &actorUUID, &body, &meta, &etag,
			&createdAt, &updatedAt, &deletedAt, &deletedBy); err != nil {
			return err
		}

		entry := CommentEntry{
			ID:        id,
			TaskUUID:  taskUUID,
			ActorUUID: actorUUID,
			Body:      body,
			ETag:      etag,
			CreatedAt: createdAt,
		}

		if meta.Valid {
			entry.Meta = meta.String
		}
		if updatedAt.Valid {
			entry.UpdatedAt = updatedAt.String
		}
		if deletedAt.Valid {
			entry.DeletedAt = deletedAt.String
		}
		if deletedBy.Valid {
			entry.DeletedBy = deletedBy.String
		}

		snap.Comments[uuid] = entry
	}

	return rows.Err()
}

func exportEvents(db *sql.DB, snap *Snapshot) error {
	snap.Events = make(map[string]EventEntry)

	rows, err := db.Query(`
		SELECT id, timestamp, actor_uuid, resource_type, resource_uuid,
		       event_type, etag, payload
		FROM event_log
		ORDER BY id
	`)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id int64
		var timestamp, resourceType, eventType string
		var actorUUID, resourceUUID, payload sql.NullString
		var etag sql.NullInt64

		if err := rows.Scan(&id, &timestamp, &actorUUID, &resourceType, &resourceUUID,
			&eventType, &etag, &payload); err != nil {
			return err
		}

		entry := EventEntry{
			ID:           id,
			Timestamp:    timestamp,
			ResourceType: resourceType,
			EventType:    eventType,
		}

		if actorUUID.Valid {
			entry.ActorUUID = actorUUID.String
		}
		if resourceUUID.Valid {
			entry.ResourceUUID = resourceUUID.String
		}
		if etag.Valid {
			entry.ETag = etag.Int64
		}
		if payload.Valid {
			entry.Payload = payload.String
		}

		// Use string ID as map key
		snap.Events[fmt.Sprintf("%d", id)] = entry
	}

	return rows.Err()
}
