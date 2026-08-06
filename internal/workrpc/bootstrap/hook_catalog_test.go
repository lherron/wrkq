//go:build wrkq_local

package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/wrkfapi"
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
	autoDir := filepath.Join(dir, ".wrkq", "wrkf-room")
	if err := os.MkdirAll(autoDir, 0o755); err != nil {
		t.Fatal(err)
	}
	catalogPath := filepath.Join(autoDir, "hook-catalog.json")
	if err := os.WriteFile(catalogPath, []byte(`{"schemaVersion":"wrkf.hook-catalog.v0","hooks":{"auto":{"kind":"exec","argv":["true"]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("WRKF_HOOK_CATALOG", "")

	constructors := map[string]func(*db.DB, *config.Config) (*wrkfapi.API, workrpc.RegistryOptions, error){
		"local":  Server,
		"daemon": DaemonServer,
	}
	for name, construct := range constructors {
		t.Run(name, func(t *testing.T) {
			_, _, err := construct(database, &config.Config{DBPath: database.Path()})
			if !errors.Is(err, workflow.ErrHookCatalogNotConfigured) {
				t.Fatalf("error=%v, want ErrHookCatalogNotConfigured", err)
			}
			if got, want := err.Error(), "hook catalog configuration is required; set --hook-catalog PATH or WRKF_HOOK_CATALOG=PATH"; got != want {
				t.Fatalf("error=%q, want %q", got, want)
			}
		})
	}

	t.Setenv("WRKF_HOOK_CATALOG", catalogPath)
	api, _, err := DaemonServer(database, &config.Config{DBPath: database.Path()})
	if err != nil {
		t.Fatal(err)
	}
	list, err := api.HookList(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Hooks) != 1 || list.Hooks[0].ID != "auto" {
		t.Fatalf("explicit daemon catalog hooks=%#v", list.Hooks)
	}
}