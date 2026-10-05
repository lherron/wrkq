//go:build wrkq_local

package wrkqapi

import (
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/domain"
)

// Delivery: what one scanned row becomes on the page, if anything — its public
// type, its membership in this timeline, and its message or comment detail.

func deliverTimelineEvent(raw timelineRawEvent, root string, affiliation map[string]bool, filters []string, selectedTasks map[string]bool, since *time.Time) (WrkqTimelineEntry, bool, error) {
	if !slices.Contains(timelineSupportedEventTypes, raw.eventType) {
		return WrkqTimelineEntry{}, false, nil
	}
	// The fan-out siblings of a say join to no envelope row (the leader
	// predicate lives in the join), so they are dropped here exactly as an
	// unsupported event type is: the cursor has already advanced past them.
	if raw.eventType == "envelope.created" && !raw.envelope.id.Valid {
		return WrkqTimelineEntry{}, false, nil
	}
	entry := raw.entry
	if err := normalizeTimelineEntry(&entry, raw.eventType, raw.payload, root); err != nil {
		return WrkqTimelineEntry{}, false, NewInternalError(err)
	}
	if timelineQuietTypes[entry.Type] && !timelineCreatedVisible(entry, raw.subtask) &&
		(len(filters) == 0 || !timelineTypeMatches(filters, entry.Type)) {
		return WrkqTimelineEntry{}, false, nil
	}
	if raw.eventType == "envelope.created" {
		applyTimelineEnvelope(&entry, raw.envelope, root)
	}
	if raw.eventType == "task.moved" {
		applyTimelineMove(&entry, raw.payload, root, affiliation)
	}
	applyTimelineMembership(&entry, root, affiliation)
	if entry.Membership == "" || !timelineTypeMatches(filters, entry.Type) ||
		(len(selectedTasks) > 0 && !selectedTasks[entry.TaskUUID]) || !timelineSinceMatches(entry.Timestamp, since) {
		return WrkqTimelineEntry{}, false, nil
	}
	if entry.Type == "comment" {
		comment := &WrkqTimelineComment{ID: raw.commentID, Body: raw.commentBody}
		if raw.commentKind.Valid {
			value := raw.commentKind.String
			comment.Kind = &value
		}
		if raw.commentMeta.Valid && json.Valid([]byte(raw.commentMeta.String)) {
			comment.Meta = json.RawMessage(raw.commentMeta.String)
		}
		entry.Comment = comment
	}
	return entry, true, nil
}

func deliverTimelineProjectEvent(raw timelineRawProjectEvent, root string, affiliation map[string]bool, filters []string, selectedTasks map[string]bool, since *time.Time) (WrkqTimelineEntry, bool) {
	applyTimelineMembership(&raw.entry, root, affiliation)
	if raw.entry.Membership == "" || !timelineTypeMatches(filters, raw.semantic) ||
		(len(selectedTasks) > 0 && !selectedTasks[raw.entry.TaskUUID]) || !timelineSinceMatches(raw.entry.Timestamp, since) {
		return WrkqTimelineEntry{}, false
	}
	return raw.entry, true
}

// applyTimelineEnvelope carries the room-message columns onto the entry. An
// envelope event's payload holds no affiliation, so container and campaign come
// from the ROOM: a task room resolves to the same container and campaign a
// comment on that task would carry, and a container room to its own container.
// Ad-hoc rooms are anchored to neither. Their endpoint stamps, and those of a
// project room read from a non-owning endpoint project, supply participant
// membership without consulting current room membership or scope slugs.
func applyTimelineEnvelope(entry *WrkqTimelineEntry, env timelineRawEnvelope, root string) {
	if env.container.Valid {
		entry.ContainerUUID = env.container.String
	}
	if env.campaign.Valid {
		value := env.campaign.String
		entry.CampaignUUID = &value
	}
	message := &WrkqTimelineMessage{
		EnvelopeID: env.id.String,
		GroupID:    env.groupID.String,
		RoomID:     env.roomID.String,
		RoomKind:   env.roomKind.String,
		From:       env.from.String,
		Obligation: env.obligation.String,
		Body:       env.body.String,
		To:         []string{},
	}
	entry.Message = message
	if (env.roomKind.String == string(domain.RoomKindAdhoc) ||
		env.roomKind.String == string(domain.RoomKindProject)) &&
		env.container.String != root &&
		(env.fromProject.String == root || timelineProjectListContains(env.toProjects.String, root)) {
		entry.Membership = "participant"
	}
}

func timelineProjectListContains(raw, target string) bool {
	for _, uuid := range strings.Split(raw, ",") {
		if uuid == target {
			return true
		}
	}
	return false
}

// timelineQuietTypes are delivered only to a read whose type filter names them
// (exactly or by a trailing glob). An unfiltered read -- the human timeline --
// never sees them. They exist for a cursor-driven reader that mirrors tasks
// and must learn of every change it has to re-read.
var timelineQuietTypes = map[string]bool{"task.created": true, "task.edited": true, "task.moved": true}

// timelineCreatedVisible lifts task.created out of the quiet set for delegated
// work: a named subtask (decided from the task row's owner relation) or a task
// created with requester fields. Ordinary task creation stays quiet.
func timelineCreatedVisible(entry WrkqTimelineEntry, subtask bool) bool {
	return entry.Type == "task.created" && (subtask || entry.Requester != nil)
}

// applyTimelineMove affiliates a move by the container it LEFT as well as the
// one it entered: a reader of the old project must learn the task went away.
// Both are immutable stamps in the move payload. The destination wins when both
// belong to this timeline.
func applyTimelineMove(entry *WrkqTimelineEntry, payload, root string, affiliation map[string]bool) {
	var move struct {
		Old string `json:"oldContainerUuid"`
		New string `json:"newContainerUuid"`
	}
	if json.Unmarshal([]byte(payload), &move) != nil {
		return
	}
	entry.ContainerUUID = move.New
	if move.New != root && !affiliation[move.New] && (move.Old == root || affiliation[move.Old]) {
		entry.ContainerUUID = move.Old
	}
}

func applyTimelineMembership(entry *WrkqTimelineEntry, root string, affiliation map[string]bool) {
	if entry.Membership == "participant" {
		return
	}
	entry.Membership = ""
	switch {
	case entry.CampaignUUID != nil && *entry.CampaignUUID == root:
		if entry.ContainerUUID == root {
			entry.Membership = "resident"
		} else {
			entry.Membership = "enrolled"
		}
	case entry.ContainerUUID == root:
		entry.Membership = "resident"
	case affiliation[entry.ContainerUUID]:
		entry.Membership = "subtree"
	case entry.Type == "container.state" && affiliation[entry.ResourceUUID]:
		entry.Membership = "subtree"
	}
}
