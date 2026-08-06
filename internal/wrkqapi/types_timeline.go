package wrkqapi

import "encoding/json"

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
	UUID          string   `json:"uuid"`
	ID            string   `json:"id"`
	Slug          string   `json:"slug"`
	Title         string   `json:"title"`
	Description   string   `json:"description"`
	Specification *string  `json:"specification,omitempty"`
	Labels        []string `json:"labels"`
	Kind          string   `json:"kind"`
	ParentUUID    string   `json:"parentUuid,omitempty"`
	Path          string   `json:"path"`
	ETag          int64    `json:"etag"`
	CreatedAt     string   `json:"createdAt"`
	UpdatedAt     string   `json:"updatedAt"`
	ArchivedAt    string   `json:"archivedAt,omitempty"`
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
	UUID       string              `json:"uuid"`
	ID         string              `json:"id"`
	Path       string              `json:"path"`
	Title      string              `json:"title"`
	State      string              `json:"state"`
	Outcome    *string             `json:"outcome,omitempty"`
	Membership string              `json:"membership"`
	Project    WrkqCampaignProject `json:"project"`
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
	Footprint       []WrkqCampaignFootprint        `json:"footprint"`
	LastActivityAt  string                         `json:"lastActivityAt"`
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
