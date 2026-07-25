package client

import (
	"context"
	"io"

	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// LocalServerOptions carries entrypoint-owned presentation/attribution labels
// into the shared local workrpc server constructor.
type LocalServerOptions struct {
	Entrypoint              string
	ServerVersion           string
	DefaultPrincipalRef     string
	WrkqDefaultPrincipalRef string
	UseWrkqDefault          bool
	DefaultRole             string
}

// NewConfiguredInProcess opens the configured local database behind the shared
// bootstrap boundary. Command adapters never receive the DB or workflow
// service; their only durable surface is Transport.
func NewConfiguredInProcess(dbLocator, hookCatalog string, opts LocalServerOptions, profile Profile) (Transport, error) {
	h, err := bootstrap.OpenWithHookCatalog(dbLocator, hookCatalog)
	if err != nil {
		return nil, err
	}
	applyLocalServerOptions(&h.Opts, opts)
	tr, err := NewInProcess(h, profile)
	if err != nil {
		_ = h.Close()
		return nil, err
	}
	return tr, nil
}

// ServeConfiguredLocalStdio serves the local unified registry without exposing
// bootstrap, database, or workflow-service construction to a command adapter.
func ServeConfiguredLocalStdio(ctx context.Context, in io.Reader, out io.Writer, dbLocator, hookCatalog string, opts LocalServerOptions) error {
	h, err := bootstrap.OpenWithHookCatalog(dbLocator, hookCatalog)
	if err != nil {
		return err
	}
	defer func() { _ = h.Close() }()
	applyLocalServerOptions(&h.Opts, opts)
	return workrpc.ServeStdio(ctx, in, out, h.API, h.Opts)
}

func applyLocalServerOptions(registryOpts *workrpc.RegistryOptions, opts LocalServerOptions) {
	registryOpts.Entrypoint = opts.Entrypoint
	if opts.ServerVersion != "" {
		registryOpts.ServerVersion = opts.ServerVersion
	}
	if opts.DefaultPrincipalRef != "" {
		registryOpts.DefaultPrincipalRef = opts.DefaultPrincipalRef
	}
	if opts.WrkqDefaultPrincipalRef != "" {
		registryOpts.WrkqDefaultPrincipalRef = opts.WrkqDefaultPrincipalRef
	}
	registryOpts.UseWrkqDefault = opts.UseWrkqDefault
	if opts.DefaultRole != "" {
		registryOpts.DefaultRole = opts.DefaultRole
	}
}

// ServeRemoteStdio forwards the unified stdio surface to a remote wrkqd.
func ServeRemoteStdio(ctx context.Context, in io.Reader, out io.Writer, endpoint, token string) error {
	return workrpc.ServeRemoteStdio(ctx, in, out, endpoint, token)
}
