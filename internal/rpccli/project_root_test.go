//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func runSetRootCLI(t *testing.T, dbPath string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", "agent:root-test"}, args...))
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	err = cmd.Execute()
	return out.String(), errOut.String(), err
}

func storedProjectRoot(t *testing.T, dbPath string) string {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	var root string
	if err := database.QueryRow(`SELECT COALESCE(root, '') FROM containers WHERE slug = 'rpccli-test-proj'`).Scan(&root); err != nil {
		t.Fatalf("read project root: %v", err)
	}
	return root
}

// TestNormalizeRegisteredProjectRoot: `wrkq set <project> --root <path>` stores
// paths under $HOME as ~-relative (so the registry is host-portable), cleans
// ~/ paths, keeps paths outside $HOME absolute, and clears on "".
func TestNormalizeRegisteredProjectRoot(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	inside := filepath.Join(home, "praesidium", "wrkq")
	outside := filepath.Join(filepath.Dir(home), "elsewhere", "wrkq")

	for _, tc := range []struct {
		name  string
		input string
		want  string
	}{
		{name: "clear", input: "", want: ""},
		{name: "home", input: home, want: "~"},
		{name: "absolute under home", input: inside, want: "~/praesidium/wrkq"},
		{name: "tilde path", input: "~/praesidium/../praesidium/wrkq", want: "~/praesidium/wrkq"},
		{name: "absolute outside home", input: outside, want: filepath.Clean(outside)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runSetRootCLI(t, dbPath, "set", "rpccli-test-proj", "--root", tc.input, "--dry-run")
			if err != nil {
				t.Fatalf("set --root %q --dry-run: %v\n%s", tc.input, err, stderr)
			}
			var plan struct {
				DryRun bool   `json:"dry_run"`
				Root   string `json:"root"`
			}
			if err := json.Unmarshal([]byte(stdout), &plan); err != nil {
				t.Fatalf("decode dry-run plan: %v\n%s", err, stdout)
			}
			if !plan.DryRun || plan.Root != tc.want {
				t.Fatalf("set --root %q --dry-run root = %q (dry_run=%v), want %q", tc.input, plan.Root, plan.DryRun, tc.want)
			}
		})
	}

	t.Run("applied root is the stored form", func(t *testing.T) {
		if _, stderr, err := runSetRootCLI(t, dbPath, "set", "rpccli-test-proj", "--root", inside); err != nil {
			t.Fatalf("set --root %q: %v\n%s", inside, err, stderr)
		}
		if got := storedProjectRoot(t, dbPath); got != "~/praesidium/wrkq" {
			t.Fatalf("stored root = %q, want ~/praesidium/wrkq", got)
		}
	})
}

// TestNormalizeRegisteredProjectRootRejectsOtherUserTilde: a ~user/ root is
// refused with the supported-form message and the stored root is unchanged.
func TestNormalizeRegisteredProjectRootRejectsOtherUserTilde(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	t.Setenv("HOME", t.TempDir())
	if _, stderr, err := runSetRootCLI(t, dbPath, "set", "rpccli-test-proj", "--root", "~/kept"); err != nil {
		t.Fatalf("seed root: %v\n%s", err, stderr)
	}

	_, stderr, err := runSetRootCLI(t, dbPath, "set", "rpccli-test-proj", "--root", "~someone/repo")
	if err == nil {
		t.Fatal("set --root ~someone/repo succeeded")
	}
	if !strings.Contains(err.Error(), "--root supports only the current user's ~ prefix") {
		t.Fatalf("refusal = %v (stderr %q), want the supported-~-prefix message", err, stderr)
	}
	if got := storedProjectRoot(t, dbPath); got != "~/kept" {
		t.Fatalf("stored root after refusal = %q, want ~/kept", got)
	}
}
