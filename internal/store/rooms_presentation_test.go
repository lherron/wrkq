package store

import (
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
)

func TestPresentationRecordPersistsInputIDInAttendance(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	projectUUID := setupTestContainer(t, database, actorUUID)
	task, err := New(database).Tasks.Create(actorUUID, CreateParams{
		Slug: "presentation-input", Title: "Presentation Input", ProjectUUID: projectUUID,
		State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	s := New(database)
	attr := attribution.Attribution{PrincipalRef: "agent:hrc"}
	room, err := s.Rooms.CreateWithAttribution(attr, RoomCreateParams{
		Kind: domain.RoomKindTask, TaskUUID: &task.UUID,
	})
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	envelopes, err := s.Rooms.CreateEnvelopesWithAttribution(attr, EnvelopeCreateParams{
		RoomUUID: room.UUID, FromPrincipalRef: "agent:clod",
		Addressees: []EnvelopeAddressee{{ScopeRef: "cody@wrkq:T-00001", PrincipalRef: "agent:cody"}},
		Obligation: domain.EnvelopeObligationReplyRequired, Body: "accept me",
	})
	if err != nil {
		t.Fatalf("create envelope: %v", err)
	}
	inputID := "broker-input-1"
	if _, recorded, err := s.Rooms.RecordPresentationWithAttribution(attr, envelopes[0].UUID, PresentationRecord{
		MemberRef: "cody@wrkq:T-00001", InputID: &inputID,
	}); err != nil {
		t.Fatalf("record presentation: %v", err)
	} else if !recorded {
		t.Fatal("presentation was not recorded")
	}
	attendance, err := s.Rooms.LatestAttendance(room.UUID)
	if err != nil {
		t.Fatalf("latest attendance: %v", err)
	}
	got := attendance["cody@wrkq:T-00001"]
	if got.InputID == nil || *got.InputID != inputID {
		t.Fatalf("attendance inputId = %v, want %q", got.InputID, inputID)
	}
}
