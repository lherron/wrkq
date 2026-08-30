//go:build wrkq_local

package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestMemberEnvelopePageUsesBoundedIndexedExactMemberQuery(t *testing.T) {
	database, roomStore := newMemberPageStore(t)
	seedPageRoom(t, database, "room-member", "cody@proj:primary", "agent:cody")
	seedPageRoom(t, database, "room-principal-mismatch", "cody@proj:primary", "agent:mable")
	seedPageRoom(t, database, "room-unrelated", "mable@proj:primary", "agent:mable")

	seedPageEnvelope(t, database, "room-member", "target-old")
	for index := 0; index < 75; index++ {
		seedPageEnvelope(t, database, "room-unrelated", fmt.Sprintf("noise-%03d", index))
		seedPageEnvelope(t, database, "room-principal-mismatch", fmt.Sprintf("mismatch-%03d", index))
	}
	seedPageEnvelope(t, database, "room-member", "target-new")

	page, err := roomStore.MemberEnvelopePage(context.Background(), MemberEnvelopePageParams{
		MemberRef: "cody@proj:primary", MemberPrincipalRef: "agent:cody",
		BeforeMessageSeq: int64Pointer(1 << 62), Limit: 1,
	})
	if err != nil {
		t.Fatalf("member page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].Envelope.Body != "target-new" {
		t.Fatalf("page items = %+v, want newest exact-member envelope", page.Items)
	}
	if page.MaterializedRows != 2 {
		t.Fatalf("materialized rows = %d, want limit+1 = 2", page.MaterializedRows)
	}
	if !page.HasMoreBefore || page.HasMoreAfter {
		t.Fatalf("availability = before:%v after:%v, want true/false", page.HasMoreBefore, page.HasMoreAfter)
	}

	rows, err := database.Query("EXPLAIN QUERY PLAN "+memberEnvelopePageBeforeSQL,
		int64(1<<62), "cody@proj:primary", "agent:cody", 2)
	if err != nil {
		t.Fatalf("explain member page: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatalf("scan query plan: %v", err)
		}
		details = append(details, detail)
	}
	plan := strings.Join(details, "\n")
	for _, index := range []string{"envelopes_message_seq_idx", "room_members_observation_idx"} {
		if !strings.Contains(plan, index) {
			t.Fatalf("query plan does not use %s:\n%s", index, plan)
		}
	}
	if strings.Contains(plan, "USE TEMP B-TREE") {
		t.Fatalf("query plan sorts outside the sequence index:\n%s", plan)
	}
	t.Logf("materialized=%d (limit+1); query plan:\n%s", page.MaterializedRows, plan)
}

func TestMemberEnvelopePageForwardBurstDrainsEveryBoundedPage(t *testing.T) {
	database, roomStore := newMemberPageStore(t)
	seedPageRoom(t, database, "room-member", "cody@proj:primary", "agent:cody")

	for index := 1; index <= 503; index++ {
		seedPageEnvelope(t, database, "room-member", fmt.Sprintf("burst-%03d", index))
	}

	cursor := int64(0)
	seen := make([]int64, 0, 503)
	incarnation := ""
	for {
		page, err := roomStore.MemberEnvelopePage(context.Background(), MemberEnvelopePageParams{
			MemberRef: "cody@proj:primary", MemberPrincipalRef: "agent:cody",
			AfterMessageSeq: &cursor, Limit: 500, ExpectedLedgerIncarnation: incarnation,
		})
		if err != nil {
			t.Fatalf("forward page after %d: %v", cursor, err)
		}
		if incarnation == "" {
			incarnation = page.LedgerIncarnation
		}
		if page.LedgerIncarnation != incarnation {
			t.Fatalf("incarnation changed while draining: %q -> %q", incarnation, page.LedgerIncarnation)
		}
		for _, item := range page.Items {
			if item.MessageSeq <= cursor {
				t.Fatalf("non-exclusive or duplicate forward item %d after %d", item.MessageSeq, cursor)
			}
			seen = append(seen, item.MessageSeq)
			cursor = item.MessageSeq
		}
		if page.HasMoreAfter && page.MaterializedRows != 501 {
			t.Fatalf("non-final materialization = %d, want 501", page.MaterializedRows)
		}
		if !page.HasMoreAfter {
			break
		}
	}
	if len(seen) != 503 {
		t.Fatalf("forward drain saw %d messages, want 503", len(seen))
	}
	for index, seq := range seen {
		if want := int64(index + 1); seq != want {
			t.Fatalf("forward drain sequence[%d] = %d, want %d", index, seq, want)
		}
	}
	t.Logf("drained %d messages in bounded forward pages; final cursor=%d", len(seen), cursor)
}

func newMemberPageStore(t *testing.T) (*db.DB, *RoomStore) {
	t.Helper()
	database, err := db.Open(filepath.Join(t.TempDir(), "member-page.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database, New(database).Rooms
}

func seedPageRoom(t *testing.T, database *db.DB, roomUUID, memberRef, principalRef string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO rooms (
		uuid, kind, opened_by_principal_ref,
		created_by_principal_ref, updated_by_principal_ref
	) VALUES (?, 'adhoc', 'agent:clod', 'agent:clod', 'agent:clod')`, roomUUID); err != nil {
		t.Fatalf("seed room %s: %v", roomUUID, err)
	}
	if _, err := database.Exec(`INSERT INTO room_members (
		uuid, room_uuid, member_ref, member_principal_ref, scoped, source
	) VALUES (?, ?, ?, ?, 1, 'joined')`, "member-"+roomUUID, roomUUID, memberRef, principalRef); err != nil {
		t.Fatalf("seed room member %s: %v", roomUUID, err)
	}
}

func seedPageEnvelope(t *testing.T, database *db.DB, roomUUID, body string) {
	t.Helper()
	if _, err := database.Exec(`INSERT INTO envelopes (
		room_uuid, from_principal_ref, obligation, body,
		created_by_principal_ref, updated_by_principal_ref
	) VALUES (?, 'agent:clod', 'none', ?, 'agent:clod', 'agent:clod')`, roomUUID, body); err != nil {
		t.Fatalf("seed envelope %s: %v", body, err)
	}
}

func int64Pointer(value int64) *int64 { return &value }
