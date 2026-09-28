//go:build !wrkq_local

package wrkfcli

import (
	"context"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/localseam"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
)

// Portable local-locator seam (T-07092). This build links no SQLite driver, so
// a local database locator is refused deterministically HERE — before any
// bootstrap or DB work — with the typed refusal shared with portable wrkq.
// Remote (`rpc://`) operation is unaffected.

func serveLocalStdio(_ context.Context, cfg *config.Config, _ string) error {
	return localseam.Refuse("wrkf", cfg.DBLocator)
}

func openLocalTransport(cfg *config.Config, _ string) (workrpcclient.Transport, func(), error) {
	return nil, nil, localseam.Refuse("wrkf", cfg.DBLocator)
}
