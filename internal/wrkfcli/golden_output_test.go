package wrkfcli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

// TestLocalOutputGolden freezes representative read, validation, mutation, and
// hook-catalog output on the real local InProcess transport. These bytes are
// the pre/post migration CLI presentation contract; remote mode uses the same
// adapters and differs only at Transport construction.
func TestLocalOutputGolden(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "golden.db")
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

	oldDB, oldJSON, oldHook := flagDB, flagJSON, flagHookCatalog
	t.Cleanup(func() {
		flagDB, flagJSON, flagHookCatalog = oldDB, oldJSON, oldHook
	})
	flagDB = dbPath
	flagJSON = true
	flagHookCatalog = ""
	t.Setenv("WRKF_PRINCIPAL_REF", "agent:golden")
	t.Setenv("WRKF_HOOK_CATALOG", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ASP_PROJECT", "")
	t.Setenv("WRKQ_PROJECT", "")

	assertCommandGolden(t, "workflow-list.golden", workflowCmd(), "list")
	assertCommandGolden(t, "workflow-validate.golden", workflowCmd(), "validate", filepath.Join("testdata", "golden", "workflow.json"))
	assertCommandGolden(t, "workflow-install.golden", workflowCmd(), "install", filepath.Join("testdata", "golden", "workflow.json"))
	assertCommandGolden(t, "hook-list.golden", hookCmd(), "list")
}

func assertCommandGolden(t *testing.T, goldenName string, cmd *cobra.Command, args ...string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("%s execute: %v (stderr=%q)", goldenName, err, stderr.String())
	}
	want, err := os.ReadFile(filepath.Join("testdata", "golden", goldenName))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stdout.Bytes(), want) {
		t.Fatalf("%s output drift\nwant:\n%s\ngot:\n%s", goldenName, want, stdout.Bytes())
	}
}
