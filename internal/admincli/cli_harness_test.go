package admincli

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// runAdm drives the shipped wrkqadm root command in-process with argv and
// returns what the command wrote to stdout plus its error. Flag values are
// package state that survives between Execute calls, so every flag is reset
// to its default first: each call sees only the argv it was given.
func runAdm(t *testing.T, args ...string) (string, error) {
	t.Helper()
	resetAdmFlags(rootAdmCmd)

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	out := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		out <- string(b)
	}()
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	rootAdmCmd.SetArgs(args)
	runErr := rootAdmCmd.Execute()
	_ = w.Close()
	os.Stdout = oldStdout
	return <-out, runErr
}

func resetAdmFlags(cmd *cobra.Command) {
	for _, fs := range []*pflag.FlagSet{cmd.Flags(), cmd.PersistentFlags()} {
		fs.VisitAll(func(f *pflag.Flag) {
			if f.Changed {
				_ = f.Value.Set(f.DefValue)
				f.Changed = false
			}
		})
	}
	for _, child := range cmd.Commands() {
		resetAdmFlags(child)
	}
}

// migratedDB creates a fully migrated database file and returns its path.
// The returned handle stays open for seeding and is closed at cleanup.
func migratedDB(t *testing.T, name string) (*db.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	database, err := db.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate %s: %v", name, err)
	}
	return database, path
}

// seedProject inserts a top-level project under the root and returns its uuid.
func seedProject(t *testing.T, database *db.DB, uuid, slug string) string {
	t.Helper()
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_principal_ref, updated_by_principal_ref)
		VALUES (?, ?, ?, (SELECT uuid FROM containers WHERE kind = 'root'), 'project', 'agent:tester', 'agent:tester')
	`, uuid, slug, slug); err != nil {
		t.Fatalf("seed project %s: %v", slug, err)
	}
	return uuid
}

// seedTask inserts an open task; an empty id lets the sequence mint one.
func seedTask(t *testing.T, database *db.DB, uuid, id, slug, projectUUID string) {
	t.Helper()
	if _, err := database.Exec(`
		INSERT INTO tasks (uuid, id, slug, title, project_uuid, state, created_by_principal_ref, updated_by_principal_ref)
		VALUES (?, ?, ?, ?, ?, 'open', 'agent:tester', 'agent:tester')
	`, uuid, id, slug, slug, projectUUID); err != nil {
		t.Fatalf("seed task %s: %v", slug, err)
	}
}
