//go:build wrkq_local

package workflow

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfiguredHookCatalogPathUsesOnlyExplicitConfiguration(t *testing.T) {
	dir := t.TempDir()
	roomDir := filepath.Join(dir, ".wrkq", "wrkf-nearby")
	if err := os.MkdirAll(roomDir, 0o755); err != nil {
		t.Fatal(err)
	}
	nearbyCatalog := filepath.Join(roomDir, "hook-catalog.json")
	if err := os.WriteFile(nearbyCatalog, []byte(`{"hooks":{"contamination":{"kind":"exec","argv":["true"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "nested", "work")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	t.Setenv("HOME", dir)
	t.Setenv("WRKF_HOOK_CATALOG", "")

	_, err := ConfiguredHookCatalogPath("")
	if !errors.Is(err, ErrHookCatalogNotConfigured) {
		t.Fatalf("error=%v, want ErrHookCatalogNotConfigured", err)
	}

	t.Setenv("WRKF_HOOK_CATALOG", nearbyCatalog)
	if got, err := ConfiguredHookCatalogPath(""); err != nil || got != nearbyCatalog {
		t.Fatalf("environment path=(%q, %v), want (%q, nil)", got, err, nearbyCatalog)
	}

	flagCatalog := filepath.Join(dir, "flag-catalog.json")
	if got, err := ConfiguredHookCatalogPath("  " + flagCatalog + "  "); err != nil || got != flagCatalog {
		t.Fatalf("flag path=(%q, %v), want (%q, nil)", got, err, flagCatalog)
	}
}

func TestLoadHookCatalogRequiresPath(t *testing.T) {
	t.Setenv("WRKF_HOOK_CATALOG", filepath.Join(t.TempDir(), "ignored.json"))
	_, err := LoadHookCatalog("")
	if !errors.Is(err, ErrHookCatalogNotConfigured) {
		t.Fatalf("error=%v, want ErrHookCatalogNotConfigured", err)
	}
}