package rpccli

import (
	"context"

	"github.com/lherron/wrkq/internal/config"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
	"github.com/spf13/cobra"
)

// Keep the existing rpccli names as compatibility aliases while protocol
// machinery is shared with wrkfcli from pkg/client.
type Transport = workrpcclient.Transport
type Error = workrpcclient.Error

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
	return openLocalTransport(cfg.DBLocator)
}
