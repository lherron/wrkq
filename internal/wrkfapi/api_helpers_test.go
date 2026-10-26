//go:build wrkq_local

package wrkfapi_test

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/wrkfapi"
)

// newTestAPI spins up a temp migrated DB and returns a wrkfapi.API ready for use.
func newTestAPI(t *testing.T) *wrkfapi.API {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc := workflow.NewService(database)
	return wrkfapi.New(svc)
}
