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
	reads       readBound
}

func NewServer(out io.Writer) *Server {
	return &Server{
		writer:   NewWriter(out),
		handlers: map[string]Handler{},
		maxBytes: DefaultMaxFrameBytes,
		reads:    readBound{deadline: ReadDeadline, maxAbandoned: MaxAbandonedReads},
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
		resp, ok := s.dispatch(ctx, req, callStdoutPure, false)
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
//
// Requests run concurrently: nothing here serializes one handler behind
// another (T-09997). Writes are serialized by SQLite itself
// (_txlock=immediate + busy_timeout); bounded reads are cut off at the read
// deadline here (HTTP only). The process stdout is not the frame channel for an HTTP
// transport, so no stdout isolation is applied — wrkqd points os.Stdout at
// stderr once at startup instead.
func (s *Server) HandleRequest(ctx context.Context, req Request) (Response, bool) {
	return s.dispatch(ctx, req, callHandler, true)
}

// invokeFunc runs one handler. The stdio transport wraps the call in stdout
// isolation; HTTP calls it directly.
type invokeFunc func(ctx context.Context, method string, handler Handler, params json.RawMessage) (json.RawMessage, error)

func callHandler(ctx context.Context, _ string, handler Handler, params json.RawMessage) (json.RawMessage, error) {
	return handler.HandleRPC(ctx, params)
}

// dispatch runs one request. bounded applies the read deadline; the stdio
// transport leaves it off because an abandoned read there would keep holding
// the stdout swap, stalling the very stream it was cut off to protect.
func (s *Server) dispatch(ctx context.Context, req Request, invoke invokeFunc, bounded bool) (Response, bool) {
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
	var (
		result json.RawMessage
		err    error
	)
	if bounded && isBoundedRead(req.Method) {
		result, err = s.reads.run(withMethod(ctx, req.Method), req.Method, handler, req.Params, invoke)
	} else {
		result, err = invoke(withMethod(ctx, req.Method), req.Method, handler, req.Params)
	}
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

// stdoutRedirectMu guards the process-global os.Stdout swap. Only the stdio
// transport takes it, where the process stdout can be the frame channel and a
// stray handler print would corrupt the stream; that transport is already one
// request at a time, so the lock is contended only across stdio streams
// sharing a process.
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
