//go:build wrkq_local

package rpccli

import (
	"context"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// TestMonitorInitialCursor_NoFlagsStartsAtHighWater is the T-07620 regression on
// the client half: `monitor watch <task>` with neither --since nor --last must
// start at the current high-water so it TAILS what happens next. Starting at
// cursor 0 replayed the entire event_log — thousands of historical rows — before
// reaching the live edge. --since/--last stay explicit overrides.
func TestMonitorInitialCursor_NoFlagsStartsAtHighWater(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	tr := inProcessTransport(t, dbPath)

	// Seed a non-trivial event log so high-water is distinguishable from 0.
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	for i := 0; i < 5; i++ {
		if _, err := database.Exec(
			`INSERT INTO event_log (resource_type, event_type) VALUES ('system', 'test.event')`,
		); err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	var want int64
	if err := database.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM event_log`).Scan(&want); err != nil {
		t.Fatalf("read high-water: %v", err)
	}
	if want == 0 {
		t.Fatal("seeded event log must be non-empty for this assertion to mean anything")
	}

	cases := []struct {
		name string
		opts monitorStreamOpts
		want int64
	}{
		{"no flags follows from high-water", monitorStreamOpts{fromHighWater: true}, want},
		{"--until follows from high-water", monitorStreamOpts{condition: "state=completed"}, want},
		{"--since is an explicit override", monitorStreamOpts{startCursor: 3}, 3},
		{"--since 0 replays the whole log", monitorStreamOpts{startCursor: 0}, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := monitorInitialCursor(context.Background(), tr, tc.opts)
			if err != nil {
				t.Fatalf("monitorInitialCursor: %v", err)
			}
			if got != tc.want {
				t.Errorf("start cursor = %d, want %d", got, tc.want)
			}
		})
	}
}
