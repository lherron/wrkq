//go:build wrkq_local

package workrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"sync"
)

type Handler interface {
	HandleRPC(context.Context, json.RawMessage) (json.RawMessage, error)
}

type HandlerFunc func(context.Context, json.RawMessage) (json.RawMessage, error)

func (f HandlerFunc) HandleRPC(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	return f(ctx, params)
}

type Server struct {
	writer      *Writer
	handlers    map[string]Handler
	maxBytes    int
	initialized bool
	shutdown    bool
	mu          sync.Mutex
}

func NewServer(out io.Writer) *Server {
	return &Server{
		writer:   NewWriter(out),
		handlers: map[string]Handler{},
		maxBytes: DefaultMaxFrameBytes,
	}
}

func (s *Server) Register(method string, handler Handler) {
	if isWrkfDomainMethod(method) {
		handler = guardLegacyActorParams(handler)
	}
	s.handlers[method] = handler
}

func (s *Server) RegisteredMethods() []string {
	methods := make([]string, 0, len(s.handlers))
	for m := range s.handlers {
		methods = append(methods, m)
	}
	sort.Strings(methods)
	return methods
}

func (s *Server) Serve(ctx context.Context, in io.Reader) error {
	reader := NewReader(in, s.maxBytes)
	for {
		req, err := reader.ReadRequest()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			var parseErr *ParseError
			if errors.As(err, &parseErr) {
				_ = s.writeError(nil, protocolError(codeParseError, "parse error", nil))
			} else {
				_ = s.writeError(nil, MapError(err))
			}
			continue
		}
		resp, ok := s.HandleRequest(ctx, req)
		if !ok {
			return nil
		}
		if req.isNotification() {
			continue
		}
		_ = s.writer.WriteResponse(resp)
	}
}

// HandleRequest dispatches a single JSON-RPC request against this server's
// registry. It returns ok=false only for rpc.exit, which terminates streaming
// transports. HTTP transports can reject notifications before calling this.
func (s *Server) HandleRequest(ctx context.Context, req Request) (Response, bool) {
	s.mu.Lock()
	if req.Method == "rpc.exit" {
		s.mu.Unlock()
		return Response{}, false
	}
	if req.Method == "$/cancelRequest" {
		s.mu.Unlock()
		return Response{}, true
	}
	if req.Method == "rpc.shutdown" {
		s.shutdown = true
		s.mu.Unlock()
		return Response{JSONRPC: "2.0", ID: responseID(req.ID), Result: json.RawMessage(`{}`)}, true
	}
	if s.shutdown {
		s.mu.Unlock()
		if req.isNotification() {
			return Response{}, true
		}
		return Response{JSONRPC: "2.0", ID: responseID(req.ID), Error: MapError(NewValidationError("server is shutting down", nil))}, true
	}
	if !s.initialized && req.Method != "rpc.initialize" {
		s.mu.Unlock()
		if req.isNotification() {
			return Response{}, true
		}
		return Response{JSONRPC: "2.0", ID: responseID(req.ID), Error: MapError(NewValidationError("rpc.initialize must be called first", map[string]any{
			"method": req.Method,
		}))}, true
	}
	handler, ok := s.handlers[req.Method]
	s.mu.Unlock()
	if !ok {
		if req.isNotification() {
			return Response{}, true
		}
		return Response{JSONRPC: "2.0", ID: responseID(req.ID), Error: protocolError(codeMethodNotFound, "method not found", nil)}, true
	}
	result, err := callStdoutPure(ctx, req.Method, handler, req.Params)
	if err != nil {
		if req.isNotification() {
			return Response{}, true
		}
		return Response{JSONRPC: "2.0", ID: responseID(req.ID), Error: MapError(err)}, true
	}
	if req.Method == "rpc.initialize" {
		s.mu.Lock()
		s.initialized = true
		s.mu.Unlock()
	}
	if req.isNotification() {
		return Response{}, true
	}
	return Response{JSONRPC: "2.0", ID: responseID(req.ID), Result: resultOrNull(result)}, true
}

func (r Request) isNotification() bool {
	return len(r.ID) == 0
}

func responseID(id json.RawMessage) json.RawMessage {
	if len(id) == 0 {
		return json.RawMessage(`null`)
	}
	return id
}

func resultOrNull(result json.RawMessage) json.RawMessage {
	if len(result) == 0 {
		return json.RawMessage(`null`)
	}
	return result
}

func (s *Server) writeError(id json.RawMessage, rpcErr *RPCError) error {
	if len(id) == 0 {
		id = json.RawMessage(`null`)
	}
	if rpcErr == nil {
		rpcErr = MapError(NewDomainError(CodeWorkRPCInternal, "internal error", false, nil))
	}
	return s.writer.WriteResponse(Response{JSONRPC: "2.0", ID: id, Error: rpcErr})
}

var stdoutRedirectMu sync.Mutex

func callStdoutPure(ctx context.Context, method string, handler Handler, params json.RawMessage) (json.RawMessage, error) {
	// External execution methods capture child-process stdout themselves. They
	// must not hold the process-global stdout redirect while waiting on the
	// child, otherwise one slow hook serializes every unrelated RPC request.
	if executesExternalProcess(method) {
		return handler.HandleRPC(ctx, params)
	}

	stdoutRedirectMu.Lock()
	defer stdoutRedirectMu.Unlock()

	orig := os.Stdout
	os.Stdout = os.Stderr
	defer func() {
		os.Stdout = orig
	}()

	return handler.HandleRPC(ctx, params)
}

func executesExternalProcess(method string) bool {
	switch method {
	case "wrkf.check.run", "wrkf.hook.run", "wrkf.effect.deliver":
		return true
	default:
		return false
	}
}