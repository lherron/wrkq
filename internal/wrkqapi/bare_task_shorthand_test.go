//go:build wrkq_local

package wrkqapi

import (
	"context"
	"testing"

	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// A bare number names the highest existing task sharing its low digits, so
// "639" keeps reaching current work after the sequence passes T-10000.
func TestBareTaskShorthandPrefersHighestSuffix(t *testing.T) {
	api, s := newMonitorAPI(t)
	project := seedMonitorProject(t, s)
	byID := map[string]string{}
	for _, want := range []string{"T-00639", "T-10639", "T-00498", "T-10498", "T-11498", "T-01498", "T-99998", "T-100098", "T-00007"} {
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
		"498":     "T-11498", // crosses the thousands boundary
		"1498":    "T-11498",
		"0498":    "T-10498",  // leading zeros preserve suffix width
		"10498":   "T-10498",  // five digits stays literal
		"98":      "T-100098", // numeric ordering beyond five digits
		"639":     "T-10639",  // rolled over: the newer block wins
		"0639":    "T-10639",
		"00639":   "T-00639", // five digits is a literal id
		"10639":   "T-10639",
		"7":       "T-00007", // only the old block exists
		"T-00639": "T-00639",
	} {
		uuid, friendly, err := selectors.ResolveTask(api.db, selector)
		if err != nil || friendly != want || uuid != byID[want] {
			t.Errorf("selector %q: got (%s, %s, %v), want %s", selector, uuid, friendly, err, want)
		}
		// wrkq log resolves through the same suffix rule (T-11874).
		if logUUID, kind, err := api.resolveLogResource(context.Background(), selector); err != nil || kind != "task" || logUUID != byID[want] {
			t.Errorf("log selector %q: got (%s, %s, %v), want %s", selector, logUUID, kind, err, want)
		}
	}
	if _, _, err := api.resolveLogResource(context.Background(), "4242"); err == nil || err.Error() != "task not found: 4242" {
		t.Errorf("log missing bare number: %v", err)
	}
	if _, _, err := selectors.ResolveTask(api.db, "4242"); err == nil || err.Error() != "task not found: T-04242" {
		t.Fatalf("missing bare number: %v", err)
	}
}
