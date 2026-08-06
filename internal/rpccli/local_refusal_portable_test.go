//go:build !wrkq_local

package rpccli

import (
	"errors"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
)

// The portable product must refuse a local locator deterministically, BEFORE any
// bootstrap or DB work, rather than failing later inside a driver that this
// build does not link (T-07090). This test only exists in the portable lane; the
// canonical build must keep serving local locators, which the tagged suites and
// the real local/remote parity tests cover.

func TestPortableBuildRefusesLocalLocator(t *testing.T) {
	cfg := &config.Config{DBLocator: "/tmp/wrkq.db", DBPath: "/tmp/wrkq.db"}

	tr, gotCfg, cleanup, err := openLocalTransport(cfg.DBLocator)
	if err == nil {
		t.Fatal("openLocalTransport accepted a local locator in the portable build")
	}
	if tr != nil || gotCfg != nil || cleanup != nil {
		t.Fatal("refusal must not hand back a transport, config, or cleanup func")
	}

	var refusal *ErrLocalLocatorUnsupported
	if !errors.As(err, &refusal) {
		t.Fatalf("want a typed *ErrLocalLocatorUnsupported, got %T: %v", err, err)
	}
	if refusal.Locator != cfg.DBLocator {
		t.Errorf("refusal should name the rejected locator: want %q, got %q", cfg.DBLocator, refusal.Locator)
	}
	// The message has to tell an operator how to proceed, since a portable
	// binary on a machine with no local database is the expected deployment.
	for _, want := range []string{"remote-only", "rpc://", "wrkq_local"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal message missing %q: %s", want, err.Error())
		}
	}
}

func TestPortableBuildRefusalNamesMissingLocator(t *testing.T) {
	err := serveLocalStdio(nil, &config.Config{})
	var refusal *ErrLocalLocatorUnsupported
	if !errors.As(err, &refusal) {
		t.Fatalf("want a typed *ErrLocalLocatorUnsupported, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "(none configured)") {
		t.Errorf("an empty locator should be named as unconfigured, got: %s", err.Error())
	}
}
