package admincli

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

func TestMigrateRejectsTrailingArgumentsBeforeOpeningDatabase(t *testing.T) {
	defaultDB := filepath.Join(t.TempDir(), "default.db")
	t.Setenv("WRKQ_DB_PATH", defaultDB)

	cmd := testMigrateRootCommand()
	cmd.SetArgs([]string{"migrate", "status"})
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

	cmd := testMigrateRootCommand()
	cmd.SetArgs([]string{"migrate", "--db", ""})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--db was explicitly provided but empty") {
		t.Fatalf("expected explicit-empty --db error, got %v", err)
	}
	if _, statErr := os.Stat(defaultDB); !os.IsNotExist(statErr) {
		t.Fatalf("migrate opened default database despite explicit empty --db: %v", statErr)
	}
}

// TestMigrateRefusesWhileLaunchdJobServesTheDatabase drives `wrkqadm migrate`
// with a fake launchctl first on PATH (the real lsof stays reachable). A
// launchd job whose resolved database is the one being migrated refuses the
// migration whether or not it has a pid; a job that cannot be inspected or
// resolved refuses too; only a job launchd does not know lets it run.
func TestMigrateRefusesWhileLaunchdJobServesTheDatabase(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd")
	}
	t.Setenv("WRKQ_LAUNCHD_LABEL", "com.praesidium.wrkq-server-test")
	fakeBin := t.TempDir()
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	script := filepath.Join(fakeBin, "launchctl")

	other := filepath.Join(t.TempDir(), "other.db")
	cases := []struct {
		name string
		// launchctl is the fake `launchctl print` body; {db} becomes the DB path.
		launchctl  string
		wantServed bool   // refusal is a DatabaseServingError naming the job
		wantErr    string // refusal text; empty with !wantServed means migrate succeeds
	}{
		{
			name:       "loaded job without pid serving the database via --db argv",
			launchctl:  "printf 'job = {\n arguments = {\n /tmp/wrkqd\n --db\n {db}\n }\n}\n'\n",
			wantServed: true,
		},
		{
			name:       "-db= argv outranks a WRKQ_DB env naming another file",
			launchctl:  "printf 'job = {\n arguments = {\n /tmp/wrkqd\n -db={db}\n }\n environment = {\n WRKQ_DB => " + other + "\n }\n}\n'\n",
			wantServed: true,
		},
		{
			name:      "-db= argv naming another file outranks a WRKQ_DB env naming this one",
			launchctl: "printf 'job = {\n arguments = {\n /tmp/wrkqd\n -db=" + other + "\n }\n environment = {\n WRKQ_DB => {db}\n }\n}\n'\n",
		},
		{
			name:      "launchd does not know the job",
			launchctl: "printf 'Could not find service x in domain for user gui' >&2\nexit 113\n",
		},
		{
			name:      "launchctl inspection fails",
			launchctl: "printf 'Operation not permitted' >&2\nexit 1\n",
			wantErr:   "DATABASE_LIFECYCLE_INSPECTION_FAILED: cannot inspect launchd job gui/" + fmt.Sprint(os.Getuid()) + "/com.praesidium.wrkq-server-test: exit status 1",
		},
		{
			name:      "loaded job whose database cannot be resolved",
			launchctl: "printf 'job = {\n pid = 123\n}\n'\n",
			wantErr:   "DATABASE_LIFECYCLE_INSPECTION_FAILED: loaded job gui/",
		},
		{
			name:      "loaded job with a relative database and no working directory",
			launchctl: "printf 'job = {\n arguments = {\n /tmp/wrkqd\n -db=relative.db\n }\n}\n'\n",
			wantErr:   "relative DB without absolute job working directory",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			database, err := db.Open(filepath.Join(t.TempDir(), "job.db"))
			if err != nil {
				t.Fatal(err)
			}
			path := database.Path()
			_ = database.Close()
			if err := os.WriteFile(script, []byte("#!/bin/sh\n"+strings.ReplaceAll(tc.launchctl, "{db}", path)), 0o755); err != nil {
				t.Fatal(err)
			}

			cmd := testMigrateRootCommand()
			cmd.SetArgs([]string{"migrate", "--db", path})
			err = cmd.Execute()

			var serving *db.DatabaseServingError
			switch {
			case tc.wantServed:
				if !errors.As(err, &serving) || serving.PID != 0 || !strings.Contains(serving.Job, "com.praesidium.wrkq-server-test") {
					t.Fatalf("migrate err = %v; want MIGRATION_DATABASE_SERVING naming the pid-less test job", err)
				}
			case tc.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("migrate err = %v; want %q", err, tc.wantErr)
				}
			default:
				if err != nil {
					t.Fatalf("migrate refused a database no launchd job serves: %v", err)
				}
			}
		})
	}
}

func testMigrateRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:          "wrkqadm",
		SilenceUsage: true,
	}
	root.PersistentFlags().String("db", "", "")
	root.AddCommand(newMigrateAdmCmd())
	return root
}
