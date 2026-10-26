//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// TestMonitorWatchWithoutSinceTailsFromHighWaterInsteadOfReplaying is the
// T-07620 regression: `monitor watch <task>` with neither --since nor --last
// must start at the current high-water so it TAILS what happens next. Starting
// at cursor 0 replayed the entire event_log — thousands of historical rows —
// before reaching the live edge. --since stays an explicit override.
func TestMonitorWatchWithoutSinceTailsFromHighWaterInsteadOfReplaying(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	for i := 0; i < 5; i++ {
		cmd := NewRootCmdFor("wrkq")
		cmd.SetArgs([]string{"--db", dbPath, "--principal-ref", "agent:monitor-test", "set", taskID, "--description", fmt.Sprintf("edit %d", i)})
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("seed history edit %d: %v\n%s", i, err, out.String())
		}
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	var seeded []int64
	rows, err := database.Query(`SELECT id FROM event_log WHERE resource_uuid = ? ORDER BY id`, seedTaskUUID)
	if err != nil {
		t.Fatalf("read seeded history: %v", err)
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan history id: %v", err)
		}
		seeded = append(seeded, id)
	}
	_ = rows.Close()
	_ = database.Close()
	if len(seeded) < 5 {
		t.Fatalf("seeded %d history events for %s, want at least 5", len(seeded), taskID)
	}

	historyIDs := func(t *testing.T, extra ...string) []int64 {
		t.Helper()
		cmd := NewRootCmdFor("wrkq")
		cmd.SetArgs(append([]string{"--db", dbPath, "monitor", "watch", taskID, "--ndjson", "--stall-after", "300ms"}, extra...))
		var out bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("monitor watch: %v\n%s", err, out.String())
		}
		var ids []int64
		for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
			var ev struct {
				ID        int64  `json:"id"`
				EventType string `json:"event_type"`
			}
			if json.Unmarshal([]byte(line), &ev) == nil && ev.ID > 0 {
				ids = append(ids, ev.ID)
			}
		}
		return ids
	}

	if got := historyIDs(t); len(got) != 0 {
		t.Fatalf("bare watch replayed history ids %v; it must tail from the high-water mark", got)
	}
	if got := historyIDs(t, "--since", "0"); !reflect.DeepEqual(got, seeded) {
		t.Fatalf("--since 0 replayed %v, want all of %v", got, seeded)
	}
	cut := seeded[2]
	if got := historyIDs(t, "--since", jsonNumberString(cut)); !reflect.DeepEqual(got, seeded[3:]) {
		t.Fatalf("--since %d replayed %v, want only the ids after it %v", cut, got, seeded[3:])
	}
}

func jsonNumberString(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
