//go:build !wrkq_local

package rpccli

import (
	"fmt"

	"github.com/lherron/wrkq/internal/config"
	"github.com/spf13/cobra"
)

// Portable local-locator seam (T-07090). This build links no SQLite driver, so
// a local database locator is refused deterministically HERE — before any
// bootstrap or DB work — rather than surfacing later as a driver-level failure.
// Remote (`rpc://`) operation is unaffected.

// ErrLocalLocatorUnsupported names the refusal so callers and tests can match it
// without string-matching a message.
type ErrLocalLocatorUnsupported struct{ Locator string }

func (e *ErrLocalLocatorUnsupported) Error() string {
	locator := e.Locator
	if locator == "" {
		locator = "(none configured)"
	}
	return fmt.Sprintf(
		"this wrkq build is remote-only and cannot open the local database %q; "+
			"set WRKQ_DB to an rpc:// endpoint (for example rpc://host:7171), "+
			"or use a wrkq built with -tags wrkq_local for local-file operation",
		locator)
}

func refuseLocal(locator string) error { return &ErrLocalLocatorUnsupported{Locator: locator} }

func openLocalTransport(locator string) (Transport, *config.Config, func(), error) {
	return nil, nil, nil, refuseLocal(locator)
}

func serveLocalStdio(_ *cobra.Command, cfg *config.Config) error {
	locator := ""
	if cfg != nil {
		locator = cfg.DBLocator
	}
	return refuseLocal(locator)
}

func reportLocalWhoami(cmd *cobra.Command, _ bool) error {
	return refuseLocal(dbOverride(cmd))
}
