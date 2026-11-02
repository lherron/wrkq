//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/store"
)

func TestExtensionIndependentFenceFiltersAndLedger(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "ext-target", Title: "Extension target", ProjectUUID: project, State: "open", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}
	var originalUpdated string
	if err := api.db.QueryRow("SELECT updated_at FROM tasks WHERE uuid=?", task.UUID).Scan(&originalUpdated); err != nil {
		t.Fatal(err)
	}
	ctx := attribution.WithCallerScope(context.Background(), "faber@wrkq:T-12364")
	// Caller principal and matching request scope are preserved on both row and ledger.
	zero := int64(0)
	p := ExtPutParams{Subject: ExtSubject{Task: task.ID}, NS: "acp", Kind: "request", ID: "main", Body: map[string]any{"rev": 1}, IfMatch: &zero, PrincipalRef: "agent:faber"}
	first, err := api.ExtPut(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Created || first.Ext.ETag != 1 || first.Ext.Subject != task.ID {
		t.Fatalf("put %#v", first)
	}
	if first.Ext.UpdatedByScopeRef != "faber@wrkq:T-12364" {
		t.Fatalf("scope lost: %#v", first)
	}
	if _, err := api.ExtPut(ctx, p); err == nil {
		t.Fatal("create-only overwrote row")
	}
	one := int64(1)
	p.IfMatch = &one
	p.Body = map[string]any{"rev": 2}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := api.ExtPut(ctx, p); results <- err }()
	}
	wg.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			var e Error
			if errors.As(err, &e) && e.Code() == CodeConflict {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("wins %d conflicts %d", wins, conflicts)
	}
	shown, err := api.ExtShow(ctx, ExtShowParams{Subject: p.Subject, NS: p.NS, Kind: p.Kind, ID: p.ID})
	if err != nil || shown.Ext.ETag != 2 {
		t.Fatalf("show %#v %v", shown, err)
	}
	var etag int64
	var updated string
	if err := api.db.QueryRow("SELECT etag,updated_at FROM tasks WHERE uuid=?", task.UUID).Scan(&etag, &updated); err != nil {
		t.Fatal(err)
	}
	if etag != task.ETag || updated != originalUpdated {
		t.Fatalf("subject mutated: %d %s / %#v", etag, updated, task)
	}
	for _, filter := range []ExtListViewParams{{Subject: &p.Subject, SubjectStates: []string{"open"}}, {NS: "acp"}, {Kind: "request"}} {
		rows, err := api.ExtListView(ctx, filter)
		if err != nil || len(rows.Items) != 1 {
			t.Fatalf("filter %#v: %#v %v", filter, rows, err)
		}
	}
	for _, filter := range []ExtListViewParams{{SubjectStates: []string{"bogus"}}, {NS: "bogus"}, {Kind: "bogus"}} {
		rows, err := api.ExtListView(ctx, filter)
		if err != nil || len(rows.Items) != 0 {
			t.Fatalf("bogus %#v: %#v %v", filter, rows, err)
		}
	}
	deleted, err := api.ExtDelete(ctx, ExtDeleteParams{Subject: p.Subject, NS: p.NS, Kind: p.Kind, ID: p.ID, PrincipalRef: p.PrincipalRef})
	if err != nil || !deleted.Deleted {
		t.Fatalf("delete %#v %v", deleted, err)
	}
	rows, err := api.db.Query("SELECT event_type,payload,principal_ref,scope_ref FROM event_log WHERE resource_uuid=? AND event_type LIKE 'task.ext.%' ORDER BY id", task.UUID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	count := 0
	for rows.Next() {
		var typ, payload, principal, scope string
		if err := rows.Scan(&typ, &payload, &principal, &scope); err != nil {
			t.Fatal(err)
		}
		var parsed map[string]any
		if err := json.Unmarshal([]byte(payload), &parsed); err != nil {
			t.Fatal(err)
		}
		if len(parsed) != 4 || parsed["body"] != nil || principal != "agent:faber" || scope != "faber@wrkq:T-12364" {
			t.Fatalf("ledger %s %s %s %s", typ, payload, principal, scope)
		}
		count++
	}
	if count != 3 {
		t.Fatalf("ledger count %d", count)
	}
}

func TestExtensionValidationQuotaAndPagination(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "ext-validation", Title: "Extension validation", ProjectUUID: project, State: "open", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	base := ExtPutParams{Subject: ExtSubject{Task: task.ID}, NS: "test", Kind: "thing", ID: "main", Body: map[string]any{}, PrincipalRef: "agent:faber"}
	for _, change := range []func(*ExtPutParams){func(p *ExtPutParams) { p.NS = "Bad" }, func(p *ExtPutParams) { p.Kind = "a.b.c.d.e" }, func(p *ExtPutParams) { p.ID = "\n" }, func(p *ExtPutParams) { p.ID = strings.Repeat("x", 201) }, func(p *ExtPutParams) { p.Body = []any{} }, func(p *ExtPutParams) { p.Body = nil }, func(p *ExtPutParams) { p.Body = map[string]any{"x": strings.Repeat("a", 65536)} }} {
		p := base
		change(&p)
		_, err := api.ExtPut(ctx, p)
		var e Error
		if !errors.As(err, &e) || e.Code() != "WRKQ_EXT_INVALID" {
			t.Fatalf("invalid admitted %#v: %v", p, err)
		}
	}
	for i := 0; i < 256; i++ {
		p := base
		p.ID = fmt.Sprint(i)
		if _, err := api.ExtPut(ctx, p); err != nil {
			t.Fatal(err)
		}
	}
	_, err = api.ExtPut(ctx, base)
	var e Error
	if !errors.As(err, &e) || e.Code() != "WRKQ_EXT_QUOTA" {
		t.Fatalf("quota: %v", err)
	}
	var cursor string
	seen := map[string]bool{}
	for {
		result, err := api.ExtListView(ctx, ExtListViewParams{NS: "test", Limit: 37, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range result.Items {
			if seen[item.ID] {
				t.Fatal("duplicate page item")
			}
			seen[item.ID] = true
		}
		cursor = result.NextCursor
		if cursor == "" {
			break
		}
	}
	if len(seen) != 256 {
		t.Fatalf("paged %d", len(seen))
	}
}

func TestExtensionBodyPreservesLargeJSONNumbers(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "ext-numbers", Title: "Numbers", ProjectUUID: project, State: "open", Priority: 2})
	if err != nil {
		t.Fatal(err)
	}
	var p ExtPutParams
	if err := json.Unmarshal([]byte(fmt.Sprintf(`{"subject":{"task":%q},"ns":"test","kind":"number","id":"main","body":{"value":9007199254740993},"principalRef":"agent:faber"}`, task.ID)), &p); err != nil {
		t.Fatal(err)
	}
	result, err := api.ExtPut(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(result.Ext.Body)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"value":9007199254740993}` {
		t.Fatalf("body changed: %s", encoded)
	}
}
