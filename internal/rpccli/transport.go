package rpccli

import (
	"context"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
	workrpcclient "github.com/lherron/wrkq/internal/workrpc/client"
	"github.com/spf13/cobra"
)

// Keep the existing rpccli names as compatibility aliases while protocol
// machinery is shared with wrkfcli from internal/workrpc/client.
type Transport = workrpcclient.Transport
type Error = workrpcclient.Error

func NewInProcess(h *bootstrap.Handle) (Transport, error) {
	return workrpcclient.NewInProcess(h, workrpcclient.WrkqProfile)
}

func NewSubprocess(ctx context.Context, binPath, dbPath string, extraEnv []string) (Transport, error) {
	return workrpcclient.NewSubprocess(ctx, binPath, dbPath, extraEnv, workrpcclient.WrkqProfile)
}

func NewRemote(endpoint, token string) (Transport, error) {
	return workrpcclient.NewRemote(endpoint, token, workrpcclient.WrkqProfile)
}

func remoteTokenFromEnv() string { return workrpcclient.TokenFromEnv() }

func openConfiguredTransport(cmd *cobra.Command) (Transport, *config.Config, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	if override := dbOverride(cmd); override != "" {
		if err := config.ApplyDBLocator(cfg, override, false); err != nil {
			return nil, nil, nil, err
		}
	}
	if cfg.RemoteEndpoint != "" {
		tr, err := NewRemote(cfg.RemoteEndpoint, remoteTokenFromEnv())
		if err != nil {
			return nil, nil, nil, err
		}
		return tr, cfg, func() { _ = tr.Close() }, nil
	}
	h, err := bootstrap.Open(cfg.DBLocator)
	if err != nil {
		return nil, nil, nil, err
	}
	tr, err := NewInProcess(h)
	if err != nil {
		_ = h.Close()
		return nil, nil, nil, err
	}
	return tr, h.Config, func() { _ = tr.Close() }, nil
}
