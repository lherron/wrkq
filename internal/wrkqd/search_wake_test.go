package wrkqd

import (
	"context"
	"testing"
	"time"
)

func TestSearchIndexerWakeLoop(t *testing.T) {
	database, _ := setupTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	ticks := make(chan time.Time)
	iterations := make(chan bool, 10)
	release := make(chan bool)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSearchIndexer(ctx, database, ticks, func(context.Context) bool {
			iterations <- true
			select {
			case paused := <-release:
				return paused
			case <-ctx.Done():
				return false
			}
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	await := func() {
		t.Helper()
		select {
		case <-iterations:
		case <-time.After(time.Second):
			t.Fatal("missing index iteration")
		}
	}
	idle := func() {
		t.Helper()
		select {
		case <-iterations:
			t.Fatal("unexpected idle/paused iteration")
		case <-time.After(100 * time.Millisecond):
		}
	}
	await() // Startup catches up without a tick.
	release <- false
	idle()
	database.EventCommits.Notify()
	await() // Commit wakes without any ticker.
	// Commits during indexing are coalesced into a single additional pass.
	for i := 0; i < 1000; i++ {
		database.EventCommits.Notify()
	}
	release <- false
	await()
	release <- false
	idle()
	ticks <- time.Now()
	await()         // External writers are covered by the safety tick.
	release <- true // paused: commits must not reread status.
	database.EventCommits.Notify()
	idle()
	database.SearchResumes.Notify()
	await()
	release <- false
	idle()
}

func TestSearchIndexerNoTwoSecondIdlePoll(t *testing.T) {
	database, _ := setupTestEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	iterations := make(chan struct{}, 10)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runSearchIndexer(ctx, database, nil, func(context.Context) bool { iterations <- struct{}{}; return false })
	}()
	<-iterations
	select {
	case <-iterations:
		t.Fatal("idle iteration without commit or safety tick")
	case <-time.After(2300 * time.Millisecond):
	}
	cancel()
	<-done
}
