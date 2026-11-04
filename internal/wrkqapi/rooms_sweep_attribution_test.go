//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"slices"
	"sort"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
)

// Inbox/pending reads write expiry and retry maintenance as the observer,
// including the supplied caller scope, never a pending-view recipient filter.
func TestEnvelopeReadSweepAttribution(t *testing.T) {
	for _, method := range []string{"inbox", "pending"} {
		for _, scopeInput := range []string{"", "cody@proj:primary", "agent:cody:project:proj:task:primary", "agent:cody:project:proj:task:primary/lane:main"} {
			t.Run(method+"/"+scopeInput, func(t *testing.T) {
				f := newRoomFixture(t)
				ctx := attribution.WithCallerPrincipal(context.Background(), "agent:cody")
				seat := "cody@proj:" + f.loneTaskID
				makeEnvelope := func(body string) WrkqEnvelope {
					return f.say(t, RoomSayParams{Ref: f.loneTaskID, Body: body, To: []string{"cody"}, PrincipalRef: "agent:clod"}).Envelopes[0]
				}
				expiring, undelivered, retry := makeEnvelope("ttl"), makeEnvelope("undelivered"), makeEnvelope("retry")
				deferred, err := f.api.EnvelopeDefer(ctx, EnvelopeDeferParams{
					Envelope: retry.ID, Reason: "later", RetryAfter: "1h", ScopeRef: seat,
				})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := f.s.DB().Exec("UPDATE envelopes SET expires_at = '2000-01-01T00:00:00Z' WHERE id = ?", expiring.ID); err != nil {
					t.Fatal(err)
				}
				f.ageEnvelope(t, undelivered.ID, "-25 hours")
				if _, err := f.s.DB().Exec("UPDATE envelopes SET retry_at = '2000-01-01T00:00:00Z' WHERE id = ?", retry.ID); err != nil {
					t.Fatal(err)
				}
				var highWater int64
				if err := f.s.DB().QueryRow("SELECT MAX(id) FROM event_log").Scan(&highWater); err != nil {
					t.Fatal(err)
				}
				if method == "inbox" {
					_, err = f.api.EnvelopeInboxView(ctx, EnvelopeInboxViewParams{ScopeRef: scopeInput})
				} else {
					_, err = f.api.EnvelopePendingView(ctx, EnvelopePendingViewParams{ScopeRef: scopeInput, Scopes: []string{seat}})
				}
				if err != nil {
					t.Fatal(err)
				}
				wantScope := ""
				if scopeInput != "" {
					wantScope = "cody@proj:primary"
				}
				check := func(label, principal string, scope sql.NullString) {
					t.Helper()
					if principal != "agent:cody" || scope.String != wantScope || scope.Valid != (wantScope != "") {
						t.Errorf("%s attribution = %q/%+v, want agent:cody/%q", label, principal, scope, wantScope)
					}
				}
				for envelope, wantState := range map[string]string{expiring.ID: "expired", undelivered.ID: "failed", retry.ID: "pending"} {
					var state, principal string
					var scope sql.NullString
					if err := f.s.DB().QueryRow("SELECT state, updated_by_principal_ref, updated_by_scope_ref FROM envelopes WHERE id = ?", envelope).Scan(&state, &principal, &scope); err != nil {
						t.Fatal(err)
					}
					if state != wantState {
						t.Errorf("%s state = %s, want %s", envelope, state, wantState)
					}
					check(envelope, principal, scope)
				}
				var state, principal string
				var scope sql.NullString
				if err := f.s.DB().QueryRow("SELECT state, updated_by_principal_ref, updated_by_scope_ref FROM promises WHERE id = ?", *deferred.RetryPromiseID).Scan(&state, &principal, &scope); err != nil {
					t.Fatal(err)
				}
				if state != "resolved" {
					t.Errorf("retry promise = %s, want resolved", state)
				}
				check("retry promise", principal, scope)
				rows, err := f.s.DB().Query("SELECT event_type, principal_ref, scope_ref FROM event_log WHERE id > ? ORDER BY id", highWater)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = rows.Close() }()
				var seen []string
				for rows.Next() {
					var eventType string
					if err := rows.Scan(&eventType, &principal, &scope); err != nil {
						t.Fatal(err)
					}
					check(eventType, principal, scope)
					seen = append(seen, eventType)
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				// The re-pend itself is a derived transition and emits no
				// envelope event; the retry promise it closes logs
				// promise.resolved so event-tail consumers see it (T-12463).
				sort.Strings(seen)
				if want := []string{"envelope.expired", "envelope.failed", "promise.resolved"}; !slices.Equal(seen, want) {
					t.Errorf("sweep events = %v, want %v", seen, want)
				}
			})
		}
	}
}
