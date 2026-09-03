package store

import (
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
)

func TestAckSenderObligationsAcceptsPendingAndEmitsReplyEvent(t *testing.T) {
	database := setupTestDB(t)
	actorUUID := setupTestActor(t, database)
	projectUUID := setupTestContainer(t, database, actorUUID)
	task, err := New(database).Tasks.Create(actorUUID, CreateParams{
		Slug: "pending-reply-ack", Title: "Pending Reply Ack", ProjectUUID: projectUUID,
		State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	s := New(database)
	clodScope := "clod@wrkq:" + task.ID
	codyScope := "cody@wrkq:" + task.ID
	room, err := s.Rooms.CreateWithAttribution(
		attribution.Attribution{PrincipalRef: "agent:clod", ScopeRef: clodScope},
		RoomCreateParams{Kind: domain.RoomKindTask, TaskUUID: &task.UUID},
	)
	if err != nil {
		t.Fatalf("create room: %v", err)
	}
	envelopes, err := s.Rooms.CreateEnvelopesWithAttribution(
		attribution.Attribution{PrincipalRef: "agent:clod", ScopeRef: clodScope},
		EnvelopeCreateParams{
			RoomUUID: room.UUID, FromPrincipalRef: "agent:clod", FromScopeRef: &clodScope,
			SenderMemberRef: clodScope, SenderScoped: true,
			Addressees: []EnvelopeAddressee{{ScopeRef: codyScope, PrincipalRef: "agent:cody"}},
			Obligation: domain.EnvelopeObligationReplyRequired, Body: "read while held",
		},
	)
	if err != nil {
		t.Fatalf("create pending envelope: %v", err)
	}
	if envelopes[0].State != domain.EnvelopeStatePending {
		t.Fatalf("created state = %s, want pending", envelopes[0].State)
	}

	acked, err := s.Rooms.AckSenderObligationsWithAttribution(
		attribution.Attribution{PrincipalRef: "agent:cody", ScopeRef: codyScope},
		room.UUID, codyScope, "agent:cody", clodScope, "agent:clod",
	)
	if err != nil {
		t.Fatalf("ack pending sender obligation: %v", err)
	}
	if len(acked) != 1 || acked[0].ID != envelopes[0].ID || acked[0].State != domain.EnvelopeStateAcked {
		t.Fatalf("acked = %+v, want pending envelope %s terminal", acked, envelopes[0].ID)
	}
	var payload string
	if err := database.QueryRow(`SELECT payload FROM event_log
		WHERE resource_uuid = ? AND event_type = 'envelope.acked' ORDER BY id DESC LIMIT 1`,
		envelopes[0].UUID).Scan(&payload); err != nil {
		t.Fatalf("read ack event: %v", err)
	}
	if !strings.Contains(payload, `"reason":"reply"`) || !strings.Contains(payload, `"previous_state":"pending"`) {
		t.Fatalf("ack event payload = %s", payload)
	}
}

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
