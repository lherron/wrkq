//go:build !wrkq_local

package client

import (
	"fmt"

	"github.com/lherron/wrkq/internal/config"
)

// ErrLocalLocatorUnsupported reports an attempt to use a local SQLite locator
// from the portable public client build.
type ErrLocalLocatorUnsupported struct{ Locator string }

func (e *ErrLocalLocatorUnsupported) Error() string {
	locator := e.Locator
	if locator == "" {
		locator = "(none configured)"
	}
	return fmt.Sprintf("this client build is remote-only and cannot open the local database %q; use an rpc:// locator or build with -tags wrkq_local", locator)
}

func newConfiguredLocalTransport(cfg *config.Config, _ string) (Transport, error) {
	return nil, &ErrLocalLocatorUnsupported{Locator: cfg.DBLocator}
}
