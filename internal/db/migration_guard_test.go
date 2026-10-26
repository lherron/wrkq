package db

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMigrationGuardLeaseAndAliases(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.db")
	database, err := OpenForMigration(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireServingLease(path); err == nil {
		t.Fatal("daemon startup allowed during migration lease")
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(filepath.Dir(path), "alias.db")
	if err := os.Symlink(path, alias); err != nil {
		t.Fatal(err)
	}
	release, err := AcquireServingLease(alias)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := OpenForMigration(path); err == nil {
		t.Fatal("migration allowed during daemon lease")
	}
	release()
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenForMigration(path); err == nil || !strings.Contains(err.Error(), "DATABASE_HARDLINK_UNSUPPORTED") {
		t.Fatalf("hardlink err=%v", err)
	}
	if _, err := AcquireServingLease(alias); err == nil {
		t.Fatal("hardlinked daemon accepted")
	}
	if _, err := OpenForMigration(path + "?cache=shared"); err == nil {
		t.Fatal("query DSN accepted")
	}
}

func TestReadOnlyMigrationDiagnosticsCannotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "space # diagnostics.db")
	writer, err := OpenForMigration(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	reader, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reader.Close() }()
	if _, _, err := reader.MigrationStatus(); err != nil {
		t.Fatal(err)
	}
	if _, err := reader.Exec("DELETE FROM schema_migrations"); err == nil {
		t.Fatal("diagnostic connection can write")
	}
	missing := filepath.Join(t.TempDir(), "missing", "scratch.db")
	if reader, err := OpenReadOnly(missing); err == nil {
		_ = reader.Close()
		t.Fatal("missing diagnostic DB accepted")
	}
	if _, err := os.Stat(filepath.Dir(missing)); !os.IsNotExist(err) {
		t.Fatalf("read-only open created directory: %v", err)
	}
}
