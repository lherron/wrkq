package store

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/webhooks"
)

type observedCampaignWebhook struct {
	Payload   webhooks.CampaignPayload
	Committed bool
}

func receiveCampaignWebhook(
	t *testing.T,
	ch <-chan observedCampaignWebhook,
) observedCampaignWebhook {
	t.Helper()
	select {
	case observed := <-ch:
		return observed
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for campaign webhook")
		return observedCampaignWebhook{}
	}
}

func waitForCampaignDeliveryCount(t *testing.T, counter *atomic.Int64, want int64) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if counter.Load() == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("delivery count = %d, want %d", counter.Load(), want)
}

func TestCampaignWebhookPostCommitPayloadInheritanceAndClassIsolation(t *testing.T) {
	f := newCampaignMembershipFixture(t)
	attr := testAttribution(f.actorUUID)
	received := make(chan observedCampaignWebhook, 8)

	containerReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload webhooks.CampaignPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		var state sql.NullString
		stateErr := f.db.QueryRow(
			"SELECT campaign_state FROM containers WHERE uuid = ?", payload.CampaignUUID,
		).Scan(&state)
		var eventType string
		eventErr := f.db.QueryRow(
			"SELECT event_type FROM event_log WHERE id = ?", payload.EventID,
		).Scan(&eventType)
		received <- observedCampaignWebhook{
			Payload: payload,
			Committed: stateErr == nil && state.Valid &&
				state.String == payload.NewCampaignState &&
				eventErr == nil && eventType == webhooks.EventContainerCampaignStateChanged,
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer containerReceiver.Close()

	var taskDeliveries atomic.Int64
	taskReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		taskDeliveries.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer taskReceiver.Close()

	var bareDeliveries atomic.Int64
	bareReceiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		bareDeliveries.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer bareReceiver.Close()

	subscriptions, err := json.Marshal([]any{
		map[string]any{"url": taskReceiver.URL, "events": []string{"task"}},
		map[string]any{"url": containerReceiver.URL, "events": []string{"container"}},
		bareReceiver.URL,
	})
	if err != nil {
		t.Fatalf("marshal webhook subscriptions: %v", err)
	}
	if _, err := f.store.Containers.UpdateFields(
		f.actorUUID,
		f.projectA,
		map[string]any{"webhook_urls": string(subscriptions)},
		0,
	); err != nil {
		t.Fatalf("set inherited webhook subscriptions: %v", err)
	}

	description := "campaign webhook fixture"
	converted, err := f.store.Containers.ConvertCampaignWithAttribution(
		attr, f.nonCampaignContainer, CampaignStateDraft, &description, nil, nil, 0,
	)
	if err != nil {
		t.Fatalf("convert campaign: %v", err)
	}
	conversion := receiveCampaignWebhook(t, received)
	if !conversion.Committed {
		t.Fatalf("conversion webhook arrived before committed state/event: %#v", conversion.Payload)
	}
	if conversion.Payload.SchemaVersion != webhooks.CampaignPayloadSchema ||
		conversion.Payload.Event != webhooks.EventContainerCampaignStateChanged ||
		conversion.Payload.EventID != converted.EventID ||
		conversion.Payload.IdempotencyKey != converted.EventID ||
		conversion.Payload.OccurredAt != converted.EventTimestamp ||
		conversion.Payload.Actor != attr.PrincipalRef ||
		conversion.Payload.PrincipalRef != attr.PrincipalRef ||
		conversion.Payload.CampaignUUID != f.nonCampaignContainer ||
		conversion.Payload.CampaignID == "" ||
		conversion.Payload.CampaignPath != "campaign-project-a/plain-bucket" ||
		conversion.Payload.OldCampaignState != nil ||
		conversion.Payload.NewCampaignState != CampaignStateDraft {
		t.Fatalf("conversion payload = %#v", conversion.Payload)
	}
	waitForCampaignDeliveryCount(t, &bareDeliveries, 1)
	if taskDeliveries.Load() != 0 || bareDeliveries.Load() != 1 {
		t.Fatalf("conversion deliveries task/bare = %d/%d, want 0/1",
			taskDeliveries.Load(), bareDeliveries.Load())
	}

	// At-least-once delivery is idempotent-safe: replaying the committed event
	// produces the exact same event id and idempotency key.
	webhooks.DispatchCampaignTransition(
		f.db,
		f.nonCampaignContainer,
		converted.PreviousState,
		converted.CampaignState,
		events.EventMetadata{ID: converted.EventID, Timestamp: converted.EventTimestamp},
		attr.PrincipalRef,
	)
	replay := receiveCampaignWebhook(t, received)
	if replay.Payload.EventID != conversion.Payload.EventID ||
		replay.Payload.IdempotencyKey != conversion.Payload.IdempotencyKey {
		t.Fatalf("duplicate delivery identity changed: first=%#v replay=%#v",
			conversion.Payload, replay.Payload)
	}
	waitForCampaignDeliveryCount(t, &bareDeliveries, 2)
	if taskDeliveries.Load() != 0 || bareDeliveries.Load() != 2 {
		t.Fatalf("replay deliveries task/bare = %d/%d, want 0/2",
			taskDeliveries.Load(), bareDeliveries.Load())
	}

	// A generic plain-container mutation has no campaign transition producer
	// and therefore emits no container webhook.
	if _, err := f.store.Containers.UpdateFields(
		f.actorUUID,
		f.projectA,
		map[string]any{"title": "still only the transition fires"},
		0,
	); err != nil {
		t.Fatalf("plain container update: %v", err)
	}
	if taskDeliveries.Load() != 0 || bareDeliveries.Load() != 2 || len(received) != 0 {
		t.Fatalf("plain update unexpectedly dispatched: task=%d bare=%d container=%d",
			taskDeliveries.Load(), bareDeliveries.Load(), len(received))
	}

	activated, err := f.store.Containers.TransitionCampaignWithAttribution(
		attr, f.nonCampaignContainer, CampaignStateActive, 0,
	)
	if err != nil {
		t.Fatalf("activate campaign: %v", err)
	}
	activationWebhook := receiveCampaignWebhook(t, received)
	if !activationWebhook.Committed ||
		activationWebhook.Payload.EventID != activated.EventID ||
		activationWebhook.Payload.IdempotencyKey != activated.EventID ||
		activationWebhook.Payload.OldCampaignState == nil ||
		*activationWebhook.Payload.OldCampaignState != CampaignStateDraft ||
		activationWebhook.Payload.NewCampaignState != CampaignStateActive {
		t.Fatalf("activation webhook = %#v committed=%v", activationWebhook.Payload, activationWebhook.Committed)
	}
	waitForCampaignDeliveryCount(t, &bareDeliveries, 3)
	if taskDeliveries.Load() != 0 || bareDeliveries.Load() != 3 {
		t.Fatalf("activation deliveries task/bare = %d/%d, want 0/3",
			taskDeliveries.Load(), bareDeliveries.Load())
	}

	closed, err := f.store.Containers.TransitionCampaignWithAttribution(
		attr, f.nonCampaignContainer, CampaignStateCompleted, 0,
	)
	if err != nil {
		t.Fatalf("close campaign: %v", err)
	}
	closeWebhook := receiveCampaignWebhook(t, received)
	if !closeWebhook.Committed ||
		closeWebhook.Payload.EventID != closed.EventID ||
		closeWebhook.Payload.IdempotencyKey != closed.EventID ||
		closeWebhook.Payload.OldCampaignState == nil ||
		*closeWebhook.Payload.OldCampaignState != CampaignStateActive ||
		closeWebhook.Payload.NewCampaignState != CampaignStateCompleted {
		t.Fatalf("close webhook = %#v committed=%v", closeWebhook.Payload, closeWebhook.Committed)
	}
	waitForCampaignDeliveryCount(t, &bareDeliveries, 4)
	if taskDeliveries.Load() != 0 || bareDeliveries.Load() != 4 {
		t.Fatalf("close deliveries task/bare = %d/%d, want 0/4",
			taskDeliveries.Load(), bareDeliveries.Load())
	}

	draftToCancel, err := f.store.Containers.Create(f.actorUUID, ContainerCreateParams{
		Slug: "draft-to-cancel", Kind: "directory", ParentUUID: &f.projectA,
	})
	if err != nil {
		t.Fatalf("create draft-to-cancel container: %v", err)
	}
	draftResult, err := f.store.Containers.ConvertCampaignWithAttribution(
		attr, draftToCancel.UUID, CampaignStateDraft, nil, nil, nil, 0,
	)
	if err != nil {
		t.Fatalf("convert second draft: %v", err)
	}
	secondDraftWebhook := receiveCampaignWebhook(t, received)
	if !secondDraftWebhook.Committed ||
		secondDraftWebhook.Payload.EventID != draftResult.EventID ||
		secondDraftWebhook.Payload.OldCampaignState != nil ||
		secondDraftWebhook.Payload.NewCampaignState != CampaignStateDraft {
		t.Fatalf("second draft webhook = %#v committed=%v", secondDraftWebhook.Payload, secondDraftWebhook.Committed)
	}
	waitForCampaignDeliveryCount(t, &bareDeliveries, 5)

	cancelledDraft, err := f.store.Containers.TransitionCampaignWithAttribution(
		attr, draftToCancel.UUID, CampaignStateCancelled, 0,
	)
	if err != nil {
		t.Fatalf("cancel second draft: %v", err)
	}
	draftCancelWebhook := receiveCampaignWebhook(t, received)
	if !draftCancelWebhook.Committed ||
		draftCancelWebhook.Payload.EventID != cancelledDraft.EventID ||
		draftCancelWebhook.Payload.OldCampaignState == nil ||
		*draftCancelWebhook.Payload.OldCampaignState != CampaignStateDraft ||
		draftCancelWebhook.Payload.NewCampaignState != CampaignStateCancelled {
		t.Fatalf("draft cancel webhook = %#v committed=%v", draftCancelWebhook.Payload, draftCancelWebhook.Committed)
	}
	waitForCampaignDeliveryCount(t, &bareDeliveries, 6)
	if taskDeliveries.Load() != 0 || bareDeliveries.Load() != 6 {
		t.Fatalf("draft cancel deliveries task/bare = %d/%d, want 0/6",
			taskDeliveries.Load(), bareDeliveries.Load())
	}
}

func TestCampaignWebhookReceiverDownDoesNotFailCommittedClose(t *testing.T) {
	f := newCampaignMembershipFixture(t)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		entered <- struct{}{}
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))

	subscriptions, err := json.Marshal([]any{
		map[string]any{"url": down.URL, "events": []string{"container"}},
	})
	if err != nil {
		t.Fatalf("marshal down receiver subscription: %v", err)
	}
	if _, err := f.store.Containers.UpdateFields(
		f.actorUUID,
		f.campaignA,
		map[string]any{"webhook_urls": string(subscriptions)},
		0,
	); err != nil {
		t.Fatalf("set down receiver: %v", err)
	}

	started := time.Now()
	result, err := f.store.Containers.TransitionCampaignWithAttribution(
		testAttribution(f.actorUUID), f.campaignA, CampaignStateCancelled, 0,
	)
	if err != nil {
		t.Fatalf("receiver-down close failed: %v", err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("receiver-down close blocked for %s", elapsed)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("receiver never observed asynchronous delivery")
	}
	close(release)
	down.Close()
	var state string
	if err := f.db.QueryRow(
		"SELECT campaign_state FROM containers WHERE uuid = ?", f.campaignA,
	).Scan(&state); err != nil {
		t.Fatalf("read committed receiver-down state: %v", err)
	}
	if state != CampaignStateCancelled || result.EventID == 0 {
		t.Fatalf("receiver-down close state/event = %s/%d", state, result.EventID)
	}
}
