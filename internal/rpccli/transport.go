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

type preflightKey struct{}

type preflightRemote struct {
	Transport
	endpoint, token string
}

// Close leaves the preflight transport available for the invocation. HTTP
// response bodies are closed per request, and HTTP connections use the shared pool.
func (preflightRemote) Close() error { return nil }

func remoteForCommand(cmd *cobra.Command, endpoint, token string) (Transport, error) {
	if ctx := cmd.Context(); ctx != nil {
		if preflight, ok := ctx.Value(preflightKey{}).(preflightRemote); ok &&
			preflight.endpoint == endpoint && preflight.token == token {
			return preflight, nil
		}
	}
	return NewRemote(endpoint, token)
}

func openConfiguredTransport(cmd *cobra.Command) (Transport, *config.Config, func(), error) {
	cfg, err := config.LoadWithDBOverride(dbOverride(cmd), false)
	if err != nil {
		return nil, nil, nil, err
	}
	if cfg.RemoteEndpoint != "" {
		tr, err := remoteForCommand(cmd, cfg.RemoteEndpoint, remoteTokenFromEnv())
		if err != nil {
			return nil, nil, nil, err
		}
		return tr, cfg, func() { _ = tr.Close() }, nil
	}
	return openLocalTransport(cfg.DBLocator)
}
