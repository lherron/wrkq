//go:build wrkq_local

package rpccli

import (
	"encoding/json"
	"strings"
	"testing"
)

// T-11305: `wrkc say --note` records addressed mail that is never presented,
// `wrkc notes` is the recipient's read, and `wrkc inbox` points at it.

func TestWrkcSayNoteRefusesConflictingFlagsBeforeWriting(t *testing.T) {
	f := newWrkcFixture(t)
	for _, extra := range [][]string{
		{"--fyi"}, {"--preempt"}, {"--wait"}, {"--ttl", "30s"},
	} {
		args := append([]string{"say", f.taskID, "-m", "conflict", "--to", "cody", "--note"}, extra...)
		out, err := runWrkc(t, f.dbPath, "agent:clod", args...)
		if err == nil || !strings.Contains(err.Error(), "--note") || !strings.Contains(err.Error(), extra[0]) {
			t.Fatalf("say --note %v = %v\n%s", extra, err, out)
		}
	}
	out, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "-m", "conflict", "--note")
	if err == nil || !strings.Contains(err.Error(), "--note requires --to") {
		t.Fatalf("say --note without --to = %v\n%s", err, out)
	}
	logOut, err := runWrkc(t, f.dbPath, "agent:clod", "log", f.taskID, "--json")
	if err == nil && strings.Contains(logOut, "conflict") {
		t.Fatalf("a refused note was written:\n%s", logOut)
	}
}

func TestWrkcNoteIsTerminalListedByNotesAndCountedInTheInbox(t *testing.T) {
	f := newWrkcFixture(t)
	codySeat := "cody@wrkc-proj:" + f.taskID

	sayOut, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "-m", "nightly queue empty\nsecond line",
		"--to", "cody", "--note", "--json")
	if err != nil {
		t.Fatalf("say --note: %v\n%s", err, sayOut)
	}
	var said roomSayResultWire
	if err := json.Unmarshal([]byte(sayOut), &said); err != nil {
		t.Fatalf("decode say: %v\n%s", err, sayOut)
	}
	if len(said.Envelopes) != 1 || said.Envelopes[0].Obligation != "note" || said.Envelopes[0].State != "acked" {
		t.Fatalf("say --note wrote %+v", said.Envelopes)
	}
	noteID := said.Envelopes[0].ID

	showOut, err := runWrkc(t, f.dbPath, "agent:cody", "show", noteID, "--output", "human")
	if err != nil || !strings.Contains(showOut, "obligation: note") || !strings.Contains(showOut, "acked") {
		t.Fatalf("show note = %v\n%s", err, showOut)
	}

	inboxOut, err := runWrkc(t, f.dbPath, "agent:cody", "inbox", "--scope-ref", codySeat, "--output", "human")
	if err != nil {
		t.Fatalf("inbox: %v\n%s", err, inboxOut)
	}
	lines := strings.Split(strings.TrimRight(inboxOut, "\n"), "\n")
	if last := lines[len(lines)-1]; last != "1 note in the last 24h: wrkc notes" {
		t.Fatalf("inbox trailing line = %q\n%s", last, inboxOut)
	}
	if !strings.Contains(inboxOut, "no standing obligations") {
		t.Fatalf("a note was listed as an obligation:\n%s", inboxOut)
	}
	jsonOut, err := runWrkc(t, f.dbPath, "agent:cody", "inbox", "--scope-ref", codySeat, "--json")
	if err != nil {
		t.Fatalf("inbox --json: %v\n%s", err, jsonOut)
	}
	var inbox struct {
		RecentNotes *int `json:"recentNotes"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &inbox); err != nil || inbox.RecentNotes == nil || *inbox.RecentNotes != 1 {
		t.Fatalf("inbox --json recentNotes = %v (%v)\n%s", inbox.RecentNotes, err, jsonOut)
	}

	// The sender's own inbox has no count line: notes are counted for their
	// addressee only.
	senderOut, err := runWrkc(t, f.dbPath, "agent:clod", "inbox", "--output", "human")
	if err != nil || strings.Contains(senderOut, "in the last 24h") {
		t.Fatalf("sender inbox = %v\n%s", err, senderOut)
	}

	notesOut, err := runWrkc(t, f.dbPath, "agent:cody", "notes", "--scope-ref", codySeat)
	if err != nil || !strings.Contains(notesOut, noteID) || !strings.Contains(notesOut, "second line") {
		t.Fatalf("wrkc notes = %v\n%s", err, notesOut)
	}
	notesJSON, err := runWrkc(t, f.dbPath, "agent:cody", "notes", "--scope-ref", codySeat, "--since", "2d", "--json")
	if err != nil {
		t.Fatalf("wrkc notes --json: %v\n%s", err, notesJSON)
	}
	var listed struct {
		ScopeRef *string        `json:"scopeRef"`
		Items    []envelopeWire `json:"items"`
	}
	if err := json.Unmarshal([]byte(notesJSON), &listed); err != nil || len(listed.Items) != 1 || listed.Items[0].ID != noteID ||
		listed.ScopeRef == nil || *listed.ScopeRef != codySeat {
		t.Fatalf("wrkc notes --json = %+v (%v)\n%s", listed, err, notesJSON)
	}
	if _, err := runWrkc(t, f.dbPath, "agent:cody", "notes", "--scope-ref", codySeat, "--since", "whenever"); err == nil {
		t.Fatal("wrkc notes accepted a malformed --since")
	}

	emptyOut, err := runWrkc(t, f.dbPath, "agent:mable", "notes", "--scope-ref", "mable@wrkc-proj:primary", "--output", "human")
	if err != nil || !strings.Contains(emptyOut, "no notes") {
		t.Fatalf("empty wrkc notes = %v\n%s", err, emptyOut)
	}
}

func TestWrkcNoteReplyDischargesTheAsk(t *testing.T) {
	f := newWrkcFixture(t)
	clodSeat := "clod@wrkc-proj:" + f.taskID
	codySeat := "cody@wrkc-proj:" + f.taskID

	askOut, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "-m", "status?", "--to", "cody",
		"--scope-ref", clodSeat, "--json")
	if err != nil {
		t.Fatalf("ask: %v\n%s", err, askOut)
	}
	var ask roomSayResultWire
	if err := json.Unmarshal([]byte(askOut), &ask); err != nil {
		t.Fatal(err)
	}
	replyOut, err := runWrkc(t, f.dbPath, "agent:cody", "say", ask.Envelopes[0].ID, "-m", "done", "--to", clodSeat,
		"--note", "--scope-ref", codySeat, "--json")
	if err != nil {
		t.Fatalf("note reply: %v\n%s", err, replyOut)
	}
	var reply roomSayResultWire
	if err := json.Unmarshal([]byte(replyOut), &reply); err != nil {
		t.Fatal(err)
	}
	if len(reply.Acked) != 1 || reply.Acked[0] != ask.Envelopes[0].ID {
		t.Fatalf("note reply acked %v, want %s", reply.Acked, ask.Envelopes[0].ID)
	}
	inboxOut, err := runWrkc(t, f.dbPath, "agent:cody", "inbox", "--scope-ref", codySeat, "--output", "human")
	if err != nil || !strings.Contains(inboxOut, "no standing obligations") {
		t.Fatalf("addressee inbox after the note reply = %v\n%s", err, inboxOut)
	}
}
