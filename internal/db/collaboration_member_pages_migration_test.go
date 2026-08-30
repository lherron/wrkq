//go:build wrkq_local

package db

import (
	"path/filepath"
	"testing"
)

func TestCollaborationLedgerIncarnationSurvivesRestartAndChangesOnReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ledger.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("open first ledger: %v", err)
	}
	if err := first.Migrate(); err != nil {
		t.Fatalf("migrate first ledger: %v", err)
	}
	incarnation := readCollaborationIncarnation(t, first)
	if err := first.Close(); err != nil {
		t.Fatalf("close first ledger: %v", err)
	}

	restarted, err := Open(path)
	if err != nil {
		t.Fatalf("reopen ledger: %v", err)
	}
	if err := restarted.Migrate(); err != nil {
		t.Fatalf("migrate reopened ledger: %v", err)
	}
	if got := readCollaborationIncarnation(t, restarted); got != incarnation {
		t.Fatalf("normal restart changed incarnation: %q -> %q", incarnation, got)
	}
	if err := restarted.Close(); err != nil {
		t.Fatalf("close restarted ledger: %v", err)
	}

	replacement, err := Open(filepath.Join(t.TempDir(), "replacement.db"))
	if err != nil {
		t.Fatalf("open replacement ledger: %v", err)
	}
	t.Cleanup(func() { _ = replacement.Close() })
	if err := replacement.Migrate(); err != nil {
		t.Fatalf("migrate replacement ledger: %v", err)
	}
	if got := readCollaborationIncarnation(t, replacement); got == incarnation {
		t.Fatalf("replacement reused incarnation %q", incarnation)
	}
}

func readCollaborationIncarnation(t *testing.T, database *DB) string {
	t.Helper()
	var incarnation string
	if err := database.QueryRow(
		"SELECT incarnation FROM collaboration_ledger_meta WHERE singleton = 1").Scan(&incarnation); err != nil {
		t.Fatalf("read collaboration incarnation: %v", err)
	}
	if len(incarnation) != 32 {
		t.Fatalf("incarnation length = %d, want 32: %q", len(incarnation), incarnation)
	}
	return incarnation
}
