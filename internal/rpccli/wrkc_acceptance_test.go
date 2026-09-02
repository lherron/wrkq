//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

// wrkc acceptance. The headline claim proven here is structural: this test
// process contains NO HRC daemon, no hrc socket, no HRC_SESSION_REF unless the
// test sets one itself — and every wrkc verb still works. That is T-07612 §2's
// boundary rule made executable: wrkq owns collaboration, HRC is a consumer, and
// the ledger does not depend on its consumer being alive.

type wrkcFixture struct {
	dbPath   string
	taskID   string
	taskUUID string
}

func newWrkcFixture(t *testing.T) wrkcFixture {
	t.Helper()
	// Any ambient session handle would make the "no HRC" claim untestable, so
	// the fixture clears it and each case supplies its own scope explicitly.
	t.Setenv(wrkcSessionEnv, "")

	dbPath := t.TempDir() + "/wrkq.db"
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	s := store.New(database)
	actor := "00000000-0000-4000-8000-0000000000a0"
	project, err := s.Containers.Create(actor, store.ContainerCreateParams{Slug: "wrkc-proj", Kind: "project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := s.Tasks.Create(actor, store.CreateParams{
		Slug: "wrkc-task", Title: "wrkc task", ProjectUUID: project.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return wrkcFixture{dbPath: dbPath, taskID: task.ID, taskUUID: task.UUID}
}

func runWrkc(t *testing.T, dbPath, principal string, args ...string) (string, error) {
	t.Helper()
	return runWrkcInput(t, dbPath, principal, "", args...)
}

func runWrkcInput(t *testing.T, dbPath, principal, input string, args ...string) (string, error) {
	t.Helper()
	cmd := NewWrkcRootCmd()
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", principal}, args...))
	cmd.SetIn(strings.NewReader(input))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return output.String(), err
}

// runWrkcSplit keeps the two streams apart, which is the whole point of §5's
// advisory: the notice must ride stderr so a piped --json read stays parseable.
func runWrkcSplit(t *testing.T, dbPath, principal string, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewWrkcRootCmd()
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", principal}, args...))
	cmd.SetIn(strings.NewReader(""))
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// backdateWrkcRoom ages every timestamp the activity clock folds, so a test can
// reach `stale` without a clock injection.
func backdateWrkcRoom(t *testing.T, dbPath, roomUUID string, age time.Duration) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	stamp := time.Now().UTC().Add(-age).Format("2006-01-02T15:04:05Z")
	for _, statement := range []struct {
		sql  string
		args []interface{}
	}{
		{"UPDATE rooms SET opened_at = ?, last_activity_at = ? WHERE uuid = ?", []interface{}{stamp, stamp, roomUUID}},
		{"UPDATE envelopes SET created_at = ? WHERE room_uuid = ?", []interface{}{stamp, roomUUID}},
		{"UPDATE room_members SET joined_at = ? WHERE room_uuid = ?", []interface{}{stamp, roomUUID}},
	} {
		if _, err := database.Exec(statement.sql, statement.args...); err != nil {
			t.Fatalf("backdate room: %v", err)
		}
	}
}

func waitForWrkcEnvelope(t *testing.T, dbPath, body string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open while waiting for envelope: %v", err)
	}
	defer func() { _ = database.Close() }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var envelopeID string
		err = database.QueryRow("SELECT id FROM envelopes WHERE body = ? ORDER BY id DESC LIMIT 1", body).Scan(&envelopeID)
		if err == nil {
			return envelopeID
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for envelope body %q", body)
	return ""
}

// TestWrkcSayWaitConsumesOnlyTheExactAddresseeReply proves that reading a
// reply through --wait is its terminal consumption. The reply fan-out uses two
// scopes of the same principal so principal-only matching would consume the
// sibling addressed to a different seat.
func TestWrkcSayWaitConsumesOnlyTheExactAddresseeReply(t *testing.T) {
	f := newWrkcFixture(t)
	aScope := "alice@wrkc-proj:" + f.taskID
	otherAliceScope := "alice@wrkc-proj:primary"
	bScope := "bob@wrkc-proj:" + f.taskID

	type waitResult struct {
		output string
		err    error
	}
	waited := make(chan waitResult, 1)
	go func() {
		output, err := runWrkc(t, f.dbPath, "agent:alice", "say", f.taskID, "question for bob",
			"--to", bScope, "--scope-ref", aScope, "--wait", "--timeout", "3s", "--json")
		waited <- waitResult{output: output, err: err}
	}()

	questionID := waitForWrkcEnvelope(t, f.dbPath, "question for bob")
	presentationDB, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open for question presentation: %v", err)
	}
	question, err := store.New(presentationDB).Rooms.GetEnvelope(questionID)
	if err != nil {
		_ = presentationDB.Close()
		t.Fatalf("read question for presentation: %v", err)
	}
	if _, _, err := store.New(presentationDB).Rooms.RecordPresentationWithAttribution(
		attribution.Attribution{PrincipalRef: "agent:hrc"}, question.UUID,
		store.PresentationRecord{MemberRef: bScope},
	); err != nil {
		_ = presentationDB.Close()
		t.Fatalf("present question: %v", err)
	}
	if err := presentationDB.Close(); err != nil {
		t.Fatalf("close question presentation database: %v", err)
	}
	replyOut, err := runWrkc(t, f.dbPath, "agent:bob", "say", f.taskID, "answer for alice",
		"--to", aScope, "--to", otherAliceScope, "--scope-ref", bScope, "--json")
	if err != nil {
		t.Fatalf("reply fan-out: %v\n%s", err, replyOut)
	}
	var reply roomSayResultWire
	if err := json.Unmarshal([]byte(replyOut), &reply); err != nil {
		t.Fatalf("decode reply fan-out: %v\n%s", err, replyOut)
	}
	var otherAliceReplyID string
	for _, envelope := range reply.Envelopes {
		if envelope.To != nil && envelope.To.ScopeRef != nil && *envelope.To.ScopeRef == otherAliceScope {
			otherAliceReplyID = envelope.ID
		}
	}
	if otherAliceReplyID == "" {
		t.Fatalf("reply fan-out has no sibling for %s: %+v", otherAliceScope, reply.Envelopes)
	}
	transport, _, closeTransport, err := openLocalTransport(f.dbPath)
	if err != nil {
		t.Fatalf("open acceptance transport: %v", err)
	}
	_, nonAddresseeErr := transport.Call(t.Context(), "wrkq.envelope.ack", map[string]any{
		"envelopes": []string{otherAliceReplyID}, "reason": "consumed_by_wait",
		"principalRef": "agent:alice", "scopeRef": aScope,
	})
	closeTransport()
	if nonAddresseeErr == nil {
		t.Fatalf("non-addressee scope consumed %s", otherAliceReplyID)
	}

	result := <-waited
	if result.err != nil {
		t.Fatalf("wrkc say --wait: %v\n%s", result.err, result.output)
	}
	var returned []envelopeWire
	if err := json.Unmarshal([]byte(result.output), &returned); err != nil {
		t.Fatalf("decode --wait replies: %v\n%s", err, result.output)
	}
	if len(returned) != 1 || returned[0].To == nil || returned[0].To.ScopeRef == nil ||
		*returned[0].To.ScopeRef != aScope || returned[0].State != "acked" ||
		returned[0].Reason == nil || *returned[0].Reason != "consumed_by_wait" {
		t.Fatalf("returned replies = %+v, want one exact-scope consumed reply", returned)
	}
	showOut, err := runWrkc(t, f.dbPath, "agent:alice", "show", returned[0].ID, "--scope-ref", aScope, "--output", "human")
	if err != nil || !strings.Contains(showOut, "reason: consumed_by_wait") {
		t.Fatalf("wrkc show did not render consumed reason: %v\n%s", err, showOut)
	}
	logOut, err := runWrkc(t, f.dbPath, "agent:alice", "log", f.taskID, "--scope-ref", aScope, "--output", "human")
	if err != nil || !strings.Contains(logOut, "reason: consumed_by_wait") {
		t.Fatalf("wrkc log did not render consumed reason: %v\n%s", err, logOut)
	}

	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open reply ledger: %v", err)
	}
	defer func() { _ = database.Close() }()
	var siblingState string
	if err := database.QueryRow("SELECT state FROM envelopes WHERE group_id = ? AND to_scope_ref = ?",
		reply.GroupID, otherAliceScope).Scan(&siblingState); err != nil {
		t.Fatalf("read non-addressee sibling: %v", err)
	}
	if siblingState != "pending" {
		t.Fatalf("reply sibling for another scope = %s, want pending", siblingState)
	}

	inboxOut, err := runWrkc(t, f.dbPath, "agent:alice", "inbox", "--scope-ref", aScope, "--json")
	if err != nil {
		t.Fatalf("alice inbox: %v\n%s", err, inboxOut)
	}
	var inbox envelopeInboxViewWire
	if err := json.Unmarshal([]byte(inboxOut), &inbox); err != nil {
		t.Fatalf("decode alice inbox: %v\n%s", err, inboxOut)
	}
	if len(inbox.Groups) != 0 {
		t.Fatalf("consumed reply remains in alice inbox: %+v", inbox.Groups)
	}
}

// TestWrkcFullSurfaceWithNoHRCDaemon walks the §9.1 verb list end to end against
// a real database with no HRC anything in the process.
func TestWrkcFullSurfaceWithNoHRCDaemon(t *testing.T) {
	f := newWrkcFixture(t)

	// Nothing in this process is talking to HRC.
	if value := os.Getenv(wrkcSessionEnv); value != "" {
		t.Fatalf("%s leaked into the test: %q", wrkcSessionEnv, value)
	}

	// say into a task room, from a task-scoped seat, addressing a bare name.
	sayOut, err := runWrkcInput(t, f.dbPath, "agent:clod", "first message\n",
		"say", f.taskID, "-", "--to", "cody",
		"--scope-ref", "clod@wrkc-proj:"+f.taskID, "--json")
	if err != nil {
		t.Fatalf("wrkc say: %v\n%s", err, sayOut)
	}
	var said roomSayResultWire
	if err := json.Unmarshal([]byte(sayOut), &said); err != nil {
		t.Fatalf("decode say: %v\n%s", err, sayOut)
	}
	if said.Room.Key != f.taskID || said.Room.Kind != "task" {
		t.Fatalf("say routed to %s/%s", said.Room.Kind, said.Room.Key)
	}
	if len(said.Envelopes) != 1 || !strings.HasPrefix(said.Envelopes[0].ID, "EN-") {
		t.Fatalf("say envelopes = %+v", said.Envelopes)
	}
	envelopeID := said.Envelopes[0].ID
	codySeat := "cody@wrkc-proj:" + f.taskID
	if said.Envelopes[0].To == nil || said.Envelopes[0].To.ScopeRef == nil ||
		*said.Envelopes[0].To.ScopeRef != codySeat {
		t.Fatalf("bare --to did not resolve to the task seat: %+v", said.Envelopes[0].To)
	}
	// HRC's receipt data must survive the wrkc wire model used by log/show.
	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("open for presentation: %v", err)
	}
	envelope, err := store.New(database).Rooms.GetEnvelope(envelopeID)
	if err != nil {
		_ = database.Close()
		t.Fatalf("resolve envelope for presentation: %v", err)
	}
	inputID := "wrkc-input-1"
	if _, _, err := store.New(database).Rooms.RecordPresentationWithAttribution(
		attribution.Attribution{PrincipalRef: "agent:hrc"}, envelope.UUID,
		store.PresentationRecord{MemberRef: codySeat, InputID: &inputID},
	); err != nil {
		_ = database.Close()
		t.Fatalf("record presentation: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close presentation database: %v", err)
	}

	// log renders a transcript in the human mode and envelopes in JSON.
	logOut, err := runWrkc(t, f.dbPath, "agent:clod", "log", f.taskID, "--json")
	if err != nil {
		t.Fatalf("wrkc log: %v\n%s", err, logOut)
	}
	var logged []envelopeWire
	if err := json.Unmarshal([]byte(logOut), &logged); err != nil {
		t.Fatalf("decode log: %v\n%s", err, logOut)
	}
	if len(logged) != 1 || logged[0].Body != "first message" {
		t.Fatalf("log = %+v", logged)
	}
	if len(logged[0].PresentedTo) != 1 || logged[0].PresentedTo[0].InputID == nil ||
		*logged[0].PresentedTo[0].InputID != inputID {
		t.Fatalf("log receipt lost inputId: %+v", logged[0].PresentedTo)
	}

	// show dispatches on the selector shape: EN- is an envelope, anything else
	// is a room.
	showEnvelope, err := runWrkc(t, f.dbPath, "agent:clod", "show", envelopeID, "--json")
	if err != nil {
		t.Fatalf("wrkc show envelope: %v\n%s", err, showEnvelope)
	}
	var shown envelopeWire
	if err := json.Unmarshal([]byte(showEnvelope), &shown); err != nil {
		t.Fatalf("decode show envelope: %v\n%s", err, showEnvelope)
	}
	if shown.ID != envelopeID {
		t.Fatalf("show envelope = %s, want %s", shown.ID, envelopeID)
	}
	showRoom, err := runWrkc(t, f.dbPath, "agent:clod", "show", f.taskID, "--json")
	if err != nil {
		t.Fatalf("wrkc show room: %v\n%s", err, showRoom)
	}
	var room roomWire
	if err := json.Unmarshal([]byte(showRoom), &room); err != nil {
		t.Fatalf("decode show room: %v\n%s", err, showRoom)
	}
	if room.Key != f.taskID {
		t.Fatalf("show room = %s", room.Key)
	}

	// ls
	lsOut, err := runWrkc(t, f.dbPath, "agent:clod", "ls", "--json")
	if err != nil {
		t.Fatalf("wrkc ls: %v\n%s", err, lsOut)
	}
	var rooms []roomWire
	if err := json.Unmarshal([]byte(lsOut), &rooms); err != nil {
		t.Fatalf("decode ls: %v\n%s", err, lsOut)
	}
	if len(rooms) != 1 {
		t.Fatalf("ls = %+v", rooms)
	}

	// members
	membersOut, err := runWrkc(t, f.dbPath, "agent:clod", "members", f.taskID, "--json")
	if err != nil {
		t.Fatalf("wrkc members: %v\n%s", err, membersOut)
	}
	var membersView roomMembersViewWire
	if err := json.Unmarshal([]byte(membersOut), &membersView); err != nil {
		t.Fatalf("decode members: %v\n%s", err, membersOut)
	}
	if len(membersView.Items) != 2 {
		t.Fatalf("members = %+v, want the sender and the addressee", membersView.Items)
	}

	// inbox for the addressee: the obligation is standing.
	inboxOut, err := runWrkc(t, f.dbPath, "agent:cody", "inbox", "--scope-ref", codySeat, "--json")
	if err != nil {
		t.Fatalf("wrkc inbox: %v\n%s", err, inboxOut)
	}
	var inbox envelopeInboxViewWire
	if err := json.Unmarshal([]byte(inboxOut), &inbox); err != nil {
		t.Fatalf("decode inbox: %v\n%s", err, inboxOut)
	}
	if len(inbox.Groups) != 1 || len(inbox.Groups[0].Items) != 1 ||
		inbox.Groups[0].Items[0].ID != envelopeID {
		t.Fatalf("inbox = %+v", inbox)
	}

	// defer with a retry: the obligation pauses and a promise carries it.
	deferOut, err := runWrkc(t, f.dbPath, "agent:cody", "defer", envelopeID,
		"--reason", "after the build", "--retry-after", "2h", "--scope-ref", codySeat, "--json")
	if err != nil {
		t.Fatalf("wrkc defer: %v\n%s", err, deferOut)
	}
	var deferred envelopeWire
	if err := json.Unmarshal([]byte(deferOut), &deferred); err != nil {
		t.Fatalf("decode defer: %v\n%s", err, deferOut)
	}
	if deferred.State != "deferred" || deferred.RetryPromiseID == nil {
		t.Fatalf("defer = %+v", deferred)
	}

	// reply-is-ack: cody says back to clod and the deferred obligation is
	// deliberately NOT swept up, because defer is how you exclude one.
	replyOut, err := runWrkc(t, f.dbPath, "agent:cody", "say", f.taskID, "on it",
		"--to", "clod", "--scope-ref", codySeat, "--json")
	if err != nil {
		t.Fatalf("wrkc reply: %v\n%s", err, replyOut)
	}
	var reply roomSayResultWire
	if err := json.Unmarshal([]byte(replyOut), &reply); err != nil {
		t.Fatalf("decode reply: %v\n%s", err, replyOut)
	}
	if len(reply.Acked) != 0 {
		t.Fatalf("a reply acked a deferred obligation: %v", reply.Acked)
	}

	// operator ack clears it.
	ackOut, err := runWrkc(t, f.dbPath, "agent:lance", "ack", envelopeID, "--note", "handled", "--json")
	if err != nil {
		t.Fatalf("wrkc ack: %v\n%s", err, ackOut)
	}
	var acked []envelopeWire
	if err := json.Unmarshal([]byte(ackOut), &acked); err != nil {
		t.Fatalf("decode ack: %v\n%s", err, ackOut)
	}
	if len(acked) != 1 || acked[0].State != "acked" {
		t.Fatalf("ack = %+v", acked)
	}

	// hide / unhide: a listing label, never a gate. There is no close and no
	// reopen — the verbs are gone from the CLI entirely.
	hideOut, err := runWrkc(t, f.dbPath, "agent:clod", "hide", f.taskID, "--json")
	if err != nil {
		t.Fatalf("wrkc hide: %v\n%s", err, hideOut)
	}
	var hidden roomWire
	if err := json.Unmarshal([]byte(hideOut), &hidden); err != nil {
		t.Fatalf("decode hide: %v\n%s", err, hideOut)
	}
	if len(hidden.Labels) != 1 || hidden.Labels[0] != "hidden" {
		t.Fatalf("hide = %+v", hidden.Labels)
	}
	if out, serr := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "hidden rooms still take talk"); serr != nil {
		t.Fatalf("say into a hidden room was refused: %v\n%s", serr, out)
	}
	lsOut, lerr := runWrkc(t, f.dbPath, "agent:clod", "ls", "--output", "raw")
	if lerr != nil {
		t.Fatalf("wrkc ls: %v\n%s", lerr, lsOut)
	}
	if strings.Contains(lsOut, f.taskID) {
		t.Fatalf("a hidden room is in the default listing: %q", lsOut)
	}
	allOut, aerr := runWrkc(t, f.dbPath, "agent:clod", "ls", "--all", "--output", "raw")
	if aerr != nil {
		t.Fatalf("wrkc ls --all: %v\n%s", aerr, allOut)
	}
	if !strings.Contains(allOut, f.taskID) {
		t.Fatalf("--all did not show the hidden room: %q", allOut)
	}
	if unhideOut, uerr := runWrkc(t, f.dbPath, "agent:clod", "unhide", f.taskID, "--json"); uerr != nil {
		t.Fatalf("wrkc unhide: %v\n%s", uerr, unhideOut)
	}
	for _, gone := range []string{"close", "reopen", "open"} {
		if out, cerr := runWrkc(t, f.dbPath, "agent:clod", gone, f.taskID); cerr == nil {
			t.Fatalf("wrkc %s still exists: %s", gone, out)
		}
	}
	if out, serr := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "no topic flag", "--subject", "x"); serr == nil {
		t.Fatalf("wrkc say --subject still exists: %s", out)
	}

	// join / invite / leave
	if out, err := runWrkc(t, f.dbPath, "agent:mable", "join", f.taskID,
		"--scope-ref", "mable@wrkc-proj:primary", "--json"); err != nil {
		t.Fatalf("wrkc join: %v\n%s", err, out)
	}
	if out, err := runWrkc(t, f.dbPath, "agent:clod", "invite", f.taskID, "fowler@wrkc-proj:primary", "--json"); err != nil {
		t.Fatalf("wrkc invite: %v\n%s", err, out)
	}
	leaveOut, err := runWrkc(t, f.dbPath, "agent:mable", "leave", f.taskID,
		"--scope-ref", "mable@wrkc-proj:primary", "--json")
	if err != nil {
		t.Fatalf("wrkc leave: %v\n%s", err, leaveOut)
	}
	var afterLeave roomMembersViewWire
	if err := json.Unmarshal([]byte(leaveOut), &afterLeave); err != nil {
		t.Fatalf("decode leave: %v\n%s", err, leaveOut)
	}
	left := false
	for _, member := range afterLeave.Items {
		if member.MemberRef == "mable@wrkc-proj:primary" && member.LeftAt != nil {
			left = true
		}
	}
	if !left {
		t.Fatalf("leave did not record a departure: %+v", afterLeave.Items)
	}

	// info is local and needs neither a database nor a daemon.
	infoOut, err := runWrkc(t, f.dbPath, "agent:clod", "info")
	if err != nil {
		t.Fatalf("wrkc info: %v\n%s", err, infoOut)
	}
	if !strings.Contains(infoOut, "Only `--to` fires") {
		t.Fatalf("wrkc info does not carry the priming rules:\n%s", infoOut)
	}
}

func TestWrkcAdmissionFlagsAndWithdrawWithNoHRCDaemon(t *testing.T) {
	f := newWrkcFixture(t)
	sayOut, err := runWrkcInput(t, f.dbPath, "agent:clod", "admission\n",
		"say", f.taskID, "-", "--to", "cody", "--ttl", "1h", "--preempt",
		"--idempotency-key", "admission-cli-1", "--scope-ref", "clod@wrkc-proj:"+f.taskID, "--json")
	if err != nil {
		t.Fatalf("say admission: %v\n%s", err, sayOut)
	}
	var said roomSayResultWire
	if err := json.Unmarshal([]byte(sayOut), &said); err != nil {
		t.Fatal(err)
	}
	if len(said.Envelopes) != 1 || said.Envelopes[0].Delivery != "hold" || said.Envelopes[0].ExpiresAt == nil {
		t.Fatalf("admission envelope = %+v", said.Envelopes)
	}

	withdrawOut, err := runWrkc(t, f.dbPath, "agent:clod", "withdraw", said.Envelopes[0].ID, "--reason", "superseded", "--scope-ref", "clod@wrkc-proj:"+f.taskID, "--json")
	if err != nil {
		t.Fatalf("withdraw: %v\n%s", err, withdrawOut)
	}
	var withdrawn envelopeWithdrawResultWire
	if err := json.Unmarshal([]byte(withdrawOut), &withdrawn); err != nil {
		t.Fatal(err)
	}
	if len(withdrawn.Withdrawn) != 1 || withdrawn.Withdrawn[0].State != "withdrawn" || !withdrawn.Withdrawn[0].Terminal || len(withdrawn.Refused) != 0 {
		t.Fatalf("withdrawn = %+v", withdrawn)
	}

	for _, args := range [][]string{{"say", f.taskID, "body", "--ttl", "30s"}, {"say", f.taskID, "body", "--preempt"}, {"say", f.taskID, "body", "--discharges", "EN-00001"}} {
		if out, err := runWrkc(t, f.dbPath, "agent:clod", args...); err == nil {
			t.Fatalf("%v unexpectedly succeeded: %s", args, out)
		}
	}
}

// TestWrkcLsScopeIsAValueNotABoolean pins the §9.1 surface: `wrkc ls --scope me`
// takes a value. It is a convenience filter, never a permission boundary —
// rooms are readable by any principal — so the only accepted value is "me" and
// anything else is refused rather than silently ignored.
func TestWrkcLsScopeIsAValueNotABoolean(t *testing.T) {
	f := newWrkcFixture(t)
	if out, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "hello",
		"--to", "cody", "--scope-ref", "clod@wrkc-proj:"+f.taskID); err != nil {
		t.Fatalf("seed say: %v\n%s", err, out)
	}

	mine, err := runWrkc(t, f.dbPath, "agent:clod", "ls", "--scope", "me",
		"--scope-ref", "clod@wrkc-proj:"+f.taskID, "--json")
	if err != nil {
		t.Fatalf("wrkc ls --scope me: %v\n%s", err, mine)
	}
	var rooms []roomWire
	if err := json.Unmarshal([]byte(mine), &rooms); err != nil {
		t.Fatalf("decode: %v\n%s", err, mine)
	}
	if len(rooms) != 1 || rooms[0].Key != f.taskID {
		t.Fatalf("--scope me = %+v", rooms)
	}

	// A scope with no membership sees no rooms — a filter, not an error.
	none, err := runWrkc(t, f.dbPath, "agent:fowler", "ls", "--scope", "me",
		"--scope-ref", "fowler@wrkc-proj:primary", "--json")
	if err != nil {
		t.Fatalf("wrkc ls --scope me (non-member): %v\n%s", err, none)
	}
	var empty []roomWire
	if err := json.Unmarshal([]byte(none), &empty); err != nil {
		t.Fatalf("decode: %v\n%s", err, none)
	}
	if len(empty) != 0 {
		t.Fatalf("a non-member saw %d rooms via --scope me", len(empty))
	}

	if out, err := runWrkc(t, f.dbPath, "agent:clod", "ls", "--scope", "everyone"); err == nil {
		t.Fatalf("--scope everyone was accepted:\n%s", out)
	}
}

// TestWrkcSayHumanRenderingShowsTheReplyPath proves the default human output is
// legible without a JSON decoder: the envelope id, who it went to, and the room.
func TestWrkcSayHumanRenderingShowsTheReplyPath(t *testing.T) {
	f := newWrkcFixture(t)
	out, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "hello",
		"--to", "cody", "--pretty")
	if err != nil {
		t.Fatalf("wrkc say --pretty: %v\n%s", err, out)
	}
	if !strings.Contains(out, "EN-") || !strings.Contains(out, f.taskID) ||
		!strings.Contains(out, "reply_required") {
		t.Fatalf("human say output is not legible:\n%s", out)
	}
}

// TestWrkcLogTranscriptRendersBodies proves `wrkc log` is a transcript in the
// human mode, not a table of truncated cells.
func TestWrkcLogTranscriptRendersBodies(t *testing.T) {
	f := newWrkcFixture(t)
	if out, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "line one\nline two"); err != nil {
		t.Fatalf("say: %v\n%s", err, out)
	}
	out, err := runWrkc(t, f.dbPath, "agent:clod", "log", f.taskID, "--pretty")
	if err != nil {
		t.Fatalf("wrkc log --pretty: %v\n%s", err, out)
	}
	if !strings.Contains(out, "line one") || !strings.Contains(out, "line two") {
		t.Fatalf("transcript dropped the body:\n%s", out)
	}
	if !strings.Contains(out, "(log entry)") {
		t.Fatalf("a say without --to is not marked as a log entry:\n%s", out)
	}
}

func TestWrkcAdhocListShowAndLogRenderPairIdentity(t *testing.T) {
	f := newWrkcFixture(t)
	senderSeat := "clod@wrkc-proj:primary"
	targetSeat := "cody@wrkc-proj:primary"
	body := strings.Repeat("界", 81) + " hidden tail"

	create := func(extra ...string) roomWire {
		t.Helper()
		args := []string{"say", targetSeat, body, "--to", targetSeat, "--scope-ref", senderSeat, "--json"}
		args = append(args, extra...)
		out, err := runWrkc(t, f.dbPath, "agent:clod", args...)
		if err != nil {
			t.Fatalf("create pair room: %v\n%s", err, out)
		}
		var result roomSayResultWire
		if err := json.Unmarshal([]byte(out), &result); err != nil {
			t.Fatalf("decode pair room: %v\n%s", err, out)
		}
		return result.Room
	}
	first := create()
	second := create("--new")
	if first.ID == nil || second.ID == nil || *first.ID == *second.ID {
		t.Fatalf("fresh pair rooms = %v and %v", first.ID, second.ID)
	}

	listed, err := runWrkc(t, f.dbPath, "agent:clod", "ls", "--kind", "adhoc", "--output", "table")
	if err != nil {
		t.Fatalf("wrkc ls pair rooms: %v\n%s", err, listed)
	}
	for _, want := range []string{*first.ID, *second.ID, "Members", "Last activity", "Last", senderSeat, targetSeat} {
		if !strings.Contains(listed, want) {
			t.Fatalf("wrkc ls omitted %q:\n%s", want, listed)
		}
	}
	if strings.Contains(listed, "Subject") || strings.Contains(listed, "hidden tail") {
		t.Fatalf("wrkc ls retained subject or failed the 80-rune clip:\n%s", listed)
	}

	shown, err := runWrkc(t, f.dbPath, "agent:clod", "show", *first.ID, "--output", "human")
	if err != nil {
		t.Fatalf("wrkc show pair room: %v\n%s", err, shown)
	}
	logged, err := runWrkc(t, f.dbPath, "agent:clod", "log", *first.ID, "--output", "human")
	if err != nil {
		t.Fatalf("wrkc log pair room: %v\n%s", err, logged)
	}
	for name, output := range map[string]string{"show": shown, "log": logged} {
		if !strings.Contains(output, "members: "+senderSeat+", "+targetSeat) || strings.Contains(output, "subject:") {
			t.Fatalf("wrkc %s did not render pair identity:\n%s", name, output)
		}
	}
}

// TestWrkcSayRefusesWaitWithoutTo proves --wait's precondition is enforced
// caller-side, before any write.
func TestWrkcSayRefusesWaitWithoutTo(t *testing.T) {
	f := newWrkcFixture(t)
	out, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "nobody", "--wait")
	if err == nil {
		t.Fatalf("--wait without --to succeeded:\n%s", out)
	}
	if !strings.Contains(err.Error(), "--wait requires --to") {
		t.Fatalf("unexpected error: %v", err)
	}
	logOut, lerr := runWrkc(t, f.dbPath, "agent:clod", "log", f.taskID, "--json")
	if lerr == nil && strings.Contains(logOut, "nobody") {
		t.Fatalf("a refused --wait still wrote an envelope:\n%s", logOut)
	}
}

// TestWrkcInboxListsEveryObligationUniformly is the T-07633 rendering half
// INVERTED (T-07642). There is no "closed room" section and no reopen hint: an
// obligation on terminal work gates like any other, so it renders in the plain
// list with `work terminal` as context.
func TestWrkcInboxListsEveryObligationUniformly(t *testing.T) {
	f := newWrkcFixture(t)
	codySeat := "cody@wrkc-proj:" + f.taskID

	sayOut, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "answer me",
		"--to", "cody", "--scope-ref", "clod@wrkc-proj:"+f.taskID, "--json")
	if err != nil {
		t.Fatalf("wrkc say: %v\n%s", err, sayOut)
	}
	var said roomSayResultWire
	if err := json.Unmarshal([]byte(sayOut), &said); err != nil {
		t.Fatalf("decode say: %v\n%s", err, sayOut)
	}
	envelopeID := said.Envelopes[0].ID

	openOut, err := runWrkc(t, f.dbPath, "agent:cody", "inbox", "--scope-ref", codySeat, "--output", "human")
	if err != nil {
		t.Fatalf("wrkc inbox: %v\n%s", err, openOut)
	}
	if !strings.Contains(openOut, f.taskID+" (task)") {
		t.Fatalf("open room inbox = %q", openOut)
	}

	completeWrkcTask(t, f)

	terminalOut, err := runWrkc(t, f.dbPath, "agent:cody", "inbox", "--scope-ref", codySeat, "--output", "human")
	if err != nil {
		t.Fatalf("wrkc inbox after completion: %v\n%s", err, terminalOut)
	}
	for _, gone := range []string{"closed room", "wrkc reopen", "not gating your turn"} {
		if strings.Contains(terminalOut, gone) {
			t.Fatalf("inbox still renders the removed carve-out (%q): %q", gone, terminalOut)
		}
	}
	if !strings.Contains(terminalOut, "work terminal; replying still works") {
		t.Fatalf("inbox does not report terminal work as context: %q", terminalOut)
	}
	if !strings.Contains(terminalOut, envelopeID) {
		t.Fatalf("the obligation vanished from the inbox: %q", terminalOut)
	}
	if strings.Contains(terminalOut, "no standing obligations") {
		t.Fatalf("a standing obligation was reported as none: %q", terminalOut)
	}
}

// TestWrkcSayIntoAStaleRoomWritesAndNoticesOnStderr is §5 end to end: the say
// succeeds, stdout stays a clean machine read, and the advisory rides stderr.
func TestWrkcSayIntoAStaleRoomWritesAndNoticesOnStderr(t *testing.T) {
	f := newWrkcFixture(t)

	sayOut, err := runWrkc(t, f.dbPath, "agent:clod", "say", f.taskID, "first", "--json")
	if err != nil {
		t.Fatalf("wrkc say: %v\n%s", err, sayOut)
	}
	var said roomSayResultWire
	if err := json.Unmarshal([]byte(sayOut), &said); err != nil {
		t.Fatalf("decode say: %v\n%s", err, sayOut)
	}
	completeWrkcTask(t, f)
	backdateWrkcRoom(t, f.dbPath, said.Room.UUID, 6*time.Hour)

	showOut, _, err := runWrkcSplit(t, f.dbPath, "agent:clod", "show", f.taskID, "--output", "human")
	if err != nil {
		t.Fatalf("wrkc show: %v\n%s", err, showOut)
	}
	if !strings.Contains(showOut, "work: terminal") || !strings.Contains(showOut, "activity: stale") {
		t.Fatalf("wrkc show does not print the projections: %q", showOut)
	}

	stdout, stderr, err := runWrkcSplit(t, f.dbPath, "agent:clod", "say", f.taskID, "still here?", "--json")
	if err != nil {
		t.Fatalf("say into a stale room was refused: %v\n%s\n%s", err, stdout, stderr)
	}
	var late roomSayResultWire
	if err := json.Unmarshal([]byte(stdout), &late); err != nil {
		t.Fatalf("the notice contaminated stdout: %v\n%s", err, stdout)
	}
	if len(late.Envelopes) != 1 {
		t.Fatalf("stale say wrote %d envelopes, want 1", len(late.Envelopes))
	}
	if !strings.HasPrefix(strings.TrimSpace(stderr), "notice: room "+f.taskID) {
		t.Fatalf("stale notice is not on stderr: %q", stderr)
	}
	if !strings.Contains(stderr, "task completed") || !strings.Contains(stderr, "last activity") {
		t.Fatalf("stale notice does not name the transition and the age: %q", stderr)
	}
}

// completeWrkcTask drives the fixture's task terminal, which is what turns its
// room's `work` projection to terminal.
func completeWrkcTask(t *testing.T, f wrkcFixture) {
	t.Helper()
	database, err := db.Open(f.dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(
		"UPDATE tasks SET state = 'completed', completed_at = strftime('%Y-%m-%dT%H:%M:%SZ','now') WHERE uuid = ?",
		f.taskUUID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
}
