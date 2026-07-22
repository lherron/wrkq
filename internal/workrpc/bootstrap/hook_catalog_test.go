package bootstrap

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
)

func TestDaemonServerNeverAutodiscoversHookCatalog(t *testing.T) {
	dir := t.TempDir()
	database, err := db.Open(filepath.Join(dir, "wrkq.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	autoDir := filepath.Join(dir, ".wrkf")
	if err := os.MkdirAll(autoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(autoDir, "hooks.json")
	if err := os.WriteFile(catalogPath, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{"auto":{"kind":"exec","argv":["true"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("WRKF_HOOK_CATALOG", "")
	api, _, err := DaemonServer(database, &config.Config{DBPath: database.Path()})
	if err != nil {
		t.Fatal(err)
	}
	list, err := api.HookList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Hooks) != 0 {
		t.Fatalf("daemon autodiscovered workspace catalog: %#v", list.Hooks)
	}

	t.Setenv("WRKF_HOOK_CATALOG", catalogPath)
	api, _, err = DaemonServer(database, &config.Config{DBPath: database.Path()})
	if err != nil {
		t.Fatal(err)
	}
	list, err = api.HookList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Hooks) != 1 || list.Hooks[0].ID != "auto" {
		t.Fatalf("explicit daemon catalog hooks=%#v", list.Hooks)
	}
}
