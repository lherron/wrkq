//go:build wrkq_local

package wrkqapi

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// T-09261: an addressed pending/deferred envelope that no presentation receipt
// ever reached fails undeliverable at created_at + 24h of SERVER time. Every
// fixture below ages rows relative to SQLite 'now', never the test clock.

func (f *roomFixture) ageEnvelope(t *testing.T, id, createdOffset string) {
	t.Helper()
	if _, err := f.s.DB().Exec(`UPDATE envelopes SET created_at = strftime('%Y-%m-%dT%H:%M:%SZ','now', ?) WHERE id = ?`, createdOffset, id); err != nil {
		t.Fatalf("age %s: %v", id, err)
	}
}

func (f *roomFixture) envelopeRow(t *testing.T, id string) (state, reason, actor string) {
	t.Helper()
	if err := f.s.DB().QueryRow(`SELECT state, COALESCE(failure_reason,''), COALESCE(terminal_actor,'') FROM envelopes WHERE id = ?`, id).Scan(&state, &reason, &actor); err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return state, reason, actor
}

func (f *roomFixture) envelopeEventCount(t *testing.T, id, eventType string) int {
	t.Helper()
	var n int
	if err := f.s.DB().QueryRow(`SELECT COUNT(*) FROM event_log WHERE event_type = ? AND resource_uuid = (SELECT uuid FROM envelopes WHERE id = ?)`, eventType, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *roomFixture) observe(t *testing.T) {
	t.Helper()
	if _, err := f.api.EnvelopeInboxView(context.Background(), EnvelopeInboxViewParams{PrincipalRef: "agent:cody", ScopeRef: "cody@proj:" + f.loneTaskID}); err != nil {
		t.Fatalf("observe: %v", err)
	}
}

func TestUndeliveredEnvelopeFailsAtServerTwentyFourHours(t *testing.T) {
	f := newRoomFixture(t)
	young := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "young", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0].ID
	old := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "old", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0].ID
	f.ageEnvelope(t, young, "-86340 seconds") // 23h59m
	f.ageEnvelope(t, old, "-86401 seconds")   // 24h + 1s
	f.observe(t)

	if state, _, _ := f.envelopeRow(t, young); state != "pending" {
		t.Fatalf("envelope younger than 24h = %s, want pending", state)
	}
	state, reason, actor := f.envelopeRow(t, old)
	if state != "failed" || reason != "undeliverable" || actor != "wrkq" {
		t.Fatalf("envelope older than 24h = %s/%s actor=%s, want failed/undeliverable actor=wrkq", state, reason, actor)
	}
	var payload string
	if err := f.s.DB().QueryRow(`SELECT payload FROM event_log WHERE event_type = 'envelope.failed' AND resource_uuid = (SELECT uuid FROM envelopes WHERE id = ?)`, old).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, `"reason":"undeliverable"`) || !strings.Contains(payload, `"detail":"envelope_ttl_expired"`) {
		t.Fatalf("failed payload = %s", payload)
	}

	f.observe(t)
	if n := f.envelopeEventCount(t, old, "envelope.failed"); n != 1 {
		t.Fatalf("replayed observation wrote %d envelope.failed events, want 1", n)
	}
	// Invalid created_at alone is not evidence of expiry.
	bad := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "bad clock", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0].ID
	if _, err := f.s.DB().Exec(`UPDATE envelopes SET created_at = 'not-a-time' WHERE id = ?`, bad); err != nil {
		t.Fatal(err)
	}
	f.observe(t)
	if state, _, _ := f.envelopeRow(t, bad); state != "pending" {
		t.Fatalf("invalid created_at envelope = %s, want pending", state)
	}
}

func TestUndeliveredFYIWithoutCandidateFailsOnEventObservationBeyondCursor(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	fyi := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "absent fyi", To: []string{"cody"}, FYI: true, PrincipalRef: "agent:clod"}).Envelopes[0].ID
	f.ageEnvelope(t, fyi, "-25 hours")

	// A virgin tail resolves its baseline with LastN, which performs no maintenance.
	base, err := f.api.MonitorEventsView(ctx, MonitorEventsViewParams{LastN: 1})
	if err != nil {
		t.Fatal(err)
	}
	if state, _, _ := f.envelopeRow(t, fyi); state != "pending" {
		t.Fatalf("LastN resolution materialized expiry: %s", state)
	}
	cursor := base.HighWater + 1 // start at the current end, like a fresh tail

	// An unrelated event lands before the first ordinary page.
	unrelated := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "unrelated", To: []string{"mable"}, PrincipalRef: "agent:clod"}).Envelopes[0].UUID
	page, err := f.api.MonitorEventsView(ctx, MonitorEventsViewParams{Cursor: cursor})
	if err != nil {
		t.Fatal(err)
	}
	sawUnrelated := false
	for _, e := range page.Items {
		if e.ResourceUUID != nil && *e.ResourceUUID == unrelated && e.EventType == "envelope.created" {
			sawUnrelated = true
		}
		if e.EventType == "envelope.failed" {
			t.Fatalf("maintenance event leaked into the page it ran after: %+v", e)
		}
	}
	if !sawUnrelated {
		t.Fatalf("page skipped the unrelated event: %+v", page.Items)
	}
	state, reason, _ := f.envelopeRow(t, fyi)
	if state != "failed" || reason != "undeliverable" {
		t.Fatalf("pending FYI with no candidate = %s/%s", state, reason)
	}
	var failedID int64
	var principal string
	if err := f.s.DB().QueryRow(`SELECT id, principal_ref FROM event_log WHERE event_type = 'envelope.failed' AND resource_uuid = (SELECT uuid FROM envelopes WHERE id = ?)`, fyi).Scan(&failedID, &principal); err != nil {
		t.Fatal(err)
	}
	if failedID <= page.HighWater {
		t.Fatalf("failure event %d is not beyond returned high-water %d", failedID, page.HighWater)
	}
	if principal != "agent:wrkq-system" {
		t.Fatalf("maintenance principal = %s", principal)
	}

	next, err := f.api.MonitorEventsView(ctx, MonitorEventsViewParams{Cursor: page.HighWater, EventTypes: []string{"envelope.created", "envelope.failed"}})
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, e := range next.Items {
		if e.EventType == "envelope.failed" && e.ID == failedID {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("next tail poll delivered the failure %d times: %+v", failed, next.Items)
	}
	again, err := f.api.MonitorEventsView(ctx, MonitorEventsViewParams{Cursor: next.HighWater})
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Items) != 0 || f.envelopeEventCount(t, fyi, "envelope.failed") != 1 {
		t.Fatalf("repeat poll produced %+v; failures=%d", again.Items, f.envelopeEventCount(t, fyi, "envelope.failed"))
	}
}

func TestReceiptBeforeExpiryWinsAndExpiryBeforeReceiptRefuses(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()

	delivered := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "delivered", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0].ID
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: delivered, PrincipalRef: "agent:hrc", RuntimeID: "rt-1", DriveAttemptID: "drive-1"}); err != nil {
		t.Fatal(err)
	}
	f.ageEnvelope(t, delivered, "-48 hours")
	f.observe(t)
	if state, _, _ := f.envelopeRow(t, delivered); state != "presented" {
		t.Fatalf("receipt-first envelope = %s, want presented", state)
	}

	// Expiry committed by an observation first: a later receipt is refused.
	swept := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "swept", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0]
	f.ageEnvelope(t, swept.ID, "-25 hours")
	f.observe(t)
	// Expiry committed by the presentation transaction itself: same refusal.
	raced := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "raced", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0]
	f.ageEnvelope(t, raced.ID, "-25 hours")

	for _, e := range []*WrkqEnvelope{&swept, &raced} {
		for attempt := 0; attempt < 2; attempt++ { // the second is a driveAttemptId replay
			_, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: e.ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-2", DriveAttemptID: "drive-" + e.ID})
			_ = assertDomainCode(t, CodeWrongState, err)
		}
		var receipts int
		if err := f.s.DB().QueryRow(`SELECT COUNT(*) FROM envelope_presentations WHERE envelope_uuid = ?`, e.UUID).Scan(&receipts); err != nil {
			t.Fatal(err)
		}
		state, reason, _ := f.envelopeRow(t, e.ID)
		if receipts != 0 || state != "failed" || reason != "undeliverable" || f.envelopeEventCount(t, e.ID, "envelope.failed") != 1 {
			t.Fatalf("%s: receipts=%d state=%s/%s failures=%d", e.ID, receipts, state, reason, f.envelopeEventCount(t, e.ID, "envelope.failed"))
		}
	}
}

func TestReceiptedDeferredEnvelopeIsPermanentlyExempt(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	codySeat := "cody@proj:" + f.loneTaskID
	id := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "remind me", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0].ID
	if _, err := f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: id, PrincipalRef: "agent:hrc", RuntimeID: "rt-1", DriveAttemptID: "drive-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{Envelope: id, Reason: "later", PrincipalRef: "agent:cody", ScopeRef: codySeat}); err != nil {
		t.Fatalf("defer: %v", err)
	}
	f.ageEnvelope(t, id, "-72 hours")
	f.observe(t)
	if state, _, _ := f.envelopeRow(t, id); state != "deferred" {
		t.Fatalf("receipted deferred envelope = %s, want deferred", state)
	}
	if n := f.envelopeEventCount(t, id, "envelope.failed"); n != 0 {
		t.Fatalf("receipted envelope failure events = %d", n)
	}
}

func TestExplicitAndImplicitDeadlinePrecedence(t *testing.T) {
	f := newRoomFixture(t)
	say := func(body string) string {
		return f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: body, To: []string{"cody"}, TTL: "1h", PrincipalRef: "agent:clod"}).Envelopes[0].ID
	}
	earlier, later, equal := say("earlier explicit"), say("later explicit"), say("equal explicit")
	for id, expires := range map[string]string{
		earlier: "+1 hours",  // explicit deadline 1h after creation: already past
		later:   "+48 hours", // explicit deadline in the future cannot extend 24h
		equal:   "+24 hours", // equal: the implicit horizon takes precedence
	} {
		if _, err := f.s.DB().Exec(`UPDATE envelopes SET
			created_at = strftime('%Y-%m-%dT%H:%M:%SZ','now','-25 hours'),
			expires_at = strftime('%Y-%m-%dT%H:%M:%SZ','now','-25 hours', ?) WHERE id = ?`, expires, id); err != nil {
			t.Fatal(err)
		}
	}
	var laterExpiresBefore string
	if err := f.s.DB().QueryRow(`SELECT expires_at FROM envelopes WHERE id = ?`, later).Scan(&laterExpiresBefore); err != nil {
		t.Fatal(err)
	}
	f.observe(t)

	if state, _, actor := f.envelopeRow(t, earlier); state != "expired" || actor != "wrkq" || f.envelopeEventCount(t, earlier, "envelope.failed") != 0 {
		t.Fatalf("earlier explicit = %s actor=%s", state, actor)
	}
	for _, id := range []string{later, equal} {
		if state, reason, _ := f.envelopeRow(t, id); state != "failed" || reason != "undeliverable" || f.envelopeEventCount(t, id, "envelope.expired") != 0 {
			t.Fatalf("%s = %s/%s, want failed/undeliverable", id, state, reason)
		}
	}
	var laterExpiresAfter string
	if err := f.s.DB().QueryRow(`SELECT expires_at FROM envelopes WHERE id = ?`, later).Scan(&laterExpiresAfter); err != nil {
		t.Fatal(err)
	}
	if laterExpiresAfter != laterExpiresBefore {
		t.Fatalf("expires_at rewritten: %s -> %s", laterExpiresBefore, laterExpiresAfter)
	}
}

func TestConcurrentObservationsWriteExactlyOneFailure(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	e := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "raced sweep", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0]
	f.ageEnvelope(t, e.ID, "-25 hours")

	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			switch i % 3 {
			case 0:
				_, err = f.api.MonitorEventsView(ctx, MonitorEventsViewParams{})
			case 1:
				_, err = f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: e.ID, PrincipalRef: "agent:cody"})
			default:
				_, err = f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: e.ID, PrincipalRef: "agent:hrc", RuntimeID: "rt-x", DriveAttemptID: "drive-race"})
				if de, ok := err.(*DomainError); ok && de.Code() == CodeWrongState {
					err = nil
				}
			}
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent observation: %v", err)
		}
	}
	if n := f.envelopeEventCount(t, e.ID, "envelope.failed"); n != 1 {
		t.Fatalf("concurrent observations wrote %d envelope.failed events, want 1", n)
	}
}
