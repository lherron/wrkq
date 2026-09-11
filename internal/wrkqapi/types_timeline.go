package wrkqapi

import "encoding/json"

// ContainerTimelineViewParams selects one container-neutral composite snapshot.
// Cursor is opaque and carries both the event high-water fence and last emitted
// event id so later pages exclude concurrent appends.
type ContainerTimelineViewParams struct {
	Container   string   `json:"container"`
	Cursor      string   `json:"cursor,omitempty"`
	Limit       int      `json:"limit,omitempty"`
	Scope       string   `json:"scope,omitempty"`
	Types       []string `json:"types,omitempty"`
	Task        string   `json:"task,omitempty"`
	Since       string   `json:"since,omitempty"`
	EntriesOnly bool     `json:"entriesOnly,omitempty"`
	Tail        bool     `json:"tail,omitempty"`
	// Order is the delivery direction: "asc" (default, oldest first) or "desc"
	// (newest first). Default asc keeps every existing caller byte-identical.
	// A cursor carries its own direction and is authoritative; an Order that
	// contradicts it is refused rather than silently reinterpreted.
	Order string `json:"order,omitempty"`
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

// WrkqTimelineMessage is one room message projected into the timeline. It is
// the leader of its fan-out group, not one envelope per addressee: a single
// `say` to N addressees writes N envelope rows sharing one group id, and the
// timeline reports the MESSAGE, so every addressee of that one say is listed
// here in `to` rather than repeated as N entries.
type WrkqTimelineMessage struct {
	EnvelopeID string   `json:"envelopeId"`
	GroupID    string   `json:"groupId,omitempty"`
	RoomID     string   `json:"roomId,omitempty"`
	RoomKind   string   `json:"roomKind,omitempty"`
	From       string   `json:"from"`
	To         []string `json:"to"`
	Obligation string   `json:"obligation"`
	Body       string   `json:"body"`
}

type WrkqTimelineOutcome struct {
	Text *string `json:"text"`
}

type WrkqTimelineTaskState struct {
	From            *string `json:"from,omitempty"`
	State           string  `json:"state"`
	SourceEventType string  `json:"sourceEventType"`
}

type WrkqTimelineProjectEvent struct {
	UUID         string          `json:"uuid"`
	Type         string          `json:"type"`
	Attributes   json.RawMessage `json:"attributes"`
	PrincipalRef *string         `json:"principalRef"`
	ScopeRef     *string         `json:"scopeRef"`
	Summary      string          `json:"summary"`
	OccurredAt   string          `json:"occurredAt"`
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
	Message        *WrkqTimelineMessage        `json:"message,omitempty"`
	Outcome        *WrkqTimelineOutcome        `json:"outcome,omitempty"`
	TaskState      *WrkqTimelineTaskState      `json:"taskState,omitempty"`
	ContainerState *WrkqTimelineContainerState `json:"containerState,omitempty"`
	ProjectEvent   *WrkqTimelineProjectEvent   `json:"projectEvent,omitempty"`
}

type WrkqContainerTimelineView struct {
	Container              WrkqTimelineContainer          `json:"container"`
	Campaign               *WrkqCampaignAdornment         `json:"campaign"`
	Members                []WrkqTimelineMember           `json:"members"`
	Rollup                 WrkqTimelineRollup             `json:"rollup"`
	MissingOutcomes        []WrkqCampaignMemberDiagnostic `json:"missingOutcomes"`
	Footprint              []WrkqCampaignFootprint        `json:"footprint"`
	LastActivityAt         string                         `json:"lastActivityAt"`
	DecisionTasks          []WrkqTimelineMember           `json:"decisionTasks"`
	Entries                []WrkqTimelineEntry            `json:"entries"`
	SnapshotEventID        int64                          `json:"snapshotEventId"`
	SnapshotProjectEventID int64                          `json:"snapshotProjectEventId,omitempty"`
	NextCursor             string                         `json:"nextCursor,omitempty"`
	entriesOnly            bool
}

// MarshalJSON keeps the legacy full projection byte-for-byte while allowing
// the v2 entriesOnly path to omit the expensive portfolio fields entirely.
func (v WrkqContainerTimelineView) MarshalJSON() ([]byte, error) {
	type fullAlias WrkqContainerTimelineView
	if !v.entriesOnly {
		return json.Marshal(fullAlias(v))
	}
	return json.Marshal(struct {
		Container              WrkqTimelineContainer  `json:"container"`
		Campaign               *WrkqCampaignAdornment `json:"campaign"`
		LastActivityAt         string                 `json:"lastActivityAt"`
		Entries                []WrkqTimelineEntry    `json:"entries"`
		SnapshotEventID        int64                  `json:"snapshotEventId"`
		SnapshotProjectEventID int64                  `json:"snapshotProjectEventId,omitempty"`
		NextCursor             string                 `json:"nextCursor,omitempty"`
	}{
		Container: v.Container, Campaign: v.Campaign,
		LastActivityAt: v.LastActivityAt, Entries: v.Entries,
		SnapshotEventID:        v.SnapshotEventID,
		SnapshotProjectEventID: v.SnapshotProjectEventID,
		NextCursor:             v.NextCursor,
	})
}

type timelineCursor struct {
	Version                int    `json:"v"`
	ContainerUUID          string `json:"containerUuid"`
	SnapshotEventID        int64  `json:"snapshotEventId"`
	AfterEventID           int64  `json:"afterEventId"`
	Scope                  string `json:"scope,omitempty"`
	SnapshotProjectEventID int64  `json:"snapshotProjectEventId,omitempty"`
	AfterProjectEventID    int64  `json:"afterProjectEventId,omitempty"`
	// Version 3 is the descending cursor. It walks each source DOWN from the
	// fence, so its position is an exclusive UPPER bound per source rather than
	// the ascending reader's exclusive lower bound. Zero means that source is
	// drained; the ascending After* fields are unused.
	BeforeEventID        int64 `json:"beforeEventId,omitempty"`
	BeforeProjectEventID int64 `json:"beforeProjectEventId,omitempty"`
}
