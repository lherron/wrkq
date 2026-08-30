//go:build wrkq_local

package client

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// In-process transport (T-07090): it links the server registry and the local
// database, so it exists only in builds carrying the wrkq_local tag.
// NewInProcess connects through the real workrpc server loop and owns h.
func NewInProcess(h *bootstrap.Handle, profile Profile) (Transport, error) {
	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()
	srv := workrpc.NewServer(serverW)
	workrpc.RegisterAPI(srv, h.API, h.Opts)
	done := make(chan error, 1)
	go func() {
		err := srv.Serve(context.Background(), clientR)
		_ = serverW.Close()
		done <- err
	}()
	c := &conn{w: clientW, br: bufio.NewReader(serverR), profile: profile}
	c.onClose = func() error {
		_ = clientW.Close()
		serveErr := <-done
		dbErr := h.Close()
		if serveErr != nil && !errors.Is(serveErr, io.EOF) {
			return fmt.Errorf("rpc server loop: %w", serveErr)
		}
		return dbErr
	}
	c.mu.Lock()
	err := c.initialize()
	c.mu.Unlock()
	if err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}
