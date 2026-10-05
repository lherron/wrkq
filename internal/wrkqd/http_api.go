package wrkqd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/nodeauth"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/workrpc"
)

func (s *daemonServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", s.withAuth(s.handleHealth))
	mux.HandleFunc("/v1/rpc", s.withAuth(s.handleWorkRPC))
	mux.HandleFunc("/v1/containers/tree", s.withAuth(s.handleContainersTree))

	mux.HandleFunc("/v1/tasks/list", s.withAuth(s.handleTasksList))
	mux.HandleFunc("/v1/tasks/get", s.withAuth(s.handleTasksGet))
	mux.HandleFunc("/v1/tasks/create", s.withAuth(s.handleTasksCreate))
	mux.HandleFunc("/v1/tasks/update", s.withAuth(s.handleTasksUpdate))
	mux.HandleFunc("/v1/tasks/archive", s.withAuth(s.handleTasksArchive))
	mux.HandleFunc("/v1/tasks/restore", s.withAuth(s.handleTasksRestore))

	mux.HandleFunc("/v1/comments/list", s.withAuth(s.handleCommentsList))
	mux.HandleFunc("/v1/comments/create", s.withAuth(s.handleCommentsCreate))

	mux.HandleFunc("/v1/relations/list", s.withAuth(s.handleRelationsList))
	mux.HandleFunc("/v1/relations/create", s.withAuth(s.handleRelationsCreate))
	mux.HandleFunc("/v1/relations/delete", s.withAuth(s.handleRelationsDelete))

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

// decodePost admits only POST and decodes the JSON body into dst, answering
// 405 or 400 itself when either fails.
func (s *daemonServer) decodePost(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if !s.allowMethod(w, r, http.MethodPost) {
		return false
	}
	if err := s.decodeJSON(r, dst); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}

// resolveTask resolves a task selector, answering 404 when it names no task.
func (s *daemonServer) resolveTask(w http.ResponseWriter, selector string) (string, bool) {
	taskUUID, _, err := selectors.ResolveTask(s.db, selector)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return "", false
	}
	return taskUUID, true
}

// writeTaskDetail answers a task route with the task's detail view.
func (s *daemonServer) writeTaskDetail(w http.ResponseWriter, taskUUID string, includeComments, includeRelations bool) {
	task, err := loadTaskDetail(s.db, taskUUID, includeComments, includeRelations)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"task": task,
	})
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
		s.writeJSON(w, http.StatusBadRequest, resp)
		return
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
		s.writeJSON(w, http.StatusBadRequest, resp)
		return
	}
	// The rpc --stdio proxy forwards its launch principal (--principal-ref /
	// --as) here; it becomes what an empty per-frame principal defaults to, the
	// same role DefaultPrincipalRef plays for a local rpc --stdio (T-10328).
	principalRef, err := parsePrincipalHeader(r)
	if err != nil {
		resp := workrpc.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: workrpc.MapError(&workrpc.ValidationError{
				Message: err.Error(),
				Code:    workrpc.CodeWRKQValidation,
			}),
		}
		s.writeJSON(w, http.StatusOK, resp)
		return
	}
	ctx := attribution.WithCallerPrincipal(r.Context(), principalRef)
	resp, ok := s.workrpc.HandleRequest(ctx, req)
	if !ok {
		resp = workrpc.Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{}`)}
	}
	s.writeJSON(w, http.StatusOK, resp)
}

func (s *daemonServer) resolveAttribution(r *http.Request) (attribution.Attribution, error) {
	scopeRef, parsedScope, err := parseDaemonScopeRef(r)
	if err != nil {
		return attribution.Attribution{}, err
	}

	principalRef, err := parsePrincipalHeader(r)
	if err != nil {
		return attribution.Attribution{}, err
	}
	if principalRef != "" {
		return attribution.Attribution{PrincipalRef: principalRef, ScopeRef: scopeRef}, nil
	}

	if parsedScope != nil {
		principalRef, err := attribution.NormalizeCanonical("agent:" + parsedScope.AgentID)
		if err != nil {
			return attribution.Attribution{}, fmt.Errorf("derive principal from X-Wrkq-Scope-Ref: %w", err)
		}
		return attribution.Attribution{PrincipalRef: principalRef, ScopeRef: scopeRef}, nil
	}

	return attribution.Attribution{}, fmt.Errorf("no principal configured (set X-Wrkq-Principal-Ref or X-Wrkq-Scope-Ref)")
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

func parseDaemonScopeRef(r *http.Request) (string, *scope.ParsedScopeRef, error) {
	raw := strings.TrimSpace(r.Header.Get("X-Wrkq-Scope-Ref"))
	if raw == "" {
		return "", nil, nil
	}
	parsed, err := scope.ParseScopeRef(raw)
	if err != nil {
		return "", nil, fmt.Errorf("invalid X-Wrkq-Scope-Ref: %w", err)
	}
	return parsed.ScopeRef, &parsed, nil
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
