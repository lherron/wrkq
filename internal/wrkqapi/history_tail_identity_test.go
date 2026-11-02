//go:build wrkq_local

package wrkqapi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
)

// Decode the wire fields so the red phase detects missing JSON fields as well
// as wrong values (including a missing head when the correct value is zero).
func assertTailIdentity(t *testing.T, api *API, params HistoryTailViewParams, incarnation string, head int64, empty bool) *WrkqHistoryTailView {
	t.Helper()
	view, err := api.HistoryTailView(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		Incarnation string `json:"incarnation"`
		Head        *int64 `json:"head"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Incarnation != incarnation || identity.Head == nil || *identity.Head != head {
		t.Fatalf("want incarnation=%s head=%d; got %s", incarnation, head, raw)
	}
	if empty && len(view.Items) != 0 {
		t.Fatalf("want empty page, got %d rows", len(view.Items))
	}
	return view
}

func TestHistoryTailView_IdentityAndHead(t *testing.T) {
	api, s := newMonitorAPI(t)
	var incarnation string
	if err := api.db.QueryRow("SELECT incarnation FROM collaboration_ledger_meta WHERE singleton = 1").Scan(&incarnation); err != nil {
		t.Fatal(err)
	}
	if len(incarnation) != 32 {
		t.Fatalf("unexpected incarnation %q", incarnation)
	}
	if _, err := api.db.Exec("DELETE FROM event_log"); err != nil {
		t.Fatal(err)
	}
	assertTailIdentity(t, api, HistoryTailViewParams{}, incarnation, 0, true)
	proj := seedMonitorProject(t, s)
	task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "watched", Title: "Watched", ProjectUUID: proj, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	var head int64
	if err := api.db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM event_log").Scan(&head); err != nil {
		t.Fatal(err)
	}
	t.Run("bounded page carries full head", func(t *testing.T) {
		view := assertTailIdentity(t, api, HistoryTailViewParams{Limit: 1}, incarnation, head, false)
		if len(view.Items) != 1 || view.HighWater != view.Items[0].ID || view.HighWater >= head {
			t.Fatalf("expected bounded page before head %d: %+v", head, view)
		}
	})
	for _, cursor := range []int64{head, head + 100} {
		view := assertTailIdentity(t, api, HistoryTailViewParams{Cursor: cursor}, incarnation, head, true)
		if view.HighWater != cursor {
			t.Fatalf("empty page changed high_water: want %d got %d", cursor, view.HighWater)
		}
	}
	// Retain a resolvable task but remove its events so the filter drops every row.
	if _, err := api.db.Exec("DELETE FROM event_log WHERE resource_uuid = ?", task.UUID); err != nil {
		t.Fatal(err)
	}
	if err := api.db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM event_log").Scan(&head); err != nil {
		t.Fatal(err)
	}
	view := assertTailIdentity(t, api, HistoryTailViewParams{Tasks: []string{task.ID}}, incarnation, head, true)
	if head == 0 || view.HighWater != head {
		t.Fatalf("filter must advance over excluded rows: head=%d high_water=%d", head, view.HighWater)
	}
	other, _ := newMonitorAPI(t)
	otherView, err := other.HistoryTailView(context.Background(), HistoryTailViewParams{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(otherView)
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		Incarnation string `json:"incarnation"`
	}
	if err := json.Unmarshal(raw, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Incarnation == "" || identity.Incarnation == incarnation {
		t.Fatalf("separately initialized DB must have a different incarnation: %s", raw)
	}
}

func TestHistoryTailView_FileCopyRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "restore.db")
	open := func() *API {
		database, err := db.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = database.Close() })
		if err := database.Migrate(); err != nil {
			t.Fatal(err)
		}
		return New(database, nil, "agent:wrkq-system", "", 0)
	}
	api := open()
	proj := seedMonitorProject(t, store.New(api.db))
	var incarnation string
	var h1 int64
	if err := api.db.QueryRow("SELECT incarnation FROM collaboration_ledger_meta WHERE singleton = 1").Scan(&incarnation); err != nil {
		t.Fatal(err)
	}
	if err := api.db.QueryRow("SELECT COALESCE(MAX(id), 0) FROM event_log").Scan(&h1); err != nil {
		t.Fatal(err)
	}
	// Close checkpoints WAL before taking an actual file-copy backup.
	if err := api.db.Close(); err != nil {
		t.Fatal(err)
	}
	backup, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	api = open()
	if _, err := store.New(api.db).Tasks.Create(monitorSystemActor, store.CreateParams{
		Slug: "after-backup", Title: "After backup", ProjectUUID: proj, State: "open", Priority: 2,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := api.HistoryTailView(context.Background(), HistoryTailViewParams{})
	if err != nil {
		t.Fatal(err)
	}
	cursor := before.HighWater
	if cursor <= h1 {
		t.Fatalf("write did not advance beyond backup head %d: %d", h1, cursor)
	}
	if err := api.db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, backup, 0600); err != nil {
		t.Fatal(err)
	}
	api = open()
	view := assertTailIdentity(t, api, HistoryTailViewParams{Cursor: cursor}, incarnation, h1, true)
	if view.HighWater != cursor {
		t.Fatalf("restore changed high_water: want %d got %d", cursor, view.HighWater)
	}
}
