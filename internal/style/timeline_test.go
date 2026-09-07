package style

import (
	"encoding/json"
	"strings"
	"testing"
)

// timelineFixture is one day of a braided project timeline: a floating project
// fact, a task run with a state tick and prose, and a second run.
func timelineFixture() []StyledEntry {
	return []StyledEntry{
		{
			Timestamp: "2026-09-07T11:43:40Z", Label: "git.commit", Accent: ColMarker,
			ID: "PE-00103", Principal: "agent:cody",
			Body: "commit a1d4cd19 on main: fix(server): project terminal warm submissions",
		},
		{
			Timestamp: "2026-09-07T14:15:05Z", Label: "→ in_progress", Accent: StateColor("in_progress"),
			Principal: "agent:cody", TaskID: "T-07733", TaskPath: "hcs/inbox/hcs-core",
			TaskState: "in_progress",
		},
		{
			Timestamp: "2026-09-07T14:15:05Z", Label: "C-17217", Accent: ColDim, ID: "C-17217",
			Principal: "agent:cody", TaskID: "T-07733", TaskPath: "hcs/inbox/hcs-core",
			Body: "Starting implementation.",
		},
		{
			Timestamp: "2026-09-08T09:00:00Z", Label: "outcome", Accent: ColDone,
			Principal: "agent:lance", TaskID: "T-07736", TaskPath: "hcs/lance/probe",
			Body: "closed via hcs",
		},
	}
}

func renderFixture(t *testing.T, entries []StyledEntry) string {
	t.Helper()
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })
	var buf strings.Builder
	RenderStyledTimeline(&buf, "hcs", entries)
	return buf.String()
}

func TestTimelineBraidsRunsAndFloats(t *testing.T) {
	got := renderFixture(t, timelineFixture())

	// A fact that names no task floats behind ◆; a task's entries hang under one
	// run header on a rail, and the run closes with └.
	for _, want := range []string{
		"hcs\n",
		"Mon 2026-09-07",
		"  ◆ 11:43   git.commit  PE-00103",
		"T-07733  ● hcs-core",
		"  │ 14:15   → in_progress",
		"  └ 14:15   C-17217",
		"Tue 2026-09-08",
		"T-07736  ● probe",
		"  └ 09:00   outcome",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("timeline missing %q in:\n%s", want, got)
		}
	}

	// One header per run: the two T-07733 entries fuse rather than repeating it.
	if n := strings.Count(got, "T-07733  ●"); n != 1 {
		t.Fatalf("expected one T-07733 run header, got %d in:\n%s", n, got)
	}
	// The run header carries where; every row carries who.
	if !strings.Contains(got, "hcs/inbox") {
		t.Fatalf("run header lost its container path:\n%s", got)
	}
	if !strings.Contains(got, "agent:cody") || !strings.Contains(got, "agent:lance") {
		t.Fatalf("rows lost their actor:\n%s", got)
	}
}

func TestTimelineNeverReordersAndSplitsRunsOnLaneChange(t *testing.T) {
	entries := timelineFixture()
	// Interleave a second task between the two T-07733 entries: adjacency, not
	// task identity, is what fuses a run.
	interleaved := []StyledEntry{
		entries[1],
		{
			Timestamp: "2026-09-07T14:16:00Z", Label: "→ completed", Accent: StateColor("completed"),
			Principal: "agent:mable", TaskID: "T-07999", TaskPath: "hcs/inbox/other",
			TaskState: "completed",
		},
		entries[2],
	}
	got := renderFixture(t, interleaved)
	if n := strings.Count(got, "T-07733  ●"); n != 2 {
		t.Fatalf("an interrupted task must start a new run, got %d headers in:\n%s", n, got)
	}
	first := strings.Index(got, "→ in_progress")
	middle := strings.Index(got, "→ completed")
	last := strings.Index(got, "C-17217")
	if first >= middle || middle >= last {
		t.Fatalf("entries were reordered:\n%s", got)
	}
}

func TestTimelineShowsLeadParagraphAndCountsTheRest(t *testing.T) {
	got := renderFixture(t, []StyledEntry{{
		Timestamp: "2026-09-07T14:15:05Z", Label: "C-1", Accent: ColDim, ID: "C-1",
		Principal: "agent:cody", TaskID: "T-1", TaskPath: "p/t",
		Body: "Lead thesis.\n\nheld one\nheld two",
	}})
	if !strings.Contains(got, "Lead thesis.") {
		t.Fatalf("lead paragraph missing:\n%s", got)
	}
	if strings.Contains(got, "held one") {
		t.Fatalf("body beyond the lead paragraph must not be flowed:\n%s", got)
	}
	if !strings.Contains(got, "… 2 more lines") {
		t.Fatalf("held-back prose must be counted, never silently dropped:\n%s", got)
	}
}

// A project-event type is a free-form dotted name; the renderer must draw one it
// has never seen rather than dropping the row.
func TestTimelineRendersAnUnknownEntryAsItself(t *testing.T) {
	got := renderFixture(t, []StyledEntry{{
		Timestamp: "2026-09-07T11:00:00Z", Label: "deploy.rolled_back", Accent: ColMarker,
		ID: "PE-09999", Principal: "agent:cody", Body: "rolled back to 1.2.3",
	}})
	if !strings.Contains(got, "deploy.rolled_back") || !strings.Contains(got, "rolled back to 1.2.3") {
		t.Fatalf("unknown type must render as itself:\n%s", got)
	}
}

func TestTimelineHandlesAnEmptyPage(t *testing.T) {
	got := renderFixture(t, nil)
	if !strings.Contains(got, "no timeline entries in hcs") {
		t.Fatalf("an empty timeline must say so, not print nothing: %q", got)
	}
}

// A run split across cursor pages keeps its rail instead of restating the header.
func TestTimelineWriterResumesARunAcrossBatches(t *testing.T) {
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })

	entries := timelineFixture()
	var buf strings.Builder
	writer := NewTimelineWriter(&buf, "hcs")
	writer.Write(entries[1:2])
	writer.Write(entries[2:3])
	got := buf.String()

	if n := strings.Count(got, "T-07733  ●"); n != 1 {
		t.Fatalf("a resumed run must not restate its header, got %d in:\n%s", n, got)
	}
	if n := strings.Count(got, "Mon 2026-09-07"); n != 1 {
		t.Fatalf("the day header must not repeat across batches, got %d in:\n%s", n, got)
	}
}

func TestStyledEventCardCarriesProvenanceAndAlignedPayload(t *testing.T) {
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })
	t.Setenv("WRKQ_NOW", "2026-09-07T15:46:22Z")

	var buf strings.Builder
	RenderStyledEvent(&buf, StyledEvent{
		ID: "PE-00104", Type: "git.push", Summary: "push main 15891101..a1d4cd19 → origin",
		Source: "lefthook", Node: "max3", Principal: "agent:cody",
		ScopeRef: "agent:cody:project:hrc-runtime:task:T-08199/lane:main",
		Project:  "hrc-runtime", OccurredAt: "2026-09-07T11:46:22Z",
		Idempotency: "git.push:origin:refs/heads/main",
		Payload:     json.RawMessage(`{"commits":1,"forced":false,"remote":"origin","tasks":[]}`),
	})
	got := buf.String()

	for _, want := range []string{
		"PE-00104  ◆ git.push",
		"lefthook · agent:cody · max3",
		"occurred 4 hours ago on 2026-09-07",
		"§ Summary",
		"§ Payload",
		"commits  1",
		"forced   false",
		"remote   origin",
		"tasks    []",
		"§ Provenance",
		"produced by  agent:cody:project:hrc-runtime:task:T-08199/lane:main",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("event card missing %q in:\n%s", want, got)
		}
	}
}

// Payload values are data, not prose: a value carrying markdown punctuation must
// survive verbatim.
func TestStyledEventPayloadIsNotReinterpretedAsMarkdown(t *testing.T) {
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })

	var buf strings.Builder
	RenderStyledEvent(&buf, StyledEvent{
		ID: "PE-1", Type: "deploy.finished", Summary: "done",
		Payload: json.RawMessage(`{"ref":"refs/heads/_main_","note":"a `+"`code`"+` span"}`),
	})
	got := buf.String()
	if !strings.Contains(got, "refs/heads/_main_") {
		t.Fatalf("payload value was reinterpreted as markup:\n%s", got)
	}
	if !strings.Contains(got, "`code`") {
		t.Fatalf("payload backticks must survive verbatim:\n%s", got)
	}
}

// A payload that is not an object still renders rather than vanishing.
func TestStyledEventToleratesNonObjectPayload(t *testing.T) {
	previous := ColorEnabled
	ColorEnabled = false
	t.Cleanup(func() { ColorEnabled = previous })

	var buf strings.Builder
	RenderStyledEvent(&buf, StyledEvent{
		ID: "PE-2", Type: "x.y", Summary: "s", Payload: json.RawMessage(`[1,2]`),
	})
	if !strings.Contains(buf.String(), "[1,2]") {
		t.Fatalf("a non-object payload must still render:\n%s", buf.String())
	}
}
