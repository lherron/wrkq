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
}

func NewServer(out io.Writer) *Server {
	return &Server{
		writer:   NewWriter(out),
		handlers: map[string]Handler{},
		maxBytes: DefaultMaxFrameBytes,
	}
}

func (s *Server) Register(method string, handler Handler) {
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
		if req.Method == "rpc.exit" {
			return nil
		}
		if req.Method == "$/cancelRequest" {
			continue
		}
		if req.Method == "rpc.shutdown" {
			s.shutdown = true
			if !req.isNotification() {
				_ = s.writeResult(req.ID, json.RawMessage(`{}`))
			}
			continue
		}
		if s.shutdown {
			if req.isNotification() {
				continue
			}
			_ = s.writeError(req.ID, MapError(NewValidationError("server is shutting down", nil)))
			continue
		}
		if !s.initialized && req.Method != "rpc.initialize" {
			if req.isNotification() {
				continue
			}
			_ = s.writeError(req.ID, MapError(NewValidationError("rpc.initialize must be called first", map[string]any{
				"method": req.Method,
			})))
			continue
		}
		handler, ok := s.handlers[req.Method]
		if !ok {
			if !req.isNotification() {
				_ = s.writeError(req.ID, protocolError(codeMethodNotFound, "method not found", nil))
			}
			continue
		}
		result, err := callStdoutPure(ctx, handler, req.Params)
		if err != nil {
			if !req.isNotification() {
				_ = s.writeError(req.ID, MapError(err))
			}
			continue
		}
		if req.Method == "rpc.initialize" {
			s.initialized = true
		}
		if req.isNotification() {
			continue
		}
		_ = s.writeResult(req.ID, result)
	}
}

func (r Request) isNotification() bool {
	return len(r.ID) == 0
}

func (s *Server) writeResult(id json.RawMessage, result json.RawMessage) error {
	if len(id) == 0 {
		id = json.RawMessage(`null`)
	}
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	return s.writer.WriteResponse(Response{JSONRPC: "2.0", ID: id, Result: result})
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

func callStdoutPure(ctx context.Context, handler Handler, params json.RawMessage) (json.RawMessage, error) {
	stdoutRedirectMu.Lock()
	defer stdoutRedirectMu.Unlock()

	orig := os.Stdout
	os.Stdout = os.Stderr
	defer func() {
		os.Stdout = orig
	}()

	return handler.HandleRPC(ctx, params)
}
