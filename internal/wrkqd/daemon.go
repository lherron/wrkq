package wrkqd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/nodeauth"
	"github.com/lherron/wrkq/internal/scope"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// DaemonOptions configures the wrkqd daemon.
type DaemonOptions struct {
	Addr          string
	Unix          string
	Token         string
	DBPath        string
	PIDPath       string
	UnsafeNoToken bool
	// NodeTokens maps bearer tokens to logical nodeIds inline
	// (`nodeId=token,nodeId=token`); NodeTokensFile reads the same grammar
	// from disk. Either one enables per-node identity, which supersedes the
	// shared Token.
	NodeTokens     string
	NodeTokensFile string
}

// ServeDaemon starts the wrkqd daemon.
func ServeDaemon(opts DaemonOptions) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	nodes, err := loadNodeRegistry(opts)
	if err != nil {
		return err
	}

	if opts.DBPath != "" {
		if err := config.ApplyDBLocator(cfg, opts.DBPath, true); err != nil {
			return err
		}
	}
	if cfg.RemoteEndpoint != "" {
		return fmt.Errorf("wrkqd requires a local database path; WRKQ_DB_PATH and --db must not be rpc:// locators")
	}
	if cfg.DBPath == "" {
		return config.MissingDatabasePathError()
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	if err := database.RequiresMigrationError(); err != nil {
		_ = database.Close()
		return err
	}
	defer func() { _ = database.Close() }()

	if opts.PIDPath != "" {
		if err := writePIDFile(opts.PIDPath); err != nil {
			return err
		}
		defer func() { _ = os.Remove(opts.PIDPath) }()
	}
	api, rpcOpts, err := bootstrap.DaemonServer(database, cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize workrpc registry: %w", err)
	}
	rpcOpts.ServerVersion = Version
	rpcOpts.ServerRevision = GitCommit
	rpcServer := workrpc.NewServer(io.Discard)
	workrpc.RegisterAPI(rpcServer, api, rpcOpts)

	server := &daemonServer{
		db:       database,
		cfg:      cfg,
		token:    opts.Token,
		nodes:    nodes,
		workrpc:  rpcServer,
		rpcToken: opts.Token,
	}
	server.startSearchIndexer()

	mux := http.NewServeMux()
	server.registerRoutes(mux)

	httpServer := &http.Server{
		Handler:      mux,
		ReadTimeout:  workrpc.HTTPResponseTimeout,
		WriteTimeout: workrpc.HTTPResponseTimeout,
	}

	if opts.Unix != "" {
		_ = os.Remove(opts.Unix)
		listener, err := net.Listen("unix", opts.Unix)
		if err != nil {
			return fmt.Errorf("failed to listen on unix socket: %w", err)
		}
		defer func() { _ = listener.Close() }()
		return serveHTTPWithSignals(httpServer, listener)
	}

	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:7171"
	}
	if opts.Unix == "" && opts.Token == "" && !nodes.Enabled() && !opts.UnsafeNoToken && !isLoopbackListenAddr(addr) {
		return fmt.Errorf("refusing to bind wrkqd on non-loopback address %s without --token; pass --unsafe-no-token for explicit dev-only override", addr)
	}
	httpServer.Addr = addr

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = listener.Close() }()
	return serveHTTPWithSignals(httpServer, listener)
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(host, "[]")
	if host == "" || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func writePIDFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create pid directory: %w", err)
	}
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}

func serveHTTPWithSignals(server *http.Server, listener net.Listener) error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()

	sigCh := make(chan os.Signal, 1)
	signalNotify(sigCh)
	defer signalStop(sigCh)

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-sigCh:
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			_ = server.Close()
			return err
		}
		if err := <-errCh; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

var (
	signalNotify = func(ch chan<- os.Signal) {
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
	}
	signalStop = signal.Stop
)

type daemonServer struct {
	db       *db.DB
	cfg      *config.Config
	token    string
	nodes    *nodeauth.Registry
	workrpc  *workrpc.Server
	rpcToken string
}

// Task mirrors wrkq cat --json output with additional deleted_at metadata.
type Task struct {
	ID                    string     `json:"id"`
	UUID                  string     `json:"uuid"`
	ArtifactDir           string     `json:"artifact_dir"`
	ProjectID             string     `json:"project_id"`
	ProjectUUID           string     `json:"project_uuid"`
	Slug                  string     `json:"slug"`
	Title                 string     `json:"title"`
	State                 string     `json:"state"`
	Priority              int        `json:"priority"`
	Kind                  string     `json:"kind"`
	ParentTaskID          *string    `json:"parent_task_id,omitempty"`
	ParentTaskUUID        *string    `json:"parent_task_uuid,omitempty"`
	AssigneeSlug          *string    `json:"assignee,omitempty"`
	AssigneePrincipalRef  *string    `json:"assignee_principal_ref,omitempty"`
	StartAt               *string    `json:"start_at,omitempty"`
	DueAt                 *string    `json:"due_at,omitempty"`
	Labels                *string    `json:"labels,omitempty"`
	Description           string     `json:"description"`
	Specification         string     `json:"specification"`
	Etag                  int64      `json:"etag"`
	CreatedAt             string     `json:"created_at"`
	UpdatedAt             string     `json:"updated_at"`
	CompletedAt           *string    `json:"completed_at,omitempty"`
	ArchivedAt            *string    `json:"archived_at,omitempty"`
	DeletedAt             *string    `json:"deleted_at,omitempty"`
	CreatedBy             string     `json:"created_by"`
	UpdatedBy             string     `json:"updated_by"`
	CreatedByPrincipalRef string     `json:"created_by_principal_ref,omitempty"`
	UpdatedByPrincipalRef string     `json:"updated_by_principal_ref,omitempty"`
	CreatedByScopeRef     string     `json:"created_by_scope_ref,omitempty"`
	UpdatedByScopeRef     string     `json:"updated_by_scope_ref,omitempty"`
	Comments              []Comment  `json:"comments,omitempty"`
	Relations             []Relation `json:"relations,omitempty"`
}

type Comment struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	Body         string `json:"body"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	ScopeRef     string `json:"scope_ref,omitempty"`
	Author       string `json:"author,omitempty"`
}

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

// loadNodeRegistry builds the per-node token registry from whichever source
// the operator configured. Configuring both an inline spec and a file is a
// config error rather than a silent precedence rule.
func loadNodeRegistry(opts DaemonOptions) (*nodeauth.Registry, error) {
	spec := strings.TrimSpace(opts.NodeTokens)
	file := strings.TrimSpace(opts.NodeTokensFile)
	switch {
	case spec != "" && file != "":
		return nil, fmt.Errorf("configure node tokens inline or by file, not both")
	case file != "":
		return nodeauth.LoadFile(file)
	case spec != "":
		return nodeauth.ParseSpec(spec)
	default:
		return nil, nil
	}
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

func (s *daemonServer) handleWorkRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
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
	resp, ok := s.workrpc.HandleRequest(r.Context(), req)
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

	if raw := strings.TrimSpace(r.Header.Get("X-Wrkq-Principal-Ref")); raw != "" {
		principalRef, err := attribution.NormalizeCanonical(raw)
		if err != nil {
			return attribution.Attribution{}, fmt.Errorf("invalid X-Wrkq-Principal-Ref: %w", err)
		}
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
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
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

type containersTreeRequest struct {
	Path            string `json:"path,omitempty"`
	Depth           int    `json:"depth,omitempty"`
	IncludeArchived bool   `json:"include_archived,omitempty"`
	OpenOnly        bool   `json:"open_only,omitempty"`
}

func (s *daemonServer) handleContainersTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req containersTreeRequest
	if err := s.decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	rootPath := strings.Trim(req.Path, "/")
	root, err := buildTree(s.db, rootPath, req.Depth, req.IncludeArchived, req.OpenOnly, !req.IncludeArchived, 0)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	path := rootPath
	if path == "" {
		path = "."
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":                            path,
		"children":                        root.Children,
		"hidden_containers_not_displayed": root.HiddenContainerCount,
	})
}

type tasksListRequest struct {
	Project    string   `json:"project,omitempty"`
	Filter     string   `json:"filter,omitempty"`
	Sort       string   `json:"sort,omitempty"`
	Direction  string   `json:"direction,omitempty"`
	Limit      int      `json:"limit,omitempty"`
	Cursor     string   `json:"cursor,omitempty"`
	PathPrefix []string `json:"path_prefix,omitempty"`
	Assignee   string   `json:"assignee,omitempty"`
	Kind       string   `json:"kind,omitempty"`
	ParentTask string   `json:"parent_task,omitempty"`
	DueBefore  string   `json:"due_before,omitempty"`
	DueAfter   string   `json:"due_after,omitempty"`
	SlugGlob   string   `json:"slug_glob,omitempty"`
}

func (s *daemonServer) handleTasksList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req tasksListRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	var pathsFilter []string

	if req.Project != "" {
		projectUUID, _, err := selectors.ResolveContainer(s.db, req.Project)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		var projectPath string
		if err := s.db.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", projectUUID).Scan(&projectPath); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		pathsFilter = append(pathsFilter, projectPath)
	}

	for _, prefix := range req.PathPrefix {
		trimmed := strings.Trim(prefix, "/")
		if trimmed != "" {
			pathsFilter = append(pathsFilter, trimmed)
		}
	}

	var assigneePrincipalRef string
	if req.Assignee != "" {
		principalRef, err := attribution.NormalizeCompat(req.Assignee)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		assigneePrincipalRef = principalRef
	}

	var parentTaskUUID string
	if req.ParentTask != "" {
		uuid, _, err := selectors.ResolveTask(s.db, req.ParentTask)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		parentTaskUUID = uuid
	}

	stateFilter := ""
	switch req.Filter {
	case "all":
		stateFilter = "all"
	case "deleted":
		stateFilter = "deleted"
	case "active", "":
		stateFilter = ""
	default:
		stateFilter = req.Filter
	}

	opts := findOptions{
		paths:                pathsFilter,
		typeFilter:           "t",
		slugGlob:             req.SlugGlob,
		state:                stateFilter,
		dueBefore:            req.DueBefore,
		dueAfter:             req.DueAfter,
		kind:                 req.Kind,
		assigneePrincipalRef: assigneePrincipalRef,
		parentTaskUUID:       parentTaskUUID,
		limit:                req.Limit,
		cursor:               req.Cursor,
	}

	results, hasMore, err := findTasks(s.db, opts, false)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	var nextCursor string
	if hasMore && len(results) > 0 {
		lastEntry := results[len(results)-1]
		nextCursor, _ = cursor.BuildNextCursor(
			[]string{"updated_at"},
			[]interface{}{lastEntry.UpdatedAt},
			lastEntry.ID,
		)
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"tasks":       results,
		"next_cursor": nextCursor,
	})
}

type taskGetRequest struct {
	Selector         string `json:"selector"`
	IncludeComments  *bool  `json:"include_comments,omitempty"`
	IncludeRelations *bool  `json:"include_relations,omitempty"`
}

func (s *daemonServer) handleTasksGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req taskGetRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Selector == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("selector required"))
		return
	}

	taskUUID, _, err := selectors.ResolveTask(s.db, req.Selector)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	includeComments := true
	includeRelations := true
	if req.IncludeComments != nil {
		includeComments = *req.IncludeComments
	}
	if req.IncludeRelations != nil {
		includeRelations = *req.IncludeRelations
	}

	task, err := loadTaskDetail(s.db, taskUUID, includeComments, includeRelations)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"task": task,
	})
}

type taskCreateRequest struct {
	Path      string                 `json:"path"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	ForceUUID string                 `json:"force_uuid,omitempty"`
}

func (s *daemonServer) handleTasksCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req taskCreateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Path == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("path required"))
		return
	}
	if req.ForceUUID != "" {
		if err := domain.ValidateUUID(req.ForceUUID); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	parentUUID, normalizedSlug, _, err := selectors.ResolveParentContainer(s.db, req.Path)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	fields := req.Fields
	if fields == nil {
		fields = map[string]interface{}{}
	}

	title := getStringField(fields, "title", normalizedSlug)
	description := getStringField(fields, "description", "")
	specification := getStringField(fields, "specification", "")
	state := getStringField(fields, "state", "open")
	parsedState, err := domain.ParseState(state)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	priority := getIntField(fields, "priority", 3)
	kind := getStringField(fields, "kind", "")
	labels := getLabelsField(fields, "labels")
	dueAt := getStringField(fields, "due_at", "")
	startAt := getStringField(fields, "start_at", "")

	var parentTaskUUID *string
	if parentTask := getStringField(fields, "parent_task", ""); parentTask != "" {
		uuid, _, err := selectors.ResolveTask(s.db, parentTask)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		parentTaskUUID = &uuid
	}

	var assigneePrincipalRef *string
	if assignee := getStringField(fields, "assignee", ""); assignee != "" {
		principalRef, err := attribution.NormalizeCompat(assignee)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		assigneePrincipalRef = &principalRef
	}

	projectUUID := ""
	if parentUUID != nil {
		projectUUID = *parentUUID
	} else {
		if err := s.db.QueryRow(`SELECT uuid FROM containers WHERE kind = 'project' LIMIT 1`).Scan(&projectUUID); err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Errorf("no project found"))
			return
		}
	}

	svc := store.New(s.db)
	result, err := svc.Tasks.CreateWithAttribution(attr, store.CreateParams{
		UUID:                 req.ForceUUID,
		Slug:                 normalizedSlug,
		Title:                title,
		Description:          description,
		Specification:        specification,
		ProjectUUID:          projectUUID,
		State:                parsedState,
		Priority:             priority,
		Kind:                 kind,
		ParentTaskUUID:       parentTaskUUID,
		AssigneePrincipalRef: assigneePrincipalRef,
		Labels:               labels,
		DueAt:                dueAt,
		StartAt:              startAt,
		Via:                  "api",
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	task, err := loadTaskDetail(s.db, result.UUID, true, true)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"task": task,
	})
}

type taskUpdateRequest struct {
	Selector string                 `json:"selector"`
	Fields   map[string]interface{} `json:"fields,omitempty"`
	IfMatch  int64                  `json:"ifMatch,omitempty"`
}

func (s *daemonServer) handleTasksUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req taskUpdateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Selector == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("selector required"))
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	taskUUID, _, err := selectors.ResolveTask(s.db, req.Selector)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	fields := map[string]interface{}{}
	for key, value := range req.Fields {
		switch key {
		case "title", "state", "description", "specification", "due_at", "start_at":
			if s, ok := value.(string); ok {
				fields[key] = s
			}
		case "labels":
			fields["labels"] = getLabelsField(req.Fields, "labels")
		case "priority":
			if p, ok := coerceInt(value); ok {
				fields["priority"] = p
			}
		case "assignee":
			if assignee, ok := value.(string); ok {
				if assignee == "" {
					fields["assignee_principal_ref"] = nil
					continue
				}
				principalRef, err := attribution.NormalizeCompat(assignee)
				if err != nil {
					s.writeError(w, http.StatusBadRequest, err)
					return
				}
				fields["assignee_principal_ref"] = principalRef
			}
		}
	}

	if len(fields) == 0 {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("no valid fields to update"))
		return
	}

	svc := store.New(s.db)
	if _, err := svc.Tasks.UpdateFieldsWithViaAttribution(attr, taskUUID, fields, req.IfMatch, "api"); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	task, err := loadTaskDetail(s.db, taskUUID, true, true)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"task": task,
	})
}

type taskArchiveRequest struct {
	Selector string `json:"selector"`
	IfMatch  int64  `json:"ifMatch,omitempty"`
}

func (s *daemonServer) handleTasksArchive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req taskArchiveRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Selector == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("selector required"))
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	taskUUID, _, err := selectors.ResolveTask(s.db, req.Selector)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	svc := store.New(s.db)
	if _, err := svc.Tasks.ArchiveWithViaAttribution(attr, taskUUID, req.IfMatch, "api"); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	task, err := loadTaskDetail(s.db, taskUUID, true, true)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"task": task,
	})
}

type taskRestoreRequest struct {
	Selector string                 `json:"selector"`
	State    string                 `json:"state,omitempty"`
	IfMatch  int64                  `json:"ifMatch,omitempty"`
	Fields   map[string]interface{} `json:"fields,omitempty"`
}

func (s *daemonServer) handleTasksRestore(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req taskRestoreRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Selector == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("selector required"))
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	taskUUID, _, err := selectors.ResolveTask(s.db, req.Selector)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	targetState := req.State
	if targetState == "" {
		targetState = "open"
	}
	parsedTargetState, err := domain.ParseState(targetState)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if parsedTargetState == domain.StateArchived || parsedTargetState == domain.StateDeleted {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("cannot restore to %s state", targetState))
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	var currentState string
	var currentETag int64
	if err := tx.QueryRow("SELECT state, etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentState, &currentETag); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if currentState != "archived" && currentState != "deleted" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("task is not deleted or archived (current state: %s)", currentState))
		return
	}

	if req.IfMatch != 0 && req.IfMatch != currentETag {
		s.writeError(w, http.StatusConflict, fmt.Errorf("etag mismatch: expected %d, got %d", req.IfMatch, currentETag))
		return
	}

	fields := map[string]interface{}{
		"state":       string(parsedTargetState),
		"archived_at": nil,
		"deleted_at":  nil,
	}

	for key, value := range req.Fields {
		switch key {
		case "title", "description", "specification", "labels", "due_at", "start_at":
			fields[key] = value
		case "priority":
			if p, ok := coerceInt(value); ok {
				fields["priority"] = p
			}
		}
	}

	setClauses := []string{}
	args := []interface{}{}
	for key, value := range fields {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", key))
		args = append(args, value)
	}

	setClauses = append(setClauses, "etag = etag + 1")
	setClauses = append(setClauses, "updated_by_principal_ref = ?")
	setClauses = append(setClauses, "updated_by_scope_ref = ?")
	args = append(args, attr.PrincipalRef, scopeBind(attr), taskUUID)

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
	if _, err := tx.Exec(query, args...); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	newETag := currentETag + 1
	payloadJSON, _ := json.Marshal(fields)
	payloadStr := string(payloadJSON)
	eventMeta, err := events.NewWriter(s.db.DB).LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.updated",
		ETag:         &newETag,
		Payload:      &payloadStr,
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	webhooks.DispatchTaskEvent(s.db, taskUUID, webhooks.EventContext{
		Metadata:     eventMeta,
		Event:        "updated",
		PrincipalRef: attr.PrincipalRef,
		Via:          "api",
		Transition:   &webhooks.Transition{From: &currentState, To: &targetState},
		Changed:      sortedMapKeys(fields),
		Changes: mapChanges(fields, map[string]interface{}{
			"state": currentState,
		}),
	})

	task, err := loadTaskDetail(s.db, taskUUID, true, true)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"task": task,
	})
}

type commentsListRequest struct {
	Task           string `json:"task"`
	IncludeDeleted bool   `json:"include_deleted,omitempty"`
}

func (s *daemonServer) handleCommentsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req commentsListRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if req.Task == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("task required"))
		return
	}

	taskUUID, _, err := selectors.ResolveTask(s.db, req.Task)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	query := `
		SELECT c.uuid, c.id, c.task_uuid, c.body, c.meta, c.etag,
		       c.created_at, c.updated_at, c.deleted_at,
		       c.created_by_principal_ref, c.created_by_scope_ref,
		       c.deleted_by_principal_ref, c.deleted_by_scope_ref,
		       t.id as task_id
		FROM comments c
		LEFT JOIN tasks t ON c.task_uuid = t.uuid
		WHERE c.task_uuid = ?
	`
	args := []interface{}{taskUUID}
	if !req.IncludeDeleted {
		query += " AND c.deleted_at IS NULL"
	}
	query += " ORDER BY c.created_at ASC"

	rows, err := s.db.Query(query, args...)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = rows.Close() }()

	var comments []map[string]interface{}
	for rows.Next() {
		var uuid, id, taskUUID, body, createdAt string
		var taskIDStr string
		var meta, updatedAt, deletedAt sql.NullString
		var createdByPrincipalRef, createdByScopeRef sql.NullString
		var deletedByPrincipalRef, deletedByScopeRef sql.NullString
		var etag int64

		if err := rows.Scan(&uuid, &id, &taskUUID, &body, &meta, &etag,
			&createdAt, &updatedAt, &deletedAt,
			&createdByPrincipalRef, &createdByScopeRef,
			&deletedByPrincipalRef, &deletedByScopeRef,
			&taskIDStr); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}

		comment := map[string]interface{}{
			"uuid":       uuid,
			"id":         id,
			"task_uuid":  taskUUID,
			"task_id":    taskIDStr,
			"body":       body,
			"etag":       etag,
			"created_at": createdAt,
		}
		if createdByPrincipalRef.Valid {
			comment["created_by_principal_ref"] = createdByPrincipalRef.String
		}
		if createdByScopeRef.Valid {
			comment["created_by_scope_ref"] = createdByScopeRef.String
		}

		if meta.Valid && meta.String != "" {
			comment["meta"] = meta.String
		}
		if updatedAt.Valid {
			comment["updated_at"] = updatedAt.String
		}
		if deletedAt.Valid {
			comment["deleted_at"] = deletedAt.String
		}
		if deletedByPrincipalRef.Valid {
			comment["deleted_by_principal_ref"] = deletedByPrincipalRef.String
		}
		if deletedByScopeRef.Valid {
			comment["deleted_by_scope_ref"] = deletedByScopeRef.String
		}

		comments = append(comments, comment)
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"comments": comments,
	})
}

type commentsCreateRequest struct {
	Task    string                 `json:"task"`
	Body    string                 `json:"body"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
	IfMatch int64                  `json:"ifMatch,omitempty"`
}

func (s *daemonServer) handleCommentsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req commentsCreateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Task == "" || strings.TrimSpace(req.Body) == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("task and body required"))
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	taskUUID, _, err := selectors.ResolveTask(s.db, req.Task)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	metaStr := ""
	if req.Meta != nil {
		if data, err := json.Marshal(req.Meta); err == nil {
			metaStr = string(data)
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if req.IfMatch > 0 {
		var currentEtag int64
		if err := tx.QueryRow("SELECT etag FROM tasks WHERE uuid = ?", taskUUID).Scan(&currentEtag); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		if currentEtag != req.IfMatch {
			s.writeError(w, http.StatusConflict, fmt.Errorf("etag mismatch: task has etag %d, expected %d", currentEtag, req.IfMatch))
			return
		}
	}

	var nextSeq int
	if err := tx.QueryRow("SELECT COALESCE(MAX(CAST(SUBSTR(id, 3) AS INTEGER)), 0) + 1 FROM comments").Scan(&nextSeq); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if _, err := tx.Exec("UPDATE comment_sequences SET value = ? WHERE name = 'next_comment'", nextSeq); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	commentUUID := generateUUID()
	commentID := fmt.Sprintf("C-%05d", nextSeq)

	var metaPtr *string
	if metaStr != "" {
		metaPtr = &metaStr
	}

	if _, err := tx.Exec(`
		INSERT INTO comments (
			uuid, id, task_uuid, created_by_principal_ref, created_by_scope_ref,
			body, meta, etag
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, 1)
	`, commentUUID, commentID, taskUUID, attr.PrincipalRef, scopeBind(attr), strings.TrimSpace(req.Body), metaPtr); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	var comment domain.Comment
	var createdAtStr string
	var createdByPrincipalRef, createdByScopeRef sql.NullString
	if err := tx.QueryRow(`
		SELECT uuid, id, task_uuid, created_by_principal_ref, created_by_scope_ref,
		       body, meta, etag, created_at
		FROM comments WHERE uuid = ?
	`, commentUUID).Scan(
		&comment.UUID, &comment.ID, &comment.TaskUUID,
		&createdByPrincipalRef, &createdByScopeRef,
		&comment.Body, &comment.Meta, &comment.ETag, &createdAtStr,
	); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if createdByPrincipalRef.Valid {
		comment.CreatedByPrincipalRef = createdByPrincipalRef.String
	}
	if createdByScopeRef.Valid {
		comment.CreatedByScopeRef = createdByScopeRef.String
	}
	if parsedCreatedAt, err := parseDaemonTimestamp(createdAtStr); err == nil {
		comment.CreatedAt = parsedCreatedAt
	}

	payloadJSON, _ := json.Marshal(map[string]interface{}{
		"task_id":       comment.TaskUUID,
		"comment_id":    comment.ID,
		"principal_ref": attr.PrincipalRef,
	})
	payload := string(payloadJSON)
	eventMeta, err := events.NewWriter(s.db.DB).LogEventReturning(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "comment",
		ResourceUUID: &comment.UUID,
		EventType:    "comment.created",
		ETag:         &comment.ETag,
		Payload:      &payload,
	})
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	webhooks.DispatchTaskEvent(s.db, taskUUID, webhooks.EventContext{
		Metadata:     eventMeta,
		Event:        "comment_added",
		PrincipalRef: attr.PrincipalRef,
		Via:          "api",
		Transition:   nil,
		Changed:      []string{"comments"},
		Changes: map[string]webhooks.Change{
			"comments": {From: nil, To: commentID},
		},
	})

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"comment": comment,
	})
}

type relationsListRequest struct {
	Task string `json:"task"`
}

func (s *daemonServer) handleRelationsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req relationsListRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	taskUUID, _, err := selectors.ResolveTask(s.db, req.Task)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	var relations []Relation

	outgoingRows, err := s.db.Query(`
		SELECT r.kind, r.created_at,
		       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
		       COALESCE(r.created_by_principal_ref, '') AS created_by_id
		FROM task_relations r
		JOIN tasks t ON r.to_task_uuid = t.uuid
		WHERE r.from_task_uuid = ?
		ORDER BY r.kind, t.id
	`, taskUUID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	for outgoingRows.Next() {
		var rel Relation
		if err := outgoingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
			_ = outgoingRows.Close()
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		rel.Direction = "outgoing"
		relations = append(relations, rel)
	}
	_ = outgoingRows.Close()

	incomingRows, err := s.db.Query(`
		SELECT r.kind, r.created_at,
		       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
		       COALESCE(r.created_by_principal_ref, '') AS created_by_id
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		WHERE r.to_task_uuid = ?
		ORDER BY r.kind, t.id
	`, taskUUID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	for incomingRows.Next() {
		var rel Relation
		if err := incomingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
			_ = incomingRows.Close()
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		rel.Direction = "incoming"
		relations = append(relations, rel)
	}
	_ = incomingRows.Close()

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"relations": relations,
	})
}

type relationsCreateRequest struct {
	From string `json:"from"`
	Kind string `json:"kind"`
	To   string `json:"to"`
}

func (s *daemonServer) handleRelationsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req relationsCreateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := domain.ValidateTaskRelationKind(req.Kind); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	fromUUID, _, err := selectors.ResolveTask(s.db, req.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	toUUID, _, err := selectors.ResolveTask(s.db, req.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if fromUUID == toUUID {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("task cannot have a relation to itself"))
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		INSERT INTO task_relations (
			from_task_uuid, to_task_uuid, kind,
			created_by_principal_ref, created_by_scope_ref
		)
		VALUES (?, ?, ?, ?, ?)
	`, fromUUID, toUUID, req.Kind, attr.PrincipalRef, scopeBind(attr)); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	payload := fmt.Sprintf(`{"from_task_uuid":"%s","to_task_uuid":"%s","kind":"%s"}`, fromUUID, toUUID, req.Kind)
	if err := events.NewWriter(s.db.DB).LogEvent(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &fromUUID,
		EventType:    "task.relation.created",
		Payload:      &payload,
	}); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
	})
}

type relationsDeleteRequest struct {
	From string `json:"from"`
	Kind string `json:"kind"`
	To   string `json:"to"`
}

func (s *daemonServer) handleRelationsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req relationsDeleteRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := domain.ValidateTaskRelationKind(req.Kind); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	attr, err := s.resolveAttribution(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	fromUUID, _, err := selectors.ResolveTask(s.db, req.From)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	toUUID, _, err := selectors.ResolveTask(s.db, req.To)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.Exec(`
		DELETE FROM task_relations
		WHERE from_task_uuid = ? AND to_task_uuid = ? AND kind = ?
	`, fromUUID, toUUID, req.Kind)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	rowsAffected, _ := result.RowsAffected()
	if rowsAffected == 0 {
		s.writeError(w, http.StatusNotFound, fmt.Errorf("relation not found"))
		return
	}
	payload := fmt.Sprintf(`{"from_task_uuid":"%s","to_task_uuid":"%s","kind":"%s","deleted_by_principal_ref":"%s"}`,
		fromUUID, toUUID, req.Kind, attr.PrincipalRef)
	if err := events.NewWriter(s.db.DB).LogEvent(tx, &domain.Event{
		PrincipalRef: attr.PrincipalRef,
		ScopeRef:     attr.ScopeRef,
		ResourceType: "task",
		ResourceUUID: &fromUUID,
		EventType:    "task.relation.deleted",
		Payload:      &payload,
	}); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if err := tx.Commit(); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
	})
}

func loadTaskDetail(database *db.DB, taskUUID string, includeComments bool, includeRelations bool) (*Task, error) {
	var id, slug, title, state, description, specification, kind string
	var priority int
	var startAt, dueAt, labels, completedAt, archivedAt, deletedAt *string
	var parentTaskUUID, assigneePrincipalRef *string
	var createdAt, updatedAt string
	var etag int64
	var projectUUID string
	var createdByPrincipalRef, updatedByPrincipalRef, createdByScopeRef, updatedByScopeRef sql.NullString

	err := database.QueryRow(`
		SELECT id, slug, title, project_uuid, state, priority,
		       kind, parent_task_uuid, assignee_principal_ref,
		       start_at, due_at, labels, description, specification, etag,
		       created_at, updated_at, completed_at, archived_at, deleted_at,
		       created_by_principal_ref, updated_by_principal_ref,
		       created_by_scope_ref, updated_by_scope_ref
		FROM tasks WHERE uuid = ?
	`, taskUUID).Scan(
		&id, &slug, &title, &projectUUID, &state, &priority,
		&kind, &parentTaskUUID, &assigneePrincipalRef,
		&startAt, &dueAt, &labels, &description, &specification, &etag,
		&createdAt, &updatedAt, &completedAt, &archivedAt, &deletedAt,
		&createdByPrincipalRef, &updatedByPrincipalRef,
		&createdByScopeRef, &updatedByScopeRef,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	var createdBySlug, updatedBySlug string
	if createdByPrincipalRef.Valid && createdByPrincipalRef.String != "" {
		createdBySlug = attribution.PrincipalHandle(createdByPrincipalRef.String)
	}
	if updatedByPrincipalRef.Valid && updatedByPrincipalRef.String != "" {
		updatedBySlug = attribution.PrincipalHandle(updatedByPrincipalRef.String)
	}

	var projectID string
	_ = database.QueryRow("SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

	var parentTaskID *string
	if parentTaskUUID != nil {
		var ptID string
		if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", *parentTaskUUID).Scan(&ptID); err == nil {
			parentTaskID = &ptID
		}
	}

	var assigneeSlug *string
	if assigneePrincipalRef != nil && *assigneePrincipalRef != "" {
		display := attribution.PrincipalHandle(*assigneePrincipalRef)
		assigneeSlug = &display
	}

	task := &Task{
		ID:                    id,
		UUID:                  taskUUID,
		ArtifactDir:           taskArtifactDir(id),
		ProjectID:             projectID,
		ProjectUUID:           projectUUID,
		Slug:                  slug,
		Title:                 title,
		State:                 state,
		Priority:              priority,
		Kind:                  kind,
		ParentTaskID:          parentTaskID,
		ParentTaskUUID:        parentTaskUUID,
		AssigneeSlug:          assigneeSlug,
		AssigneePrincipalRef:  assigneePrincipalRef,
		StartAt:               startAt,
		DueAt:                 dueAt,
		Labels:                labels,
		Description:           description,
		Specification:         specification,
		Etag:                  etag,
		CreatedAt:             createdAt,
		UpdatedAt:             updatedAt,
		CompletedAt:           completedAt,
		ArchivedAt:            archivedAt,
		DeletedAt:             deletedAt,
		CreatedBy:             createdBySlug,
		UpdatedBy:             updatedBySlug,
		CreatedByPrincipalRef: valueOrEmpty(createdByPrincipalRef),
		UpdatedByPrincipalRef: valueOrEmpty(updatedByPrincipalRef),
		CreatedByScopeRef:     valueOrEmpty(createdByScopeRef),
		UpdatedByScopeRef:     valueOrEmpty(updatedByScopeRef),
	}

	if includeComments {
		rows, err := database.Query(`
			SELECT c.id, c.created_at, c.body,
			       c.created_by_principal_ref, c.created_by_scope_ref
			FROM comments c
			WHERE c.task_uuid = ? AND c.deleted_at IS NULL
			ORDER BY c.created_at ASC
		`, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query comments: %w", err)
		}

		var comments []Comment
		for rows.Next() {
			var comment Comment
			var principalRef, scopeRef sql.NullString
			if err := rows.Scan(&comment.ID, &comment.CreatedAt, &comment.Body, &principalRef, &scopeRef); err != nil {
				_ = rows.Close()
				return nil, fmt.Errorf("failed to scan comment: %w", err)
			}
			comment.PrincipalRef = valueOrEmpty(principalRef)
			comment.ScopeRef = valueOrEmpty(scopeRef)
			comment.Author = attribution.PrincipalHandle(comment.PrincipalRef)
			comments = append(comments, comment)
		}
		_ = rows.Close()

		if len(comments) > 0 {
			task.Comments = comments
		}
	}

	if includeRelations {
		var relations []Relation

		outgoingRows, err := database.Query(`
			SELECT r.kind, r.created_at,
			       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
			       COALESCE(r.created_by_principal_ref, '') AS created_by_id
			FROM task_relations r
			JOIN tasks t ON r.to_task_uuid = t.uuid
			WHERE r.from_task_uuid = ?
			ORDER BY r.kind, t.id
		`, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query outgoing relations: %w", err)
		}

		for outgoingRows.Next() {
			var rel Relation
			if err := outgoingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				_ = outgoingRows.Close()
				return nil, fmt.Errorf("failed to scan relation: %w", err)
			}
			rel.Direction = "outgoing"
			relations = append(relations, rel)
		}
		_ = outgoingRows.Close()

		incomingRows, err := database.Query(`
			SELECT r.kind, r.created_at,
			       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
			       COALESCE(r.created_by_principal_ref, '') AS created_by_id
			FROM task_relations r
			JOIN tasks t ON r.from_task_uuid = t.uuid
			WHERE r.to_task_uuid = ?
			ORDER BY r.kind, t.id
		`, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query incoming relations: %w", err)
		}

		for incomingRows.Next() {
			var rel Relation
			if err := incomingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				_ = incomingRows.Close()
				return nil, fmt.Errorf("failed to scan relation: %w", err)
			}
			rel.Direction = "incoming"
			relations = append(relations, rel)
		}
		_ = incomingRows.Close()

		if len(relations) > 0 {
			task.Relations = relations
		}
	}

	return task, nil
}

func getStringField(fields map[string]interface{}, key string, fallback string) string {
	if fields == nil {
		return fallback
	}
	if value, ok := fields[key]; ok {
		if s, ok := value.(string); ok {
			return s
		}
	}
	return fallback
}

func getIntField(fields map[string]interface{}, key string, fallback int) int {
	if fields == nil {
		return fallback
	}
	if value, ok := fields[key]; ok {
		if i, ok := coerceInt(value); ok {
			return i
		}
	}
	return fallback
}

func coerceInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, false
		}
		return int(i), true
	default:
		return 0, false
	}
}

func generateUUID() string {
	return uuid.New().String()
}

func sortedMapKeys(fields map[string]interface{}) []string {
	keys := make([]string, 0, len(fields))
	for key := range fields {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func mapChanges(fields map[string]interface{}, oldValues map[string]interface{}) map[string]webhooks.Change {
	changes := make(map[string]webhooks.Change, len(fields))
	for _, key := range sortedMapKeys(fields) {
		changes[key] = webhooks.Change{From: oldValues[key], To: fields[key]}
	}
	return changes
}

func parseDaemonTimestamp(value string) (time.Time, error) {
	formats := []string{
		time.RFC3339,
		"2006-01-02T15:04:05Z",
		"2006-01-02T15:04:05",
		"2006-01-02 15:04:05",
	}
	for _, format := range formats {
		if parsed, err := time.Parse(format, value); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported timestamp format: %s", value)
}

func getLabelsField(fields map[string]interface{}, key string) string {
	if fields == nil {
		return ""
	}
	value, ok := fields[key]
	if !ok {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	case []interface{}:
		if data, err := json.Marshal(v); err == nil {
			return string(data)
		}
	}
	return ""
}
