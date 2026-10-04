package db

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestMigrationGuardLoadedJobResolution(t *testing.T) {
	print := `gui/501/com.praesidium.wrkq-server = {
 path = /tmp/wrkqd.plist
 arguments = {
 /tmp/wrkqd
 -db=/tmp/job.db
 }
 environment = {
 WRKQ_DB => /tmp/env.db
 }
 }`
	job, err := parseMigrationJob(print)
	if err != nil || job.path != "/tmp/job.db" || job.pid != 0 {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if _, err := parseMigrationJob(strings.ReplaceAll(print, "/tmp/job.db", "relative.db")); err == nil {
		t.Fatal("relative DB without job working directory accepted")
	}
	if _, err := parseMigrationJob(strings.ReplaceAll(print, "-db=/tmp/job.db", "-addr=127.0.0.1:7171")); err != nil {
		t.Fatal(err)
	}
	if _, err := parseMigrationJob("job = {\n pid = 123\n}"); err == nil {
		t.Fatal("unresolved loaded job accepted")
	}
}

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

func TestMigrationGuardLoadedJobWithoutPIDAndInspectionFailure(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "job.db")
	t.Setenv("WRKQ_LAUNCHD_LABEL", "com.praesidium.wrkq-server-test")
	t.Setenv("PATH", dir)
	script := filepath.Join(dir, "launchctl")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(script, []byte("#!/bin/sh\n"+body), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Use shell builtin printf: PATH intentionally has no external helpers.
	write(fmt.Sprintf("printf 'job = {\n arguments = {\n /tmp/wrkqd\n --db\n %s\n }\n}\n'\n", path))
	err := refuseLoadedMigrationJob(path)
	var serving *DatabaseServingError
	if !errors.As(err, &serving) || serving.PID != 0 || serving.Job == "" {
		t.Fatalf("loaded no-PID job: %v", err)
	}
	write("printf 'Could not find service x in domain for user gui' >&2\nexit 113\n")
	if err := refuseLoadedMigrationJob(path); err != nil {
		t.Fatal(err)
	}
	write("printf 'Operation not permitted' >&2\nexit 1\n")
	if err := refuseLoadedMigrationJob(path); err == nil {
		t.Fatal("inspection failure accepted as unloaded")
	}
	write("printf 'job = {\n pid = 123\n}\n'\n")
	if err := refuseLoadedMigrationJob(path); err == nil {
		t.Fatal("unresolved loaded job allowed")
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
