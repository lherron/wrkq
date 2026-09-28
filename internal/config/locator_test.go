package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyDBLocatorRemote(t *testing.T) {
	cfg := &Config{}
	if err := ApplyDBLocator(cfg, "rpc://max3", false); err != nil {
		t.Fatalf("ApplyDBLocator remote: %v", err)
	}
	if cfg.DBPath != "" {
		t.Fatalf("DBPath=%q want empty for remote locator", cfg.DBPath)
	}
	if cfg.RemoteEndpoint != "max3:7171" {
		t.Fatalf("RemoteEndpoint=%q want max3:7171", cfg.RemoteEndpoint)
	}
	if cfg.DBLocator != "rpc://max3" {
		t.Fatalf("DBLocator=%q want rpc://max3", cfg.DBLocator)
	}
}

func TestApplyDBLocatorPathOnlyRejectsRemote(t *testing.T) {
	cfg := &Config{}
	if err := ApplyDBLocator(cfg, "rpc://max3:7171", true); err == nil {
		t.Fatal("expected path-only remote rejection")
	}
}

func TestApplyDBLocatorLocalPath(t *testing.T) {
	cfg := &Config{}
	if err := ApplyDBLocator(cfg, "/tmp/wrkq.db", false); err != nil {
		t.Fatalf("ApplyDBLocator local: %v", err)
	}
	if cfg.DBPath != "/tmp/wrkq.db" || cfg.DBLocator != "/tmp/wrkq.db" || cfg.RemoteEndpoint != "" {
		t.Fatalf("local locator mismatch: %#v", cfg)
	}
}

func TestLoadWRKQDBRemoteBeatingDBPathIsRefused(t *testing.T) {
	isolateLoadConfig(t)
	t.Setenv("WRKQ_DB", "rpc://max3:7171")
	t.Setenv("WRKQ_DB_PATH", "rpc://stale:7171")

	_, err := Load()
	var conflict *DBEnvConflictError
	if !errors.As(err, &conflict) {
		t.Fatalf("Load err=%v, want *DBEnvConflictError", err)
	}
}

// TestLoadDBPathConflictRule pins what "WRKQ_DB_PATH loses" means (T-08302):
// it is honoured when nothing else selects a database or the winner names the
// same file; otherwise Load refuses, and an explicit --db override moots it.
func TestLoadDBPathConflictRule(t *testing.T) {
	tmp := isolateLoadConfig(t)
	scratch := filepath.Join(tmp, "scratch.db")
	other := filepath.Join(tmp, "other.db")

	t.Run("honoured alone", func(t *testing.T) {
		t.Setenv("WRKQ_DB_PATH", scratch)
		cfg, err := Load()
		if err != nil || cfg.DBPath != scratch || cfg.RemoteEndpoint != "" {
			t.Fatalf("cfg=%#v err=%v, want DBPath=%s", cfg, err, scratch)
		}
	})
	t.Run("same path as WRKQ_DB is honoured", func(t *testing.T) {
		t.Setenv("WRKQ_DB", scratch)
		t.Setenv("WRKQ_DB_PATH", filepath.Join(tmp, ".", "scratch.db"))
		cfg, err := Load()
		if err != nil || cfg.DBPath != scratch {
			t.Fatalf("cfg=%#v err=%v, want DBPath=%s", cfg, err, scratch)
		}
	})
	for name, winner := range map[string]string{"remote winner": "rpc://mini", "local winner": other} {
		t.Run(name+" refuses naming WRKQ_DB", func(t *testing.T) {
			t.Setenv("WRKQ_DB", winner)
			t.Setenv("WRKQ_DB_PATH", scratch)
			_, err := Load()
			var conflict *DBEnvConflictError
			if !errors.As(err, &conflict) {
				t.Fatalf("Load err=%v, want *DBEnvConflictError", err)
			}
			if conflict.Value != scratch || conflict.Locator != winner || conflict.Source != "WRKQ_DB" {
				t.Fatalf("conflict=%#v", conflict)
			}
			if !strings.Contains(err.Error(), "WRKQ_DB="+scratch) {
				t.Fatalf("error %q does not name the WRKQ_DB lever", err)
			}
		})
		t.Run(name+" mooted by --db", func(t *testing.T) {
			t.Setenv("WRKQ_DB", winner)
			t.Setenv("WRKQ_DB_PATH", scratch)
			flag := filepath.Join(tmp, "flag.db")
			cfg, err := LoadWithDBOverride(flag, false)
			if err != nil || cfg.DBPath != flag || cfg.RemoteEndpoint != "" {
				t.Fatalf("cfg=%#v err=%v, want DBPath=%s", cfg, err, flag)
			}
		})
	}
	t.Run("WRKQ_DB_PATH_FILE losing refuses", func(t *testing.T) {
		pathFile := filepath.Join(tmp, "db-path")
		if err := os.WriteFile(pathFile, []byte(scratch+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("WRKQ_DB", "rpc://mini")
		t.Setenv("WRKQ_DB_PATH_FILE", pathFile)
		_, err := Load()
		var conflict *DBEnvConflictError
		if !errors.As(err, &conflict) || conflict.Var != "WRKQ_DB_PATH_FILE" || conflict.Value != scratch {
			t.Fatalf("Load err=%v conflict=%#v", err, conflict)
		}
	})
	t.Run("WRKQ_DB from .env.local names its source", func(t *testing.T) {
		envLocal := filepath.Join(tmp, ".env.local")
		if err := os.WriteFile(envLocal, []byte("WRKQ_DB=rpc://mini\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Remove(envLocal) })
		unsetEnv(t, "WRKQ_DB")
		t.Setenv("WRKQ_DB_PATH", scratch)
		_, err := Load()
		var conflict *DBEnvConflictError
		if !errors.As(err, &conflict) || !strings.Contains(conflict.Source, ".env.local") {
			t.Fatalf("Load err=%v conflict=%#v", err, conflict)
		}
	})
}

func TestLoadRemoteDBPathWithoutWRKQDBIsRejected(t *testing.T) {
	t.Setenv("WRKQ_DB", "")
	t.Setenv("WRKQ_DB_PATH", "rpc://stale:7171")
	t.Setenv("WRKQ_DB_PATH_FILE", "")

	if _, err := Load(); err == nil {
		t.Fatal("expected path-only WRKQ_DB_PATH rejection")
	}
}

func TestLoadWithoutDBPathDoesNotDefaultToHomeDB(t *testing.T) {
	isolateLoadConfig(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != "" {
		t.Fatalf("DBPath=%q want empty without env, config, --db, or .wrkq/wrkq.db", cfg.DBPath)
	}
	if cfg.DBLocator != "" {
		t.Fatalf("DBLocator=%q want empty without configured database", cfg.DBLocator)
	}
	if cfg.AttachDir != "" {
		t.Fatalf("AttachDir=%q want empty without project-local DB or explicit attach dir", cfg.AttachDir)
	}
	if _, err := os.Stat(filepath.Join(os.Getenv("HOME"), ".local", "share", "wrkq", "wrkq.db")); !os.IsNotExist(err) {
		t.Fatalf("home fallback DB should not exist, stat err=%v", err)
	}
}

func TestLoadProjectLocalDBDefaultStillWorks(t *testing.T) {
	cwd := isolateLoadConfig(t)
	if err := os.MkdirAll(filepath.Join(cwd, ".wrkq"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".wrkq", "wrkq.db"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != ".wrkq/wrkq.db" {
		t.Fatalf("DBPath=%q want project-local default", cfg.DBPath)
	}
	if cfg.DBLocator != ".wrkq/wrkq.db" {
		t.Fatalf("DBLocator=%q want project-local default", cfg.DBLocator)
	}
	if cfg.AttachDir != ".wrkq/attachments" {
		t.Fatalf("AttachDir=%q want project-local attachment default", cfg.AttachDir)
	}
}

func TestLoadHomePlatformDBDefaultFromUnrelatedDirectory(t *testing.T) {
	home := t.TempDir()
	platformRoot := filepath.Join(home, "praesidium")
	if err := os.MkdirAll(platformRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(platformRoot, "var", "db", "wrkq.db")
	if err := os.WriteFile(
		filepath.Join(platformRoot, ".env.local"),
		[]byte("WRKQ_DB_PATH="+dbPath+"\n"),
		0o644,
	); err != nil {
		t.Fatal(err)
	}

	unrelated := t.TempDir()
	for _, key := range []string{"WRKQ_DB", "WRKQ_DB_PATH", "WRKQ_DB_PATH_FILE"} {
		unsetEnv(t, key)
	}
	t.Setenv("HOME", home)
	t.Setenv("PRAESIDIUM_HOME", "")
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(unrelated); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldCwd) })

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DBPath != dbPath {
		t.Fatalf("DBPath=%q want platform default %q", cfg.DBPath, dbPath)
	}
	if cfg.DBLocator != dbPath {
		t.Fatalf("DBLocator=%q want platform default %q", cfg.DBLocator, dbPath)
	}
}

func unsetEnv(t *testing.T, key string) {
	t.Helper()
	old, existed := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func isolateLoadConfig(t *testing.T) string {
	t.Helper()
	tmp := t.TempDir()
	for _, key := range []string{
		"WRKQ_DB",
		"WRKQ_DB_PATH",
		"WRKQ_DB_PATH_FILE",
		"WRKQ_ATTACH_DIR",
		"WRKQ_PROJECT_ROOT",
		"ASP_PROJECT",
		"PRAESIDIUM_HOME",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", tmp)
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})
	return tmp
}
