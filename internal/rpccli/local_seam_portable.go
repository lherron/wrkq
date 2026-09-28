//go:build !wrkq_local

package rpccli

import (
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/localseam"
	"github.com/spf13/cobra"
)

// Portable local-locator seam (T-07090). This build links no SQLite driver, so
// a local database locator is refused deterministically HERE — before any
// bootstrap or DB work — rather than surfacing later as a driver-level failure.
// Remote (`rpc://`) operation is unaffected.

// ErrLocalLocatorUnsupported is the shared portable refusal (see localseam).
type ErrLocalLocatorUnsupported = localseam.ErrLocalLocatorUnsupported

func refuseLocal(locator string) error { return localseam.Refuse("wrkq", locator) }

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
