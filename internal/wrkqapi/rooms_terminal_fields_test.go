//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

func terminalFieldsJSON(t *testing.T, value any) map[string]any {
	t.Helper()
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestEnvelopeShowFailureDetailMatchesTerminalEvent(t *testing.T) {
	for _, detail := range []string{"its runtime ended first", "", strings.Repeat("界", store.MaxEnvelopeFailureDetailBytes)} {
		t.Run(string(rune(len(detail)+65)), func(t *testing.T) {
			f := newRoomFixture(t)
			ctx := context.Background()
			ask := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "ask", To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0]
			before, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: ask.ID})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := terminalFieldsJSON(t, before)["failureDetail"]; ok {
				t.Fatal("pending envelope has failureDetail")
			}
			if _, err := f.api.EnvelopeFail(ctx, EnvelopeFailParams{Envelope: ask.ID, Reason: "unconfirmed", Detail: detail, PrincipalRef: "agent:hrc"}); err != nil {
				t.Fatal(err)
			}
			shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: ask.ID})
			if err != nil {
				t.Fatal(err)
			}
			var payload string
			if err := f.s.DB().QueryRow(`SELECT payload FROM event_log WHERE resource_uuid = ? AND event_type = 'envelope.failed'`, ask.UUID).Scan(&payload); err != nil {
				t.Fatal(err)
			}
			var event map[string]any
			if err := json.Unmarshal([]byte(payload), &event); err != nil {
				t.Fatal(err)
			}
			got := terminalFieldsJSON(t, shown)
			if got["failureDetail"] != event["detail"] {
				t.Fatalf("show detail = %v; event = %v", got["failureDetail"], event["detail"])
			}
			if detail != "" && got["failureDetail"] == nil {
				t.Fatal("missing failureDetail")
			}
			if _, ok := got["dischargedBy"]; ok {
				t.Fatal("failure has dischargedBy")
			}
		})
	}
}

func TestEnvelopeShowDischargedByReply(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, note := range []bool{false, true} {
			name := "wide"
			if explicit {
				name = "explicit"
			}
			if note {
				name += "-note"
			}
			t.Run(name, func(t *testing.T) {
				f := newRoomFixture(t)
				ctx := context.Background()
				cody := "cody@proj:" + f.loneTaskID
				senders := []string{"clod@proj:primary", "mable@proj:primary", "agent:lance"}
				asks := []WrkqEnvelope{}
				for i, sender := range senders {
					principal := []string{"agent:clod", "agent:mable", "agent:lance"}[i]
					scoped := sender
					if sender == "agent:lance" {
						scoped = ""
					}
					ask := f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: "ask", To: []string{cody}, PrincipalRef: principal, ScopeRef: scoped}).Envelopes[0]
					f.present(t, ask.ID)
					asks = append(asks, ask)
				}
				p := RoomSayParams{Ref: f.loneTaskID, Body: "answer", To: senders, PrincipalRef: "agent:cody", ScopeRef: cody, Note: note}
				if explicit {
					for _, ask := range asks {
						p.DischargeEnvelopeIDs = append(p.DischargeEnvelopeIDs, ask.ID)
					}
				}
				reply := f.say(t, p)
				for i, ask := range asks {
					shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: ask.ID})
					if err != nil {
						t.Fatal(err)
					}
					got := terminalFieldsJSON(t, shown)
					want := reply.Envelopes[i].ID
					if got["dischargedBy"] != want {
						t.Fatalf("%s dischargedBy = %v, want %s", ask.ID, got["dischargedBy"], want)
					}
					var payload string
					if err := f.s.DB().QueryRow(`SELECT payload FROM event_log WHERE resource_uuid = ? AND event_type = 'envelope.acked'`, ask.UUID).Scan(&payload); err != nil {
						t.Fatal(err)
					}
					var event map[string]any
					if err := json.Unmarshal([]byte(payload), &event); err != nil {
						t.Fatal(err)
					}
					if event["discharged_by"] != want {
						t.Fatalf("event = %s, want discharged_by %s", payload, want)
					}
				}
				// The note's own note_recorded acknowledgement never names a discharger.
				for _, e := range reply.Envelopes {
					if _, ok := terminalFieldsJSON(t, e)["dischargedBy"]; ok {
						t.Fatal("reply itself has dischargedBy")
					}
				}
			})
		}
	}
}

func TestEnvelopeShowNonReplyOmitsDischargedBy(t *testing.T) {
	for _, mode := range []string{"fyi", "operator", "withdrawn", "failed", "note", "consumed_by_wait", "legacy_reply", "expired"} {
		t.Run(mode, func(t *testing.T) {
			f := newRoomFixture(t)
			ctx := context.Background()
			cody := "cody@proj:" + f.loneTaskID
			p := RoomSayParams{Ref: f.loneTaskID, Body: "ask", To: []string{cody}, PrincipalRef: "agent:clod", FYI: mode == "fyi", Note: mode == "note"}
			ask := f.say(t, p).Envelopes[0]
			var err error
			switch mode {
			case "fyi":
				f.present(t, ask.ID)
			case "operator", "legacy_reply":
				_, err = f.api.EnvelopeAck(ctx, EnvelopeAckParams{Envelopes: []string{ask.ID}, PrincipalRef: "agent:lance"})
				if mode == "legacy_reply" && err == nil {
					_, err = f.s.DB().Exec(`UPDATE event_log SET payload = '{"reason":"reply"}' WHERE resource_uuid = ? AND event_type = 'envelope.acked'`, ask.UUID)
				}
			case "consumed_by_wait":
				_, err = f.api.EnvelopeAck(ctx, EnvelopeAckParams{Envelopes: []string{ask.ID}, PrincipalRef: "agent:cody", ScopeRef: cody, Reason: envelopeAckReasonConsumedByWait})
			case "withdrawn":
				_, err = f.api.EnvelopeWithdraw(ctx, EnvelopeWithdrawParams{Envelope: ask.ID, PrincipalRef: "agent:clod"})
			case "failed":
				_, err = f.api.EnvelopeFail(ctx, EnvelopeFailParams{Envelope: ask.ID, Reason: "undeliverable", PrincipalRef: "agent:hrc"})
			case "expired":
				_, err = f.s.DB().Exec(`UPDATE envelopes SET expires_at = '2000-01-01T00:00:00Z' WHERE uuid = ?`, ask.UUID)
			}
			if err != nil {
				t.Fatal(err)
			}
			shown, err := f.api.EnvelopeShow(ctx, EnvelopeShowParams{Envelope: ask.ID})
			if err != nil {
				t.Fatal(err)
			}
			got := terminalFieldsJSON(t, shown)
			if _, ok := got["dischargedBy"]; ok {
				t.Fatal("non-reply has dischargedBy")
			}
			if _, ok := got["failureDetail"]; ok {
				t.Fatal("no-detail envelope has failureDetail")
			}
		})
	}
}
