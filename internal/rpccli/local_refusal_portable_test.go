//go:build !wrkq_local

package rpccli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The portable product must refuse a local locator deterministically, BEFORE any
// bootstrap or DB work, rather than failing later inside a driver that this
// build does not link (T-07090). This test only exists in the portable lane; the
// canonical build must keep serving local locators, which the tagged suites and
// the real local/remote parity tests cover.
func TestPortableWrkqRefusesLocalDBLocatorNamingRemoteOnlyRemedy(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	t.Setenv("HOME", dir)
	for _, key := range []string{"WRKQ_DB", "WRKQ_DB_PATH", "WRKQ_CONFIG", "XDG_CONFIG_HOME"} {
		t.Setenv(key, "")
	}
	local := filepath.Join(dir, "x", "wrkq.db")

	for _, tc := range []struct {
		name        string
		args        []string
		wantLocator string
		wantText    []string
	}{
		{
			name:        "a local --db on any command",
			args:        []string{"--db", local, "ls"},
			wantLocator: local,
			wantText:    []string{local, "remote-only", "rpc://", "wrkq_local"},
		},
		{
			name:     "rpc --stdio with no locator configured",
			args:     []string{"rpc", "--stdio"},
			wantText: []string{"(none configured)", "remote-only"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cmd := NewRootCmdFor("wrkq")
			cmd.SetArgs(tc.args)
			var out bytes.Buffer
			cmd.SetOut(&out)
			cmd.SetErr(&out)
			err := cmd.Execute()

			var refusal *ErrLocalLocatorUnsupported
			if !errors.As(err, &refusal) {
				t.Fatalf("want a typed *ErrLocalLocatorUnsupported, got %T: %v\n%s", err, err, out.String())
			}
			if tc.wantLocator != "" && refusal.Locator != tc.wantLocator {
				t.Errorf("refusal should name the rejected locator: want %q, got %q", tc.wantLocator, refusal.Locator)
			}
			// The message has to tell an operator how to proceed, since a portable
			// binary on a machine with no local database is the expected deployment.
			for _, want := range tc.wantText {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal message missing %q: %s", want, err.Error())
				}
			}
			if _, statErr := os.Stat(local); !os.IsNotExist(statErr) {
				t.Errorf("refusal touched the local locator path %s (stat err %v)", local, statErr)
			}
		})
	}
}
