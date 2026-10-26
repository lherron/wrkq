//go:build wrkq_local

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

// Local wrkf never autodiscovers a room's .wrkq/wrkf-room/hook-catalog.json:
// without --hook-catalog/WRKF_HOOK_CATALOG it refuses, and an explicit
// --hook-catalog is the catalog it serves.
func TestLocalWrkfWithoutHookCatalogRefusesAndIgnoresRoomCatalog(t *testing.T) {
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
	principal := []string{"WRKF_PRINCIPAL_REF=agent:hook-catalog-test"}

	refused := runWrkfIn(t, dir, "", principal, "--db", dbPath, "hook", "list", "--json")
	requireWrkfRefusal(t, refused, "set --hook-catalog PATH or WRKF_HOOK_CATALOG=PATH")
	blankEnv := runWrkfIn(t, dir, "", append([]string{"WRKF_HOOK_CATALOG=  "}, principal...), "--db", dbPath, "hook", "list", "--json")
	requireWrkfRefusal(t, blankEnv, "set --hook-catalog PATH or WRKF_HOOK_CATALOG=PATH")

	listsRoomCatalog := func(label string, res wrkfResult) {
		t.Helper()
		if res.exitCode != 0 {
			t.Fatalf("%s exit %d: %s", label, res.exitCode, res.stderr)
		}
		if !strings.Contains(res.stdout, `"contamination"`) {
			t.Fatalf("%s hook list = %s, want the explicitly selected catalog", label, res.stdout)
		}
	}
	listsRoomCatalog("explicit --hook-catalog",
		runWrkfIn(t, dir, "", principal, "--db", dbPath, "--hook-catalog", roomCatalog, "hook", "list", "--json"))
	listsRoomCatalog("--hook-catalog with surrounding whitespace",
		runWrkfIn(t, dir, "", principal, "--db", dbPath, "--hook-catalog", "  "+roomCatalog+"  ", "hook", "list", "--json"))
	withEnv := append([]string{"WRKF_HOOK_CATALOG=" + roomCatalog}, principal...)
	listsRoomCatalog("WRKF_HOOK_CATALOG without a flag",
		runWrkfIn(t, dir, "", withEnv, "--db", dbPath, "hook", "list", "--json"))

	// An explicit flag wins over the environment.
	flagWins := runWrkfIn(t, dir, "", withEnv, "--db", dbPath, "--hook-catalog", explicitEmptyHookCatalog(t), "hook", "list", "--json")
	if flagWins.exitCode != 0 {
		t.Fatalf("--hook-catalog over WRKF_HOOK_CATALOG exit %d: %s", flagWins.exitCode, flagWins.stderr)
	}
	if strings.Contains(flagWins.stdout, `"contamination"`) {
		t.Fatalf("hook list = %s, want the --hook-catalog catalog, not WRKF_HOOK_CATALOG", flagWins.stdout)
	}
}
