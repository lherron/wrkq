//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"strings"
	"time"
)

// The merged timeline is assembled from two independently scanned sources, and
// each scan is bounded by a ROW COUNT, not by time. Those two facts do not
// compose: event_log is large and project_events is small, so one page of the
// fixed fence drains project_events entirely while event_log is still tens of
// thousands of rows into its own past. Merging those two pages is locally
// correct and globally wrong — every project event is emitted before the event
// rows that share its week, which arrive pages later and jump the delivered
// stream backwards (T-08328: a 47-day reversal at the page-one boundary, so any
// --limit under the project-event count returned nothing but git facts).
//
// The horizon is the repair. A source truncated by its scan cap still has
// unread rows immediately after its last one, so NO source may emit past that
// timestamp; the withheld rows would otherwise be delivered on a later page,
// behind entries older than themselves. A source that drained to the fence has
// no such rows and imposes no bound. The page's horizon is therefore the
// EARLIEST bound any truncated source imposes, and every source is trimmed to
// it before the merge. Per-source cursors advance only to the last KEPT row, so
// the withheld rows are re-read, in order, on the next page.
//
// Progress is guaranteed: the source that DEFINES the horizon keeps its whole
// page, because its own last row is the horizon. A page can therefore never
// trim both sources to nothing, and the fence always drains.

// timelineV2Page is one page's scan of both sources, trimmed to the merge
// horizon, and how far the merge has consumed each.
type timelineV2Page struct {
	eventRows      []timelineRawEvent
	projectRows    []timelineRawProjectEvent
	eventDrained   bool
	projectDrained bool
	eventIndex     int
	projectIndex   int
}

// scanTimelineSources reads one scan page from each eligible source and trims
// both to the merge horizon. A source is drained when its scan was neither
// truncated by the cap nor trimmed by the horizon.
func scanTimelineSources(ctx context.Context, tx *sql.Tx, req *timelineV2Request, selectedTasks, affiliation map[string]bool, root string) (*timelineV2Page, error) {
	cur := req.cur
	eventLow, eventHigh := timelineScanWindow(req.desc, cur.AfterEventID, cur.SnapshotEventID, cur.BeforeEventID)
	projectLow, projectHigh := timelineScanWindow(req.desc, cur.AfterProjectEventID, cur.SnapshotProjectEventID, cur.BeforeProjectEventID)
	var eventRows []timelineRawEvent
	var projectRows []timelineRawProjectEvent
	var eventTruncated, projectTruncated bool
	var err error
	if req.eventEligible {
		eventRows, eventTruncated, err = loadTimelineRawEvents(ctx, tx, eventLow, eventHigh, req.desc, req.eventTypes, req.since, req.before, selectedTasks, affiliation, root)
		if err != nil {
			return nil, err
		}
	}
	if req.projectEligible {
		projectRows, projectTruncated, err = loadTimelineRawProjectEvents(ctx, tx, projectLow, projectHigh, req.desc, req.filters, req.since, req.before, selectedTasks, affiliation, root)
		if err != nil {
			return nil, err
		}
	}
	horizon := timelineMergeHorizon(req.desc,
		timelineScanBound(eventTruncated, len(eventRows) > 0, lastTimelineTimestamp(eventRows)),
		timelineScanBound(projectTruncated, len(projectRows) > 0, lastTimelineTimestamp(projectRows)),
	)
	eventKept := trimTimelineRows(eventRows, horizon, req.desc)
	projectKept := trimTimelineRows(projectRows, horizon, req.desc)
	return &timelineV2Page{
		eventRows: eventKept, projectRows: projectKept,
		eventDrained:   !eventTruncated && len(eventKept) == len(eventRows),
		projectDrained: !projectTruncated && len(projectKept) == len(projectRows),
	}, nil
}

// merge delivers up to limit entries from both sources in delivery order,
// moving each source's cursor past every row it consumes, delivered or not.
func (page *timelineV2Page) merge(req *timelineV2Request, p ContainerTimelineViewParams, limit int,
	root string, affiliation, selectedTasks map[string]bool) ([]WrkqTimelineEntry, error) {
	entries := make([]WrkqTimelineEntry, 0, limit)
	for len(entries) < limit && (page.eventIndex < len(page.eventRows) || page.projectIndex < len(page.projectRows)) {
		popEvent := page.projectIndex >= len(page.projectRows)
		if page.eventIndex < len(page.eventRows) && page.projectIndex < len(page.projectRows) {
			popEvent = timelineHeadFirst(page.eventRows[page.eventIndex], page.projectRows[page.projectIndex], req.desc)
		}
		if popEvent {
			raw := page.eventRows[page.eventIndex]
			page.eventIndex++
			req.cur.AfterEventID = raw.entry.EventID
			req.cur.BeforeEventID = raw.entry.EventID
			entry, included, err := deliverTimelineEvent(raw, root, affiliation, p.Types, selectedTasks, req.since)
			if err != nil {
				return nil, err
			}
			if included {
				entries = append(entries, entry)
			}
			continue
		}
		raw := page.projectRows[page.projectIndex]
		page.projectIndex++
		req.cur.AfterProjectEventID = raw.id
		req.cur.BeforeProjectEventID = raw.id
		if !p.AllTypes && len(req.filters) == 0 && strings.HasPrefix(raw.semantic, "turn.") {
			continue
		}
		if entry, included := deliverTimelineProjectEvent(raw, root, affiliation, p.Types, selectedTasks, req.since); included {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// advanceCursor closes every source the page drained and reports whether the
// timeline has more to deliver.
func (page *timelineV2Page) advanceCursor(req *timelineV2Request) bool {
	cur := &req.cur
	eventConsumed := page.eventDrained && page.eventIndex == len(page.eventRows)
	projectConsumed := page.projectDrained && page.projectIndex == len(page.projectRows)
	if !req.desc {
		if eventConsumed {
			cur.AfterEventID = cur.SnapshotEventID
		}
		if projectConsumed {
			cur.AfterProjectEventID = cur.SnapshotProjectEventID
		}
		return cur.AfterEventID < cur.SnapshotEventID || cur.AfterProjectEventID < cur.SnapshotProjectEventID
	}
	// The descending reader has no cheap fence to compare against -- it walks
	// toward id 0 -- so a source reports itself drained when its scan was
	// neither truncated by the cap nor trimmed by the horizon. `since` closes it
	// earlier: below the floor no older row can ever match. Only the CONSUMED
	// prefix may close a source. A page that filled the delivery limit early
	// leaves kept rows unread, and those are re-read from the unchanged position
	// on the next page.
	if eventConsumed || timelineFloorReached(page.eventRows[:page.eventIndex], req.since) {
		cur.BeforeEventID = 0
	}
	if projectConsumed || timelineFloorReached(page.projectRows[:page.projectIndex], req.since) {
		cur.BeforeProjectEventID = 0
	}
	return cur.BeforeEventID > 0 || cur.BeforeProjectEventID > 0
}

// timelineScannedRow is a row of either source, as the merge sees it.
type timelineScannedRow interface {
	timestamp() string
}

func (raw timelineRawEvent) timestamp() string        { return raw.entry.Timestamp }
func (raw timelineRawProjectEvent) timestamp() string { return raw.entry.Timestamp }

// timelineFloorReached reports that a descending scan has passed below
// `since`: within one source id order is timestamp order, so once a consumed
// row is older than the floor no older row can ever match.
func timelineFloorReached[R timelineScannedRow](consumed []R, since *time.Time) bool {
	if since == nil || len(consumed) == 0 {
		return false
	}
	return !timelineSinceMatches(consumed[len(consumed)-1].timestamp(), since)
}

func lastTimelineTimestamp[R timelineScannedRow](rows []R) string {
	if len(rows) == 0 {
		return ""
	}
	return rows[len(rows)-1].timestamp()
}

// trimTimelineRows drops the trailing rows past the horizon. Rows are already
// in scan order within a source, so the kept prefix is contiguous and the
// source cursor still advances monotonically.
func trimTimelineRows[R timelineScannedRow](rows []R, horizon string, desc bool) []R {
	if horizon == "" {
		return rows
	}
	for index, row := range rows {
		if timelineAheadOf(row.timestamp(), horizon, desc) {
			return rows[:index]
		}
	}
	return rows
}

// timelineScanBound reports the timestamp bound one source imposes, or "" when
// it imposes none. Only a truncated source with rows bounds the page.
func timelineScanBound(truncated, hasRows bool, last string) string {
	if !truncated || !hasRows {
		return ""
	}
	return last
}

// timelineMergeHorizon is the earliest bound across sources. Timestamps are
// normalized by toRFC3339 to a fixed-width UTC layout, so lexical order is
// chronological order — the same comparison the merge itself makes.
func timelineMergeHorizon(desc bool, bounds ...string) string {
	horizon := ""
	for _, bound := range bounds {
		if bound == "" {
			continue
		}
		if horizon == "" || timelineAheadOf(horizon, bound, desc) {
			horizon = bound
		}
	}
	return horizon
}

// timelineAheadOf reports whether a is further along the delivery direction
// than b. Ascending delivery runs toward later timestamps, descending toward
// earlier ones, so "the earliest bound wins" inverts with the direction.
func timelineAheadOf(a, b string, desc bool) bool {
	if desc {
		return a < b
	}
	return a > b
}

// timelineHeadFirst reports whether the event head is delivered before the
// project head. Descending delivery is the exact reverse of ascending, ties
// included: event_log has source rank zero and wins an ascending server-time
// tie, so it must LOSE the descending one for a descending page to be the
// reverse of the ascending page over the same rows.
func timelineHeadFirst(event timelineRawEvent, project timelineRawProjectEvent, desc bool) bool {
	if event.entry.Timestamp != project.entry.Timestamp {
		return timelineAheadOf(project.entry.Timestamp, event.entry.Timestamp, desc)
	}
	return !desc
}
