package client

import (
	"context"
	"io"

	"github.com/lherron/wrkq/internal/workrpc"
)

// ServeRemoteStdio forwards the unified stdio surface to a remote wrkqd. It is
// the remote path, so it stays in the portable build (T-07090). principalRef is
// the caller's launch principal, sent as X-Wrkq-Principal-Ref; empty sends none.
func ServeRemoteStdio(ctx context.Context, in io.Reader, out io.Writer, endpoint, token, principalRef string) error {
	return workrpc.ServeRemoteStdio(ctx, in, out, endpoint, token, principalRef)
}
