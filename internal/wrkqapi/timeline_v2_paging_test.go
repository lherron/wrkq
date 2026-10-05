//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// The v2 timeline reader's paging contract: the merge horizon between its two
// sources, both directions, windows, type filters, seeks, and task selection.

// TestTimelineMergeHorizonAcrossPages is T-08328's regression. The two sources
// are scanned at a fixed ROW cap, so a small project_events table drains in one
// page while event_log is still deep in its own past. Without a common
// timestamp horizon the page-one merge emits every project event before the
// older event rows that arrive pages later, and the delivered stream jumps
// backwards at the boundary.
//
// The fixture is that shape in miniature: a scan-cap's worth of old filler
// events, then one deliverable event that is NEWER than the filler but OLDER
// than the only project event, positioned beyond the first page's reach.
func TestTimelineMergeHorizonAcrossPages(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "horizon", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")

	insertRawEvents(t, api, monitorMaxPageLimit, "ignored.raw", "")
	// Every row written so far is filler as far as the merge is concerned, and
	// backdating the whole prefix keeps id order equal to timestamp order --
	// the invariant the per-source scan depends on.
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2020-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	setTaskState(t, api, task.ID, "completed")
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2026-01-01 00:00:00' WHERE timestamp <> '2020-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	event := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "git.commit"})
	if _, err := api.db.Exec(`UPDATE project_events SET created_at = '2026-01-02 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	delivered := drainTimeline(t, api, ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 100,
	}, 8)

	for index := 1; index < len(delivered); index++ {
		if delivered[index].Timestamp < delivered[index-1].Timestamp {
			t.Fatalf("delivered stream jumps backwards at %d: %s -> %s (%#v)",
				index, delivered[index-1].Timestamp, delivered[index].Timestamp, delivered)
		}
	}
	if len(delivered) < 2 {
		t.Fatalf("expected the state change and the project event, got %#v", delivered)
	}
	last := delivered[len(delivered)-1]
	if last.ProjectEvent == nil || last.ProjectEvent.UUID != event.UUID {
		t.Fatalf("newest entry is not the project event: %#v", delivered)
	}
	if !timelineContainsIDs(delivered, 0, event.ID) {
		t.Fatalf("project event never delivered: %#v", delivered)
	}
}

// TestTimelineMergeHorizonBound pins the horizon rule itself: only a truncated
// source bounds the page, and the earliest such bound wins.
func TestTimelineMergeHorizonBound(t *testing.T) {
	if bound := timelineScanBound(false, true, "2026-01-01T00:00:00Z"); bound != "" {
		t.Fatalf("a drained source must impose no bound, got %q", bound)
	}
	if bound := timelineScanBound(true, false, ""); bound != "" {
		t.Fatalf("an empty source must impose no bound, got %q", bound)
	}
	if got := timelineMergeHorizon(false, "2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z"); got != "2026-01-01T00:00:00Z" {
		t.Fatalf("horizon = %q, want the earliest bound", got)
	}
	if got := timelineMergeHorizon(false, "", ""); got != "" {
		t.Fatalf("no bound must leave the page unbounded, got %q", got)
	}
	rows := []timelineRawProjectEvent{
		{entry: WrkqTimelineEntry{Timestamp: "2026-01-01T00:00:00Z"}},
		{entry: WrkqTimelineEntry{Timestamp: "2026-01-05T00:00:00Z"}},
	}
	if kept := trimTimelineRows(rows, "2026-01-01T00:00:00Z", false); len(kept) != 1 {
		t.Fatalf("trim kept %d rows, want the horizon prefix", len(kept))
	}
	if kept := trimTimelineRows(rows, "", false); len(kept) != 2 {
		t.Fatalf("an unbounded page must keep every row, got %d", len(kept))
	}
	// Descending delivery inverts the rule: the LATEST bound wins, and rows
	// EARLIER than the horizon are the ones withheld.
	if got := timelineMergeHorizon(true, "2026-02-01T00:00:00Z", "2026-01-01T00:00:00Z"); got != "2026-02-01T00:00:00Z" {
		t.Fatalf("descending horizon = %q, want the latest bound", got)
	}
	descRows := []timelineRawProjectEvent{
		{entry: WrkqTimelineEntry{Timestamp: "2026-01-05T00:00:00Z"}},
		{entry: WrkqTimelineEntry{Timestamp: "2026-01-01T00:00:00Z"}},
	}
	if kept := trimTimelineRows(descRows, "2026-01-05T00:00:00Z", true); len(kept) != 1 {
		t.Fatalf("descending trim kept %d rows, want the horizon prefix", len(kept))
	}
}

// TestTimelineDescendingIsTheExactReverse is F2's contract. A `log` verb reads
// newest-first, so --limit N means "the N most recent". The descending page
// must be the exact reverse of the ascending one over the same rows -- ties
// included -- or the two directions disagree about what the timeline IS.
func TestTimelineDescendingIsTheExactReverse(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "desc", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	setTaskState(t, api, task.ID, "in_progress", "completed")
	for index := 0; index < 3; index++ {
		postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "git.commit"})
	}

	drain := func(order string) []WrkqTimelineEntry {
		t.Helper()
		return drainTimeline(t, api, ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 2, Order: order,
		}, 8)
	}

	ascending := drain("asc")
	descending := drain("desc")
	if len(ascending) != len(descending) || len(ascending) < 5 {
		t.Fatalf("asc delivered %d, desc delivered %d", len(ascending), len(descending))
	}
	for index, entry := range descending {
		mirror := ascending[len(ascending)-1-index]
		if entry.Timestamp != mirror.Timestamp || entry.EventID != mirror.EventID ||
			(entry.ProjectEvent == nil) != (mirror.ProjectEvent == nil) {
			t.Fatalf("desc[%d] is not the mirror of asc[%d]: %#v vs %#v",
				index, len(ascending)-1-index, entry, mirror)
		}
	}
	for index := 1; index < len(descending); index++ {
		if descending[index].Timestamp > descending[index-1].Timestamp {
			t.Fatalf("descending stream jumps forwards at %d: %s -> %s",
				index, descending[index-1].Timestamp, descending[index].Timestamp)
		}
	}

	// A cursor is a position in ONE direction and must never be reinterpreted
	// in the other, and a tail follows appends so it cannot run backwards.
	first, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 2, Order: "desc",
	})
	if err != nil || first.NextCursor == "" {
		t.Fatalf("descending first page = %#v %v", first, err)
	}
	if timelineCursorVersion(first.NextCursor) != 3 {
		t.Fatalf("descending cursor version = %d, want 3", timelineCursorVersion(first.NextCursor))
	}
	if _, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Order: "asc", Cursor: first.NextCursor,
	}); err == nil {
		t.Fatal("asc over a descending cursor must be refused")
	}
	if _, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Order: "desc", Tail: true,
	}); err == nil {
		t.Fatal("a descending tail must be refused")
	}
	if _, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Order: "sideways",
	}); err == nil {
		t.Fatal("an unknown order must be refused")
	}
}

// TestTimelineDescendingHonoursTheHorizon is the T-08328 fixture read backwards:
// the newest end is reached on page one instead of after the whole history, and
// the horizon still keeps the two sources from crossing.
func TestTimelineDescendingHonoursTheHorizon(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "deschorizon", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	setTaskState(t, api, task.ID, "completed")
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2026-01-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}
	insertRawEvents(t, api, monitorMaxPageLimit, "ignored.raw", "2026-03-01 00:00:00")
	event := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "git.commit"})
	if _, err := api.db.Exec(`UPDATE project_events SET created_at = '2026-02-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	// A page may legitimately deliver nothing -- here the newest 1000 rows are
	// all unmatched filler -- so the contract is about the DELIVERED stream,
	// not about page one. The first entry delivered must be the newest match,
	// and the stream must never run forwards.
	delivered := drainTimeline(t, api, ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 100, Order: "desc",
	}, 8)
	if len(delivered) < 2 {
		t.Fatalf("expected the project event and the state change, got %#v", delivered)
	}
	if delivered[0].ProjectEvent == nil || delivered[0].ProjectEvent.UUID != event.UUID {
		t.Fatalf("descending stream must open on the newest match, got %#v", delivered)
	}
	for index := 1; index < len(delivered); index++ {
		if delivered[index].Timestamp > delivered[index-1].Timestamp {
			t.Fatalf("descending stream jumps forwards at %d: %s -> %s",
				index, delivered[index-1].Timestamp, delivered[index].Timestamp)
		}
	}
	// The horizon still separates the sources: the project event is NEWER than
	// the state change and must be delivered before it, never beside it.
	if delivered[len(delivered)-1].ProjectEvent != nil && delivered[len(delivered)-1].ProjectEvent.UUID == event.UUID {
		t.Fatalf("project event landed at the oldest end: %#v", delivered)
	}
}

// A sparse foreign type must page only its matching rows even when the wrkq
// source contains far more unrelated events than one raw scan page. The same
// fixture checks an empty type and the exclusive server-time upper bound.
func TestTimelineSparseTypeWindowPages(t *testing.T) {
	if !timelineRequestUsesV2(ContainerTimelineViewParams{Before: "2026-01-04T00:00:00Z"}) {
		t.Fatal("before-only request must select the bounded timeline reader")
	}
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "sparsewindow", "project", nil)
	insertRawEvents(t, api, monitorMaxPageLimit+17, "unrelated.raw", "2026-01-01T00:00:00Z")
	var facts []string
	for _, stamp := range []string{
		"2026-01-02T00:00:00Z", "2026-01-03T00:00:00Z", "2026-01-04T00:00:00Z",
	} {
		fact := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "verify.upkeep"})
		facts = append(facts, fact.UUID)
		if _, err := api.db.Exec(`UPDATE project_events SET created_at = ? WHERE id = ?`, stamp, fact.ID); err != nil {
			t.Fatal(err)
		}
	}
	read := func(order string) []string {
		t.Helper()
		got := []string{}
		for _, entry := range drainTimeline(t, api, ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true,
			Types: []string{"verify.upkeep"}, Since: "2026-01-02T00:00:00Z",
			Before: "2026-01-04T00:00:00Z", Order: order, Limit: 1,
		}, 6) {
			if entry.ProjectEvent == nil {
				t.Fatalf("%s window included wrkq entry: %#v", order, entry)
			}
			got = append(got, entry.ProjectEvent.UUID)
		}
		return got
	}
	for _, tc := range []struct {
		order string
		want  []string
	}{
		{"asc", []string{facts[0], facts[1]}},
		{"desc", []string{facts[1], facts[0]}},
	} {
		got := read(tc.order)
		if len(got) != len(tc.want) || got[0] != tc.want[0] || got[1] != tc.want[1] {
			t.Fatalf("%s window = %v, want %v", tc.order, got, tc.want)
		}
	}
	zero, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true,
		Types: []string{"schedule.fired"}, Since: "2026-01-02T00:00:00Z",
		Before: "2026-01-04T00:00:00Z", Limit: 1,
	})
	if err != nil || len(zero.Entries) != 0 || zero.NextCursor != "" || zero.SnapshotEventID != 0 {
		t.Fatalf("zero-match sparse window = %#v, %v", zero, err)
	}
	var plan string
	if err := api.db.QueryRow(`EXPLAIN QUERY PLAN SELECT pe.id FROM project_events pe INDEXED BY project_events_type_time_idx
		WHERE pe.id > ? AND pe.id <= ? AND pe.type = ? AND pe.created_at >= ? AND pe.created_at < ?
		ORDER BY pe.id DESC LIMIT ?`, 0, 1000000, "verify.upkeep", "2026-01-02T00:00:00Z", "2026-01-04T00:00:00Z", 1000).Scan(new(int), new(int), new(int), &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "project_events_type_time_idx") || !strings.Contains(plan, "type=?") {
		t.Fatalf("sparse project query did not seek type-leading index: %s", plan)
	}
	for _, before := range []string{"invalid", "2026-01-02T00:00:00Z"} {
		_, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true,
			Since: "2026-01-02T00:00:00Z", Before: before,
		})
		if err == nil {
			t.Fatalf("invalid or empty window accepted: before=%q", before)
		}
	}
}

func TestTimelineMixedTypeCursorWindowBinding(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "mixedwindow", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "task", "open", "")
	setTaskState(t, api, task.ID, "completed")
	fact := postProjectEvent(t, api, ProjectEventPostParams{Project: project.UUID, Type: "hook.settled"})
	first, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true,
		Types: []string{"task.state", "hook.*"}, Since: "2160h", Limit: 1,
	})
	if err != nil || len(first.Entries) != 1 || first.NextCursor == "" {
		t.Fatalf("mixed first page = %#v, %v", first, err)
	}
	second, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true,
		Types: []string{"task.state", "hook.*"}, Since: "2160h", Limit: 1,
		Cursor: first.NextCursor,
	})
	if err != nil || len(second.Entries) != 1 {
		t.Fatalf("mixed second page = %#v, %v", second, err)
	}
	if first.Entries[0].Type != "task.state" || second.Entries[0].ProjectEvent == nil ||
		second.Entries[0].ProjectEvent.Type != "hook.settled" || second.Entries[0].ProjectEvent.UUID != fact.UUID {
		t.Fatalf("mixed normalized/project types were not paged exactly: %#v %#v", first.Entries, second.Entries)
	}
	for _, mutation := range []func(*ContainerTimelineViewParams){
		func(p *ContainerTimelineViewParams) { p.Types = []string{"hook.*"} },
		func(p *ContainerTimelineViewParams) { p.Before = "2026-01-01T00:00:00Z" },
		func(p *ContainerTimelineViewParams) { p.Task = task.ID },
	} {
		params := ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true,
			Types: []string{"task.state", "hook.*"}, Since: "2160h", Limit: 1,
			Cursor: first.NextCursor,
		}
		mutation(&params)
		if _, err := api.ContainerTimelineView(context.Background(), params); err == nil {
			t.Fatalf("changed cursor window/filter was accepted: %#v", params)
		}
	}
}

// TestAscendingSinceSeeksPastHistory pins the fix for the `--follow --since`
// stall. A tail is ascending by construction, and an ascending scan starts at
// the OLDEST row, so before the seek it walked every event ever written before
// reaching the window: on a real ledger that was ~78 pages at the poll interval
// (~16s) to print a backlog that `--since` alone returned in 0.15s.
//
// The assertion is on the CURSOR POSITION, not on wall time, so it states the
// mechanism and cannot flake: the opening tail page must start just below the
// first in-window row rather than at zero. Removing the seek leaves
// AfterEventID at 0 and fails this.
func TestAscendingSinceSeeksPastHistory(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "seek", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "target", "open", "")

	// A long prefix of history, all far older than the floor.
	insertRawEvents(t, api, 2500, "ignored.raw", "")
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2020-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}

	// One recent, deliverable entry inside the window.
	setTaskState(t, api, task.ID, "completed")
	var firstRecent int64
	if err := api.db.QueryRow(
		`SELECT MIN(id) FROM event_log WHERE timestamp > '2020-01-01T00:00:00Z'`,
	).Scan(&firstRecent); err != nil {
		t.Fatal(err)
	}

	view := timelineView(t, api, ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true,
		Since: "2h", Tail: true, Limit: 100,
	})

	cur, err := decodeTimelineCursorAny(view.NextCursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	// The scan must have skipped the 2500-row prefix outright.
	if cur.AfterEventID < firstRecent-1 {
		t.Fatalf("ascending tail did not seek past history: AfterEventID=%d, want >= %d "+
			"(the first in-window row is %d; starting below that walks the whole ledger)",
			cur.AfterEventID, firstRecent-1, firstRecent)
	}
	// And the in-window entry is still delivered -- a seek that overshot would
	// be fast and WRONG.
	found := false
	for _, entry := range view.Entries {
		if entry.TaskID == task.ID && entry.Type == "task.state" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the in-window entry was skipped by the seek: %#v", view.Entries)
	}
}

// TestAscendingSinceSeekKeepsAnEmptyWindowUsable pins the no-match arm: when
// every row predates the floor there is nothing to deliver, and the tail must
// start at the END rather than replaying history.
func TestAscendingSinceSeekKeepsAnEmptyWindowUsable(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "seekempty", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "target", "open", "")

	setTaskState(t, api, task.ID, "completed")
	if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2020-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	var maxEvent int64
	if err := api.db.QueryRow(`SELECT MAX(id) FROM event_log`).Scan(&maxEvent); err != nil {
		t.Fatal(err)
	}

	view := timelineView(t, api, ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true,
		Since: "2h", Tail: true, Limit: 100,
	})
	if len(view.Entries) != 0 {
		t.Fatalf("every row predates the floor, so nothing may be delivered: %#v", view.Entries)
	}
	cur, err := decodeTimelineCursorAny(view.NextCursor)
	if err != nil {
		t.Fatalf("decode cursor: %v", err)
	}
	if cur.AfterEventID != maxEvent {
		t.Fatalf("an all-behind-the-floor source must start at the end: AfterEventID=%d, want %d",
			cur.AfterEventID, maxEvent)
	}
}

// Selection must commute with paging and preserve the entire public entry,
// including affiliation, across both independently fenced sources.
func TestTimelineTaskSelectionMatchesUnfilteredPages(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := createProjectEventContainer(t, s, "selection", "project", nil)
	task := createTimelineTask(t, s, project.UUID, "target", "open", "")
	other := createTimelineTask(t, s, project.UUID, "other", "open", "")
	for _, id := range []string{task.ID, other.ID, task.ID} {
		setTaskState(t, api, id, "in_progress")
		postProjectEvent(t, api, ProjectEventPostParams{Task: id})
		if _, err := api.RoomSay(context.Background(), RoomSayParams{Ref: id, ScopeRef: "clod@selection:primary", PrincipalRef: "agent:clod", To: []string{"cody@selection:primary", "astra@selection:primary"}, Body: "selected message"}); err != nil {
			t.Fatal(err)
		}
	}
	for _, order := range []string{"asc", "desc"} {
		read := func(selector string) []WrkqTimelineEntry {
			t.Helper()
			entries := []WrkqTimelineEntry{}
			for _, entry := range drainTimeline(t, api, ContainerTimelineViewParams{
				Container: project.UUID, Scope: "subtree", EntriesOnly: true, AllTypes: true, Order: order, Limit: 1, Task: selector,
			}, 101) {
				if entry.TaskUUID == task.UUID {
					entries = append(entries, entry)
				}
			}
			return entries
		}
		want, got := read(""), read(task.ID)
		if len(want) == 0 {
			t.Fatal("empty comparison")
		}
		wantJSON, _ := json.Marshal(want)
		gotJSON, _ := json.Marshal(got)
		if string(wantJSON) != string(gotJSON) {
			t.Fatalf("%s filtered entries differ: got %s want %s", order, gotJSON, wantJSON)
		}
	}
}
