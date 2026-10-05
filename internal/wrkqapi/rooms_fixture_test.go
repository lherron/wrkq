//go:build wrkq_local

package wrkqapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/store"
)

// The room test suite (bundle 1 of T-07612 §15) covers the ledger and its
// routing table. Every case is a behavioral claim about the collaboration
// ledger ALONE — no HRC daemon exists in this process, which is itself the point
// (§2: wrkc and the ledger must work with every HRC daemon down). This file owns
// the shared fixture and the helpers every rooms_*_test.go file builds on.

// roomFixture seeds a project, a campaign container, and tasks in each so the
// §4 routing table has real work to route against.
type roomFixture struct {
	api  *API
	s    *store.Store
	proj string // project container uuid, slug "proj"

	loneTaskID   string // a task NOT in a campaign
	loneTaskUUID string

	campaignUUID string // a campaign-adorned container under the project
	campaignPath string

	memberTaskID   string // a task ENROLLED in the campaign (campaign_uuid)
	memberTaskUUID string

	residentTaskID   string // a task RESIDENT in the campaign (project_uuid), no campaign_uuid
	residentTaskUUID string

	plainContainerPath string // a directory: has no room and must be refused
}

func newRoomFixture(t *testing.T) *roomFixture {
	t.Helper()
	api, s := newMonitorAPI(t)
	f := &roomFixture{api: api, s: s, proj: seedMonitorProject(t, s)}

	lone, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "lone", Title: "Lone", ProjectUUID: f.proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create lone task: %v", err)
	}
	f.loneTaskID, f.loneTaskUUID = lone.ID, lone.UUID

	campaign, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "wave", Kind: "feature", ParentUUID: &f.proj,
	})
	if err != nil {
		t.Fatalf("create campaign container: %v", err)
	}
	f.campaignUUID, f.campaignPath = campaign.UUID, "proj/wave"
	if _, err := s.DB().Exec("UPDATE containers SET campaign_state = 'active' WHERE uuid = ?", f.campaignUUID); err != nil {
		t.Fatalf("adorn campaign: %v", err)
	}

	member, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "member", Title: "Member", ProjectUUID: f.proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create member task: %v", err)
	}
	f.memberTaskID, f.memberTaskUUID = member.ID, member.UUID
	if _, err := s.DB().Exec("UPDATE tasks SET campaign_uuid = ? WHERE uuid = ?", f.campaignUUID, f.memberTaskUUID); err != nil {
		t.Fatalf("enroll member task: %v", err)
	}

	// A RESIDENT campaign member: it lives inside the campaign container and has
	// no campaign_uuid at all. This is the COMMON membership form and the one a
	// campaign_uuid-only coalesce silently misses.
	resident, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "resident", Title: "Resident", ProjectUUID: campaign.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create resident task: %v", err)
	}
	f.residentTaskID, f.residentTaskUUID = resident.ID, resident.UUID

	plain, err := s.Containers.Create(monitorSystemActor, store.ContainerCreateParams{
		Slug: "notes", Kind: "directory", ParentUUID: &f.proj,
	})
	if err != nil {
		t.Fatalf("create plain container: %v", err)
	}
	_ = plain
	f.plainContainerPath = "proj/notes"
	return f
}

func (f *roomFixture) say(t *testing.T, p RoomSayParams) *WrkqRoomSayResult {
	t.Helper()
	result, err := f.api.RoomSay(context.Background(), p)
	if err != nil {
		t.Fatalf("RoomSay(%+v): %v", p, err)
	}
	return result
}

func assertDomainCode(t *testing.T, want string, err error) *DomainError {
	t.Helper()
	if err == nil {
		t.Fatalf("expected %s, got nil error", want)
	}
	de, ok := err.(*DomainError)
	if !ok {
		t.Fatalf("expected *DomainError %s, got %T: %v", want, err, err)
	}
	if de.Code() != want {
		t.Fatalf("code = %s, want %s (%v)", de.Code(), want, err)
	}
	return de
}

func assertValidationReason(t *testing.T, want string, err error) {
	t.Helper()
	de := assertDomainCode(t, CodeValidation, err)
	data, ok := de.Data().(map[string]any)
	if !ok {
		t.Fatalf("validation data = %T(%v), want reason %q", de.Data(), de.Data(), want)
	}
	if got, _ := data["reason"].(string); got != want {
		t.Fatalf("validation reason = %q, want %q (data=%v)", got, want, data)
	}
}

func databaseRowsSnapshot(t *testing.T, database interface {
	Query(query string, args ...any) (*sql.Rows, error)
}, query string, args ...any) string {
	t.Helper()
	rows, err := database.Query(query, args...)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("snapshot columns: %v", err)
	}
	result := make([][]any, 0)
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			t.Fatalf("snapshot scan: %v", err)
		}
		for i, value := range values {
			if raw, ok := value.([]byte); ok {
				values[i] = string(raw)
			}
		}
		result = append(result, values)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot rows: %v", err)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("snapshot marshal: %v", err)
	}
	return string(encoded)
}

// backdateRoom ages every timestamp the activity clock folds — opened_at, the
// room's envelopes, and its member joins — so a test can reach `quiet` or
// `stale` without a clock injection. The projection reads stored timestamps, so
// this is the same arithmetic production does.
func backdateRoom(t *testing.T, f *roomFixture, roomUUID string, age time.Duration) {
	t.Helper()
	stamp := time.Now().UTC().Add(-age).Format("2006-01-02T15:04:05Z")
	for _, statement := range []string{
		"UPDATE rooms SET opened_at = ?, last_activity_at = ? WHERE uuid = ?",
		"UPDATE envelopes SET created_at = ? WHERE room_uuid = ?",
		"UPDATE room_members SET joined_at = ? WHERE room_uuid = ?",
	} {
		args := []interface{}{stamp, roomUUID}
		if strings.Count(statement, "?") == 3 {
			args = []interface{}{stamp, stamp, roomUUID}
		}
		if _, err := f.s.DB().Exec(statement, args...); err != nil {
			t.Fatalf("backdate room: %v", err)
		}
	}
}

// roomActivity is the projection under test, read back through the public DTO.
func roomActivity(t *testing.T, f *roomFixture, selector string) *WrkqRoom {
	t.Helper()
	room, err := f.api.RoomShow(context.Background(), RoomShowParams{Room: selector})
	if err != nil {
		t.Fatalf("RoomShow(%s): %v", selector, err)
	}
	return room
}

func roomMemberPrincipal(t *testing.T, f *roomFixture, memberRef string) string {
	t.Helper()
	var principal string
	if err := f.s.DB().QueryRow(
		"SELECT member_principal_ref FROM room_members WHERE member_ref = ?", memberRef,
	).Scan(&principal); err != nil {
		t.Fatalf("read member %s: %v", memberRef, err)
	}
	return principal
}

// present records one presentation of each envelope on runtime rt-1: the
// kicker's ordinary drive, which every obligation test needs before a reply
// can discharge anything.
func (f *roomFixture) present(t *testing.T, envelopeIDs ...string) {
	t.Helper()
	for _, envelopeID := range envelopeIDs {
		if _, err := f.api.EnvelopePresent(context.Background(), EnvelopePresentParams{
			Envelope: envelopeID, PrincipalRef: "agent:hrc", RuntimeID: "rt-1",
		}); err != nil {
			t.Fatalf("present %s: %v", envelopeID, err)
		}
	}
}

// assertEnvelopeStates reads each envelope back through the public show verb
// and compares its state.
func (f *roomFixture) assertEnvelopeStates(t *testing.T, want map[string]string) {
	t.Helper()
	for envelopeID, wantState := range want {
		shown, err := f.api.EnvelopeShow(context.Background(), EnvelopeShowParams{Envelope: envelopeID})
		if err != nil {
			t.Fatalf("show %s: %v", envelopeID, err)
		}
		if shown.State != wantState {
			t.Fatalf("envelope %s = %s, want %s", envelopeID, shown.State, wantState)
		}
	}
}

// completeTask moves a task to the terminal state the room projection reads.
func (f *roomFixture) completeTask(t *testing.T, taskID string) {
	t.Helper()
	if _, err := f.api.TaskUpdate(context.Background(), TaskUpdateParams{
		Task: taskID, Patch: TaskPatch{State: strp("completed")},
	}); err != nil {
		t.Fatalf("complete task %s: %v", taskID, err)
	}
}

// pendingView is the kicker's read over the given seats.
func (f *roomFixture) pendingView(t *testing.T, includeFyi bool, scopes ...string) *WrkqEnvelopePendingView {
	t.Helper()
	view, err := f.api.EnvelopePendingView(context.Background(), EnvelopePendingViewParams{
		Scopes: scopes, IncludeFyi: includeFyi, PrincipalRef: "agent:hrc",
	})
	if err != nil {
		t.Fatalf("pendingView: %v", err)
	}
	return view
}

// lastEventPayload is the newest event_log payload of one type for a resource.
func (f *roomFixture) lastEventPayload(t *testing.T, resourceUUID, eventType string) string {
	t.Helper()
	var payload string
	if err := f.s.DB().QueryRow(`SELECT payload FROM event_log
		WHERE resource_uuid = ? AND event_type = ? ORDER BY id DESC LIMIT 1`,
		resourceUUID, eventType).Scan(&payload); err != nil {
		t.Fatalf("read %s event: %v", eventType, err)
	}
	return payload
}

// ackedSet indexes a reply's acked envelope ids.
func ackedSet(ids []string) map[string]bool {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}

// envelopeByScope indexes a fan-out's envelopes by addressee seat, failing on
// any sibling without one.
func envelopeByScope(t *testing.T, envelopes []WrkqEnvelope) map[string]string {
	t.Helper()
	byScope := make(map[string]string, len(envelopes))
	for _, envelope := range envelopes {
		if envelope.To == nil || envelope.To.ScopeRef == nil {
			t.Fatalf("fan-out envelope %s has no addressee scope", envelope.ID)
		}
		byScope[*envelope.To.ScopeRef] = envelope.ID
	}
	return byScope
}

// pendingItems indexes a pendingView's wake set by envelope id, mapping each to
// its obligation.
func pendingItems(view *WrkqEnvelopePendingView) map[string]string {
	items := make(map[string]string, len(view.Items))
	for _, item := range view.Items {
		items[item.ID] = item.Obligation
	}
	return items
}
