package wrkqd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/nodeauth"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/wrkqapi"
)

func (s *daemonServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", s.withAuth(s.handleHealth))
	mux.HandleFunc("/v1/rpc", s.withAuth(s.handleWorkRPC))
}

func (s *daemonServer) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Per-node identity supersedes the shared token: every request must
		// carry a token that resolves to exactly one server-side nodeId.
		if s.nodes.Enabled() {
			node, ok := s.nodes.Resolve(requestToken(r))
			if !ok {
				s.writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
				return
			}
			next(w, r.WithContext(nodeauth.WithNode(r.Context(), node)))
			return
		}

		if s.token != "" {
			if requestToken(r) != s.token {
				s.writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
				return
			}
		}

		next(w, r)
	}
}

func requestToken(r *http.Request) string {
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if token == "" {
		token = r.Header.Get("X-Wrkqd-Token")
	}
	return token
}

func (s *daemonServer) decodeJSON(r *http.Request, dst interface{}) error {
	decoder := json.NewDecoder(r.Body)
	return decoder.Decode(dst)
}

func (s *daemonServer) writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func (s *daemonServer) writeError(w http.ResponseWriter, status int, err error) {
	s.writeJSON(w, status, map[string]interface{}{
		"message": err.Error(),
	})
}

// allowMethod admits only the given HTTP method, answering 405 otherwise.
func (s *daemonServer) allowMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return false
	}
	return true
}

func (s *daemonServer) handleWorkRPC(w http.ResponseWriter, r *http.Request) {
	if !s.allowMethod(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, workrpc.DefaultMaxFrameBytes)
	var req workrpc.Request
	if err := s.decodeJSON(r, &req); err != nil {
		resp := workrpc.Response{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`null`),
			Error:   workrpc.MapError(&workrpc.ParseError{Err: err}),
		}
		s.writeRPCResponse(w, r, http.StatusBadRequest, req.Method, resp)
		return
	}
	if note, ok := r.Context().Value(rpcLogKey{}).(*rpcLogNote); ok {
		note.caller = req.Caller
	}
	if len(req.ID) == 0 {
		resp := workrpc.Response{
			JSONRPC: "2.0",
			ID:      json.RawMessage(`null`),
			Error: workrpc.MapError(&workrpc.ValidationError{
				Message: "remote workrpc requires request id",
				Code:    workrpc.CodeWRKQValidation,
			}),
		}
		s.writeRPCResponse(w, r, http.StatusBadRequest, req.Method, resp)
		return
	}
	// The rpc --stdio proxy forwards its launch principal (--principal-ref /
	// --as) here; it becomes what an empty per-frame principal defaults to, the
	// same role DefaultPrincipalRef plays for a local rpc --stdio (T-10328).
	principalRef, err := parsePrincipalHeader(r)
	var callerScope string
	if err == nil {
		callerScope, err = wrkqapi.NormalizeCallerScopeRef(r.Header.Get("X-Wrkq-Scope-Ref"))
	}
	if err != nil {
		resp := workrpc.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: workrpc.MapError(&workrpc.ValidationError{
				Message: err.Error(),
				Code:    workrpc.CodeWRKQValidation,
			}),
		}
		s.writeRPCResponse(w, r, http.StatusOK, req.Method, resp)
		return
	}
	ctx := attribution.WithCallerScope(attribution.WithCallerPrincipal(r.Context(), principalRef), callerScope)
	resp, ok := s.workrpc.HandleRequest(ctx, req)
	if !ok {
		resp = workrpc.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
	}
	s.writeRPCResponse(w, r, http.StatusOK, req.Method, resp)
}

// writeRPCResponse answers one /v1/rpc frame and tells the request log which
// method it served and whether the frame failed, which HTTP 200 hides.
func (s *daemonServer) writeRPCResponse(w http.ResponseWriter, r *http.Request, status int, method string, resp workrpc.Response) {
	noteRPC(r.Context(), method, resp.Error)
	s.writeJSON(w, status, resp)
}

// parsePrincipalHeader reads X-Wrkq-Principal-Ref, the caller principal a
// client sends with its request. Empty means the header is absent; a malformed
// value is refused, never ignored.
func parsePrincipalHeader(r *http.Request) (string, error) {
	raw := strings.TrimSpace(r.Header.Get("X-Wrkq-Principal-Ref"))
	if raw == "" {
		return "", nil
	}
	principalRef, err := attribution.NormalizeCanonical(raw)
	if err != nil {
		return "", fmt.Errorf("invalid X-Wrkq-Principal-Ref: %w", err)
	}
	return principalRef, nil
}

func (s *daemonServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.allowMethod(w, r, http.MethodGet) {
		return
	}

	payload := map[string]interface{}{
		"ok":   true,
		"time": time.Now().UTC().Format(time.RFC3339),
	}
	// The nodeId the caller authenticated as, so an operator can prove the
	// identity boundary live without trusting anything the caller sent.
	if node, ok := nodeauth.FromContext(r.Context()); ok {
		payload["node"] = node
	}
	s.writeJSON(w, http.StatusOK, payload)
}
