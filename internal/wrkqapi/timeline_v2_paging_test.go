//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"testing"
	"time"
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

// TestTimelineDescendingHonoursTheHorizon is the T-08328 fixture read backwards.
// Descending, event_log is scanned newest-first: a scan-cap's worth of NEWER
// filler fills page one, and the only deliverable event row sits beyond the cap.
// The lone project event is OLDER than that event row, so project_events drains
// on page one while event_log is still truncated. Without the descending horizon
// page one emits the project event, the event row arrives a page later, and the
// delivered stream jumps forwards in time.
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
	if _, err := api.db.Exec(`UPDATE project_events SET created_at = '2025-12-01 00:00:00'`); err != nil {
		t.Fatal(err)
	}

	// A page may legitimately deliver nothing -- here the newest 1000 rows are
	// all unmatched filler -- so the contract is about the DELIVERED stream,
	// not about page one: it must never run forwards, and the older project
	// event must come after the newer event rows, exactly once.
	delivered := drainTimeline(t, api, ContainerTimelineViewParams{
		Container: project.UUID, Scope: "subtree", EntriesOnly: true, Limit: 100, Order: "desc",
	}, 8)
	if len(delivered) < 2 {
		t.Fatalf("expected the state change and the project event, got %#v", delivered)
	}
	for index := 1; index < len(delivered); index++ {
		if delivered[index].Timestamp > delivered[index-1].Timestamp {
			t.Fatalf("descending stream jumps forwards at %d: %s -> %s (%#v)",
				index, delivered[index-1].Timestamp, delivered[index].Timestamp, delivered)
		}
	}
	projectEvents := 0
	for _, entry := range delivered {
		if entry.ProjectEvent != nil && entry.ProjectEvent.UUID == event.UUID {
			projectEvents++
		}
	}
	if projectEvents != 1 {
		t.Fatalf("project event delivered %d times, want once: %#v", projectEvents, delivered)
	}
	last := delivered[len(delivered)-1]
	if last.ProjectEvent == nil || last.ProjectEvent.UUID != event.UUID {
		t.Fatalf("the oldest entry is not the project event: %#v", delivered)
	}
}

// A sparse foreign type must page only its matching rows even when the wrkq
// source contains far more unrelated events than one raw scan page. The same
// fixture checks an empty type and the exclusive server-time upper bound.
func TestTimelineSparseTypeWindowPages(t *testing.T) {
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
	// A bare `before` (no scope, types or since) is still a bounded read: it
	// delivers the facts before the bound and withholds the one at it.
	seen := map[string]bool{}
	for _, entry := range drainTimeline(t, api, ContainerTimelineViewParams{
		Container: project.UUID, Before: "2026-01-04T00:00:00Z",
	}, 6) {
		if entry.ProjectEvent != nil {
			seen[entry.ProjectEvent.UUID] = true
		}
	}
	if !seen[facts[0]] || !seen[facts[1]] || seen[facts[2]] {
		t.Fatalf("before-only read delivered %v, want facts before the bound only (%v)", seen, facts)
	}
	for _, before := range []string{"invalid", "2026-01-02T00:00:00Z"} {
		_, err := api.ContainerTimelineView(context.Background(), ContainerTimelineViewParams{
			Container: project.UUID, Scope: "subtree", EntriesOnly: true,
			Since: "2026-01-02T00:00:00Z", Before: before,
		})
		if err == nil {
			t.Fatalf("invalid or empty window accepted: before=%q", before)
		}
		requireValidationReason(t, err, "before", "")
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

// TestAscendingSinceTailDeliversOnlyTheWindowThenOnlyNewAppends is what a
// `--follow --since` caller sees. The opening tail page delivers the in-window
// entries and nothing from the history behind the floor, and the next poll on
// its cursor delivers only what was appended since. When every row predates
// the floor the opening page is empty and the tail still picks up the next
// append, instead of replaying history.
//
// The window has two floors that back each other up: the opening page's seek
// past history (the `--follow --since` stall fix in timeline_v2_request.go) and
// the raw scan's own `timestamp >= since` predicate (timelineSourcePredicate).
// Removing either alone delivers the same entries, so the seek's benefit is
// query cost only and this test does not claim to guard it; removing both
// fails it (the opening page then spends its scan cap on history).
func TestAscendingSinceTailDeliversOnlyTheWindowThenOnlyNewAppends(t *testing.T) {
	rows := []struct {
		name          string
		inWindowEntry bool // a state change lands inside the window before the first read
	}{
		{name: "long history behind the floor and one in-window entry", inWindowEntry: true},
		{name: "every row behind the floor", inWindowEntry: false},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			api, s := newMonitorAPI(t)
			project := createProjectEventContainer(t, s, "seek", "project", nil)
			task := createTimelineTask(t, s, project.UUID, "target", "open", "")
			insertRawEvents(t, api, 2500, "ignored.raw", "")
			if !row.inWindowEntry {
				setTaskState(t, api, task.ID, "in_progress")
			}
			// Everything written so far, task entries included, predates the floor.
			if _, err := api.db.Exec(`UPDATE event_log SET timestamp = '2020-01-01T00:00:00Z'`); err != nil {
				t.Fatal(err)
			}
			if row.inWindowEntry {
				setTaskState(t, api, task.ID, "in_progress")
			}

			params := ContainerTimelineViewParams{
				Container: project.UUID, Scope: "subtree", EntriesOnly: true,
				Since: "2h", Tail: true, Limit: 100,
			}
			floor := time.Now().Add(-3 * time.Hour)
			first := timelineView(t, api, params)
			for _, entry := range first.Entries {
				at, err := time.Parse(time.RFC3339Nano, entry.Timestamp)
				if err != nil {
					t.Fatalf("entry timestamp %q: %v", entry.Timestamp, err)
				}
				if at.Before(floor) {
					t.Fatalf("history behind the floor was delivered: %#v", entry)
				}
			}
			stateEntries := func(entries []WrkqTimelineEntry) int {
				n := 0
				for _, entry := range entries {
					if entry.TaskID == task.ID && entry.Type == "task.state" {
						n++
					}
				}
				return n
			}
			wantFirst := 0
			if row.inWindowEntry {
				wantFirst = 1
			}
			if len(first.Entries) != wantFirst || stateEntries(first.Entries) != wantFirst {
				t.Fatalf("opening tail page = %#v, want %d in-window state change(s)", first.Entries, wantFirst)
			}
			if first.NextCursor == "" {
				t.Fatal("the opening tail page returned no cursor to poll from")
			}

			setTaskState(t, api, task.ID, "completed")
			params.Cursor = first.NextCursor
			next := timelineView(t, api, params)
			if len(next.Entries) != 1 || stateEntries(next.Entries) != 1 {
				t.Fatalf("next poll = %#v, want only the one appended state change", next.Entries)
			}
		})
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
