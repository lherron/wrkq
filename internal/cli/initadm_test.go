package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUpdateGitignoreAddsRelativeDBPath(t *testing.T) {
	withTempCwd(t, func() {
		if err := updateGitignore(".wrkq/wrkq.db"); err != nil {
			t.Fatalf("updateGitignore: %v", err)
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

func TestUpdateGitignoreSkipsAbsoluteDBPath(t *testing.T) {
	withTempCwd(t, func() {
		const original = "# existing\n"
		if err := os.WriteFile(".gitignore", []byte(original), 0o644); err != nil {
			t.Fatalf("write .gitignore: %v", err)
		}
		if err := updateGitignore(filepath.Join(t.TempDir(), "wrkq.db")); err != nil {
			t.Fatalf("updateGitignore: %v", err)
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
