package wrkqd

import (
	"net/http"
	"testing"

	"github.com/lherron/wrkq/internal/store"
)

// TestLegacyHTTPContainerViewsExcludeNamedSubtasks pins the legacy list and
// tree routes: a named subtask is not a row of its own in either view; its
// owner stands for it and carries open_subtask_count.
func TestLegacyHTTPContainerViewsExcludeNamedSubtasks(t *testing.T) {
	database, _ := setupTestEnv(t)
	mux := http.NewServeMux()
	(&daemonServer{db: database}).registerRoutes(mux)
	c := &legacyClient{t: t, handler: mux}

	s := store.New(database)
	owner, err := s.Tasks.Create("00000000-0000-0000-0000-000000000001", store.CreateParams{ProjectUUID: "00000000-0000-0000-0000-000000000002", Slug: "owner", Title: "Owner", State: "open", Priority: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Tasks.Create("00000000-0000-0000-0000-000000000001", store.CreateParams{SubtaskOwnerUUID: &owner.UUID, Slug: "preview", Title: "Preview", State: "open", Priority: 3}); err != nil {
		t.Fatal(err)
	}

	requireOnlyOwner := func(view string, rows []interface{}) {
		t.Helper()
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows %v; want only the owner", view, len(rows), rows)
		}
		row := rows[0].(map[string]interface{})
		requireField(t, view, row, "id", owner.ID)
		requireField(t, view, row, "open_subtask_count", float64(1))
	}
	requireOnlyOwner("/v1/tasks/list", listOf(t, c.post("/v1/tasks/list", `{"filter":"all","path_prefix":["inbox"]}`, http.StatusOK), "tasks"))
	requireOnlyOwner("/v1/containers/tree", listOf(t, c.post("/v1/containers/tree", `{"path":"inbox"}`, http.StatusOK), "children"))
}
