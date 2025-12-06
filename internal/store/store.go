// Package store provides a persistence layer that abstracts database operations,
// automatically handling etag management, timestamps, and event logging.
package store

import (
	"database/sql"
	"fmt"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
)

// Store is the root store that provides access to domain-specific stores.
type Store struct {
	db *db.DB

	// Domain-specific stores
	Tasks      *TaskStore
	Containers *ContainerStore
}

// New creates a new Store wrapping the given database connection.
func New(database *db.DB) *Store {
	s := &Store{db: database}
	s.Tasks = &TaskStore{store: s}
	s.Containers = &ContainerStore{store: s}
	return s
}

// DB returns the underlying database connection (for read-only queries).
func (s *Store) DB() *db.DB {
	return s.db
}

// withTx executes fn within a transaction. If fn returns nil, the transaction
// is committed; otherwise it is rolled back.
func (s *Store) withTx(fn func(tx *sql.Tx, ew *events.Writer) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	ew := events.NewWriter(s.db.DB)
	if err := fn(tx, ew); err != nil {
		return err
	}

	return tx.Commit()
}

// checkETag verifies etag matches if ifMatch > 0, returns ETagMismatchError on mismatch.
func checkETag(currentETag, ifMatch int64) error {
	if ifMatch > 0 && currentETag != ifMatch {
		return &domain.ETagMismatchError{Expected: ifMatch, Actual: currentETag}
	}
	return nil
}
