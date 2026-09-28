//go:build wrkq_local

package wrkfcli

import (
	"context"
	"os"

	"github.com/lherron/wrkq/internal/config"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
)

// Local-locator seam (T-07092, the wrkf twin of rpccli's T-07090 seam).
// Everything that opens durable local state lives behind the wrkq_local build
// tag so the portable wrkf links no SQLite. The portable counterparts are in
// local_seam_portable.go and refuse before any bootstrap/DB work.

// serveLocalStdio serves wrkf JSON-RPC over stdio from a local database.
func serveLocalStdio(ctx context.Context, cfg *config.Config, principalRef string) error {
	return workrpcclient.ServeConfiguredLocalStdio(ctx, os.Stdin, os.Stdout, cfg.DBLocator, flagHookCatalog, workrpcclient.LocalServerOptions{
		Entrypoint:              "wrkf",
		ServerVersion:           Version,
		DefaultPrincipalRef:     principalRef,
		WrkqDefaultPrincipalRef: principalRef,
		UseWrkqDefault:          true,
		DefaultRole:             roleDefault(),
	})
}

// openLocalTransport serves the wrkf method catalog in-process from a local
// database.
func openLocalTransport(cfg *config.Config, principalRef string) (workrpcclient.Transport, func(), error) {
	tr, err := workrpcclient.NewConfiguredInProcess(
		cfg.DBLocator,
		flagHookCatalog,
		workrpcclient.LocalServerOptions{
			Entrypoint:              "wrkf",
			DefaultPrincipalRef:     principalRef,
			WrkqDefaultPrincipalRef: principalRef,
			UseWrkqDefault:          true,
			DefaultRole:             roleDefault(),
		},
		workrpcclient.WrkfProfile,
	)
	if err != nil {
		return nil, nil, err
	}
	return tr, func() { _ = tr.Close() }, nil
}
