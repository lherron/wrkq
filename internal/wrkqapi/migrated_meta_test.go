//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestMigratedMetaGuardPathsAndFences(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name, ns, kind, id string
		before, after      map[string]any
	}{
		{"acp.request", "acp", "request", "main", map[string]any{"acp": map[string]any{"request": map[string]any{"rev": 1}}}, map[string]any{"acp": map[string]any{"request": map[string]any{"rev": 2}}}},
		{"scribe", "scribe", "request", "main", map[string]any{"scribe": map[string]any{"slug": "old"}}, map[string]any{"scribe": map[string]any{"slug": "new"}}},
		{"wrkf.taskStateMirror", "agentloop", "taskStateMirror", "main", map[string]any{"wrkf": map[string]any{"taskStateMirror": map[string]any{"version": 1}}}, map[string]any{"wrkf": map[string]any{"taskStateMirror": map[string]any{"version": 2}}}},
		{"hcs_target", "hcs", "launch", "main", map[string]any{"hcs_target": "old"}, map[string]any{"hcs_target": "new"}},
		{"hcs_future", "hcs", "launch", "main", map[string]any{}, map[string]any{"hcs_future": "new"}},
	}
	for _, key := range strings.Fields("technique source risk package file apiImpact deltaId target targetSlug gate sourceWorkItem reportPath path") {
		cases = append(cases, struct {
			name, ns, kind, id string
			before, after      map[string]any
		}{key, "archagent", "refactor", "different-delta", map[string]any{key: "old"}, map[string]any{key: "new"}})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newAttributionAPI(t, "agent:seed")
			task, err := a.TaskCreate(ctx, TaskCreateParams{Title: "guarded", Meta: tc.before})
			if err != nil {
				t.Fatal(err)
			}
			// Before the mapped extension exists, legacy writers still work.
			task, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Meta: &tc.after}})
			if err != nil {
				t.Fatal(err)
			}
			task, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Meta: &tc.before}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := a.ExtPut(ctx, ExtPutParams{Subject: ExtSubject{Task: task.ID}, NS: tc.ns, Kind: tc.kind, ID: tc.id, Body: map[string]any{}}); err != nil {
				t.Fatal(err)
			}
			initial := *task
			_, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, ExpectEtag: &task.ETag, Patch: TaskPatch{Meta: &tc.after}})
			if err == nil || claimErrorCode(t, err) != "WRKQ_META_KEY_MIGRATED" || !strings.Contains(err.Error(), "wrkq ext put") || !strings.Contains(err.Error(), "meta."+tc.name) {
				t.Fatalf("guard: %v", err)
			}
			shown, err := a.TaskShow(ctx, TaskShowParams{Task: task.ID})
			if err != nil {
				t.Fatal(err)
			}
			if shown.ETag != initial.ETag || shown.UpdatedAt != initial.UpdatedAt {
				t.Fatalf("refusal mutated task: %+v -> %+v", initial, shown)
			}
			// Carrying the key unchanged through edits to unrelated metadata is allowed.
			carry := map[string]any{}
			for k, v := range tc.before {
				carry[k] = v
			}
			carry["unrelated"] = true
			if tc.name == "acp.request" {
				carry["acp"] = map[string]any{"request": map[string]any{"rev": 1}, "unrelated": "changed"}
			}
			if _, err := a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Meta: &carry}}); err != nil {
				t.Fatalf("carry: %v", err)
			}
			// Raw JSON path, used by wrkq set --meta, has the same protection.
			raw, _ := json.Marshal(tc.after)
			rawText := string(raw)
			if _, err := a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{MetaRaw: &rawText}}); err == nil || claimErrorCode(t, err) != "WRKQ_META_KEY_MIGRATED" {
				t.Fatalf("raw: %v", err)
			}
			empty := map[string]any{}
			if _, err := a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Meta: &empty}}); err != nil {
				t.Fatalf("remove: %v", err)
			}
		})
	}
}

func TestMigratedMetaGuardDistinguishesAbsentAndNull(t *testing.T) {
	a := newAttributionAPI(t, "agent:seed")
	ctx := context.Background()
	for _, present := range []bool{false, true} {
		meta := map[string]any{}
		if present {
			meta["scribe"] = nil
		}
		title := "absent"
		if present {
			title = "present"
		}
		task, err := a.TaskCreate(ctx, TaskCreateParams{Title: title, Meta: meta})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.ExtPut(ctx, ExtPutParams{Subject: ExtSubject{Task: task.ID}, NS: "scribe", Kind: "request", ID: "main", Body: map[string]any{}}); err != nil {
			t.Fatal(err)
		}
		next := map[string]any{"scribe": nil}
		_, err = a.TaskUpdate(ctx, TaskUpdateParams{Task: task.ID, Patch: TaskPatch{Meta: &next}})
		if present && err != nil {
			t.Fatalf("unchanged null: %v", err)
		}
		if !present && (err == nil || claimErrorCode(t, err) != "WRKQ_META_KEY_MIGRATED") {
			t.Fatalf("added null: %v", err)
		}
	}
}
