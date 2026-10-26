package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNearestAncestorEnvLocalSuppliesTheDatabase: Load walks from the working
// directory up to $HOME and takes WRKQ_DB_PATH from the nearest .env.local it
// finds; with none on the walk (and no platform root) no database is
// configured.
//
// rewrite-held (§1): the built-binary form is a `wrkq` argv row run from each
// cwd; that harness lives in internal/rpccli, so this table drives Load, the
// API the CLI calls, and asserts the database it ends up with.
func TestNearestAncestorEnvLocalSuppliesTheDatabase(t *testing.T) {
	tests := []struct {
		name    string
		envAt   []string // dirs under $HOME holding a .env.local
		cwd     string
		wantDir string // dir whose .env.local wins; "" means none configured
	}{
		{name: "in the working directory", envAt: []string{"work/proj"}, cwd: "work/proj", wantDir: "work/proj"},
		{name: "two levels up", envAt: []string{"work"}, cwd: "work/proj/sub", wantDir: "work"},
		{name: "closest of two ancestors wins", envAt: []string{"work", "work/proj"}, cwd: "work/proj/sub", wantDir: "work/proj"},
		{name: "none anywhere", cwd: "work/proj", wantDir: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			// godotenv never overrides a variable that is set, even to "",
			// so the locator vars must be unset, not emptied.
			for _, key := range []string{"WRKQ_DB", "WRKQ_DB_PATH", "WRKQ_DB_PATH_FILE"} {
				unsetEnv(t, key)
			}
			t.Setenv("HOME", home)
			t.Setenv("PRAESIDIUM_HOME", "")
			cwd := filepath.Join(home, tt.cwd)
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			for _, dir := range tt.envAt {
				line := "WRKQ_DB_PATH=" + filepath.Join(home, dir, "marker.db") + "\n"
				if err := os.WriteFile(filepath.Join(home, dir, ".env.local"), []byte(line), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			oldCwd, err := os.Getwd()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chdir(cwd); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chdir(oldCwd) })

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			want := ""
			if tt.wantDir != "" {
				want = filepath.Join(home, tt.wantDir, "marker.db")
			}
			if cfg.DBPath != want || cfg.DBLocator != want {
				t.Fatalf("DBPath = %q, DBLocator = %q, want both %q", cfg.DBPath, cfg.DBLocator, want)
			}
		})
	}
}

func TestLoadExplicitTokenFileShieldsInlineTokenFromDotenv(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(tmpDir, ".env.local"),
		[]byte("WRKQD_TOKEN=stale-dotenv-token\n"),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCwd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}

	oldToken, hadToken := os.LookupEnv("WRKQD_TOKEN")
	if err := os.Unsetenv("WRKQD_TOKEN"); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if hadToken {
			_ = os.Setenv("WRKQD_TOKEN", oldToken)
		} else {
			_ = os.Unsetenv("WRKQD_TOKEN")
		}
	}()
	t.Setenv("WRKQD_TOKEN_FILE", filepath.Join(tmpDir, "node-token"))
	t.Setenv("HOME", tmpDir)
	t.Setenv("PRAESIDIUM_HOME", filepath.Join(tmpDir, "missing-platform"))

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if token, exists := os.LookupEnv("WRKQD_TOKEN"); !exists || token != "" {
		t.Fatalf("expected explicit token file to reserve an empty inline token, got exists=%v value=%q", exists, token)
	}
}

func TestLoadExplicitInlineTokenStillOutranksTokenFileAndDotenv(t *testing.T) {
	tmpDir := t.TempDir()
	if err := os.WriteFile(
		filepath.Join(tmpDir, ".env.local"),
		[]byte("WRKQD_TOKEN=stale-dotenv-token\n"),
		0644,
	); err != nil {
		t.Fatal(err)
	}

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(oldCwd) }()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WRKQD_TOKEN", "explicit-inline-token")
	t.Setenv("WRKQD_TOKEN_FILE", filepath.Join(tmpDir, "node-token"))
	t.Setenv("HOME", tmpDir)
	t.Setenv("PRAESIDIUM_HOME", filepath.Join(tmpDir, "missing-platform"))

	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	if token := os.Getenv("WRKQD_TOKEN"); token != "explicit-inline-token" {
		t.Fatalf("expected explicit inline token to remain authoritative, got %q", token)
	}
}
