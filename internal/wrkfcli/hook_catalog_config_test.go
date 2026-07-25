package wrkfcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func explicitEmptyHookCatalog(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "empty-hook-catalog.json")
	if err := os.WriteFile(path, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHookCatalogHelpRequiresExplicitLocalConfiguration(t *testing.T) {
	usage := rootCmd.PersistentFlags().Lookup("hook-catalog").Usage
	if strings.Contains(strings.ToLower(usage), "autodiscover") {
		t.Fatalf("hook catalog help still advertises autodiscovery: %q", usage)
	}
	if !strings.Contains(usage, "required in local mode") {
		t.Fatalf("hook catalog help=%q, want explicit local requirement", usage)
	}
}

func TestLocalCLIRequiresExplicitHookCatalog(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	roomDir := filepath.Join(dir, ".wrkq", "wrkf-room")
	if err := os.MkdirAll(roomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	roomCatalog := filepath.Join(roomDir, "hook-catalog.json")
	if err := os.WriteFile(roomCatalog, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{"contamination":{"kind":"exec","argv":["true"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WRKF_HOOK_CATALOG", "")

	oldDB, oldHook := flagDB, flagHookCatalog
	t.Cleanup(func() {
		flagDB, flagHookCatalog = oldDB, oldHook
	})
	flagDB = dbPath
	flagHookCatalog = ""

	_, _, _, err = openConfiguredTransport(nil)
	if err == nil || !strings.Contains(err.Error(), "set --hook-catalog PATH or WRKF_HOOK_CATALOG=PATH") {
		t.Fatalf("missing catalog error=%v, want actionable explicit-configuration refusal", err)
	}

	flagHookCatalog = roomCatalog
	tr, _, closeTransport, err := openConfiguredTransport(nil)
	if err != nil {
		t.Fatalf("explicit catalog: %v", err)
	}
	defer closeTransport()
	raw, err := tr.Call(t.Context(), "wrkf.hook.list", map[string]any{})
	if err != nil {
		t.Fatalf("hook.list: %v", err)
	}
	if !strings.Contains(string(raw), `"contamination"`) {
		t.Fatalf("hook.list=%s, want explicitly selected catalog", raw)
	}
}
