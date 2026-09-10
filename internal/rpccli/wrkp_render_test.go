package rpccli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/style"
)

// resolveWrkpMode is the interactive-defaults contract: a terminal gets the human
// porcelain, a pipe gets the machine format, and --pretty forces the layout the
// way it does for `wrkq cat`.
func TestResolveWrkpModeDefaultsInteractiveToHumanPorcelain(t *testing.T) {
	for _, tc := range []struct {
		name   string
		args   []string
		pretty bool
		ndjson bool
		piped  bool
		want   string
	}{
		{name: "tty default is the styled render", want: "human"},
		{name: "a pipe falls to the machine format", piped: true, want: "ndjson"},
		{name: "--pretty forces layout through a pipe", pretty: true, piped: true, want: "human"},
		{name: "--pretty overrides an explicit machine mode", pretty: true, args: []string{"--json"}, want: "human"},
		{name: "--json wins on a terminal", args: []string{"--json"}, want: "json"},
		{name: "--ndjson wins on a terminal", ndjson: true, want: "ndjson"},
		{name: "--output human is honored through a pipe", args: []string{"--output", "human"}, piped: true, want: "human"},
		{name: "--output json is honored", args: []string{"--output", "json"}, want: "json"},
		{name: "--output ndjson is honored", args: []string{"--output", "ndjson"}, want: "ndjson"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewWrkpRootCmd()
			cmd.SetArgs(append([]string{"log"}, tc.args...))
			log, _, err := cmd.Find(append([]string{"log"}, tc.args...))
			if err != nil {
				t.Fatalf("find log command: %v", err)
			}
			if err := log.ParseFlags(tc.args); err != nil {
				t.Fatalf("parse flags: %v", err)
			}
			log.SetOut(modeWriter(t, tc.piped))
			if got := resolveWrkpMode(log, tc.pretty, tc.ndjson); got != tc.want {
				t.Fatalf("mode = %q, want %q", got, tc.want)
			}
		})
	}
}

// modeWriter stands in for stdout. isStdoutTTY asks whether the writer is an
// *os.File on a character device, so /dev/null is a real terminal as far as the
// gate is concerned, and a buffer is a pipe.
func modeWriter(t *testing.T, piped bool) io.Writer {
	t.Helper()
	if piped {
		return &bytes.Buffer{}
	}
	device, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatalf("open %s: %v", os.DevNull, err)
	}
	t.Cleanup(func() { _ = device.Close() })
	return device
}

func TestStyledTimelineEntriesGivesEachKindItsWeight(t *testing.T) {
	kind := "note"
	from := "open"
	text := "closed via hcs"
	campaignFrom := "draft"

	entries := []timelineEntry{
		{Type: "comment", Timestamp: "2026-09-07T14:15:05Z", PrincipalRef: "agent:cody",
			TaskID: "T-1", TaskPath: "p/c/t"},
		{Type: "task.state", Timestamp: "2026-09-07T14:16:05Z", PrincipalRef: "agent:cody"},
		{Type: "task.outcome", Timestamp: "2026-09-07T14:17:05Z", PrincipalRef: "agent:lance"},
		{Type: "container.state", Timestamp: "2026-09-07T14:18:05Z", PrincipalRef: "agent:mable"},
		{Type: "project.event", Timestamp: "2026-09-07T14:19:05Z"},
	}
	entries[0].Comment = &struct {
		ID   string  `json:"id,omitempty"`
		Kind *string `json:"kind,omitempty"`
		Body string  `json:"body"`
	}{ID: "C-9", Kind: &kind, Body: "prose"}
	entries[1].TaskState = &struct {
		From            *string `json:"from,omitempty"`
		State           string  `json:"state"`
		SourceEventType string  `json:"sourceEventType"`
	}{From: &from, State: "in_progress", SourceEventType: "task.updated"}
	entries[2].Outcome = &struct {
		Text *string `json:"text"`
	}{Text: &text}
	entries[3].ContainerState = &struct {
		From *string `json:"from"`
		To   string  `json:"to"`
	}{From: &campaignFrom, To: "active"}
	entries[4].ProjectEvent = &struct {
		FID          string          `json:"fid"`
		Type         string          `json:"type"`
		Source       string          `json:"source"`
		Node         *string         `json:"node,omitempty"`
		PrincipalRef string          `json:"principalRef"`
		Summary      string          `json:"summary"`
		Payload      json.RawMessage `json:"payload,omitempty"`
		OccurredAt   string          `json:"occurredAt"`
	}{FID: "PE-7", Type: "deploy.rolled_back", Source: "ci", PrincipalRef: "agent:clod", Summary: "back to 1.2.3"}

	styled := styledTimelineEntries(entries)
	if len(styled) != len(entries) {
		t.Fatalf("every entry must survive projection: got %d want %d", len(styled), len(entries))
	}

	// A comment names its author and what it was written on: the authoring seat
	// leads in From, the label points at the task, and the id — with its kind —
	// rides beside them. The author moves out of Principal so one row never
	// prints the same handle twice.
	if styled[0].Label != "→ T-1" || styled[0].From != "agent:cody" || styled[0].Principal != "" ||
		styled[0].ID != "C-9 note" || styled[0].Body != "prose" || styled[0].TaskID != "T-1" {
		t.Fatalf("comment projection: %+v", styled[0])
	}
	if styled[1].Label != "open → in_progress" || styled[1].TaskState != "in_progress" ||
		styled[1].Accent != style.StateColor("in_progress") {
		t.Fatalf("state projection: %+v", styled[1])
	}
	if styled[2].Label != "outcome" || styled[2].Body != text || styled[2].Accent != style.ColDone {
		t.Fatalf("outcome projection: %+v", styled[2])
	}
	if styled[3].Label != "campaign draft → active" {
		t.Fatalf("container projection: %+v", styled[3])
	}
	// An unrecognized dotted type is drawn as itself, and a project event falls
	// back to its own principal when the entry carries none.
	if styled[4].Label != "deploy.rolled_back" || styled[4].ID != "PE-7" ||
		styled[4].Body != "back to 1.2.3" || styled[4].Principal != "agent:clod" {
		t.Fatalf("project event projection: %+v", styled[4])
	}
	// A row with no task floats; the renderer reads that from an empty TaskID.
	if styled[4].TaskID != "" {
		t.Fatalf("an untasked fact must float: %+v", styled[4])
	}
}

// The CLI projection must not drop principalRef: the server sends it on every
// entry and it is the field a reader asks for first.
func TestTimelineEntryCarriesPrincipalRefThroughNDJSON(t *testing.T) {
	var entry timelineEntry
	if err := json.Unmarshal([]byte(`{"type":"comment","principalRef":"agent:chief"}`), &entry); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if entry.PrincipalRef != "agent:chief" {
		t.Fatalf("principalRef was dropped on decode: %+v", entry)
	}
	encoded, err := json.Marshal(entry)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if !strings.Contains(string(encoded), `"principalRef":"agent:chief"`) {
		t.Fatalf("principalRef was dropped on encode: %s", encoded)
	}
}

// wrkpPageLimit is the T-08328 clamp. --limit is the caller's DELIVERED budget
// and the paging loop stitches pages to reach it, so a budget above the server's
// per-page cap must be satisfied by more pages rather than refused. Before the
// clamp the whole budget went to the server verbatim, which returned
// WRKQ_VALIDATION and no rows at all.
func TestWrkpPageLimitClampsTheRequestNotTheRead(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining int
		want      int
	}{
		{name: "the default budget fits in one page", remaining: wrkpDefaultLimit, want: wrkpDefaultLimit},
		{name: "the cap itself is not clamped", remaining: wrkpMaxPageLimit, want: wrkpMaxPageLimit},
		{name: "one over the cap becomes a full page", remaining: wrkpMaxPageLimit + 1, want: wrkpMaxPageLimit},
		{name: "a large budget becomes a full page", remaining: 5000, want: wrkpMaxPageLimit},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := wrkpPageLimit(tc.remaining); got != tc.want {
				t.Fatalf("wrkpPageLimit(%d) = %d, want %d", tc.remaining, got, tc.want)
			}
		})
	}
}
