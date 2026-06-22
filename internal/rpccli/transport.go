// Package rpccli is the RPC-backed mirror of the wrkq CLI. Every command obtains
// durable wrkq behavior ONLY by sending JSON-RPC frames through the same
// protocol boundary as `wrkq rpc --stdio`. No command in this package may import
// store, wrkqapi, direct db/SQL, or internal/cli command handlers for business
// behavior (enforced by importguard_test.go).
package rpccli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"sync"

	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// Transport is the CLI's only boundary to durable wrkq behavior. Command
// adapters depend on this interface, never on a server implementation.
type Transport interface {
	// Call sends a single JSON-RPC request and returns the raw result. Command
	// adapters decode only the fields they need from the result.
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	// Close shuts the transport down (rpc.shutdown + rpc.exit where possible) and
	// surfaces abnormal shutdown.
	Close() error
}

// Error is the client-facing RPC error. It carries the numeric protocol code,
// message, raw data, the parsed domain code (e.g. WRKQ_NOT_FOUND), and the
// retryable flag, so exit-code mapping can live in the CLI without coupling to
// server internals beyond protocol constants.
type Error struct {
	Code      int
	Message   string
	Data      json.RawMessage
	DomainID  string
	Retryable bool
}

func (e *Error) Error() string {
	if e.DomainID != "" {
		return fmt.Sprintf("%s: %s", e.DomainID, e.Message)
	}
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// errorFromRPC parses a wire RPCError into the client-facing Error, pulling the
// domain code and retryable flag out of the error data payload when present.
func errorFromRPC(re *workrpc.RPCError) *Error {
	out := &Error{Code: re.Code, Message: re.Message, Data: re.Data}
	if len(re.Data) > 0 {
		var d struct {
			Code      string `json:"code"`
			Retryable bool   `json:"retryable"`
		}
		if json.Unmarshal(re.Data, &d) == nil {
			out.DomainID = d.Code
			out.Retryable = d.Retryable
		}
	}
	return out
}

// conn is the single-flight request/response engine shared by every transport.
// CLI calls are strictly serial: one mutex guards write-frame/read-response,
// ids are monotonically unique, there is no batching, no concurrent calls, and
// no notification is ever hidden inside Call (lifecycle frames are sent
// explicitly by Close).
type conn struct {
	mu      sync.Mutex
	w       io.Writer
	br      *bufio.Reader
	nextID  int
	onClose func() error
	closed  bool
}

// Call satisfies Transport. The context is accepted for interface stability;
// cancellation mid-frame is not supported by the synchronous line protocol.
func (c *conn) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request(method, params)
}

// request writes one request frame and reads exactly one response frame. Caller
// must hold c.mu.
func (c *conn) request(method string, params any) (json.RawMessage, error) {
	c.nextID++
	id := json.RawMessage(strconv.Itoa(c.nextID))
	if err := c.writeFrame(id, method, params); err != nil {
		return nil, err
	}
	line, err := c.br.ReadBytes('\n')
	if err != nil {
		return nil, fmt.Errorf("read rpc response (%s): %w", method, err)
	}
	var resp workrpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		return nil, fmt.Errorf("decode rpc response (%s): %w", method, err)
	}
	if resp.Error != nil {
		return nil, errorFromRPC(resp.Error)
	}
	return resp.Result, nil
}

// notify writes a notification frame (no id, no response). Caller must hold c.mu.
func (c *conn) notify(method string, params any) error {
	return c.writeFrame(nil, method, params)
}

func (c *conn) writeFrame(id json.RawMessage, method string, params any) error {
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return err
		}
		raw = b
	}
	frame, err := json.Marshal(workrpc.Request{JSONRPC: "2.0", ID: id, Method: method, Params: raw})
	if err != nil {
		return err
	}
	if _, err := c.w.Write(append(frame, '\n')); err != nil {
		return err
	}
	return nil
}

// initialize performs the rpc.initialize handshake and validates enough of the
// result to catch a mis-wired server (wrong protocol version or a server that
// does not expose the wrkq method surface). Caller must hold c.mu.
func (c *conn) initialize() error {
	res, err := c.request("rpc.initialize", map[string]string{"protocolVersion": workrpc.ProtocolVersion})
	if err != nil {
		return fmt.Errorf("rpc.initialize: %w", err)
	}
	var init struct {
		ProtocolVersion string   `json:"protocolVersion"`
		Methods         []string `json:"methods"`
	}
	if err := json.Unmarshal(res, &init); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}
	if init.ProtocolVersion != workrpc.ProtocolVersion {
		return fmt.Errorf("rpc protocol mismatch: server %q, client %q", init.ProtocolVersion, workrpc.ProtocolVersion)
	}
	if !containsMethod(init.Methods, "wrkq.task.show") {
		return errors.New("rpc server does not expose the wrkq method surface (wrong wiring)")
	}
	return nil
}

func containsMethod(methods []string, want string) bool {
	for _, m := range methods {
		if m == want {
			return true
		}
	}
	return false
}

// Close sends rpc.shutdown + rpc.exit best-effort, then runs the transport's
// onClose (close pipes/process stdin, wait for server goroutine/process) and
// surfaces any abnormal shutdown error.
func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	// Best-effort graceful shutdown; ignore errors since the peer may already
	// be gone. rpc.exit is a notification so the server's Serve loop returns.
	_, _ = c.request("rpc.shutdown", nil)
	_ = c.notify("rpc.exit", nil)
	if c.onClose != nil {
		return c.onClose()
	}
	return nil
}

// NewInProcess wires a client to the REAL workrpc.Server.Serve loop over pipes.
// This exercises the same codec, rpc.initialize gating, dispatch, stdout-purity
// redirection, and MapError path as `wrkq rpc --stdio` — not a friendlier
// in-process shortcut. The returned transport owns the supplied Handle and
// closes its database on Close.
func NewInProcess(h *bootstrap.Handle) (Transport, error) {
	clientR, clientW := io.Pipe() // client -> server stdin
	serverR, serverW := io.Pipe() // server stdout -> client
	srv := workrpc.NewServer(serverW)
	workrpc.RegisterAPI(srv, h.API, h.Opts)

	done := make(chan error, 1)
	go func() {
		err := srv.Serve(context.Background(), clientR)
		_ = serverW.Close()
		done <- err
	}()

	c := &conn{w: clientW, br: bufio.NewReader(serverR)}
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

// NewSubprocess spawns `wrkq rpc --stdio` and speaks the same conn protocol,
// proving the seam is transport-agnostic (and is the on-ramp for a future wrkqd
// daemon transport). binPath is the wrkq binary; dbPath, when non-empty, is
// passed via the --db global flag. extraEnv is appended to the child env.
func NewSubprocess(ctx context.Context, binPath, dbPath string, extraEnv []string) (Transport, error) {
	args := []string{}
	if dbPath != "" {
		args = append(args, "--db", dbPath)
	}
	args = append(args, "rpc", "--stdio")
	cmd := exec.CommandContext(ctx, binPath, args...)
	if len(extraEnv) > 0 {
		cmd.Env = append(cmd.Environ(), extraEnv...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}

	c := &conn{w: stdin, br: bufio.NewReader(stdout)}
	c.onClose = func() error {
		_ = stdin.Close()
		return cmd.Wait()
	}

	c.mu.Lock()
	initErr := c.initialize()
	c.mu.Unlock()
	if initErr != nil {
		_ = c.Close()
		return nil, initErr
	}
	return c, nil
}
