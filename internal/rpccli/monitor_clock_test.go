//go:build wrkq_local

package rpccli

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

// TestMonitorWatchClocksBoundAFollowExit0UnlessUntilLeftUnmet is the T-07621
// regression: --timeout and --stall-after were only evaluated inside the
// `condition != ""` branch, so `wrkq monitor watch <selector> --timeout 3s`
// accepted both flags and then followed forever. Both clocks must bound a
// follow that named no --until, on a room selector (the P-xxxxx room key a
// supervisor arms, T-07612 §3.4) and on a task selector alike — a room selector
// has no other in-CLI bound at all, because --until terminal refuses room
// selectors.
//
// A bounded follow with no --until exits 0 (mable's ruling): the caller asked
// for a bounded follow and got exactly that. A --until the clock outran still
// exits 1 with the condition's unmet list on the terminal line, so scripts can
// tell "condition failed" from "bounded tail ended". The real binary runs here
// because monitor exits the process itself on a nonzero result.
func TestMonitorWatchClocksBoundAFollowExit0UnlessUntilLeftUnmet(t *testing.T) {
	if testing.Short() {
		t.Skip("builds production wrkq and wrkqadm binaries; skipped under -short")
	}
	bins := buildProductionBinaries(t)
	// The harness task is completed, so --until state=blocked never holds.
	dir := seedFixture(t, bins, [][]string{
		{"mkdir", "proj"},
		{"touch", "proj/watched", "-t", "Watched", "--state", "completed"},
	})
	roomKey := seedProjectRoomFor(t, filepath.Join(dir, "wrkq.db"), "proj")

	const clock = 250 * time.Millisecond
	for _, tc := range []struct {
		name       string
		args       []string
		wantResult string
		wantExit   int
	}{
		{"task selector honours --timeout", []string{"T-00001", "--timeout", "250ms"}, "timeout", 0},
		{"task selector honours --stall-after", []string{"T-00001", "--stall-after", "250ms"}, "stall", 0},
		{"room selector honours --timeout", []string{roomKey, "--timeout", "250ms"}, "timeout", 0},
		{"room selector honours --stall-after", []string{roomKey, "--stall-after", "250ms"}, "stall", 0},
		{"--until + --timeout left unmet exits 1", []string{"T-00001", "--until", "state=blocked", "--timeout", "250ms"}, "timeout", 1},
		{"--until + --stall-after left unmet exits 1", []string{"T-00001", "--until", "state=blocked", "--stall-after", "250ms"}, "stall", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Now()
			res := runCLI(t, bins.wrkq, dir, append([]string{"monitor", "watch", "--ndjson"}, tc.args...))
			elapsed := time.Since(started)

			if res.exit != tc.wantExit {
				t.Errorf("exit = %d, want %d; stdout=%q stderr=%q", res.exit, tc.wantExit, res.stdout, res.stderr)
			}
			lines := strings.Split(strings.TrimSpace(res.stdout), "\n")
			var terminal struct {
				Result string   `json:"result"`
				Unmet  []string `json:"unmet"`
			}
			if err := json.Unmarshal([]byte(lines[len(lines)-1]), &terminal); err != nil {
				t.Fatalf("decode terminal line %q: %v", lines[len(lines)-1], err)
			}
			if terminal.Result != tc.wantResult {
				t.Errorf("terminal result = %q, want %q (stdout %q)", terminal.Result, tc.wantResult, res.stdout)
			}
			if wantUnmet := tc.wantExit == 1; (len(terminal.Unmet) > 0) != wantUnmet {
				t.Errorf("terminal unmet = %v, want non-empty=%v", terminal.Unmet, wantUnmet)
			}
			if elapsed < clock {
				t.Errorf("returned after %s, before the %s clock expired", elapsed, clock)
			}
			// The whole point of the bug: without the fix this never returns.
			// The ceiling is loose so a slow poll cycle is not a flake.
			if elapsed > 10*time.Second {
				t.Errorf("returned after %s; the clock is not bounding the follow", elapsed)
			}
		})
	}
}

// seedProjectRoomFor gives the project container slug a project-kind room and
// returns the container's P-xxxxx id — the room KEY a supervisor arms.
func seedProjectRoomFor(t *testing.T, dbPath, slug string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var containerUUID, containerID string
	if err := database.QueryRow("SELECT uuid, id FROM containers WHERE slug = ?", slug).Scan(&containerUUID, &containerID); err != nil {
		t.Fatalf("read container %s: %v", slug, err)
	}
	if _, err := database.Exec(
		`INSERT INTO rooms (kind, container_uuid, opened_by_principal_ref,
		     created_by_principal_ref, updated_by_principal_ref)
		 VALUES ('project', ?, ?, ?, ?)`,
		containerUUID, seedActor, seedActor, seedActor,
	); err != nil {
		t.Fatalf("seed project room: %v", err)
	}
	return containerID
}
