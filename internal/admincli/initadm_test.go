package admincli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitAppendsRelativeDBPathToGitignore(t *testing.T) {
	withTempCwd(t, func() {
		if out, err := runAdm(t, "init", "--db", ".wrkq/wrkq.db"); err != nil {
			t.Fatalf("wrkqadm init: %v\n%s", err, out)
		}
		data, err := os.ReadFile(".gitignore")
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}
		got := string(data)
		if !strings.Contains(got, "# wrkq database\n.wrkq/wrkq.db\n") {
			t.Fatalf(".gitignore missing relative db path:\n%s", got)
		}
	})
}

func TestInitLeavesGitignoreAloneForAbsoluteDBPath(t *testing.T) {
	withTempCwd(t, func() {
		const original = "# existing\n"
		if err := os.WriteFile(".gitignore", []byte(original), 0o644); err != nil {
			t.Fatalf("write .gitignore: %v", err)
		}
		dbPath := filepath.Join(t.TempDir(), "wrkq.db")
		if out, err := runAdm(t, "init", "--db", dbPath); err != nil {
			t.Fatalf("wrkqadm init: %v\n%s", err, out)
		}
		if _, err := os.Stat(dbPath); err != nil {
			t.Fatalf("init did not create %s: %v", dbPath, err)
		}
		data, err := os.ReadFile(".gitignore")
		if err != nil {
			t.Fatalf("read .gitignore: %v", err)
		}
		if string(data) != original {
			t.Fatalf(".gitignore changed for absolute db path:\n%s", data)
		}
	})
}

func withTempCwd(t *testing.T, fn func()) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := t.TempDir()
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir temp: %v", err)
	}
	defer func() {
		if err := os.Chdir(old); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	}()
	fn()
}
