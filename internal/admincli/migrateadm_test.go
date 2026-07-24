package admincli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestMigrateRejectsTrailingArgumentsBeforeOpeningDatabase(t *testing.T) {
	defaultDB := filepath.Join(t.TempDir(), "default.db")
	t.Setenv("WRKQ_DB_PATH", defaultDB)

	cmd := testMigrateCommand()
	cmd.SetArgs([]string{"status"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected trailing-argument error, got %v", err)
	}
	if _, statErr := os.Stat(defaultDB); !os.IsNotExist(statErr) {
		t.Fatalf("migrate opened default database despite invalid trailing argument: %v", statErr)
	}
}

func TestMigrateRejectsExplicitEmptyDatabaseBeforeOpeningDefault(t *testing.T) {
	defaultDB := filepath.Join(t.TempDir(), "default.db")
	t.Setenv("WRKQ_DB_PATH", defaultDB)

	cmd := testMigrateCommand()
	cmd.SetArgs([]string{"--db", ""})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--db was explicitly provided but empty") {
		t.Fatalf("expected explicit-empty --db error, got %v", err)
	}
	if _, statErr := os.Stat(defaultDB); !os.IsNotExist(statErr) {
		t.Fatalf("migrate opened default database despite explicit empty --db: %v", statErr)
	}
}

func testMigrateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "migrate",
		Args:         cobra.NoArgs,
		RunE:         runMigrateAdm,
		SilenceUsage: true,
	}
	cmd.Flags().String("db", "", "")
	return cmd
}
