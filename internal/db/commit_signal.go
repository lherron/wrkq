package db

import (
	"database/sql"
	"sync"
	"time"
)

// ChangeSignal broadcasts a process-local wake to any number of subscribers.
// Its zero value is ready to use. The sequence is a wake generation, not an
// event_log cursor. Snapshot before reading durable state, then wait on the
// returned channel: a change during the read cannot be lost. Slow subscribers
// coalesce any number of notifications by taking a fresh snapshot.
type ChangeSignal struct {
	mu           sync.Mutex
	sequence     uint64
	changed      chan struct{}
	coalesced    chan struct{}
	flushPending bool
}

func (s *ChangeSignal) Snapshot() (uint64, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed == nil {
		s.changed = make(chan struct{})
	}
	return s.sequence, s.changed
}

// CoalescedSnapshot shares the commit sequence, but broadcasts at most once
// per 100ms. The sequence advances synchronously at commit, so a token taken
// before a durable read cannot lose a commit while a broadcast is pending.
func (s *ChangeSignal) CoalescedSnapshot() (uint64, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.coalesced == nil {
		s.coalesced = make(chan struct{})
	}
	return s.sequence, s.coalesced
}

func (s *ChangeSignal) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
	s.sequence++
	if s.coalesced != nil && !s.flushPending {
		s.flushPending = true
		time.AfterFunc(100*time.Millisecond, func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			close(s.coalesced)
			s.coalesced = make(chan struct{})
			s.flushPending = false
		})
	}
}

// CommitEvents commits a transaction that may have appended event_log rows.
// Publish only after SQLite makes the commit visible; failed commits and
// rollbacks must never wake readers. Callers without a transaction (autocommit)
// can notify EventCommits directly after a successful write.
func (db *DB) CommitEvents(tx *sql.Tx) error {
	if err := tx.Commit(); err != nil {
		return err
	}
	db.EventCommits.Notify()
	return nil
}
