package db

import (
	"database/sql"
	"sync"
)

// ChangeSignal broadcasts a process-local wake to any number of subscribers.
// Its zero value is ready to use. The sequence is a wake generation, not an
// event_log cursor. Snapshot before reading durable state, then wait on the
// returned channel: a change during the read cannot be lost. Slow subscribers
// coalesce any number of notifications by taking a fresh snapshot.
type ChangeSignal struct {
	mu       sync.Mutex
	sequence uint64
	changed  chan struct{}
}

func (s *ChangeSignal) Snapshot() (uint64, <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed == nil {
		s.changed = make(chan struct{})
	}
	return s.sequence, s.changed
}

func (s *ChangeSignal) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.changed != nil {
		close(s.changed)
	}
	s.changed = make(chan struct{})
	s.sequence++
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
