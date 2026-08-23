//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

func TestPromiseCreateOnBehalfNormalizesAndAudits(t *testing.T) {
	api, s := newMonitorAPI(t)
	ctx := context.Background()

	promise, err := api.PromiseAdd(ctx, PromiseAddParams{
		OwnerPrincipalRef: "lance",
		OnBehalf:          true,
		Subject:           "Check the HRC envelope rollout",
		ReviewAt:          "2099-08-24T00:30:00+01:00",
		PrincipalRef:      "agent:cody",
	})
	if err != nil {
		t.Fatalf("PromiseAdd: %v", err)
	}
	if promise.OwnerPrincipalRef != "agent:lance" || promise.CreatedByPrincipalRef != "agent:cody" {
		t.Fatalf("owner/creator = %q/%q", promise.OwnerPrincipalRef, promise.CreatedByPrincipalRef)
	}
	if promise.ReviewAt != "2099-08-23T23:30:00Z" {
		t.Fatalf("reviewAt = %q, want canonical UTC", promise.ReviewAt)
	}
	ready, err := api.PromiseReady(ctx, PromiseReadyParams{OwnerPrincipalRef: "lance"})
	if err != nil {
		t.Fatalf("PromiseReady: %v", err)
	}
	if len(ready.Items) != 0 {
		t.Fatalf("future promise unexpectedly ready: %#v", ready.Items)
	}

	var payload string
	if err := s.DB().QueryRow(`
		SELECT payload FROM event_log
		 WHERE resource_type = 'promise' AND resource_uuid = ? AND event_type = 'promise.created'
	`, promise.UUID).Scan(&payload); err != nil {
		t.Fatalf("read created event: %v", err)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(payload), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event["on_behalf_asserted_by"] != "agent:cody" {
		t.Fatalf("on_behalf_asserted_by = %#v", event["on_behalf_asserted_by"])
	}

	self, err := api.PromiseAdd(ctx, PromiseAddParams{
		Subject: "Agent-owned rollout check", ReviewIn: "7d", PrincipalRef: "agent:cody",
	})
	if err != nil {
		t.Fatalf("self PromiseAdd: %v", err)
	}
	if self.OwnerPrincipalRef != "agent:cody" || self.CreatedByPrincipalRef != "agent:cody" {
		t.Fatalf("self-owned promise = %#v", self)
	}
	listed, err := api.PromiseList(ctx, PromiseListParams{PrincipalRef: "agent:cody"})
	if err != nil || len(listed.Items) != 1 || listed.Items[0].UUID != self.UUID {
		t.Fatalf("agent promise list = %#v, err=%v", listed, err)
	}
}

func TestPromiseAssignmentAndReviewValidationWriteNothing(t *testing.T) {
	api, s := newMonitorAPI(t)
	ctx := context.Background()

	assertPromiseCode(t, CodeForbidden, func() error {
		_, err := api.PromiseAdd(ctx, PromiseAddParams{
			OwnerPrincipalRef: "agent:lance", Subject: "unauthorized", ReviewIn: "7d", PrincipalRef: "agent:cody",
		})
		return err
	}())
	assertPromiseCount(t, s, 0)

	for name, params := range map[string]PromiseAddParams{
		"bad absolute": {Subject: "bad", ReviewAt: "tomorrow", PrincipalRef: "agent:cody"},
		"bad relative": {Subject: "bad", ReviewIn: "seven days", PrincipalRef: "agent:cody"},
		"both":         {Subject: "bad", ReviewAt: "2099-01-01T00:00:00Z", ReviewIn: "7d", PrincipalRef: "agent:cody"},
	} {
		t.Run(name, func(t *testing.T) {
			assertPromiseCode(t, CodeValidation, func() error {
				_, err := api.PromiseAdd(ctx, params)
				return err
			}())
			assertPromiseCount(t, s, 0)
		})
	}
}

func TestPromiseListOwnerAndSubjectFilters(t *testing.T) {
	api, s := newMonitorAPI(t)
	ctx := context.Background()
	projectUUID := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "promise-list-target", Title: "Promise list target", ProjectUUID: projectUUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	create := func(owner, subject, target string) WrkqPromise {
		t.Helper()
		params := PromiseAddParams{OwnerPrincipalRef: owner, Subject: subject, Task: target, ReviewIn: "7d", PrincipalRef: "agent:cody"}
		if owner != "" && owner != "agent:cody" {
			params.OnBehalf = true
		}
		promise, err := api.PromiseAdd(ctx, params)
		if err != nil {
			t.Fatalf("create %s: %v", subject, err)
		}
		return *promise
	}
	codyAttached := create("agent:cody", "cody attached", task.ID)
	codyStandalone := create("agent:cody", "cody standalone", "")
	mableAttached := create("agent:mable", "mable attached", task.ID)
	mableStandalone := create("agent:mable", "mable standalone", "")

	tests := []struct {
		name   string
		params PromiseListParams
		want   []string
	}{
		{"default owner", PromiseListParams{PrincipalRef: "agent:cody"}, []string{codyAttached.ID, codyStandalone.ID}},
		{"subject all owners", PromiseListParams{Task: task.ID, PrincipalRef: "agent:cody"}, []string{codyAttached.ID, mableAttached.ID}},
		{"explicit owner", PromiseListParams{OwnerPrincipalRef: "agent:mable", PrincipalRef: "agent:cody"}, []string{mableAttached.ID, mableStandalone.ID}},
		{"owner subject intersection", PromiseListParams{OwnerPrincipalRef: "agent:mable", Task: task.ID, PrincipalRef: "agent:cody"}, []string{mableAttached.ID}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := api.PromiseList(ctx, tt.params)
			if err != nil {
				t.Fatal(err)
			}
			got := make([]string, 0, len(result.Items))
			for _, item := range result.Items {
				got = append(got, item.ID)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ids = %v, want %v (all items=%#v)", got, tt.want, result.Items)
			}
			for index := range got {
				if got[index] != tt.want[index] {
					t.Fatalf("ids = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestPromiseNonOwnerCannotMutateAnySurface(t *testing.T) {
	api, s := newMonitorAPI(t)
	ctx := context.Background()
	projectUUID := seedMonitorProject(t, s)
	promise, err := api.PromiseAdd(ctx, PromiseAddParams{
		OwnerPrincipalRef: "agent:lance", OnBehalf: true, Subject: "owner boundary",
		ReviewAt: "2099-01-01T00:00:00Z", PrincipalRef: "agent:cody",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	other := "agent:cody"
	newSubject := "changed"
	mutations := map[string]func() error{
		"edit": func() error {
			_, err := api.PromiseEdit(ctx, PromiseEditParams{Promise: promise.ID, Subject: &newSubject, PrincipalRef: other})
			return err
		},
		"attach": func() error {
			_, err := api.PromiseAttach(ctx, PromiseRetargetParams{Promise: promise.ID, Container: projectUUID, PrincipalRef: other})
			return err
		},
		"detach": func() error {
			_, err := api.PromiseDetach(ctx, PromiseRetargetParams{Promise: promise.ID, PrincipalRef: other})
			return err
		},
		"renew": func() error {
			_, err := api.PromiseRenew(ctx, PromiseReviewParams{Promise: promise.ID, ReviewIn: "7d", PrincipalRef: other})
			return err
		},
		"resolve": func() error {
			_, err := api.PromiseResolve(ctx, PromiseReviewParams{Promise: promise.ID, PrincipalRef: other})
			return err
		},
		"abandon": func() error {
			_, err := api.PromiseAbandon(ctx, PromiseReviewParams{Promise: promise.ID, PrincipalRef: other})
			return err
		},
		"rm": func() error {
			_, err := api.PromiseDelete(ctx, PromiseDeleteParams{Promise: promise.ID, Mode: "soft", PrincipalRef: other})
			return err
		},
		"purge": func() error {
			_, err := api.PromiseDelete(ctx, PromiseDeleteParams{Promise: promise.ID, Mode: "purge", PrincipalRef: other})
			return err
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			assertPromiseCode(t, CodeForbidden, mutate())
			unchanged, err := api.PromiseShow(ctx, PromiseShowParams{Promise: promise.ID})
			if err != nil {
				t.Fatalf("show unchanged: %v", err)
			}
			if unchanged.ETag != promise.ETag || unchanged.Subject != promise.Subject || unchanged.State != "open" || unchanged.SubjectRef != nil {
				t.Fatalf("non-owner mutation changed row: %#v", unchanged)
			}
		})
	}
	var eventCount int
	if err := s.DB().QueryRow("SELECT COUNT(*) FROM event_log WHERE resource_uuid = ?", promise.UUID).Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	if eventCount != 1 {
		t.Fatalf("non-owner mutations appended events: count=%d", eventCount)
	}
}

func TestPromiseOwnerMutationsAndHistory(t *testing.T) {
	api, s := newMonitorAPI(t)
	ctx := context.Background()
	projectUUID := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "promise-target", Title: "Promise target", ProjectUUID: projectUUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	promise, err := api.PromiseAdd(ctx, PromiseAddParams{
		Task: task.ID, ReviewAt: "2099-01-01T00:00:00Z", PrincipalRef: "agent:cody",
	})
	if err != nil {
		t.Fatalf("create attached: %v", err)
	}
	if promise.Subject != "Promise target" || promise.SubjectRef == nil || promise.SubjectRef.ID != task.ID {
		t.Fatalf("derived attached subject = %#v", promise)
	}

	question := "Still needed?"
	edited, err := api.PromiseEdit(ctx, PromiseEditParams{
		Promise: promise.ID, ReviewQuestion: &question, ReviewAt: "2099-01-02T01:00:00+01:00",
		IfMatch: promise.ETag, PrincipalRef: "agent:cody",
	})
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.ReviewAt != "2099-01-02T00:00:00Z" || edited.ETag != promise.ETag+1 {
		t.Fatalf("edited = %#v", edited)
	}
	detached, err := api.PromiseDetach(ctx, PromiseRetargetParams{
		Promise: promise.ID, IfMatch: edited.ETag, PrincipalRef: "agent:cody",
	})
	if err != nil || detached.SubjectRef != nil {
		t.Fatalf("detach = %#v, err=%v", detached, err)
	}
	renewed, err := api.PromiseRenew(ctx, PromiseReviewParams{
		Promise: promise.ID, ReviewIn: "36h", IfMatch: detached.ETag, PrincipalRef: "agent:cody",
	})
	if err != nil || renewed.State != "open" || renewed.LastReviewedAt == nil {
		t.Fatalf("renew = %#v, err=%v", renewed, err)
	}
	resolved, err := api.PromiseResolve(ctx, PromiseReviewParams{
		Promise: promise.ID, IfMatch: renewed.ETag, PrincipalRef: "agent:cody",
	})
	if err != nil || resolved.State != "resolved" {
		t.Fatalf("resolve = %#v, err=%v", resolved, err)
	}
	history, err := api.HistoryListView(ctx, HistoryListViewParams{Target: promise.ID})
	if err != nil {
		t.Fatalf("promise history: %v", err)
	}
	if len(history.Items) != 5 || history.Items[0].EventType != "promise.resolved" {
		t.Fatalf("history = %#v", history.Items)
	}
}

func assertPromiseCode(t *testing.T, want string, err error) {
	t.Helper()
	var apiErr Error
	if !errors.As(err, &apiErr) || apiErr.Code() != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}

func assertPromiseCount(t *testing.T, s *store.Store, want int) {
	t.Helper()
	var got int
	if err := s.DB().QueryRow("SELECT COUNT(*) FROM promises").Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("promise count = %d, want %d", got, want)
	}
}
