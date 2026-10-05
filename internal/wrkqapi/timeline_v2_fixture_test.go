//go:build wrkq_local

package wrkqapi

import (
	"context"
	"testing"
)

// Shared fixture for the v2 timeline reader tests: read and drain pages, seed
// the rows the reader merges, and inspect what it delivered.

// setTaskState walks a task through each state in turn, one task.state event
// per step.
func setTaskState(t *testing.T, api *API, taskID string, states ...string) {
	t.Helper()
	for _, state := range states {
		if _, err := api.TaskUpdate(context.Background(), TaskUpdateParams{Task: taskID, Patch: TaskPatch{State: &state}}); err != nil {
			t.Fatalf("set %s to %s: %v", taskID, state, err)
		}
	}
}

// insertRawEvents writes event_log rows no timeline delivers: filler that only
// moves the raw scan. An empty timestamp keeps the column default.
func insertRawEvents(t *testing.T, api *API, count int, eventType, timestamp string) {
	t.Helper()
	for index := 0; index < count; index++ {
		var err error
		if timestamp == "" {
			_, err = api.db.Exec(`INSERT INTO event_log(resource_type,event_type) VALUES ('system',?)`, eventType)
		} else {
			_, err = api.db.Exec(`INSERT INTO event_log(resource_type,event_type,timestamp) VALUES ('system',?,?)`, eventType, timestamp)
		}
		if err != nil {
			t.Fatalf("insert raw event: %v", err)
		}
	}
}

// timelineView reads one timeline page, failing the test on error.
func timelineView(t *testing.T, api *API, p ContainerTimelineViewParams) *WrkqContainerTimelineView {
	t.Helper()
	view, err := api.ContainerTimelineView(context.Background(), p)
	if err != nil {
		t.Fatalf("timeline %+v: %v", p, err)
	}
	return view
}

// drainTimeline follows the cursor from p until the timeline drains, failing if
// it needs more than maxPages pages.
func drainTimeline(t *testing.T, api *API, p ContainerTimelineViewParams, maxPages int) []WrkqTimelineEntry {
	t.Helper()
	delivered := []WrkqTimelineEntry{}
	for page := 0; page < maxPages; page++ {
		view := timelineView(t, api, p)
		delivered = append(delivered, view.Entries...)
		if view.NextCursor == "" {
			return delivered
		}
		p.Cursor = view.NextCursor
	}
	t.Fatalf("timeline %+v did not drain in %d pages", p, maxPages)
	return nil
}

// messagesWithBody is a project's subtree message stream narrowed to one body.
func messagesWithBody(t *testing.T, api *API, projectUUID, body string) []WrkqTimelineEntry {
	t.Helper()
	view := timelineView(t, api, ContainerTimelineViewParams{
		Container: projectUUID, Scope: "subtree", EntriesOnly: true, Types: []string{"message"}, Limit: 100,
	})
	entries := []WrkqTimelineEntry{}
	for _, entry := range view.Entries {
		if entry.Message != nil && entry.Message.Body == body {
			entries = append(entries, entry)
		}
	}
	return entries
}

// countEventEntries counts the entries sourced from event_log.
func countEventEntries(entries []WrkqTimelineEntry) int {
	count := 0
	for _, entry := range entries {
		if entry.EventID != 0 {
			count++
		}
	}
	return count
}

// countEnvelopeStamps counts a say group's envelopes carrying a project stamp
// on either endpoint.
func countEnvelopeStamps(t *testing.T, api *API, groupID string) int {
	t.Helper()
	var stamped int
	if err := api.db.QueryRow(`SELECT COUNT(*) FROM envelopes
		WHERE group_id = ? AND (from_project_uuid IS NOT NULL OR to_project_uuid IS NOT NULL)`, groupID).Scan(&stamped); err != nil {
		t.Fatalf("count envelope stamps: %v", err)
	}
	return stamped
}

func timelineContainsIDs(entries []WrkqTimelineEntry, eventID, projectEventID int64) bool {
	sawEvent, sawProject := false, false
	for _, entry := range entries {
		if eventID != 0 && entry.EventID == eventID {
			sawEvent = true
		}
		if projectEventID != 0 && entry.ProjectEvent != nil && entry.ProjectEvent.UUID != "" {
			sawProject = true
		}
	}
	return (eventID == 0 || sawEvent) && (projectEventID == 0 || sawProject)
}

func timelineContainsProjectUUID(entries []WrkqTimelineEntry, uuid string) bool {
	for _, entry := range entries {
		if entry.ProjectEvent != nil && entry.ProjectEvent.UUID == uuid {
			return true
		}
	}
	return false
}
