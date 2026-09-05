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
		OutputPath:        opts.OutputPath,
		SnapshotRev:       snapshotRev,
		ContainerCount:    len(snap.Containers),
		TaskCount:         len(snap.Tasks),
		PromiseCount:      len(snap.Promises),
		CommentCount:      len(snap.Comments),
		LinkCount:         len(snap.Links),
		EventCount:        len(snap.Events),
		ProjectEventCount: len(snap.ProjectEvents),
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
		Containers: make(map[string]ContainerEntry),
		Tasks:      make(map[string]TaskEntry),
		Promises:   make(map[string]PromiseEntry),
		Comments:   make(map[string]CommentEntry),
		Links:      make(map[string]LinkEntry),
	}

	// Export containers
	if err := exportContainers(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export containers: %w", err)
	}

	// Export tasks
	if err := exportTasks(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export tasks: %w", err)
	}

	// Export promises after their optional task/container subjects.
	if err := exportPromises(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export promises: %w", err)
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
		if err := exportProjectEvents(db, snap); err != nil {
			return nil, fmt.Errorf("failed to export project events: %w", err)
		}
	}

	return snap, nil
}

func exportProjectEvents(db *sql.DB, snap *Snapshot) error {
	snap.ProjectEvents = make(map[string]ProjectEventEntry)
	rows, err := db.Query(`SELECT id, fid, project_uuid, container_uuid, campaign_uuid,
		task_uuid, type, source, node, principal_ref, scope_ref, summary, payload,
		idempotency_key, occurred_at, created_at FROM project_events ORDER BY id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var entry ProjectEventEntry
		var campaign, task, node, scope, payload, key sql.NullString
		if err := rows.Scan(&entry.ID, &entry.FID, &entry.ProjectUUID, &entry.ContainerUUID,
			&campaign, &task, &entry.Type, &entry.Source, &node, &entry.PrincipalRef,
			&scope, &entry.Summary, &payload, &key, &entry.OccurredAt, &entry.CreatedAt); err != nil {
			return err
		}
		entry.CampaignUUID = snapshotNullString(campaign)
		entry.TaskUUID = snapshotNullString(task)
		entry.Node = snapshotNullString(node)
		entry.ScopeRef = snapshotNullString(scope)
		entry.Payload = snapshotNullString(payload)
		entry.IdempotencyKey = snapshotNullString(key)
		snap.ProjectEvents[entry.FID] = entry
	}
	return rows.Err()
}

func snapshotNullString(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	return &value.String
}

func exportPromises(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, owner_principal_ref, subject, review_question,
		       subject_task_uuid, subject_container_uuid, review_at, state,
		       closed_at, last_reviewed_at, last_review_note, meta, etag,
		       created_at, updated_at, created_by_principal_ref,
		       created_by_scope_ref, updated_by_principal_ref, updated_by_scope_ref
		  FROM promises
		 ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uuid string
		var entry PromiseEntry
		var reviewQuestion, subjectTaskUUID, subjectContainerUUID sql.NullString
		var closedAt, lastReviewedAt, lastReviewNote, meta sql.NullString
		var createdByScopeRef, updatedByScopeRef sql.NullString
		if err := rows.Scan(
			&uuid, &entry.ID, &entry.OwnerPrincipalRef, &entry.Subject, &reviewQuestion,
			&subjectTaskUUID, &subjectContainerUUID, &entry.ReviewAt, &entry.State,
			&closedAt, &lastReviewedAt, &lastReviewNote, &meta, &entry.ETag,
			&entry.CreatedAt, &entry.UpdatedAt, &entry.CreatedByPrincipalRef,
			&createdByScopeRef, &entry.UpdatedByPrincipalRef, &updatedByScopeRef,
		); err != nil {
			return err
		}
		if reviewQuestion.Valid {
			entry.ReviewQuestion = &reviewQuestion.String
		}
		if subjectTaskUUID.Valid {
			entry.SubjectTaskUUID = &subjectTaskUUID.String
		}
		if subjectContainerUUID.Valid {
			entry.SubjectContainerUUID = &subjectContainerUUID.String
		}
		if closedAt.Valid {
			entry.ClosedAt = &closedAt.String
		}
		if lastReviewedAt.Valid {
			entry.LastReviewedAt = &lastReviewedAt.String
		}
		if lastReviewNote.Valid {
			entry.LastReviewNote = &lastReviewNote.String
		}
		if meta.Valid {
			entry.Meta = &meta.String
		}
		if createdByScopeRef.Valid {
			entry.CreatedByScopeRef = &createdByScopeRef.String
		}
		if updatedByScopeRef.Valid {
			entry.UpdatedByScopeRef = &updatedByScopeRef.String
		}
		snap.Promises[uuid] = entry
	}
	return rows.Err()
}

func exportContainers(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, slug, title, parent_uuid, etag,
		       created_at, updated_at, archived_at,
		       created_by_principal_ref, updated_by_principal_ref
		FROM containers
		WHERE archived_at IS NULL
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uuid, id, slug, title, createdAt, updatedAt string
		var parentUUID, archivedAt sql.NullString
		var createdByPrincipal, updatedByPrincipal sql.NullString
		var etag int64

		if err := rows.Scan(&uuid, &id, &slug, &title, &parentUUID, &etag,
			&createdAt, &updatedAt, &archivedAt,
			&createdByPrincipal, &updatedByPrincipal); err != nil {
			return err
		}

		entry := ContainerEntry{
			ID:        id,
			Slug:      slug,
			Title:     title,
			ETag:      etag,
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		}

		if parentUUID.Valid {
			entry.ParentUUID = parentUUID.String
		}
		if archivedAt.Valid {
			entry.ArchivedAt = archivedAt.String
		}
		if createdByPrincipal.Valid {
			entry.CreatedByPrincipalRef = createdByPrincipal.String
		}
		if updatedByPrincipal.Valid {
			entry.UpdatedByPrincipalRef = updatedByPrincipal.String
		}

		snap.Containers[uuid] = entry
	}

	return rows.Err()
}

func exportTasks(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, slug, title, project_uuid, campaign_uuid, requested_by_project_id,
		       assigned_project_id, acknowledged_at, resolution,
		       workflow_preset, preset_version, phase, risk_class,
		       state, priority,
		       start_at, due_at, labels, description, specification, etag,
		       created_at, updated_at, completed_at, archived_at,
		       created_by_principal_ref, updated_by_principal_ref
		FROM tasks
		WHERE archived_at IS NULL
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uuid, id, slug, title, projectUUID, state, createdAt, updatedAt string
		var description string
		var specification string
		var startAt, dueAt, labels, completedAt, archivedAt sql.NullString
		var campaignUUID sql.NullString
		var requestedBy, assignedProject, acknowledgedAt, resolution sql.NullString
		var workflowPreset, phase, riskClass sql.NullString
		var presetVersion sql.NullInt64
		var createdByPrincipal, updatedByPrincipal sql.NullString
		var priority int
		var etag int64

		if err := rows.Scan(&uuid, &id, &slug, &title, &projectUUID, &campaignUUID, &requestedBy,
			&assignedProject, &acknowledgedAt, &resolution,
			&workflowPreset, &presetVersion, &phase, &riskClass,
			&state, &priority,
			&startAt, &dueAt, &labels, &description, &specification, &etag,
			&createdAt, &updatedAt, &completedAt, &archivedAt,
			&createdByPrincipal, &updatedByPrincipal); err != nil {
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
		}
		if createdByPrincipal.Valid {
			entry.CreatedByPrincipalRef = createdByPrincipal.String
		}
		if updatedByPrincipal.Valid {
			entry.UpdatedByPrincipalRef = updatedByPrincipal.String
		}

		if campaignUUID.Valid {
			entry.CampaignUUID = campaignUUID.String
		}
		if requestedBy.Valid {
			entry.RequestedByProjectID = requestedBy.String
		}
		if assignedProject.Valid {
			entry.AssignedProjectID = assignedProject.String
		}
		if acknowledgedAt.Valid {
			entry.AcknowledgedAt = acknowledgedAt.String
		}
		if resolution.Valid {
			entry.Resolution = resolution.String
		}
		if workflowPreset.Valid {
			entry.WorkflowPreset = workflowPreset.String
		}
		if presetVersion.Valid {
			entry.PresetVersion = int(presetVersion.Int64)
		}
		if phase.Valid {
			entry.Phase = phase.String
		}
		if riskClass.Valid {
			entry.RiskClass = riskClass.String
		}
		if description != "" {
			entry.Description = description
		}
		if specification != "" {
			entry.Specification = specification
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
		SELECT uuid, id, task_uuid, container_uuid, created_by_principal_ref, body, meta, etag,
		       created_at, updated_at, deleted_at, deleted_by_principal_ref
		FROM comments
		WHERE deleted_at IS NULL
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uuid, id, body, createdAt string
		var taskUUID, containerUUID sql.NullString
		var createdByPrincipal, meta, updatedAt, deletedAt, deletedByPrincipal sql.NullString
		var etag int64

		if err := rows.Scan(&uuid, &id, &taskUUID, &containerUUID, &createdByPrincipal, &body, &meta, &etag,
			&createdAt, &updatedAt, &deletedAt, &deletedByPrincipal); err != nil {
			return err
		}

		entry := CommentEntry{
			ID:        id,
			Body:      body,
			ETag:      etag,
			CreatedAt: createdAt,
		}

		if taskUUID.Valid {
			entry.TaskUUID = taskUUID.String
		}
		if containerUUID.Valid {
			entry.ContainerUUID = containerUUID.String
		}
		if createdByPrincipal.Valid {
			entry.CreatedByPrincipalRef = createdByPrincipal.String
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
		if deletedByPrincipal.Valid {
			entry.DeletedByPrincipalRef = deletedByPrincipal.String
		}

		snap.Comments[uuid] = entry
	}

	return rows.Err()
}

func exportEvents(db *sql.DB, snap *Snapshot) error {
	snap.Events = make(map[string]EventEntry)

	rows, err := db.Query(`
		SELECT id, timestamp, principal_ref, resource_type, resource_uuid,
		       event_type, etag, payload
		FROM event_log
		ORDER BY id
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id int64
		var timestamp, resourceType, eventType string
		var principalRef, resourceUUID, payload sql.NullString
		var etag sql.NullInt64

		if err := rows.Scan(&id, &timestamp, &principalRef, &resourceType, &resourceUUID,
			&eventType, &etag, &payload); err != nil {
			return err
		}

		entry := EventEntry{
			ID:           id,
			Timestamp:    timestamp,
			ResourceType: resourceType,
			EventType:    eventType,
		}

		if principalRef.Valid {
			entry.PrincipalRef = principalRef.String
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
