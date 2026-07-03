package rpccli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
)

func TestMissingDBPathFailsWithoutHomeFallback(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	for _, key := range []string{
		"WRKQ_DB",
		"WRKQ_DB_PATH",
		"WRKQ_DB_PATH_FILE",
		"WRKQ_ATTACH_DIR",
		"WRKQ_PROJECT_ROOT",
		"ASP_PROJECT",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", home)
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldCwd)
	})

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{"ls", "inbox"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	err = cmd.Execute()
	if err == nil {
		t.Fatal("expected missing database path error")
	}
	if !strings.Contains(err.Error(), config.MissingDatabasePathMessage) {
		t.Fatalf("error %q does not name WRKQ_DB_PATH/--db", err.Error())
	}
	if strings.Contains(err.Error(), ".local/share/wrkq") {
		t.Fatalf("error should not reference home fallback path: %q", err.Error())
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout=%q want empty", stdout.String())
	}
	homeDB := filepath.Join(home, ".local", "share", "wrkq", "wrkq.db")
	if _, err := os.Stat(homeDB); !os.IsNotExist(err) {
		t.Fatalf("home fallback DB should not be created, stat err=%v", err)
	}
}
