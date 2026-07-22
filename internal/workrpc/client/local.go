package client

import (
	"context"
	"io"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// LocalServerOptions carries entrypoint-owned presentation/attribution labels
// into the shared local workrpc server constructor.
type LocalServerOptions struct {
	Entrypoint       string
	ServerVersion    string
	DefaultActor     string
	WrkqDefaultActor string
	UseWrkqDefault   bool
	DefaultRole      string
}

// NewConfiguredInProcess opens the configured local database behind the shared
// bootstrap boundary. Command adapters never receive the DB or workflow
// service; their only durable surface is Transport.
func NewConfiguredInProcess(dbLocator, hookCatalog, entrypoint string, profile Profile) (Transport, *config.Config, error) {
	h, err := bootstrap.OpenWithHookCatalog(dbLocator, hookCatalog)
	if err != nil {
		return nil, nil, err
	}
	h.Opts.Entrypoint = entrypoint
	tr, err := NewInProcess(h, profile)
	if err != nil {
		_ = h.Close()
		return nil, nil, err
	}
	return tr, h.Config, nil
}

// ServeConfiguredLocalStdio serves the local unified registry without exposing
// bootstrap, database, or workflow-service construction to a command adapter.
func ServeConfiguredLocalStdio(ctx context.Context, in io.Reader, out io.Writer, dbLocator, hookCatalog string, opts LocalServerOptions) error {
	h, err := bootstrap.OpenWithHookCatalog(dbLocator, hookCatalog)
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()
	h.Opts.Entrypoint = opts.Entrypoint
	h.Opts.ServerVersion = opts.ServerVersion
	h.Opts.DefaultActor = opts.DefaultActor
	h.Opts.WrkqDefaultActor = opts.WrkqDefaultActor
	h.Opts.UseWrkqDefault = opts.UseWrkqDefault
	h.Opts.DefaultRole = opts.DefaultRole
	return workrpc.ServeStdio(ctx, in, out, h.API, h.Opts)
}

// ServeRemoteStdio forwards the unified stdio surface to a remote wrkqd.
func ServeRemoteStdio(ctx context.Context, in io.Reader, out io.Writer, endpoint, token string) error {
	return workrpc.ServeRemoteStdio(ctx, in, out, endpoint, token)
}
