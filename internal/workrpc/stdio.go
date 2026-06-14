package workrpc

import (
	"context"
	"io"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

func ServeStdio(ctx context.Context, in io.Reader, out io.Writer, api *wrkfapi.API, opts RegistryOptions) error {
	srv := NewServer(out)
	RegisterAPI(srv, api, opts)
	return srv.Serve(ctx, in)
}
