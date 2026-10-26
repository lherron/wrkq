package store

import (
	"context"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

func TestGetHandoffByIDAndUUID(t *testing.T) {
	database := setupTestDB(t)
	created := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Lookup", "Body")).Handoff

	byID, err := GetHandoff(context.Background(), database, created.ID)
	if err != nil {
		t.Fatalf("GetHandoff by id failed: %v", err)
	}
	byUUID, err := GetHandoff(context.Background(), database, created.UUID)
	if err != nil {
		t.Fatalf("GetHandoff by uuid failed: %v", err)
	}
	if byID.UUID != created.UUID || byUUID.ID != created.ID {
		t.Fatalf("lookup mismatch: byID=%+v byUUID=%+v created=%+v", byID, byUUID, created)
	}
}

func TestListHandoffs_DefaultPendingOnlyAndAll(t *testing.T) {
	database := setupTestDB(t)
	pending := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Pending", "Body")).Handoff
	ack := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Ack", "Body")).Handoff
	if _, err := AcknowledgeHandoff(context.Background(), database, ack.ID, AcknowledgeHandoffArgs{ActorAgentID: "larry"}); err != nil {
		t.Fatalf("AcknowledgeHandoff failed: %v", err)
	}

	got, next, err := ListHandoffs(context.Background(), database, ListHandoffsOpts{ScopeRef: "agent:larry:project:wrkq"})
	if err != nil {
		t.Fatalf("ListHandoffs default failed: %v", err)
	}
	if next != "" {
		t.Fatalf("expected empty next cursor, got %q", next)
	}
	if len(got) != 1 || got[0].ID != pending.ID {
		t.Fatalf("expected only pending handoff %s, got %+v", pending.ID, got)
	}

	all, _, err := ListHandoffs(context.Background(), database, ListHandoffsOpts{ScopeRef: "agent:larry:project:wrkq", Status: "all"})
	if err != nil {
		t.Fatalf("ListHandoffs all failed: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 handoffs for status all, got %d", len(all))
	}
}

func TestListHandoffs_CursorPagination(t *testing.T) {
	database := setupTestDB(t)
	createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "First", "Body"))
	time.Sleep(1100 * time.Millisecond)
	second := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Second", "Body")).Handoff
	time.Sleep(1100 * time.Millisecond)
	third := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Third", "Body")).Handoff

	page1, cursor, err := ListHandoffs(context.Background(), database, ListHandoffsOpts{ScopeRef: "agent:larry:project:wrkq", Limit: 2})
	if err != nil {
		t.Fatalf("ListHandoffs page1 failed: %v", err)
	}
	if cursor == "" {
		t.Fatal("expected next cursor")
	}
	if len(page1) != 2 || page1[0].ID != third.ID || page1[1].ID != second.ID {
		t.Fatalf("unexpected first page: %+v", page1)
	}

	page2, next, err := ListHandoffs(context.Background(), database, ListHandoffsOpts{ScopeRef: "agent:larry:project:wrkq", Limit: 2, Cursor: cursor})
	if err != nil {
		t.Fatalf("ListHandoffs page2 failed: %v", err)
	}
	if next != "" {
		t.Fatalf("expected empty next cursor, got %q", next)
	}
	if len(page2) != 1 || page2[0].Title != "First" {
		t.Fatalf("unexpected second page: %+v", page2)
	}
}

func TestSearchHandoffs(t *testing.T) {
	database := setupTestDB(t)
	titleHit := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Need Quartz notes", "General body")).Handoff
	bodyHit := createTestHandoff(t, database, testHandoffArgs("agent:curly:project:wrkq", "Other", "Body mentions quartz here")).Handoff
	scopeHit := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:quartz", "Other", "General body")).Handoff
	nonHit := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "No match", "General body")).Handoff
	if _, err := AcknowledgeHandoff(context.Background(), database, bodyHit.ID, AcknowledgeHandoffArgs{ActorAgentID: "curly"}); err != nil {
		t.Fatalf("AcknowledgeHandoff failed: %v", err)
	}

	results, _, err := SearchHandoffs(context.Background(), database, SearchHandoffsOpts{Query: "QUARTZ", Status: "all"})
	if err != nil {
		t.Fatalf("SearchHandoffs failed: %v", err)
	}
	assertIDs(t, results, titleHit.ID, bodyHit.ID, scopeHit.ID)
	assertNotID(t, results, nonHit.ID)

	pendingWrkq, _, err := SearchHandoffs(context.Background(), database, SearchHandoffsOpts{Query: "quartz", ScopeRef: "agent:larry:project:wrkq", Status: HandoffStatusPending})
	if err != nil {
		t.Fatalf("filtered SearchHandoffs failed: %v", err)
	}
	if len(pendingWrkq) != 1 || pendingWrkq[0].ID != titleHit.ID {
		t.Fatalf("expected only title hit after filters, got %+v", pendingWrkq)
	}
}

func TestSearchHandoffs_CursorPagination(t *testing.T) {
	database := setupTestDB(t)
	createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "alpha first", "Body"))
	time.Sleep(1100 * time.Millisecond)
	second := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "alpha second", "Body")).Handoff
	time.Sleep(1100 * time.Millisecond)
	third := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "alpha third", "Body")).Handoff

	page1, cursor, err := SearchHandoffs(context.Background(), database, SearchHandoffsOpts{Query: "alpha", Limit: 2})
	if err != nil {
		t.Fatalf("SearchHandoffs page1 failed: %v", err)
	}
	if cursor == "" {
		t.Fatal("expected next cursor")
	}
	if len(page1) != 2 || page1[0].ID != third.ID || page1[1].ID != second.ID {
		t.Fatalf("unexpected first page: %+v", page1)
	}
	page2, next, err := SearchHandoffs(context.Background(), database, SearchHandoffsOpts{Query: "alpha", Limit: 2, Cursor: cursor})
	if err != nil {
		t.Fatalf("SearchHandoffs page2 failed: %v", err)
	}
	if next != "" {
		t.Fatalf("expected empty next cursor, got %q", next)
	}
	if len(page2) != 1 || page2[0].Title != "alpha first" {
		t.Fatalf("unexpected second page: %+v", page2)
	}
}

func createTestHandoff(t *testing.T, database *db.DB, args CreateHandoffArgs) CreateHandoffResult {
	t.Helper()
	result, err := createHandoffInTx(t, database, args)
	if err != nil {
		t.Fatalf("CreateHandoff failed: %v", err)
	}
	return result
}

func createHandoffInTx(t *testing.T, database *db.DB, args CreateHandoffArgs) (CreateHandoffResult, error) {
	t.Helper()
	tx, err := database.Begin()
	if err != nil {
		t.Fatalf("failed to begin tx: %v", err)
	}
	result, err := CreateHandoff(context.Background(), tx, args)
	if err != nil {
		_ = tx.Rollback()
		return CreateHandoffResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return CreateHandoffResult{}, err
	}
	return result, nil
}

func testHandoffArgs(scopeRef, title, body string) CreateHandoffArgs {
	return CreateHandoffArgs{
		ScopeRef:         scopeRef,
		ScopeKind:        "project",
		AgentID:          "larry",
		ProjectID:        "wrkq",
		CreatedByAgentID: "larry",
		Title:            title,
		Body:             body,
	}
}

func assertIDs(t *testing.T, handoffs []Handoff, wantIDs ...string) {
	t.Helper()
	got := map[string]bool{}
	for _, handoff := range handoffs {
		got[handoff.ID] = true
	}
	for _, wantID := range wantIDs {
		if !got[wantID] {
			t.Fatalf("expected result %s in %+v", wantID, handoffs)
		}
	}
}

func assertNotID(t *testing.T, handoffs []Handoff, unwantedID string) {
	t.Helper()
	for _, handoff := range handoffs {
		if handoff.ID == unwantedID {
			t.Fatalf("did not expect result %s in %+v", unwantedID, handoffs)
		}
	}
}

// Repeated sequence synchronization must neither create duplicate identities nor
// lower a high-water mark when an older handoff is restored.
func TestSyncHandoffSequenceKeepsOneHighWaterRow(t *testing.T) {
	database := setupTestDB(t)
	for _, highWater := range []int{17, 9, 21, 21} {
		tx, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		if err := syncHandoffSequence(context.Background(), tx, highWater); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if err := tx.Commit(); err != nil {
			t.Fatal(err)
		}
		var count, stored int
		if err := database.QueryRow(`SELECT COUNT(*), MAX(seq) FROM sqlite_sequence WHERE name = 'handoff_seq'`).Scan(&count, &stored); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("sync(%d) created %d sequence rows, want one", highWater, count)
		}
		want := 17
		if highWater == 21 {
			want = 21
		}
		if stored != want {
			t.Fatalf("sync(%d) stored %d, want high-water %d", highWater, stored, want)
		}
	}
}
