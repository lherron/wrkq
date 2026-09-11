//go:build wrkq_local

package rpccli

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

// An empty window used to be SILENT on both wrkp log paths: the bounded read
// exited 0 with no output at all, and --follow simply waited forever. That is
// how `wrkp log --since 3h --follow` on a project whose newest entry was ten
// hours old came to be reported as a performance regression -- the command was
// correct and fast, and said so in no way.
//
// Both tests assert the NOTICE, on stderr, with the newest-entry fact that
// distinguishes "your window is empty" from "the timeline is dead". Removing
// either notice block in newWrkpLogCmd fails them.

// seedAgedTimeline puts one real timeline entry in the seed project and ages
// every event far past any plausible --since floor, so the window under test is
// empty while the timeline itself is not.
func seedAgedTimeline(t *testing.T, dbPath, taskID string) {
	t.Helper()
	if out, err := runCampaignCLI(t, dbPath, "comment", "add", taskID, "-m", "an aged entry"); err != nil {
		t.Fatalf("seed comment: %v (%s)", err, out)
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if _, err := database.Exec(`UPDATE event_log SET timestamp = '2020-01-01T00:00:00Z'`); err != nil {
		t.Fatalf("age the ledger: %v", err)
	}
}

func TestWrkpLogNamesAnEmptyWindowInsteadOfExitingSilently(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	seedAgedTimeline(t, dbPath, taskID)
	// The stamp renders in the READER's zone, so pin one to assert on.
	t.Setenv("TZ", "UTC")

	cmd := NewWrkpRootCmd()
	cmd.SetArgs([]string{"--db", dbPath, "--principal-ref", "agent:wrkp-test",
		"log", "rpccli-test-proj", "--since", "1h", "--pretty"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("wrkp log: %v (stderr=%q)", err, stderr.String())
	}

	if got := strings.TrimSpace(stdout.String()); got != "" {
		t.Fatalf("an empty window delivers no entries, so stdout must stay clean: %q", got)
	}
	notice := stderr.String()
	for _, want := range []string{"no entries", "since 1h", "newest is", "2020-01-01 00:00 UTC"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("the empty-window notice must carry %q; got %q", want, notice)
		}
	}
}

// A tail says it ONCE, and only once it has caught up: a cursor that is still
// advancing means a sparse page of excluded rows is being walked, not that the
// window is empty. The notice is therefore never printed on the opening page.
func TestWrkpLogFollowSaysItIsWaitingOnAnEmptyWindow(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	seedAgedTimeline(t, dbPath, taskID)

	cmd := NewWrkpRootCmd()
	cmd.SetArgs([]string{"--db", dbPath, "--principal-ref", "agent:wrkp-test",
		"log", "rpccli-test-proj", "--since", "1h", "--follow", "--pretty"})
	var stdout bytes.Buffer
	stderr := &lockedBuffer{}
	cmd.SetOut(&stdout)
	cmd.SetErr(stderr)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.ExecuteContext(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for stderr.String() == "" && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	notice := stderr.String()
	// Give the tail several more polls: the notice must not repeat itself.
	time.Sleep(500 * time.Millisecond)
	repeated := stderr.String()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wrkp log --follow: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wrkp log --follow did not exit on a cancelled context")
	}

	for _, want := range []string{"no entries", "since 1h", "newest is", "following for new ones"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("a waiting tail must say %q; got %q", want, notice)
		}
	}
	if strings.Count(repeated, "no entries") != 1 {
		t.Fatalf("the waiting notice must print once, not once per poll: %q", repeated)
	}
	if got := strings.TrimSpace(stdout.String()); got != "" {
		t.Fatalf("nothing is deliverable, so stdout must stay clean: %q", got)
	}
}

// lockedBuffer lets the test read the notice while the tail goroutine is still
// writing to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
