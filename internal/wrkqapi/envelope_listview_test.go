//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvelopeListViewFiltersValidationAndExactShape(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	live := f.say(t, RoomSayParams{Ref: f.loneTaskID, To: []string{"cody"}, Body: "live", PrincipalRef: "agent:clod"}).Envelopes[0]
	terminal := f.say(t, RoomSayParams{Ref: f.memberTaskID, To: []string{"cody"}, Body: "terminal", Note: true, PrincipalRef: "agent:clod"}).Envelopes[0]
	page, err := f.api.EnvelopeListView(ctx, EnvelopeListViewParams{})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != live.ID {
		t.Fatalf("default page: %+v %v", page, err)
	}
	page, err = f.api.EnvelopeListView(ctx, EnvelopeListViewParams{States: []string{"acked"}})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != terminal.ID {
		t.Fatalf("explicit page: %+v %v", page, err)
	}
	page, err = f.api.EnvelopeListView(ctx, EnvelopeListViewParams{IDs: []string{terminal.UUID, "EN-99999", live.ID}})
	if err != nil || len(page.Items) != 2 || page.Items[0].ID != live.ID || page.Items[1].ID != terminal.ID {
		t.Fatalf("ids page: %+v %v", page, err)
	}
	_, err = f.api.EnvelopePresent(ctx, EnvelopePresentParams{Envelope: live.ID, PrincipalRef: "agent:hrc", Node: "mini", RuntimeID: "list-view", Generation: "1", RunID: "run"})
	if err != nil {
		t.Fatal(err)
	}
	page, err = f.api.EnvelopeListView(ctx, EnvelopeListViewParams{IDs: []string{live.ID, terminal.ID}})
	if err != nil || len(page.Items[0].PresentedTo) != 1 {
		t.Fatalf("presentation absent: %+v %v", page, err)
	}
	for _, item := range page.Items {
		shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: item.ID})
		if err != nil {
			t.Fatal(err)
		}
		got, _ := json.Marshal(item)
		want, _ := json.Marshal(shown)
		if string(got) != string(want) {
			t.Fatalf("shape differs:\n%s\n%s", got, want)
		}
	}
	page, err = f.api.EnvelopeListView(ctx, EnvelopeListViewParams{IDs: []string{terminal.ID}, States: []string{"pending"}})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("ids and states: %+v %v", page, err)
	}
	page, err = f.api.EnvelopeListView(ctx, EnvelopeListViewParams{IDs: []string{}})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("empty ids: %+v %v", page, err)
	}
	for _, p := range []EnvelopeListViewParams{{States: []string{"bad"}}, {Limit: envelopeViewLimit(0)}, {Limit: envelopeViewLimit(501)}, {Cursor: "bad"}, {IDs: make([]string, 501)}} {
		_, err := f.api.EnvelopeListView(ctx, p)
		_ = assertDomainCode(t, "WRKQ_VALIDATION", err)
	}
}

func TestEnvelopeListViewPagingAndReadOnly(t *testing.T) {
	f := newRoomFixture(t)
	ctx := context.Background()
	old := f.say(t, RoomSayParams{Ref: f.loneTaskID, To: []string{"cody"}, Body: "old", PrincipalRef: "agent:clod"}).Envelopes[0]
	f.ageEnvelope(t, old.ID, "-25 hours")
	// Seed a full three-page ledger, beyond the first envelope sequence.
	_, err := f.s.DB().Exec(`WITH RECURSIVE n(x) AS (VALUES(1) UNION ALL SELECT x+1 FROM n WHERE x<1200)
 INSERT INTO envelopes(id,room_uuid,from_principal_ref,obligation,body,state,created_by_principal_ref,updated_by_principal_ref)
 SELECT printf('EN-%05d',90000+x),?,'agent:clod','none','page row','pending','agent:clod','agent:clod' FROM n`, old.RoomUUID)
	if err != nil {
		t.Fatal(err)
	}
	var before, after int64
	if err = f.s.DB().QueryRow("SELECT MAX(id) FROM event_log").Scan(&before); err != nil {
		t.Fatal(err)
	}
	p := EnvelopeListViewParams{Limit: envelopeViewLimit(500)}
	seen := map[string]bool{}
	var last int64
	for i, want := range []int{500, 500, 201} {
		page, err := f.api.EnvelopeListView(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != want {
			t.Fatalf("page %d length %d want %d", i, len(page.Items), want)
		}
		for _, e := range page.Items {
			if seen[e.ID] || e.MessageSeq <= last {
				t.Fatalf("duplicate or unordered %s seq %d after %d", e.ID, e.MessageSeq, last)
			}
			seen[e.ID] = true
			last = e.MessageSeq
		}
		p.Cursor = page.NextCursor
		if (i < 2) != (p.Cursor != "") {
			t.Fatalf("page %d cursor %q", i, p.Cursor)
		}
		raw, _ := json.Marshal(page)
		if i == 2 && strings.Contains(string(raw), "nextCursor") {
			t.Fatal("last page includes cursor")
		}
	}
	if err = f.s.DB().QueryRow("SELECT MAX(id) FROM event_log").Scan(&after); err != nil {
		t.Fatal(err)
	}
	if before != after {
		t.Fatalf("read wrote events %d -> %d", before, after)
	}
	if state, _, _ := f.envelopeRow(t, old.ID); state != "pending" {
		t.Fatalf("list swept %s", state)
	}
	shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: old.ID, PrincipalRef: "agent:cody"})
	if err != nil || shown.State != "failed" {
		t.Fatalf("show did not sweep: %+v %v", shown, err)
	}
}

func envelopeViewLimit(n int) *int { return &n }
