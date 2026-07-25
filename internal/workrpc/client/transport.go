// Package client provides the transport-independent client side of the shared
// wrkq/wrkf workrpc protocol. Command packages own locator selection and
// presentation; this package owns framing, lifecycle, authentication, and
// initialize compatibility checks.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// Profile identifies the minimum surface a client requires from a compatible
// unified workrpc server. ProtocolSchemaHash still pins the complete contract;
// the profile catches a server wired to the wrong capability surface.
type Profile struct {
	Capability      string
	RequiredMethods []string
}

var (
	WrkqProfile = Profile{Capability: "wrkq", RequiredMethods: []string{"wrkq.task.show"}}
	WrkfProfile = Profile{Capability: "wrkf", RequiredMethods: []string{"wrkf.workflow.list"}}
)

// Transport is a command adapter's only boundary to durable workrpc behavior.
type Transport interface {
	Call(ctx context.Context, method string, params any) (json.RawMessage, error)
	Close() error
}

// Error is the client-facing RPC error. It preserves both the wire error and
// the parsed domain code used by command packages for exit-code mapping.
type Error struct {
	RPCCode   int
	Message   string
	Data      json.RawMessage
	DomainID  string
	Retryable bool
}

func (e *Error) Error() string {
	if e.DomainID != "" {
		return fmt.Sprintf("%s: %s", e.DomainID, e.Message)
	}
	return fmt.Sprintf("rpc error %d: %s", e.RPCCode, e.Message)
}

// Code exposes the preserved domain identifier to CLI error renderers.
func (e *Error) Code() string {
	if e == nil {
		return ""
	}
	return e.DomainID
}

func errorFromRPC(re *workrpc.RPCError) *Error {
	out := &Error{RPCCode: re.Code, Message: re.Message, Data: re.Data}
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

type conn struct {
	mu      sync.Mutex
	w       io.Writer
	br      *bufio.Reader
	nextID  int
	onClose func() error
	closed  bool
	profile Profile
}

func (c *conn) Call(_ context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request(method, params)
}

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
	_, err = c.w.Write(append(frame, '\n'))
	return err
}

func (c *conn) initialize() error {
	res, err := c.request("rpc.initialize", map[string]string{"protocolVersion": workrpc.ProtocolVersion})
	if err != nil {
		return fmt.Errorf("rpc.initialize: %w", err)
	}
	return validateInitializeResult(res, c.profile)
}

type initializeResult struct {
	ProtocolVersion    string `json:"protocolVersion"`
	ProtocolSchemaHash string `json:"protocolSchemaHash"`
	Server             struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
	} `json:"server"`
	Capabilities map[string]bool `json:"capabilities"`
	Methods      []string        `json:"methods"`
}

func validateInitializeResult(res json.RawMessage, profile Profile) error {
	if strings.TrimSpace(profile.Capability) == "" {
		return errors.New("rpc client profile requires a capability")
	}
	if len(profile.RequiredMethods) == 0 {
		return errors.New("rpc client profile requires at least one method")
	}
	var init initializeResult
	if err := json.Unmarshal(res, &init); err != nil {
		return fmt.Errorf("decode initialize result: %w", err)
	}
	if init.ProtocolVersion != workrpc.ProtocolVersion {
		return fmt.Errorf(
			"rpc protocol mismatch: server %q, client %q (server revision %q)",
			init.ProtocolVersion,
			workrpc.ProtocolVersion,
			serverRevision(init.Server.Revision),
		)
	}
	if want := workrpc.ProtocolSchemaHash(); init.ProtocolSchemaHash != want {
		if init.ProtocolSchemaHash == "" {
			return fmt.Errorf(
				"rpc server reported no protocolSchemaHash: client expected %s, server actual <missing> (server revision %q)",
				want,
				serverRevision(init.Server.Revision),
			)
		}
		return fmt.Errorf(
			"rpc protocol schema mismatch: client expected %s, server actual %s (server revision %q)",
			want,
			init.ProtocolSchemaHash,
			serverRevision(init.Server.Revision),
		)
	}
	for _, method := range profile.RequiredMethods {
		if !containsMethod(init.Methods, method) {
			return fmt.Errorf("rpc server does not expose required method %q (wrong wiring)", method)
		}
	}
	if !init.Capabilities[profile.Capability] {
		return fmt.Errorf("rpc server does not advertise the %s capability", profile.Capability)
	}
	return nil
}

func serverRevision(revision string) string {
	if revision = strings.TrimSpace(revision); revision != "" {
		return revision
	}
	return "unknown"
}

func containsMethod(methods []string, want string) bool {
	for _, method := range methods {
		if method == want {
			return true
		}
	}
	return false
}

func (c *conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	_, _ = c.request("rpc.shutdown", nil)
	_ = c.notify("rpc.exit", nil)
	if c.onClose != nil {
		return c.onClose()
	}
	return nil
}

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

// NewSubprocess starts binaryPath with `rpc --stdio` and connects using profile.
func NewSubprocess(ctx context.Context, binaryPath, dbPath string, extraEnv []string, profile Profile) (Transport, error) {
	args := []string{}
	if dbPath != "" {
		args = append(args, "--db", dbPath)
	}
	args = append(args, "rpc", "--stdio")
	cmd := exec.CommandContext(ctx, binaryPath, args...)
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
	c := &conn{w: stdin, br: bufio.NewReader(stdout), profile: profile}
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

type remoteTransport struct {
	mu      sync.Mutex
	client  *http.Client
	url     string
	token   string
	nextID  int
	closed  bool
	profile Profile
}

func NewRemote(endpoint, token string, profile Profile) (Transport, error) {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return nil, errors.New("remote endpoint is required")
	}
	t := &remoteTransport{
		client:  http.DefaultClient,
		url:     "http://" + endpoint + "/v1/rpc",
		token:   token,
		profile: profile,
	}
	if err := t.initialize(); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *remoteTransport) initialize() error {
	res, err := t.request(context.Background(), "rpc.initialize", map[string]string{"protocolVersion": workrpc.ProtocolVersion})
	if err != nil {
		return fmt.Errorf("rpc.initialize: %w", err)
	}
	return validateInitializeResult(res, t.profile)
}

func (t *remoteTransport) Call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errors.New("remote transport is closed")
	}
	return t.request(ctx, method, params)
}

func (t *remoteTransport) request(ctx context.Context, method string, params any) (json.RawMessage, error) {
	t.nextID++
	id := json.RawMessage(strconv.Itoa(t.nextID))
	var raw json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		raw = b
	}
	frame, err := json.Marshal(workrpc.Request{JSONRPC: "2.0", ID: id, Method: method, Params: raw})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("remote workrpc request %s: %w", method, err)
	}
	defer func() { _ = resp.Body.Close() }()
	var rpcResp workrpc.Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode remote workrpc response %s: %w", method, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if rpcResp.Error != nil {
			return nil, errorFromRPC(rpcResp.Error)
		}
		// An auth rejection is the one status where the caller cannot act without
		// knowing WHICH credential was sent, so name the source and what else was
		// available. Source and length only — never the token bytes.
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return nil, fmt.Errorf("remote workrpc HTTP %d (%s)", resp.StatusCode, workrpc.DescribeTokenSource(t.token))
		}
		return nil, fmt.Errorf("remote workrpc HTTP %d", resp.StatusCode)
	}
	if rpcResp.Error != nil {
		return nil, errorFromRPC(rpcResp.Error)
	}
	return rpcResp.Result, nil
}

func (t *remoteTransport) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
	return nil
}

// TokenFromEnv resolves wrkqd bearer credentials. config.Load preserves an
// explicit token-file reference from dotenv shadowing before this function is
// called, while an explicitly exported inline token retains precedence.
//
// Resolution lives in package workrpc so the stdio-forwarding path shares the
// same credential diagnostics; resolving also emits the one-per-process warning
// when an env token shadows a readable token file (T-06976).
func TokenFromEnv() string {
	token, _ := workrpc.ResolveToken()
	return token
}
