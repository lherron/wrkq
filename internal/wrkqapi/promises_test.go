//go:build wrkq_local

package wrkqapi

import (
	"context"
	"errors"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

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

func assertPromiseCode(t *testing.T, want string, err error) {
	t.Helper()
	var apiErr Error
	if !errors.As(err, &apiErr) || apiErr.Code() != want {
		t.Fatalf("error = %v, want %s", err, want)
	}
}
