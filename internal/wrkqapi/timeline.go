package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/paths"
)

const (
	timelineDefaultLimit = 100
	timelineMaxLimit     = 1000
)

// ContainerTimelineViewParams selects one container-neutral composite snapshot.
// Cursor is opaque and carries both the event high-water fence and last emitted
// event id so later pages exclude concurrent appends.
type ContainerTimelineViewParams struct {
	Container string `json:"container"`
	Cursor    string `json:"cursor,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

// WrkqTimelineContainer is the timeline's BASE container object. Content and
// archive fields are orthogonal to campaign adornment and therefore exist for
// every container.
type WrkqTimelineContainer struct {
	UUID          string  `json:"uuid"`
	ID            string  `json:"id"`
	Slug          string  `json:"slug"`
	Title         string  `json:"title"`
	Description   string  `json:"description"`
	Specification *string `json:"specification,omitempty"`
	Kind          string  `json:"kind"`
	ParentUUID    string  `json:"parentUuid,omitempty"`
	Path          string  `json:"path"`
	ETag          int64   `json:"etag"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
	ArchivedAt    string  `json:"archivedAt,omitempty"`
}

// WrkqCampaignAdornment is nullable in the timeline result. Archived is a
// projection of archived_at; campaign state never substitutes for archive state.
type WrkqCampaignAdornment struct {
	State      string `json:"state"`
	Archived   bool   `json:"archived"`
	ArchivedAt string `json:"archivedAt,omitempty"`
}

// WrkqTimelineMember is one current effective member. Membership is derived
// from present residency/enrollment only; historical entries never use it for
// affiliation.
type WrkqTimelineMember struct {
	UUID       string  `json:"uuid"`
	ID         string  `json:"id"`
	Path       string  `json:"path"`
	Title      string  `json:"title"`
	State      string  `json:"state"`
	Outcome    *string `json:"outcome,omitempty"`
	Membership string  `json:"membership"`
}

type WrkqTimelineRollup struct {
	Terminal int `json:"terminal"`
	Total    int `json:"total"`
}

type WrkqTimelineComment struct {
	ID   string          `json:"id,omitempty"`
	Kind *string         `json:"kind,omitempty"`
	Body string          `json:"body"`
	Meta json.RawMessage `json:"meta,omitempty"`
}

type WrkqTimelineOutcome struct {
	Text *string `json:"text"`
}

type WrkqTimelineTaskState struct {
	State           string `json:"state"`
	SourceEventType string `json:"sourceEventType"`
}

type WrkqTimelineContainerState struct {
	From *string `json:"from"`
	To   string  `json:"to"`
}

// WrkqTimelineEntry is a discriminated event projection. Exactly one detail
// object matching Type is populated.
type WrkqTimelineEntry struct {
	Type           string                      `json:"type"`
	EventID        int64                       `json:"eventId"`
	Timestamp      string                      `json:"timestamp"`
	PrincipalRef   string                      `json:"principalRef,omitempty"`
	ResourceUUID   string                      `json:"resourceUuid,omitempty"`
	TaskUUID       string                      `json:"taskUuid,omitempty"`
	TaskID         string                      `json:"taskId,omitempty"`
	TaskPath       string                      `json:"taskPath,omitempty"`
	Membership     string                      `json:"membership,omitempty"`
	CampaignUUID   *string                     `json:"campaignUuid"`
	ContainerUUID  string                      `json:"containerUuid,omitempty"`
	Comment        *WrkqTimelineComment        `json:"comment,omitempty"`
	Outcome        *WrkqTimelineOutcome        `json:"outcome,omitempty"`
	TaskState      *WrkqTimelineTaskState      `json:"taskState,omitempty"`
	ContainerState *WrkqTimelineContainerState `json:"containerState,omitempty"`
}

type WrkqContainerTimelineView struct {
	Container       WrkqTimelineContainer          `json:"container"`
	Campaign        *WrkqCampaignAdornment         `json:"campaign"`
	Members         []WrkqTimelineMember           `json:"members"`
	Rollup          WrkqTimelineRollup             `json:"rollup"`
	MissingOutcomes []WrkqCampaignMemberDiagnostic `json:"missingOutcomes"`
	DecisionTasks   []WrkqTimelineMember           `json:"decisionTasks"`
	Entries         []WrkqTimelineEntry            `json:"entries"`
	SnapshotEventID int64                          `json:"snapshotEventId"`
	NextCursor      string                         `json:"nextCursor,omitempty"`
}

type timelineCursor struct {
	Version         int    `json:"v"`
	ContainerUUID   string `json:"containerUuid"`
	SnapshotEventID int64  `json:"snapshotEventId"`
	AfterEventID    int64  `json:"afterEventId"`
}

// ContainerTimelineView assembles the base container, nullable adornment,
// current projections, and historical stream under one read transaction.
func (a *API) ContainerTimelineView(
	ctx context.Context,
	p ContainerTimelineViewParams,
) (*WrkqContainerTimelineView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(p.Container) == "" {
		return nil, NewValidationError("container is required", map[string]any{"field": "container"})
	}
	limit := p.Limit
	if limit <= 0 {
		limit = timelineDefaultLimit
	}
	if limit > timelineMaxLimit {
		return nil, NewValidationError(
			fmt.Sprintf("limit must be at most %d", timelineMaxLimit),
			map[string]any{"field": "limit"},
		)
	}

	tx, err := a.db.DB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, NewInternalError(err)
	}
	defer func() { _ = tx.Rollback() }()

	containerUUID, err := resolveTimelineContainerTx(ctx, tx, p.Container)
	if err != nil {
		return nil, err
	}
	container, campaign, err := loadTimelineContainerTx(ctx, tx, containerUUID)
	if err != nil {
		return nil, err
	}
	members, rollup, missing, decisions, err := loadTimelineMembersTx(ctx, tx, containerUUID)
	if err != nil {
		return nil, err
	}

	snapshotEventID, afterEventID := int64(0), int64(0)
	if p.Cursor == "" {
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM event_log").Scan(&snapshotEventID); err != nil {
			return nil, NewInternalError(fmt.Errorf("read timeline snapshot high-water: %w", err))
		}
	} else {
		cur, err := decodeTimelineCursor(p.Cursor)
		if err != nil {
			return nil, NewValidationError("invalid timeline cursor", map[string]any{"field": "cursor"})
		}
		if cur.ContainerUUID != containerUUID {
			return nil, NewValidationError("timeline cursor belongs to a different container", map[string]any{"field": "cursor"})
		}
		snapshotEventID = cur.SnapshotEventID
		afterEventID = cur.AfterEventID
	}

	entries, hasMore, err := loadTimelineEntriesTx(
		ctx, tx, containerUUID, afterEventID, snapshotEventID, limit,
	)
	if err != nil {
		return nil, err
	}
	nextCursor := ""
	if hasMore {
		nextCursor, err = encodeTimelineCursor(timelineCursor{
			Version:         1,
			ContainerUUID:   containerUUID,
			SnapshotEventID: snapshotEventID,
			AfterEventID:    entries[len(entries)-1].EventID,
		})
		if err != nil {
			return nil, NewInternalError(err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, NewInternalError(err)
	}
	return &WrkqContainerTimelineView{
		Container: container, Campaign: campaign,
		Members: members, Rollup: rollup, MissingOutcomes: missing,
		DecisionTasks: decisions, Entries: entries,
		SnapshotEventID: snapshotEventID, NextCursor: nextCursor,
	}, nil
}

func resolveTimelineContainerTx(ctx context.Context, tx *sql.Tx, raw string) (string, error) {
	token := strings.TrimSpace(raw)
	if strings.HasPrefix(token, "t:") || strings.HasPrefix(token, "c:") {
		return "", NewValidationError("expected container selector", map[string]any{"field": "container"})
	}
	var containerUUID string
	var err error
	switch {
	case strings.HasPrefix(token, "P-"):
		err = tx.QueryRowContext(ctx, "SELECT uuid FROM containers WHERE id = ?", token).Scan(&containerUUID)
	case len(token) == 36 && strings.Count(token, "-") == 4:
		err = tx.QueryRowContext(ctx, "SELECT uuid FROM containers WHERE uuid = ?", token).Scan(&containerUUID)
	default:
		segments := paths.SplitPath(token)
		if len(segments) == 0 {
			return "", NewNotFoundError(token, "container")
		}
		for index, segment := range segments {
			normalized, normalizeErr := paths.NormalizeSlug(segment)
			if normalizeErr != nil {
				return "", NewNotFoundError(token, "container")
			}
			segments[index] = normalized
		}
		canonicalPath := paths.JoinPath(segments...)
		err = tx.QueryRowContext(
			ctx, "SELECT uuid FROM v_container_paths WHERE path = ?", canonicalPath,
		).Scan(&containerUUID)
	}
	if err == sql.ErrNoRows {
		return "", NewNotFoundError(token, "container")
	}
	if err != nil {
		return "", NewInternalError(err)
	}
	return containerUUID, nil
}

func loadTimelineContainerTx(
	ctx context.Context,
	tx *sql.Tx,
	containerUUID string,
) (WrkqTimelineContainer, *WrkqCampaignAdornment, error) {
	var (
		container                                WrkqTimelineContainer
		specification, campaignState, parentUUID sql.NullString
		archivedAt                               sql.NullString
		createdAt, updatedAt                     string
	)
	err := tx.QueryRowContext(ctx, `
		SELECT c.id, c.slug, c.title, c.description, c.specification,
		       c.campaign_state, c.kind, c.parent_uuid, COALESCE(v.path, c.slug),
		       c.etag, c.created_at, c.updated_at, c.archived_at
		  FROM containers c
		  LEFT JOIN v_container_paths v ON v.uuid = c.uuid
		 WHERE c.uuid = ?
	`, containerUUID).Scan(
		&container.ID, &container.Slug, &container.Title, &container.Description,
		&specification, &campaignState, &container.Kind, &parentUUID, &container.Path,
		&container.ETag, &createdAt, &updatedAt, &archivedAt,
	)
	if err != nil {
		return WrkqTimelineContainer{}, nil, NewInternalError(err)
	}
	container.UUID = containerUUID
	container.Specification = nullStringPtr(specification)
	container.ParentUUID = parentUUID.String
	container.CreatedAt = toRFC3339(createdAt)
	container.UpdatedAt = toRFC3339(updatedAt)
	container.ArchivedAt = toRFC3339(archivedAt.String)

	var campaign *WrkqCampaignAdornment
	if campaignState.Valid {
		campaign = &WrkqCampaignAdornment{
			State: campaignState.String, Archived: archivedAt.Valid,
			ArchivedAt: container.ArchivedAt,
		}
	}
	return container, campaign, nil
}

func loadTimelineMembersTx(
	ctx context.Context,
	tx *sql.Tx,
	containerUUID string,
) ([]WrkqTimelineMember, WrkqTimelineRollup, []WrkqCampaignMemberDiagnostic, []WrkqTimelineMember, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT t.uuid, t.id, COALESCE(tp.path, t.slug), t.title, t.state,
		       t.outcome, t.labels,
		       CASE WHEN t.project_uuid = ? THEN 'resident' ELSE 'enrolled' END
		  FROM tasks t
		  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
		 WHERE t.project_uuid = ? OR t.campaign_uuid = ?
		 ORDER BY t.id
	`, containerUUID, containerUUID, containerUUID)
	if err != nil {
		return nil, WrkqTimelineRollup{}, nil, nil, NewInternalError(err)
	}
	defer func() { _ = rows.Close() }()

	members := []WrkqTimelineMember{}
	missing := []WrkqCampaignMemberDiagnostic{}
	decisions := []WrkqTimelineMember{}
	terminal := 0
	for rows.Next() {
		var member WrkqTimelineMember
		var outcome, labels sql.NullString
		if err := rows.Scan(
			&member.UUID, &member.ID, &member.Path, &member.Title, &member.State,
			&outcome, &labels, &member.Membership,
		); err != nil {
			return nil, WrkqTimelineRollup{}, nil, nil, NewInternalError(err)
		}
		if outcome.Valid {
			value := outcome.String
			member.Outcome = &value
		}
		if isTimelineTerminal(member.State) {
			terminal++
		}
		if member.State == "completed" && (!outcome.Valid || strings.TrimSpace(outcome.String) == "") {
			missing = append(missing, WrkqCampaignMemberDiagnostic{
				UUID: member.UUID, ID: member.ID, Path: member.Path,
				State: member.State, Membership: member.Membership,
			})
		}
		if member.State == "open" && timelineLabelsContain(labels.String, "awaiting-lance") {
			decisions = append(decisions, member)
		}
		members = append(members, member)
	}
	if err := rows.Err(); err != nil {
		return nil, WrkqTimelineRollup{}, nil, nil, NewInternalError(err)
	}
	return members, WrkqTimelineRollup{Terminal: terminal, Total: len(members)}, missing, decisions, nil
}

func isTimelineTerminal(state string) bool {
	switch state {
	case "completed", "cancelled", "archived", "deleted":
		return true
	default:
		return false
	}
}

func timelineLabelsContain(raw, label string) bool {
	if strings.TrimSpace(raw) == "" {
		return false
	}
	var labels []string
	if json.Unmarshal([]byte(raw), &labels) != nil {
		return false
	}
	for _, candidate := range labels {
		if candidate == label {
			return true
		}
	}
	return false
}

const timelineEntriesQuery = `
	SELECT e.id, e.timestamp, COALESCE(e.principal_ref, ''), e.resource_uuid,
	       e.event_type, COALESCE(e.payload, ''),
	       COALESCE(t.uuid, comment_task.uuid, ''),
	       COALESCE(t.id, comment_task.id, ''),
	       COALESCE(tp.path, comment_tp.path, json_extract(e.payload, '$.slug'), ''),
	       COALESCE(cm.id, ''), cm.kind, COALESCE(cm.body, ''), cm.meta
	  FROM event_log e
	  LEFT JOIN tasks t
	    ON e.resource_type = 'task' AND t.uuid = e.resource_uuid
	  LEFT JOIN v_task_paths tp ON tp.uuid = t.uuid
	  LEFT JOIN comments cm
	    ON e.event_type = 'comment.created' AND cm.uuid = e.resource_uuid
	  LEFT JOIN tasks comment_task
	    ON e.event_type = 'comment.created'
	   AND comment_task.uuid = json_extract(e.payload, '$.task_id')
	  LEFT JOIN v_task_paths comment_tp ON comment_tp.uuid = comment_task.uuid
	 WHERE e.id > ? AND e.id <= ?
	   AND (
	     (
	       (
	         e.event_type IN (
	           'comment.created', 'task.outcome_set', 'task.archived',
	           'task.deleted', 'task.restored', 'task.purged'
	         )
	         OR (
	           e.event_type = 'task.updated'
	           AND json_type(e.payload, '$.state') IS NOT NULL
	         )
	       )
	       AND (
	         json_extract(e.payload, '$.campaign_uuid') = ?
	         OR json_extract(e.payload, '$.container_uuid') = ?
	       )
	     )
	     OR (
	       e.event_type = 'container.campaign_state_changed'
	       AND e.resource_uuid = ?
	     )
	   )
	 ORDER BY e.id ASC
	 LIMIT ?`

func loadTimelineEntriesTx(
	ctx context.Context,
	tx *sql.Tx,
	containerUUID string,
	afterEventID, snapshotEventID int64,
	limit int,
) ([]WrkqTimelineEntry, bool, error) {
	rows, err := tx.QueryContext(
		ctx, timelineEntriesQuery,
		afterEventID, snapshotEventID,
		containerUUID, containerUUID, containerUUID, limit+1,
	)
	if err != nil {
		return nil, false, NewInternalError(fmt.Errorf("query timeline entries: %w", err))
	}
	defer func() { _ = rows.Close() }()

	entries := make([]WrkqTimelineEntry, 0, limit+1)
	for rows.Next() {
		var (
			entry                    WrkqTimelineEntry
			resourceUUID, payload    string
			eventType                string
			commentID, commentBody   string
			commentKind, commentMeta sql.NullString
		)
		if err := rows.Scan(
			&entry.EventID, &entry.Timestamp, &entry.PrincipalRef, &resourceUUID,
			&eventType, &payload, &entry.TaskUUID, &entry.TaskID, &entry.TaskPath,
			&commentID, &commentKind, &commentBody, &commentMeta,
		); err != nil {
			return nil, false, NewInternalError(err)
		}
		entry.ResourceUUID = resourceUUID
		if err := normalizeTimelineEntry(&entry, eventType, payload, containerUUID); err != nil {
			return nil, false, NewInternalError(err)
		}
		if entry.Type == "comment" {
			comment := &WrkqTimelineComment{ID: commentID, Body: commentBody}
			if commentKind.Valid {
				value := commentKind.String
				comment.Kind = &value
			}
			if commentMeta.Valid && json.Valid([]byte(commentMeta.String)) {
				comment.Meta = json.RawMessage(commentMeta.String)
			}
			entry.Comment = comment
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, false, NewInternalError(err)
	}
	hasMore := len(entries) > limit
	if hasMore {
		entries = entries[:limit]
	}
	return entries, hasMore, nil
}

func normalizeTimelineEntry(
	entry *WrkqTimelineEntry,
	eventType, payloadRaw, timelineContainerUUID string,
) error {
	payload := map[string]json.RawMessage{}
	if payloadRaw != "" {
		if err := json.Unmarshal([]byte(payloadRaw), &payload); err != nil {
			return fmt.Errorf("decode timeline event %d payload: %w", entry.EventID, err)
		}
	}
	campaignUUID, campaignPresent := timelinePayloadString(payload, "campaign_uuid")
	containerUUID, _ := timelinePayloadString(payload, "container_uuid")
	if campaignPresent {
		entry.CampaignUUID = campaignUUID
	}
	if containerUUID != nil {
		entry.ContainerUUID = *containerUUID
	}
	switch {
	case campaignUUID != nil && *campaignUUID == timelineContainerUUID:
		if containerUUID != nil && *containerUUID == timelineContainerUUID {
			entry.Membership = "resident"
		} else {
			entry.Membership = "enrolled"
		}
	case containerUUID != nil && *containerUUID == timelineContainerUUID:
		entry.Membership = "resident"
	}

	switch eventType {
	case "comment.created":
		entry.Type = "comment"
		if taskUUID, _ := timelinePayloadString(payload, "task_id"); taskUUID != nil {
			entry.TaskUUID = *taskUUID
		}
	case "task.outcome_set":
		entry.Type = "task.outcome"
		outcome, _ := timelinePayloadString(payload, "outcome")
		entry.Outcome = &WrkqTimelineOutcome{Text: outcome}
	case "task.updated":
		state, _ := timelinePayloadString(payload, "state")
		if state == nil {
			return fmt.Errorf("timeline task.updated event %d lacks state", entry.EventID)
		}
		entry.Type = "task.state"
		entry.TaskState = &WrkqTimelineTaskState{State: *state, SourceEventType: eventType}
	case "task.archived":
		entry.Type = "task.state"
		entry.TaskState = &WrkqTimelineTaskState{State: "archived", SourceEventType: eventType}
	case "task.deleted":
		entry.Type = "task.state"
		entry.TaskState = &WrkqTimelineTaskState{State: "deleted", SourceEventType: eventType}
	case "task.restored":
		state, _ := timelinePayloadString(payload, "target_state")
		if state == nil {
			value := "open"
			state = &value
		}
		entry.Type = "task.state"
		entry.TaskState = &WrkqTimelineTaskState{State: *state, SourceEventType: eventType}
	case "task.purged":
		entry.Type = "task.state"
		entry.TaskState = &WrkqTimelineTaskState{State: "purged", SourceEventType: eventType}
	case "container.campaign_state_changed":
		entry.Type = "container.state"
		from, _ := timelinePayloadString(payload, "from")
		to, _ := timelinePayloadString(payload, "to")
		if to == nil {
			return fmt.Errorf("timeline campaign state event %d lacks target state", entry.EventID)
		}
		entry.ContainerState = &WrkqTimelineContainerState{From: from, To: *to}
	default:
		return fmt.Errorf("unsupported timeline event type %s", eventType)
	}
	return nil
}

func timelinePayloadString(payload map[string]json.RawMessage, key string) (*string, bool) {
	raw, ok := payload[key]
	if !ok {
		return nil, false
	}
	if string(raw) == "null" {
		return nil, true
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return nil, true
	}
	return &value, true
}

func encodeTimelineCursor(cur timelineCursor) (string, error) {
	raw, err := json.Marshal(cur)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeTimelineCursor(raw string) (timelineCursor, error) {
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return timelineCursor{}, err
	}
	var cur timelineCursor
	if err := json.Unmarshal(decoded, &cur); err != nil {
		return timelineCursor{}, err
	}
	if cur.Version != 1 || cur.ContainerUUID == "" ||
		cur.SnapshotEventID < 0 || cur.AfterEventID < 0 ||
		cur.AfterEventID > cur.SnapshotEventID {
		return timelineCursor{}, fmt.Errorf("invalid timeline cursor fields")
	}
	return cur, nil
}
