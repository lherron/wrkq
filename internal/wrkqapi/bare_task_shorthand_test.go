//go:build wrkq_local

package wrkqapi

import (
	"testing"

	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// A bare number names the highest existing task sharing its low digits, so
// "639" keeps reaching current work after the sequence passes T-10000.
func TestBareTaskShorthandPrefersHighestBlock(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	byID := map[string]string{}
	for _, want := range []string{"T-00639", "T-10639", "T-00007"} {
		task, err := s.Tasks.Create(monitorSystemActor, store.CreateParams{Slug: "t" + want[2:], Title: want, ProjectUUID: project, State: "open", Priority: 3})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := api.db.Exec("UPDATE tasks SET id = ? WHERE uuid = ?", want, task.UUID); err != nil {
			t.Fatal(err)
		}
		byID[want] = task.UUID
	}
	for selector, want := range map[string]string{
		"639":     "T-10639", // rolled over: the newer block wins
		"0639":    "T-10639",
		"00639":   "T-00639", // five digits is a literal id
		"10639":   "T-10639",
		"7":       "T-00007", // only the old block exists
		"T-00639": "T-00639",
	} {
		uuid, friendly, err := selectors.ResolveTask(api.db, selector)
		if err != nil || friendly != want || uuid != byID[want] {
			t.Fatalf("selector %q: got (%s, %s, %v), want %s", selector, uuid, friendly, err, want)
		}
	}
	if _, _, err := selectors.ResolveTask(api.db, "4242"); err == nil || err.Error() != "task not found: T-04242" {
		t.Fatalf("missing bare number: %v", err)
	}
}
