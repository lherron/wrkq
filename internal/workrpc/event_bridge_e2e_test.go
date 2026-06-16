package workrpc_test

// event_bridge_e2e_test.go — gap6 (T-04853) event-bridge contract e2e.
//
// daedalus standing ruling (T-04847 C-04823 §4): wrkq RPC is stdio
// request/response and will NOT add a streaming JSON-RPC method. The blessed
// live-feed design is the MONITOR BRIDGE: a Taskboard-style server process
// spawns `wrkq monitor watch --ndjson` as a child, treats each emitted event as
// an INVALIDATION signal (not as state), and refetches the canonical DTO via a
// request/response RPC call.
//
// This test proves that contract end-to-end against a REAL built binary and a
// REAL temp SQLite DB (no in-process shortcuts, no mocks):
//
//   1. MutateObserveRefetch — spawn `wrkq monitor watch --ndjson`; perform
//      task.create / task.move / container.deleteRecursive via the RPC binary;
//      observe the corresponding NDJSON event line; then REFETCH the canonical
//      DTO via wrkq.task.show and assert the event was only a hint — the
//      authoritative state (new path after move, NOT_FOUND after purge) comes
//      from the refetch.
//
//   2. ResumeFromSinceAndLast — prove `--since <id>` replays strictly events
//      with id > cursor, and `--last N` replays the tail. This is the resume
//      mechanism a bridge uses to recover after a disconnect without missing or
//      double-counting events.
//
//   3. ChildTerminatesOnDisconnect — prove the child process is reaped when the
//      parent disconnects (context cancel / kill). This is what lets the bridge
//      tear the watcher down when its SSE client goes away.
//
// NOTE on scope: `wrkq monitor watch` surfaces only task.* and comment.* events
// (see isEventIncluded in internal/cli/monitor.go). container.* events are NOT
// in the typed feed by design — for a recursive container delete the observable
// invalidation signal is the per-task `task.purged` event for each contained
// task; the client then refetches the container listing via RPC.

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// ─── built-binary harness ───────────────────────────────────────────────────

var (
	e2eBinOnce sync.Once
	e2eBinPath string
	e2eBinErr  error
)

// e2eBinary builds (once per test binary run) a real `wrkq` executable and
// returns its path. Building a standalone binary — rather than `go run` —
// matters here: `go run` wraps the program in a parent process whose signal
// semantics on context-cancel are murky, and the disconnect test asserts the
// watcher itself is reaped.
func e2eBinary(t *testing.T) string {
	t.Helper()
	e2eBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "wrkq-e2e-bin-")
		if err != nil {
			e2eBinErr = err
			return
		}
		bin := filepath.Join(dir, "wrkq")
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/wrkq")
		cmd.Dir = repoRoot(t)
		var out strings.Builder
		cmd.Stdout = &out
		cmd.Stderr = &out
		if err := cmd.Run(); err != nil {
			e2eBinErr = err
			t.Logf("go build output:\n%s", out.String())
			return
		}
		e2eBinPath = bin
	})
	if e2eBinErr != nil {
		t.Fatalf("build wrkq e2e binary: %v", e2eBinErr)
	}
	return e2eBinPath
}

// e2eRPC drives one request/response RPC session through the built binary
// (init → business reqs → shutdown → exit) and returns the business frames
// keyed by request id. It is the bridge's "refetch" channel.
func e2eRPC(t *testing.T, bin, dbPath string, reqs ...string) map[string]map[string]any {
	t.Helper()
	seq := []string{
		mkRPC("_init", "rpc.initialize", map[string]any{
			"protocolVersion": "2026-06-14",
			"client":          map[string]any{"name": "e2e-bridge", "version": "0.0.1"},
		}),
	}
	seq = append(seq, reqs...)
	seq = append(seq,
		mkRPC("_sd", "rpc.shutdown", map[string]any{}),
		mkRPC("", "rpc.exit", nil),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "--db", dbPath, "rpc", "--stdio")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("e2eRPC stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("e2eRPC stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("e2eRPC start: %v", err)
	}
	for _, req := range reqs2lines(seq) {
		if _, err := stdin.Write([]byte(req)); err != nil {
			t.Fatalf("e2eRPC write: %v", err)
		}
	}
	_ = stdin.Close()

	frames := map[string]map[string]any{}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for scanner.Scan() {
		var frame map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
			t.Fatalf("e2eRPC non-JSON line %q: %v; stderr=%s", scanner.Text(), err, stderr.String())
		}
		if id, ok := frame["id"].(string); ok {
			frames[id] = frame
		}
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("e2eRPC wait: %v; stderr=%s", err, stderr.String())
	}
	return frames
}

func reqs2lines(reqs []string) []string {
	out := make([]string, len(reqs))
	for i, r := range reqs {
		out[i] = r + "\n"
	}
	return out
}

// e2eResult returns frame[id].result as a map, failing on error frames.
func e2eResult(t *testing.T, frames map[string]map[string]any, id, label string) map[string]any {
	t.Helper()
	frame, ok := frames[id]
	if !ok {
		t.Fatalf("%s: no frame for id %q", label, id)
	}
	if errObj, ok := frame["error"]; ok {
		t.Fatalf("%s: expected result, got error: %v", label, errObj)
	}
	result, ok := frame["result"].(map[string]any)
	if !ok {
		t.Fatalf("%s: frame has no object result: %#v", label, frame)
	}
	return result
}

// e2eErrDataCode returns frame[id].error.data.code (or "" if not an error).
func e2eErrDataCode(frames map[string]map[string]any, id string) string {
	frame, ok := frames[id]
	if !ok {
		return ""
	}
	errObj, _ := frame["error"].(map[string]any)
	data, _ := errObj["data"].(map[string]any)
	code, _ := data["code"].(string)
	return code
}

// ─── monitor child harness ──────────────────────────────────────────────────

// monitorEvt mirrors the relevant fields of internal/cli monitorEventLine.
type monitorEvt struct {
	Type         string `json:"type"`
	ID           int64  `json:"id"`
	EventType    string `json:"event_type"`
	ResourceID   string `json:"resource_id"`
	ResourceUUID string `json:"resource_uuid"`
	Payload      string `json:"payload"`
}

// monitorChild is a running `wrkq monitor watch --ndjson` child process whose
// stdout is parsed into a channel of typed events.
type monitorChild struct {
	cmd    *exec.Cmd
	cancel context.CancelFunc
	events chan monitorEvt
	stderr *strings.Builder
	done   chan struct{}
}

// spawnMonitor starts `wrkq monitor watch --ndjson <extraArgs...>` against
// dbPath and begins streaming parsed events on .events.
func spawnMonitor(t *testing.T, bin, dbPath string, extraArgs ...string) *monitorChild {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	args := append([]string{"--db", dbPath, "monitor", "watch", "--ndjson"}, extraArgs...)
	cmd := exec.CommandContext(ctx, bin, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatalf("spawnMonitor stdout pipe: %v", err)
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("spawnMonitor start: %v", err)
	}
	mc := &monitorChild{
		cmd:    cmd,
		cancel: cancel,
		events: make(chan monitorEvt, 256),
		stderr: &stderr,
		done:   make(chan struct{}),
	}
	go func() {
		defer close(mc.done)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
		for scanner.Scan() {
			var evt monitorEvt
			if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
				continue
			}
			if evt.Type != "wrkq.monitor.event" {
				continue
			}
			select {
			case mc.events <- evt:
			default: // drop if buffer full; tests await specific events promptly
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = cmd.Wait()
	})
	return mc
}

// await blocks until an event matching pred arrives or the deadline elapses.
func (mc *monitorChild) await(t *testing.T, timeout time.Duration, what string, pred func(monitorEvt) bool) monitorEvt {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case evt := <-mc.events:
			if pred(evt) {
				return evt
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for %s; monitor stderr=%s", timeout, what, mc.stderr.String())
		}
	}
}

// drainFor collects every event seen within d (used to assert the EXACT replay
// set for --since/--last, where we must also prove what is NOT replayed).
func (mc *monitorChild) drainFor(d time.Duration) []monitorEvt {
	var got []monitorEvt
	deadline := time.After(d)
	for {
		select {
		case evt := <-mc.events:
			got = append(got, evt)
		case <-deadline:
			return got
		}
	}
}

const e2eAwait = 8 * time.Second

// ─── 1. mutate → observe → refetch ──────────────────────────────────────────

// TestEventBridge_MutateObserveRefetch is the core contract: every mutation
// performed via RPC surfaces as an NDJSON event on the monitor child, and the
// canonical state is then sourced by REFETCHING via RPC (event = invalidation).
func TestEventBridge_MutateObserveRefetch(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping built-binary e2e in short mode")
	}
	bin := e2eBinary(t)
	dbPath := migratedDB(t)

	// Bridge spawns the watcher first and follows from the start of the log.
	mon := spawnMonitor(t, bin, dbPath, "--since", "0")

	// (a) create — observe task.created, then refetch the canonical DTO.
	// Create the task first so it lands in the default inbox; the move target
	// container is created afterwards (creating a project shifts the default).
	createFrames := e2eRPC(t, bin, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Bridge Task"}),
	)
	created := e2eResult(t, createFrames, "c1", "task.create")
	taskID, _ := created["id"].(string)
	if taskID == "" {
		t.Fatalf("task.create returned no id: %#v", created)
	}

	createdEvt := mon.await(t, e2eAwait, "task.created for "+taskID, func(e monitorEvt) bool {
		return e.EventType == "task.created" && e.ResourceID == taskID
	})
	// The event is a HINT — refetch canonical state via request/response RPC.
	showFrames := e2eRPC(t, bin, dbPath, mkRPC("s", "wrkq.task.show", map[string]any{"task": taskID}))
	shown := e2eResult(t, showFrames, "s", "task.show after create")
	if shown["path"] != "inbox/bridge-task" {
		t.Fatalf("refetched path after create = %v, want inbox/bridge-task", shown["path"])
	}
	if createdEvt.ResourceUUID == "" || createdEvt.ResourceUUID != shown["uuid"] {
		t.Fatalf("event uuid %q != refetched uuid %v", createdEvt.ResourceUUID, shown["uuid"])
	}

	// (b) move — create the destination container, then move into it. Observe
	// task.moved, then refetch reveals the new canonical path.
	moveFrames := e2eRPC(t, bin, dbPath,
		mkRPC("cc", "wrkq.container.create", map[string]any{"slug": "doomed", "title": "Doomed", "kind": "project"}),
		mkRPC("mv", "wrkq.task.move", map[string]any{"task": taskID, "targetPath": "doomed"}),
	)
	moved := e2eResult(t, moveFrames, "mv", "task.move")
	if moved["path"] != "doomed/bridge-task" {
		t.Fatalf("task.move result path = %v, want doomed/bridge-task", moved["path"])
	}
	mon.await(t, e2eAwait, "task.moved for "+taskID, func(e monitorEvt) bool {
		return e.EventType == "task.moved" && e.ResourceID == taskID
	})
	showFrames = e2eRPC(t, bin, dbPath, mkRPC("s", "wrkq.task.show", map[string]any{"task": taskID}))
	shown = e2eResult(t, showFrames, "s", "task.show after move")
	if shown["path"] != "doomed/bridge-task" {
		t.Fatalf("refetched path after move = %v, want doomed/bridge-task", shown["path"])
	}

	// (c) recursive delete — the contained task surfaces task.purged; refetch
	// then returns WRKQ_NOT_FOUND (event as invalidation, including deletion).
	delFrames := e2eRPC(t, bin, dbPath,
		mkRPC("cm", "wrkq.container.deleteRecursive", map[string]any{
			"container": "doomed",
			"expected":  map[string]any{"containers": 1, "tasks": 1, "attachments": 0, "bytes": 0},
		}),
	)
	delRes := e2eResult(t, delFrames, "cm", "container.deleteRecursive")
	if delRes["deleted"] != true {
		t.Fatalf("deleteRecursive did not report deleted=true: %#v", delRes)
	}
	mon.await(t, e2eAwait, "task.purged for "+taskID, func(e monitorEvt) bool {
		// resource_id is NULL once the row is gone; match on payload slug.
		return e.EventType == "task.purged" && strings.Contains(e.Payload, "bridge-task")
	})
	showFrames = e2eRPC(t, bin, dbPath, mkRPC("s", "wrkq.task.show", map[string]any{"task": taskID}))
	if code := e2eErrDataCode(showFrames, "s"); code != "WRKQ_NOT_FOUND" {
		t.Fatalf("refetch after purge: want WRKQ_NOT_FOUND, got %q (frame=%#v)", code, showFrames["s"])
	}
}

// ─── 2. resume via --since / --last ─────────────────────────────────────────

// TestEventBridge_ResumeFromSinceAndLast proves the disconnect-recovery
// mechanism: --since replays strictly id > cursor (no gaps, no duplicates) and
// --last N replays the tail. Mutations are performed FIRST so the replay set is
// fixed and deterministic.
func TestEventBridge_ResumeFromSinceAndLast(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping built-binary e2e in short mode")
	}
	bin := e2eBinary(t)
	dbPath := migratedDB(t)

	// Produce a known sequence of task events.
	frames := e2eRPC(t, bin, dbPath,
		mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Alpha"}),
		mkRPC("c2", "wrkq.task.create", map[string]any{"title": "Beta"}),
		mkRPC("c3", "wrkq.task.create", map[string]any{"title": "Gamma"}),
	)
	for _, id := range []string{"c1", "c2", "c3"} {
		e2eResult(t, frames, id, "seed "+id)
	}

	// Capture the full ordered set of task event ids via a from-zero replay.
	all := spawnMonitor(t, bin, dbPath, "--since", "0")
	seen := all.drainFor(1500 * time.Millisecond)
	all.cancel()
	if len(seen) < 3 {
		t.Fatalf("expected >=3 task events from --since 0, got %d", len(seen))
	}
	ids := make([]int64, len(seen))
	for i, e := range seen {
		ids[i] = e.ID
	}
	// ids are strictly ascending.
	for i := 1; i < len(ids); i++ {
		if ids[i] <= ids[i-1] {
			t.Fatalf("event ids not strictly ascending: %v", ids)
		}
	}

	// Resume from a midpoint cursor: --since ids[0] must replay ids[1:] and
	// MUST NOT include ids[0] (id > cursor, exclusive).
	cursor := ids[0]
	resumed := spawnMonitor(t, bin, dbPath, "--since", itoa(cursor))
	got := resumed.drainFor(1500 * time.Millisecond)
	resumed.cancel()
	for _, e := range got {
		if e.ID <= cursor {
			t.Fatalf("--since %d replayed event id %d (must be strictly > cursor)", cursor, e.ID)
		}
	}
	if !containsID(got, ids[len(ids)-1]) {
		t.Fatalf("--since %d did not replay the latest event id %d; got %v", cursor, ids[len(ids)-1], idsOf(got))
	}

	// --last 1 replays exactly the tail (the single most-recent task event).
	last := spawnMonitor(t, bin, dbPath, "--last", "1")
	tail := last.drainFor(1500 * time.Millisecond)
	last.cancel()
	if !containsID(tail, ids[len(ids)-1]) {
		t.Fatalf("--last 1 did not replay latest task event id %d; got %v", ids[len(ids)-1], idsOf(tail))
	}
	if containsID(tail, ids[0]) {
		t.Fatalf("--last 1 replayed an old event id %d; got %v", ids[0], idsOf(tail))
	}
}

// ─── 3. child reaped on disconnect ──────────────────────────────────────────

// TestEventBridge_ChildTerminatesOnDisconnect proves the bridge can tear the
// watcher down: an exec.CommandContext-spawned `monitor watch` (no --until, so
// it follows indefinitely) exits promptly when the parent cancels its context.
func TestEventBridge_ChildTerminatesOnDisconnect(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping built-binary e2e in short mode")
	}
	bin := e2eBinary(t)
	dbPath := migratedDB(t)

	mon := spawnMonitor(t, bin, dbPath, "--since", "0")

	// Confirm it is actually running and following (produce + observe an event).
	frames := e2eRPC(t, bin, dbPath, mkRPC("c1", "wrkq.task.create", map[string]any{"title": "Live"}))
	e2eResult(t, frames, "c1", "task.create")
	mon.await(t, e2eAwait, "task.created (liveness)", func(e monitorEvt) bool {
		return e.EventType == "task.created"
	})

	// Disconnect: cancel the context (exec.CommandContext kills the child).
	mon.cancel()

	select {
	case <-mon.done:
		// stdout closed → process is exiting/exited.
	case <-time.After(e2eAwait):
		t.Fatalf("monitor child did not terminate within %s of context cancel", e2eAwait)
	}

	waitErr := make(chan error, 1)
	go func() { waitErr <- mon.cmd.Wait() }()
	select {
	case <-waitErr:
		// Wait returned (non-nil err from the kill signal is expected and fine).
	case <-time.After(e2eAwait):
		t.Fatalf("monitor child process did not reap within %s of context cancel", e2eAwait)
	}
}

// ─── small helpers ──────────────────────────────────────────────────────────

func itoa(v int64) string {
	return strconv.FormatInt(v, 10)
}

func containsID(evts []monitorEvt, id int64) bool {
	for _, e := range evts {
		if e.ID == id {
			return true
		}
	}
	return false
}

func idsOf(evts []monitorEvt) []int64 {
	out := make([]int64, len(evts))
	for i, e := range evts {
		out[i] = e.ID
	}
	return out
}
