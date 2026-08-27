//go:build wrkq_local

package rpccli

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

// seedProjectRoom gives the harness project a project-kind room and returns the
// container's P-xxxxx id — the room KEY a supervisor actually arms
// (`wrkq monitor watch P-00001`), per T-07612 §3.4.
func seedProjectRoom(t *testing.T, dbPath string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(
		`INSERT INTO rooms (kind, container_uuid, opened_by_principal_ref,
		     created_by_principal_ref, updated_by_principal_ref)
		 VALUES ('project', ?, ?, ?, ?)`,
		seedProject, seedActor, seedActor, seedActor,
	); err != nil {
		t.Fatalf("seed project room: %v", err)
	}
	var containerID string
	if err := database.QueryRow("SELECT id FROM containers WHERE uuid = ?", seedProject).Scan(&containerID); err != nil {
		t.Fatalf("read container id: %v", err)
	}
	return containerID
}

// TestMonitorFollowLoop_ClocksBoundAPlainFollow is the T-07621 regression:
// --timeout and --stall-after were only evaluated inside the `condition != ""`
// branch, so `wrkq monitor watch <selector> --timeout 3s` accepted both flags and
// then followed forever. Both clocks must bound a follow that named no --until,
// on a room selector and on a task selector alike — a room selector has no other
// in-CLI bound at all, because --until terminal refuses room selectors.
//
// Exit code is 0 on this path (mable's ruling): the caller asked for a bounded
// follow and got exactly that, and nothing was unmet.
func TestMonitorFollowLoop_ClocksBoundAPlainFollow(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	roomKey := seedProjectRoom(t, dbPath)
	tr := inProcessTransport(t, dbPath)

	const clock = 250 * time.Millisecond
	cases := []struct {
		name     string
		selector string
		opts     monitorStreamOpts
		want     monitorTerminalResult
	}{
		{"task selector honours --timeout", taskID, monitorStreamOpts{timeout: clock}, monitorResultTimeout},
		{"task selector honours --stall-after", taskID, monitorStreamOpts{stallAfter: clock}, monitorResultStall},
		{"room selector honours --timeout", roomKey, monitorStreamOpts{timeout: clock}, monitorResultTimeout},
		{"room selector honours --stall-after", roomKey, monitorStreamOpts{stallAfter: clock}, monitorResultStall},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			opts.scopedTasks = []string{tc.selector}
			opts.fromHighWater = true

			var buf bytes.Buffer
			started := time.Now()
			result, unmet, exitCode, err := monitorFollowLoop(
				context.Background(), tr, json.NewEncoder(&buf), opts,
			)
			elapsed := time.Since(started)

			if err != nil {
				t.Fatalf("monitorFollowLoop: %v", err)
			}
			if result != tc.want {
				t.Errorf("result = %q, want %q", result, tc.want)
			}
			if exitCode != 0 {
				t.Errorf("exit code = %d, want 0 (a bounded follow with no --until ended as asked)", exitCode)
			}
			if len(unmet) != 0 {
				t.Errorf("unmet = %v, want empty (nothing was asked for)", unmet)
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

// TestMonitorFollowLoop_ClocksKeepExitOneWithUnmetCondition pins the other half of
// the ruling: the conditional path is unchanged. A --until that the clock outran is
// still exit 1, with the condition's unmet list on the terminal line, so scripts can
// still tell "condition failed" from "bounded tail ended".
func TestMonitorFollowLoop_ClocksKeepExitOneWithUnmetCondition(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	tr := inProcessTransport(t, dbPath)

	const clock = 250 * time.Millisecond
	cases := []struct {
		name string
		opts monitorStreamOpts
		want monitorTerminalResult
	}{
		{"--until + --timeout", monitorStreamOpts{timeout: clock}, monitorResultTimeout},
		{"--until + --stall-after", monitorStreamOpts{stallAfter: clock}, monitorResultStall},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts := tc.opts
			opts.scopedTasks = []string{taskID}
			// The harness task is completed, so this condition never holds.
			opts.condition = "state=blocked"

			var buf bytes.Buffer
			result, unmet, exitCode, err := monitorFollowLoop(
				context.Background(), tr, json.NewEncoder(&buf), opts,
			)
			if err != nil {
				t.Fatalf("monitorFollowLoop: %v", err)
			}
			if result != tc.want {
				t.Errorf("result = %q, want %q", result, tc.want)
			}
			if exitCode != 1 {
				t.Errorf("exit code = %d, want 1 (--until was left unmet)", exitCode)
			}
			if len(unmet) == 0 {
				t.Errorf("unmet = %v, want the unmet selector list from the condition snapshot", unmet)
			}
		})
	}
}
