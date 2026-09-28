//go:build !wrkq_local

package wrkfcli

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/localseam"
)

// The portable wrkf must refuse a local locator before any bootstrap or DB
// work (T-07092). Only the portable lane compiles this test; the canonical
// build's local path is covered by the tagged suites.

func TestPortableBuildRefusesLocalLocator(t *testing.T) {
	cfg := &config.Config{DBLocator: "/tmp/wrkq.db", DBPath: "/tmp/wrkq.db"}

	for name, err := range map[string]error{
		"transport": func() error {
			tr, cleanup, err := openLocalTransport(cfg, "")
			if tr != nil || cleanup != nil {
				t.Fatal("refusal must not hand back a transport or cleanup func")
			}
			return err
		}(),
		"stdio": serveLocalStdio(context.Background(), cfg, ""),
	} {
		var refusal *localseam.ErrLocalLocatorUnsupported
		if !errors.As(err, &refusal) {
			t.Fatalf("%s: want a typed *localseam.ErrLocalLocatorUnsupported, got %T: %v", name, err, err)
		}
		if refusal.Locator != cfg.DBLocator || refusal.Binary != "wrkf" {
			t.Errorf("%s: refusal should name wrkf and the rejected locator, got %+v", name, refusal)
		}
		if !strings.Contains(err.Error(), "this wrkf build is remote-only") {
			t.Errorf("%s: unexpected refusal message: %s", name, err.Error())
		}
	}
}
