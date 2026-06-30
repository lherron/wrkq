package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
)

func TestCreateHandoff(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	containerUUID := setupTestContainer(t, database, actorUUID)

	result := createTestHandoff(t, database, CreateHandoffArgs{
		ScopeRef:             "agent:larry:project:wrkq",
		ScopeKind:            "project",
		AgentID:              "larry",
		ProjectID:            "wrkq",
		AgentActorUUID:       &actorUUID,
		ProjectContainerUUID: &containerUUID,
		CreatedByAgentID:     "larry",
		CreatedByActorUUID:   &actorUUID,
		Title:                "Continue store work",
		Body:                 "Implement the handoff store layer.",
		Meta:                 strPtr(`{"source":"test"}`),
	})

	if result.IdempotentReplay {
		t.Fatal("expected fresh create, got idempotent replay")
	}
	handoff := result.Handoff
	if handoff.ID != "H-00001" {
		t.Fatalf("expected H-00001, got %s", handoff.ID)
	}
	if handoff.Status != HandoffStatusPending {
		t.Fatalf("expected pending status, got %s", handoff.Status)
	}
	if handoff.ETag != 1 {
		t.Fatalf("expected etag 1, got %d", handoff.ETag)
	}
	// Legacy actor UUIDs are no longer persisted under principal-only attribution.
	if handoff.AgentActorUUID != nil {
		t.Fatalf("expected agent actor uuid to be nil, got %v", *handoff.AgentActorUUID)
	}
	if handoff.ProjectContainerUUID == nil || *handoff.ProjectContainerUUID != containerUUID {
		t.Fatalf("expected project container uuid %s, got %v", containerUUID, handoff.ProjectContainerUUID)
	}

	assertEventCount(t, database, handoff.UUID, "handoff.created", 1)
}

func TestCreateHandoff_IdempotencyReplay(t *testing.T) {
	database := setupTestDB(t)
	key := "retry-key"
	args := testHandoffArgs("agent:larry:project:wrkq", "Replay", "Same payload")
	args.IdempotencyKey = &key

	first := createTestHandoff(t, database, args)
	second := createTestHandoff(t, database, args)

	if !second.IdempotentReplay {
		t.Fatal("expected idempotent replay")
	}
	if second.Handoff.UUID != first.Handoff.UUID {
		t.Fatalf("expected existing uuid %s, got %s", first.Handoff.UUID, second.Handoff.UUID)
	}
	assertEventCount(t, database, first.Handoff.UUID, "handoff.created", 1)
}

func TestCreateHandoff_IdempotencyPayloadMismatch(t *testing.T) {
	database := setupTestDB(t)
	key := "retry-key"
	args := testHandoffArgs("agent:larry:project:wrkq", "Replay", "Original body")
	args.IdempotencyKey = &key
	first := createTestHandoff(t, database, args)

	args.Body = "Different body"
	_, err := createHandoffInTx(t, database, args)
	if err == nil {
		t.Fatal("expected mismatch error")
	}
	var mismatch *HandoffIdempotencyPayloadMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected HandoffIdempotencyPayloadMismatchError, got %T: %v", err, err)
	}
	if mismatch.ExistingID != first.Handoff.ID {
		t.Fatalf("expected existing id %s, got %s", first.Handoff.ID, mismatch.ExistingID)
	}
	assertEventCount(t, database, first.Handoff.UUID, "handoff.created", 1)
}

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

func TestAcknowledgeHandoff(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	created := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Ack", "Body")).Handoff
	time.Sleep(1100 * time.Millisecond)
	note := "consumed"

	ack, err := AcknowledgeHandoff(context.Background(), database, created.ID, AcknowledgeHandoffArgs{
		Note:         &note,
		ActorAgentID: "larry",
		ActorUUID:    &actorUUID,
		IfMatch:      created.ETag,
	})
	if err != nil {
		t.Fatalf("AcknowledgeHandoff failed: %v", err)
	}
	if ack.Status != HandoffStatusAcknowledged {
		t.Fatalf("expected acknowledged, got %s", ack.Status)
	}
	if ack.ETag != created.ETag+1 {
		t.Fatalf("expected etag %d, got %d", created.ETag+1, ack.ETag)
	}
	if ack.AcknowledgedAt == nil {
		t.Fatal("expected acknowledged_at")
	}
	if ack.AcknowledgedByAgentID == nil || *ack.AcknowledgedByAgentID != "larry" {
		t.Fatalf("expected acknowledged_by_agent_id larry, got %v", ack.AcknowledgedByAgentID)
	}
	if ack.AcknowledgementNote == nil || *ack.AcknowledgementNote != note {
		t.Fatalf("expected note %q, got %v", note, ack.AcknowledgementNote)
	}
	if !ack.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("expected updated_at to advance: before=%s after=%s", created.UpdatedAt, ack.UpdatedAt)
	}
	assertEventCount(t, database, created.UUID, "handoff.acknowledged", 1)
}

func TestAcknowledgeHandoffRejectsAlreadyAcknowledged(t *testing.T) {
	database := setupTestDB(t)
	created := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Ack once", "Body")).Handoff
	if _, err := AcknowledgeHandoff(context.Background(), database, created.ID, AcknowledgeHandoffArgs{ActorAgentID: "larry"}); err != nil {
		t.Fatalf("first AcknowledgeHandoff failed: %v", err)
	}

	_, err := AcknowledgeHandoff(context.Background(), database, created.ID, AcknowledgeHandoffArgs{ActorAgentID: "larry"})
	if err == nil {
		t.Fatal("expected already acknowledged error")
	}
	if !strings.Contains(err.Error(), "already acknowledged") {
		t.Fatalf("expected already acknowledged error, got %v", err)
	}
}

func TestAcknowledgeHandoffDryRun(t *testing.T) {
	database := setupTestDB(t)
	created := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "Dry run", "Body")).Handoff
	note := "would consume"

	projected, err := AcknowledgeHandoff(context.Background(), database, created.ID, AcknowledgeHandoffArgs{
		Note:         &note,
		ActorAgentID: "larry",
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("dry run AcknowledgeHandoff failed: %v", err)
	}
	if projected.Status != HandoffStatusAcknowledged || projected.ETag != created.ETag+1 {
		t.Fatalf("unexpected projected state: %+v", projected)
	}

	stored, err := GetHandoff(context.Background(), database, created.ID)
	if err != nil {
		t.Fatalf("GetHandoff failed: %v", err)
	}
	if stored.Status != HandoffStatusPending || stored.ETag != created.ETag {
		t.Fatalf("dry run mutated stored handoff: %+v", stored)
	}
	assertEventCount(t, database, created.UUID, "handoff.acknowledged", 0)
}

func TestAcknowledgeHandoffETagMismatch(t *testing.T) {
	database := setupTestDB(t)
	created := createTestHandoff(t, database, testHandoffArgs("agent:larry:project:wrkq", "ETag", "Body")).Handoff

	_, err := AcknowledgeHandoff(context.Background(), database, created.ID, AcknowledgeHandoffArgs{ActorAgentID: "larry", IfMatch: 999})
	if err == nil {
		t.Fatal("expected etag mismatch")
	}
	var mismatch *domain.ETagMismatchError
	if !errors.As(err, &mismatch) {
		t.Fatalf("expected ETagMismatchError, got %T: %v", err, err)
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

func assertEventCount(t *testing.T, database *db.DB, resourceUUID, eventType string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM event_log
		WHERE resource_type = 'handoff' AND resource_uuid = ? AND event_type = ?
	`, resourceUUID, eventType).Scan(&got); err != nil {
		t.Fatalf("failed to count events: %v", err)
	}
	if got != want {
		t.Fatalf("expected %d %s events, got %d", want, eventType, got)
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
