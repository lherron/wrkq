package snapshot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	dbsync "github.com/lherron/wrkq/internal/db"
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

	if err := exportLinks(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export links: %w", err)
	}

	if err := exportSequences(db, snap); err != nil {
		return nil, fmt.Errorf("failed to export sequences: %w", err)
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
	rows, err := db.Query(`SELECT id, uuid, project_uuid, container_uuid, campaign_uuid,
		task_uuid, type, summary, attributes, principal_ref, scope_ref,
		idempotency_key, occurred_at, created_at FROM project_events ORDER BY id`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var entry ProjectEventEntry
		var campaign, task, principal, scope, key sql.NullString
		if err := rows.Scan(&entry.ID, &entry.UUID, &entry.ProjectUUID, &entry.ContainerUUID,
			&campaign, &task, &entry.Type, &entry.Summary, &entry.Attributes, &principal,
			&scope, &key, &entry.OccurredAt, &entry.CreatedAt); err != nil {
			return err
		}
		entry.CampaignUUID = snapshotNullString(campaign)
		entry.TaskUUID = snapshotNullString(task)
		entry.PrincipalRef = snapshotNullString(principal)
		entry.ScopeRef = snapshotNullString(scope)
		entry.IdempotencyKey = snapshotNullString(key)
		snap.ProjectEvents[entry.UUID] = entry
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

// Every exporter below reads every row of its table: archived containers and
// tasks, deleted-state tasks and soft-deleted comments are part of the ledger
// and are referenced by rows that are not (T-07498). Legacy *_actor_uuid
// columns are deliberately not carried; wrkq attribution is principal-only.

func exportContainers(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, slug, title, kind, description, parent_uuid, sort_index,
		       section_uuid, webhook_urls, root, specification, labels, campaign_state,
		       etag, created_at, updated_at, archived_at,
		       created_by_principal_ref, created_by_scope_ref,
		       updated_by_principal_ref, updated_by_scope_ref
		FROM containers
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uuid string
		var entry ContainerEntry
		var parentUUID, archivedAt, createdByPrincipal, updatedByPrincipal sql.NullString
		var sectionUUID, webhookURLs, root, specification, labels, campaignState sql.NullString
		var createdByScope, updatedByScope sql.NullString

		if err := rows.Scan(&uuid, &entry.ID, &entry.Slug, &entry.Title, &entry.Kind,
			&entry.Description, &parentUUID, &entry.SortIndex,
			&sectionUUID, &webhookURLs, &root, &specification, &labels, &campaignState,
			&entry.ETag, &entry.CreatedAt, &entry.UpdatedAt, &archivedAt,
			&createdByPrincipal, &createdByScope, &updatedByPrincipal, &updatedByScope); err != nil {
			return err
		}

		entry.ParentUUID = parentUUID.String
		entry.ArchivedAt = archivedAt.String
		entry.CreatedByPrincipalRef = createdByPrincipal.String
		entry.UpdatedByPrincipalRef = updatedByPrincipal.String
		entry.SectionUUID = snapshotNullString(sectionUUID)
		entry.WebhookURLs = snapshotNullString(webhookURLs)
		entry.Root = snapshotNullString(root)
		entry.Specification = snapshotNullString(specification)
		entry.Labels = snapshotNullString(labels)
		entry.CampaignState = snapshotNullString(campaignState)
		entry.CreatedByScopeRef = snapshotNullString(createdByScope)
		entry.UpdatedByScopeRef = snapshotNullString(updatedByScope)

		snap.Containers[uuid] = entry
	}

	return rows.Err()
}

func exportTasks(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, slug, title, kind, project_uuid, campaign_uuid, parent_task_uuid,
		       requested_by_project_id, assigned_project_id, acknowledged_at, resolution,
		       workflow_preset, preset_version, phase, risk_class,
		       state, priority, assignee_principal_ref,
		       start_at, due_at, labels, meta, outcome, description, specification, etag,
		       created_at, updated_at, completed_at, archived_at,
		       deleted_at, deleted_by_principal_ref, deleted_by_scope_ref,
		       created_by_principal_ref, created_by_scope_ref,
		       updated_by_principal_ref, updated_by_scope_ref,
		       cp_project_id, cp_run_id, cp_session_id, cp_work_item_id,
		       sdk_session_id, run_status,
		       claimed_by_principal_ref, claimed_scope_ref, claimed_node, claimed_at,
		       claim_token_hash, claim_generation
		FROM tasks
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uuid string
		var entry TaskEntry
		var startAt, dueAt, labels, completedAt, archivedAt sql.NullString
		var campaignUUID, parentTaskUUID sql.NullString
		var requestedBy, assignedProject, acknowledgedAt, resolution sql.NullString
		var workflowPreset, phase, riskClass sql.NullString
		var presetVersion sql.NullInt64
		var assignee, meta, outcome sql.NullString
		var deletedAt, deletedByPrincipal, deletedByScope sql.NullString
		var createdByPrincipal, createdByScope, updatedByPrincipal, updatedByScope sql.NullString
		var cpProject, cpRun, cpSession, cpWorkItem, sdkSession, runStatus sql.NullString
		var claimedBy, claimedScope, claimedNode, claimedAt, claimTokenHash sql.NullString

		if err := rows.Scan(&uuid, &entry.ID, &entry.Slug, &entry.Title, &entry.Kind,
			&entry.ProjectUUID, &campaignUUID, &parentTaskUUID,
			&requestedBy, &assignedProject, &acknowledgedAt, &resolution,
			&workflowPreset, &presetVersion, &phase, &riskClass,
			&entry.State, &entry.Priority, &assignee,
			&startAt, &dueAt, &labels, &meta, &outcome, &entry.Description, &entry.Specification, &entry.ETag,
			&entry.CreatedAt, &entry.UpdatedAt, &completedAt, &archivedAt,
			&deletedAt, &deletedByPrincipal, &deletedByScope,
			&createdByPrincipal, &createdByScope, &updatedByPrincipal, &updatedByScope,
			&cpProject, &cpRun, &cpSession, &cpWorkItem, &sdkSession, &runStatus,
			&claimedBy, &claimedScope, &claimedNode, &claimedAt,
			&claimTokenHash, &entry.ClaimGeneration); err != nil {
			return err
		}

		// Existing string fields collapse NULL and '' (no date / no value):
		// start_at, due_at and labels ('' / '[]') restore as NULL.
		entry.CampaignUUID = campaignUUID.String
		entry.RequestedByProjectID = requestedBy.String
		entry.AssignedProjectID = assignedProject.String
		entry.AcknowledgedAt = acknowledgedAt.String
		entry.Resolution = resolution.String
		entry.WorkflowPreset = workflowPreset.String
		if presetVersion.Valid {
			entry.PresetVersion = int(presetVersion.Int64)
		}
		entry.Phase = phase.String
		entry.RiskClass = riskClass.String
		entry.StartAt = startAt.String
		entry.DueAt = dueAt.String
		entry.CompletedAt = completedAt.String
		entry.ArchivedAt = archivedAt.String
		entry.CreatedByPrincipalRef = createdByPrincipal.String
		entry.UpdatedByPrincipalRef = updatedByPrincipal.String
		if labels.Valid && labels.String != "" && labels.String != "[]" {
			var labelSlice []string
			if err := json.Unmarshal([]byte(labels.String), &labelSlice); err != nil {
				return fmt.Errorf("task %s has unparseable labels %q: %w", uuid, labels.String, err)
			}
			if len(labelSlice) > 0 {
				sort.Strings(labelSlice)
				entry.Labels = labelSlice
			}
		}

		entry.ParentTaskUUID = snapshotNullString(parentTaskUUID)
		entry.AssigneePrincipalRef = snapshotNullString(assignee)
		entry.Meta = snapshotNullString(meta)
		entry.Outcome = snapshotNullString(outcome)
		entry.DeletedAt = snapshotNullString(deletedAt)
		entry.DeletedByPrincipalRef = snapshotNullString(deletedByPrincipal)
		entry.DeletedByScopeRef = snapshotNullString(deletedByScope)
		entry.CreatedByScopeRef = snapshotNullString(createdByScope)
		entry.UpdatedByScopeRef = snapshotNullString(updatedByScope)
		entry.CPProjectID = snapshotNullString(cpProject)
		entry.CPRunID = snapshotNullString(cpRun)
		entry.CPSessionID = snapshotNullString(cpSession)
		entry.CPWorkItemID = snapshotNullString(cpWorkItem)
		entry.SDKSessionID = snapshotNullString(sdkSession)
		entry.RunStatus = snapshotNullString(runStatus)
		entry.ClaimedByPrincipalRef = snapshotNullString(claimedBy)
		entry.ClaimedScopeRef = snapshotNullString(claimedScope)
		entry.ClaimedNode = snapshotNullString(claimedNode)
		entry.ClaimedAt = snapshotNullString(claimedAt)
		entry.ClaimTokenHash = snapshotNullString(claimTokenHash)

		snap.Tasks[uuid] = entry
	}

	return rows.Err()
}

func exportComments(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT uuid, id, task_uuid, container_uuid, kind,
		       created_by_principal_ref, created_by_scope_ref, body, meta, etag,
		       created_at, updated_at, deleted_at,
		       deleted_by_principal_ref, deleted_by_scope_ref
		FROM comments
		ORDER BY uuid
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var uuid string
		var entry CommentEntry
		var taskUUID, containerUUID, kind sql.NullString
		var createdByPrincipal, createdByScope, meta, updatedAt, deletedAt sql.NullString
		var deletedByPrincipal, deletedByScope sql.NullString

		if err := rows.Scan(&uuid, &entry.ID, &taskUUID, &containerUUID, &kind,
			&createdByPrincipal, &createdByScope, &entry.Body, &meta, &entry.ETag,
			&entry.CreatedAt, &updatedAt, &deletedAt,
			&deletedByPrincipal, &deletedByScope); err != nil {
			return err
		}

		entry.TaskUUID = taskUUID.String
		entry.ContainerUUID = containerUUID.String
		entry.CreatedByPrincipalRef = createdByPrincipal.String
		entry.Meta = meta.String
		entry.UpdatedAt = updatedAt.String
		entry.DeletedAt = deletedAt.String
		entry.DeletedByPrincipalRef = deletedByPrincipal.String
		entry.Kind = snapshotNullString(kind)
		entry.CreatedByScopeRef = snapshotNullString(createdByScope)
		entry.DeletedByScopeRef = snapshotNullString(deletedByScope)

		snap.Comments[uuid] = entry
	}

	return rows.Err()
}

func exportLinks(db *sql.DB, snap *Snapshot) error {
	rows, err := db.Query(`
		SELECT from_task_uuid, to_task_uuid, kind, meta, created_at,
		       created_by_principal_ref, created_by_scope_ref
		FROM task_relations
		ORDER BY from_task_uuid, to_task_uuid, kind
	`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var entry LinkEntry
		var meta, createdByPrincipal, createdByScope sql.NullString
		if err := rows.Scan(&entry.SourceUUID, &entry.TargetUUID, &entry.LinkType, &meta,
			&entry.CreatedAt, &createdByPrincipal, &createdByScope); err != nil {
			return err
		}
		entry.Meta = snapshotNullString(meta)
		entry.CreatedByPrincipalRef = createdByPrincipal.String
		entry.CreatedByScopeRef = snapshotNullString(createdByScope)
		snap.Links[LinkKey(entry.SourceUUID, entry.TargetUUID, entry.LinkType)] = entry
	}
	return rows.Err()
}

// exportSequences records the friendly-id high-water marks of the modelled
// entities. sqlite_sequence may exceed MAX(id) when the newest rows were
// purged; carrying it keeps a restore from reissuing those ids.
func exportSequences(db *sql.DB, snap *Snapshot) error {
	sequences := make(map[string]int64)
	for _, spec := range dbsync.DefaultSequenceSpecs() {
		name, ok := snapshotSequenceNames[spec.SeqTable]
		if !ok {
			continue
		}
		hw, err := dbsync.HighWater(db, spec)
		if err != nil {
			return err
		}
		if hw > 0 {
			sequences[name] = int64(hw)
		}
	}
	comment, err := commentHighWater(db)
	if err != nil {
		return err
	}
	if comment > 0 {
		sequences["comment"] = comment
	}
	if len(sequences) > 0 {
		snap.Meta.Sequences = sequences
	}
	return nil
}

// commentHighWater is the larger of comment_sequences and the largest
// canonical comment id (comments keep their own counter table).
func commentHighWater(db interface {
	QueryRow(query string, args ...any) *sql.Row
}) (int64, error) {
	var hw int64
	err := db.QueryRow(`SELECT MAX(
		COALESCE((SELECT value FROM comment_sequences WHERE name = 'next_comment'), 0),
		COALESCE((SELECT MAX(CAST(SUBSTR(id, 3) AS INTEGER)) FROM comments
		           WHERE id LIKE 'C-%' AND SUBSTR(id, 3) <> '' AND SUBSTR(id, 3) NOT GLOB '*[^0-9]*'), 0))`).Scan(&hw)
	return hw, err
}

// snapshotSequenceNames maps the modelled entities' sqlite_sequence rows to
// their Meta.Sequences keys; "comment" is carried from comment_sequences.
var snapshotSequenceNames = map[string]string{
	"container_seq": "container",
	"task_seq":      "task",
	"promise_seq":   "promise",
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
