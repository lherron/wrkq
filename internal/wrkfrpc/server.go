package wrkfrpc

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sort"
	"sync"

	"github.com/lherron/wrkq/internal/wrkfapi"
)

type Handler interface {
	HandleRPC(context.Context, json.RawMessage) (json.RawMessage, error)
}

type HandlerFunc func(context.Context, json.RawMessage) (json.RawMessage, error)

func (f HandlerFunc) HandleRPC(ctx context.Context, params json.RawMessage) (json.RawMessage, error) {
	return f(ctx, params)
}

type Server struct {
	out         io.Writer
	writer      *Writer
	handlers    map[string]Handler
	maxBytes    int
	initialized bool
	shutdown    bool
}

func NewServer(out io.Writer) *Server {
	return &Server{
		out:      out,
		writer:   NewWriter(out),
		handlers: map[string]Handler{},
		maxBytes: DefaultMaxFrameBytes,
	}
}

func (s *Server) Register(method string, handler Handler) {
	s.handlers[method] = handler
}

// RegisteredMethods returns the sorted list of method names currently registered
// with this server. This is an introspection seam intended for contract tests only.
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
		if req.Method == "wrkf.exit" {
			return nil
		}
		if req.Method == "$/cancelRequest" {
			continue
		}
		if s.shutdown {
			if req.isNotification() {
				continue
			}
			_ = s.writeError(req.ID, MapError(wrkfapi.NewValidationError("server is shutting down", nil)))
			continue
		}
		if !s.initialized && req.Method != "wrkf.initialize" {
			if req.isNotification() {
				continue
			}
			_ = s.writeError(req.ID, MapError(wrkfapi.NewValidationError("wrkf.initialize must be called first", nil)))
			continue
		}
		if req.Method == "wrkf.shutdown" {
			s.shutdown = true
			if !req.isNotification() {
				_ = s.writeResult(req.ID, json.RawMessage(`{}`))
			}
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
		if req.Method == "wrkf.initialize" {
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
	if len(result) == 0 {
		result = json.RawMessage(`null`)
	}
	return s.writer.WriteResponse(Response{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) writeError(id json.RawMessage, rpcErr *RPCError) error {
	if rpcErr == nil {
		rpcErr = MapError(wrkfapi.NewInternalError(nil))
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

	result, err := handler.HandleRPC(ctx, params)
	return result, err
}
