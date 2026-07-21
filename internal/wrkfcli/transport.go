package wrkfcli

import (
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	workrpcclient "github.com/lherron/wrkq/internal/workrpc/client"
	"github.com/spf13/cobra"
)

// openConfiguredTransport is the single local-vs-remote selection point for
// wrkf command adapters. Commands added to the transport path must not branch
// on the locator themselves.
func openConfiguredTransport(cmd *cobra.Command) (workrpcclient.Transport, *config.Config, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	if flagDB != "" {
		if err := config.ApplyDBLocator(cfg, flagDB, false); err != nil {
			return nil, nil, nil, err
		}
	}
	if cfg.RemoteEndpoint != "" {
		tr, err := workrpcclient.NewRemote(cfg.RemoteEndpoint, workrpcclient.TokenFromEnv(), workrpcclient.WrkfProfile)
		if err != nil {
			return nil, nil, nil, err
		}
		return tr, cfg, func() { _ = tr.Close() }, nil
	}
	h, err := bootstrap.Open(cfg.DBLocator)
	if err != nil {
		return nil, nil, nil, err
	}
	h.Opts.Entrypoint = "wrkf"
	tr, err := workrpcclient.NewInProcess(h, workrpcclient.WrkfProfile)
	if err != nil {
		_ = h.Close()
		return nil, nil, nil, err
	}
	return tr, h.Config, func() { _ = tr.Close() }, nil
}
