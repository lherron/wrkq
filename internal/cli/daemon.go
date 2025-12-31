package cli

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/bundle"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/cursor"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/paths"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
)

// DaemonOptions configures the wrkqd daemon.
type DaemonOptions struct {
	Addr   string
	Unix   string
	Token  string
	DBPath string
}

// ServeDaemon starts the wrkqd daemon.
func ServeDaemon(opts DaemonOptions) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	if opts.DBPath != "" {
		cfg.DBPath = opts.DBPath
	}

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}

	_, pending, err := database.MigrationStatus()
	if err != nil {
		database.Close()
		return fmt.Errorf("failed to check migration status: %w", err)
	}
	if len(pending) > 0 {
		database.Close()
		return fmt.Errorf("database requires migration: %d pending migration(s). Run 'wrkqadm migrate' to update", len(pending))
	}

	server := &daemonServer{
		db:    database,
		cfg:   cfg,
		token: opts.Token,
	}

	mux := http.NewServeMux()
	server.registerRoutes(mux)

	httpServer := &http.Server{
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
	}

	if opts.Unix != "" {
		_ = os.Remove(opts.Unix)
		listener, err := net.Listen("unix", opts.Unix)
		if err != nil {
			database.Close()
			return fmt.Errorf("failed to listen on unix socket: %w", err)
		}
		defer listener.Close()
		return httpServer.Serve(listener)
	}

	addr := opts.Addr
	if addr == "" {
		addr = "127.0.0.1:7171"
	}
	httpServer.Addr = addr

	return httpServer.ListenAndServe()
}

type daemonServer struct {
	db    *db.DB
	cfg   *config.Config
	token string
}

// Task mirrors wrkq cat --json output with additional deleted_at metadata.
type Task struct {
	ID             string     `json:"id"`
	UUID           string     `json:"uuid"`
	ProjectID      string     `json:"project_id"`
	ProjectUUID    string     `json:"project_uuid"`
	Slug           string     `json:"slug"`
	Title          string     `json:"title"`
	State          string     `json:"state"`
	Priority       int        `json:"priority"`
	Kind           string     `json:"kind"`
	ParentTaskID   *string    `json:"parent_task_id,omitempty"`
	ParentTaskUUID *string    `json:"parent_task_uuid,omitempty"`
	AssigneeSlug   *string    `json:"assignee,omitempty"`
	AssigneeUUID   *string    `json:"assignee_uuid,omitempty"`
	StartAt        *string    `json:"start_at,omitempty"`
	DueAt          *string    `json:"due_at,omitempty"`
	Labels         *string    `json:"labels,omitempty"`
	Description    string     `json:"description"`
	Etag           int64      `json:"etag"`
	CreatedAt      string     `json:"created_at"`
	UpdatedAt      string     `json:"updated_at"`
	CompletedAt    *string    `json:"completed_at,omitempty"`
	ArchivedAt     *string    `json:"archived_at,omitempty"`
	DeletedAt      *string    `json:"deleted_at,omitempty"`
	CreatedBy      string     `json:"created_by"`
	UpdatedBy      string     `json:"updated_by"`
	Comments       []Comment  `json:"comments,omitempty"`
	Relations      []Relation `json:"relations,omitempty"`
}

type Comment struct {
	ID        string `json:"id"`
	CreatedAt string `json:"created_at"`
	Body      string `json:"body"`
	ActorSlug string `json:"actor_slug"`
	ActorRole string `json:"actor_role"`
}

func (s *daemonServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/v1/health", s.withAuth(s.handleHealth))
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

	mux.HandleFunc("/v1/actors/list", s.withAuth(s.handleActorsList))
	mux.HandleFunc("/v1/actors/create", s.withAuth(s.handleActorsCreate))
	mux.HandleFunc("/v1/actors/update", s.withAuth(s.handleActorsUpdate))

	mux.HandleFunc("/v1/bundle/create", s.withAuth(s.handleBundleCreate))
	mux.HandleFunc("/v1/bundle/apply", s.withAuth(s.handleBundleApply))
}

func (s *daemonServer) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			token := r.Header.Get("Authorization")
			if strings.HasPrefix(token, "Bearer ") {
				token = strings.TrimPrefix(token, "Bearer ")
			}
			if token == "" {
				token = r.Header.Get("X-Wrkqd-Token")
			}
			if token != s.token {
				s.writeError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
				return
			}
		}

		next(w, r)
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

func (s *daemonServer) resolveActorUUID(r *http.Request) (string, error) {
	actorIdentifier := r.Header.Get("X-Wrkq-Actor")
	if actorIdentifier == "" {
		actorIdentifier = s.cfg.GetActorID()
	}
	if actorIdentifier == "" {
		actorIdentifier = "codex-agent"
	}

	resolver := actors.NewResolver(s.db.DB)
	actorUUID, err := resolver.Resolve(actorIdentifier)
	if err == nil {
		return actorUUID, nil
	}

	normalized, normErr := paths.NormalizeSlug(actorIdentifier)
	if normErr != nil {
		return "", fmt.Errorf("failed to resolve actor: %w", err)
	}

	actor, createErr := resolver.Create(normalized, "", "agent")
	if createErr != nil {
		return "", fmt.Errorf("failed to resolve actor: %w", err)
	}

	return actor.UUID, nil
}

func (s *daemonServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok":   true,
		"time": time.Now().UTC().Format(time.RFC3339),
	})
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
	root, err := buildTree(s.db, rootPath, req.Depth, req.IncludeArchived, req.OpenOnly, 0)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	path := rootPath
	if path == "" {
		path = "."
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"path":     path,
		"children": root.Children,
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

	var assigneeUUID string
	if req.Assignee != "" {
		resolver := actors.NewResolver(s.db.DB)
		uuid, err := resolver.Resolve(req.Assignee)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		assigneeUUID = uuid
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
		paths:          pathsFilter,
		typeFilter:     "t",
		slugGlob:       req.SlugGlob,
		state:          stateFilter,
		dueBefore:      req.DueBefore,
		dueAfter:       req.DueAfter,
		kind:           req.Kind,
		assigneeUUID:   assigneeUUID,
		parentTaskUUID: parentTaskUUID,
		limit:          req.Limit,
		cursor:         req.Cursor,
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

	actorUUID, err := s.resolveActorUUID(r)
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
	state := getStringField(fields, "state", "open")
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

	var assigneeActorUUID *string
	if assignee := getStringField(fields, "assignee", ""); assignee != "" {
		resolver := actors.NewResolver(s.db.DB)
		uuid, err := resolver.Resolve(assignee)
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		assigneeActorUUID = &uuid
	}

	projectUUID := ""
	if parentUUID != nil {
		projectUUID = *parentUUID
	} else {
		if err := s.db.QueryRow(`SELECT uuid FROM containers WHERE parent_uuid IS NULL LIMIT 1`).Scan(&projectUUID); err != nil {
			s.writeError(w, http.StatusBadRequest, fmt.Errorf("no root container found"))
			return
		}
	}

	svc := store.New(s.db)
	result, err := svc.Tasks.Create(actorUUID, store.CreateParams{
		UUID:              req.ForceUUID,
		Slug:              normalizedSlug,
		Title:             title,
		Description:       description,
		ProjectUUID:       projectUUID,
		State:             state,
		Priority:          priority,
		Kind:              kind,
		ParentTaskUUID:    parentTaskUUID,
		AssigneeActorUUID: assigneeActorUUID,
		Labels:            labels,
		DueAt:             dueAt,
		StartAt:           startAt,
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

	actorUUID, err := s.resolveActorUUID(r)
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
		case "title", "state", "description", "due_at", "start_at":
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
					fields["assignee_actor_uuid"] = nil
					continue
				}
				resolver := actors.NewResolver(s.db.DB)
				uuid, err := resolver.Resolve(assignee)
				if err != nil {
					s.writeError(w, http.StatusBadRequest, err)
					return
				}
				fields["assignee_actor_uuid"] = uuid
			}
		}
	}

	if len(fields) == 0 {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("no valid fields to update"))
		return
	}

	svc := store.New(s.db)
	if _, err := svc.Tasks.UpdateFields(actorUUID, taskUUID, fields, req.IfMatch); err != nil {
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

	actorUUID, err := s.resolveActorUUID(r)
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
	if _, err := svc.Tasks.Archive(actorUUID, taskUUID, req.IfMatch); err != nil {
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

	actorUUID, err := s.resolveActorUUID(r)
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
	if err := domain.ValidateState(targetState); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if targetState == "archived" || targetState == "deleted" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("cannot restore to %s state", targetState))
		return
	}

	tx, err := s.db.Begin()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	defer tx.Rollback()

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
		"state":       targetState,
		"archived_at": nil,
		"deleted_at":  nil,
	}

	for key, value := range req.Fields {
		switch key {
		case "title", "description", "labels", "due_at", "start_at":
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
	setClauses = append(setClauses, "updated_by_actor_uuid = ?")
	args = append(args, actorUUID, taskUUID)

	query := fmt.Sprintf("UPDATE tasks SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
	if _, err := tx.Exec(query, args...); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	newETag := currentETag + 1
	payloadJSON, _ := json.Marshal(fields)
	payloadStr := string(payloadJSON)
	if err := events.NewWriter(s.db.DB).LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "task",
		ResourceUUID: &taskUUID,
		EventType:    "task.updated",
		ETag:         &newETag,
		Payload:      &payloadStr,
	}); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if err := tx.Commit(); err != nil {
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
		SELECT c.uuid, c.id, c.task_uuid, c.actor_uuid, c.body, c.meta, c.etag,
		       c.created_at, c.updated_at, c.deleted_at, c.deleted_by_actor_uuid,
		       a.slug as actor_slug, a.role as actor_role,
		       t.id as task_id
		FROM comments c
		LEFT JOIN actors a ON c.actor_uuid = a.uuid
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
	defer rows.Close()

	var comments []map[string]interface{}
	for rows.Next() {
		var uuid, id, taskUUID, actorUUID, body, createdAt string
		var actorSlug, actorRole, taskIDStr string
		var meta, updatedAt, deletedAt, deletedByActorUUID sql.NullString
		var etag int64

		if err := rows.Scan(&uuid, &id, &taskUUID, &actorUUID, &body, &meta, &etag,
			&createdAt, &updatedAt, &deletedAt, &deletedByActorUUID,
			&actorSlug, &actorRole, &taskIDStr); err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}

		comment := map[string]interface{}{
			"uuid":       uuid,
			"id":         id,
			"task_uuid":  taskUUID,
			"task_id":    taskIDStr,
			"actor_uuid": actorUUID,
			"actor_slug": actorSlug,
			"actor_role": actorRole,
			"body":       body,
			"etag":       etag,
			"created_at": createdAt,
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
		if deletedByActorUUID.Valid {
			comment["deleted_by_actor_uuid"] = deletedByActorUUID.String
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

	actorUUID, err := s.resolveActorUUID(r)
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
	defer tx.Rollback()

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
		INSERT INTO comments (uuid, id, task_uuid, actor_uuid, body, meta, etag)
		VALUES (?, ?, ?, ?, ?, ?, 1)
	`, commentUUID, commentID, taskUUID, actorUUID, strings.TrimSpace(req.Body), metaPtr); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	var comment domain.Comment
	var createdAtStr string
	if err := tx.QueryRow(`
		SELECT uuid, id, task_uuid, actor_uuid, body, meta, etag, created_at
		FROM comments WHERE uuid = ?
	`, commentUUID).Scan(
		&comment.UUID, &comment.ID, &comment.TaskUUID, &comment.ActorUUID,
		&comment.Body, &comment.Meta, &comment.ETag, &createdAtStr,
	); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	payload := fmt.Sprintf(`{"task_id":"%s","comment_id":"%s","actor_id":"%s"}`, comment.TaskUUID, comment.ID, comment.ActorUUID)
	if err := events.NewWriter(s.db.DB).LogEvent(tx, &domain.Event{
		ActorUUID:    &actorUUID,
		ResourceType: "comment",
		ResourceUUID: &comment.UUID,
		EventType:    "comment.created",
		ETag:         &comment.ETag,
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
		       a.id AS created_by_id
		FROM task_relations r
		JOIN tasks t ON r.to_task_uuid = t.uuid
		JOIN actors a ON r.created_by_actor_uuid = a.uuid
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
			outgoingRows.Close()
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		rel.Direction = "outgoing"
		relations = append(relations, rel)
	}
	outgoingRows.Close()

	incomingRows, err := s.db.Query(`
		SELECT r.kind, r.created_at,
		       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
		       a.id AS created_by_id
		FROM task_relations r
		JOIN tasks t ON r.from_task_uuid = t.uuid
		JOIN actors a ON r.created_by_actor_uuid = a.uuid
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
			incomingRows.Close()
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		rel.Direction = "incoming"
		relations = append(relations, rel)
	}
	incomingRows.Close()

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

	actorUUID, err := s.resolveActorUUID(r)
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

	if _, err := s.db.Exec(`
		INSERT INTO task_relations (from_task_uuid, to_task_uuid, kind, created_by_actor_uuid)
		VALUES (?, ?, ?, ?)
	`, fromUUID, toUUID, req.Kind, actorUUID); err != nil {
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

	result, err := s.db.Exec(`
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

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
	})
}

type actorsListRequest struct{}

func (s *daemonServer) handleActorsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	resolver := actors.NewResolver(s.db.DB)
	actorsList, err := resolver.List()
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"actors": actorsList,
	})
}

type actorsCreateRequest struct {
	Slug        string `json:"slug"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role,omitempty"`
}

func (s *daemonServer) handleActorsCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req actorsCreateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	normalizedSlug, err := paths.NormalizeSlug(req.Slug)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	role := req.Role
	if role == "" {
		role = "agent"
	}

	resolver := actors.NewResolver(s.db.DB)
	actor, err := resolver.Create(normalizedSlug, req.DisplayName, role)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"actor": actor,
	})
}

type actorsUpdateRequest struct {
	Actor       string `json:"actor"`
	DisplayName string `json:"display_name,omitempty"`
	Role        string `json:"role,omitempty"`
}

func (s *daemonServer) handleActorsUpdate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req actorsUpdateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	if req.Actor == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("actor required"))
		return
	}

	resolver := actors.NewResolver(s.db.DB)
	actorUUID, err := resolver.Resolve(req.Actor)
	if err != nil {
		s.writeError(w, http.StatusNotFound, err)
		return
	}

	setClauses := []string{}
	args := []interface{}{}
	if req.DisplayName != "" {
		setClauses = append(setClauses, "display_name = ?")
		args = append(args, req.DisplayName)
	}
	if req.Role != "" {
		setClauses = append(setClauses, "role = ?")
		args = append(args, req.Role)
	}

	if len(setClauses) == 0 {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("no fields to update"))
		return
	}

	args = append(args, actorUUID)
	query := fmt.Sprintf("UPDATE actors SET %s WHERE uuid = ?", strings.Join(setClauses, ", "))
	if _, err := s.db.Exec(query, args...); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	actor, err := resolver.GetByUUID(actorUUID)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"actor": actor,
	})
}

type bundleCreateRequest struct {
	Out             string   `json:"out,omitempty"`
	Actor           string   `json:"actor,omitempty"`
	Since           string   `json:"since,omitempty"`
	Until           string   `json:"until,omitempty"`
	Project         string   `json:"project,omitempty"`
	PathPrefixes    []string `json:"path_prefix,omitempty"`
	IncludeRefs     bool     `json:"include_refs,omitempty"`
	WithAttachments bool     `json:"with_attachments,omitempty"`
	WithEvents      *bool    `json:"with_events,omitempty"`
}

func (s *daemonServer) handleBundleCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req bundleCreateRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	outDir := req.Out
	if outDir == "" {
		outDir = ".wrkq"
	}

	opts := bundle.CreateOptions{
		OutputDir:       outDir,
		Actor:           req.Actor,
		Since:           req.Since,
		Until:           req.Until,
		WithAttachments: req.WithAttachments,
		WithEvents:      true,
		IncludeRefs:     req.IncludeRefs,
		Version:         "0.1.0",
		Commit:          "",
		BuildDate:       "",
	}
	if req.WithEvents != nil {
		opts.WithEvents = *req.WithEvents
	}

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
		opts.ProjectUUID = projectUUID
		opts.ProjectPath = projectPath
	}

	for _, prefix := range req.PathPrefixes {
		trimmed := strings.Trim(prefix, "/")
		if trimmed != "" {
			opts.PathPrefixes = append(opts.PathPrefixes, trimmed)
		}
	}

	b, err := bundle.Create(s.db.DB, opts)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"bundle_dir":       b.Dir,
		"tasks_count":      len(b.Tasks),
		"containers_count": len(b.Containers),
		"refs_count":       len(b.Refs),
		"manifest":         b.Manifest,
	})
}

type bundleApplyRequest struct {
	From            string `json:"from,omitempty"`
	DryRun          bool   `json:"dry_run,omitempty"`
	ContinueOnError bool   `json:"continue_on_error,omitempty"`
}

func (s *daemonServer) handleBundleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeError(w, http.StatusMethodNotAllowed, fmt.Errorf("method not allowed"))
		return
	}

	var req bundleApplyRequest
	if err := s.decodeJSON(r, &req); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	from := req.From
	if from == "" {
		from = ".wrkq"
	}

	b, err := bundle.Load(from)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}
	if b.Manifest.MachineInterfaceVersion != 1 {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("bundle machine_interface_version (%d) doesn't match current version (1)", b.Manifest.MachineInterfaceVersion))
		return
	}

	actorUUID, err := s.resolveActorUUID(r)
	if err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	result := &applyResult{Success: true}

	if req.ContinueOnError {
		for _, containerPath := range b.Containers {
			created, err := ensureContainer(s.db, actorUUID, containerPath, req.DryRun)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("container %s: %v", containerPath, err))
				result.Success = false
				continue
			}
			if created {
				result.ContainersAdded++
			}
		}

		for _, task := range b.Tasks {
			if err := applyTaskDocumentWithDB(s.db, actorUUID, task, req.DryRun); err != nil {
				result.TasksFailed++
				result.Success = false
				if conflict := conflictFromError(err); conflict != nil {
					result.Conflicts = append(result.Conflicts, *conflict)
				} else {
					result.Errors = append(result.Errors, fmt.Sprintf("task %s: %v", task.Path, err))
				}
				continue
			}
			result.TasksApplied++
		}
	} else {
		tx, err := s.db.Begin()
		if err != nil {
			s.writeError(w, http.StatusBadRequest, err)
			return
		}
		defer tx.Rollback()

		ew := events.NewWriter(s.db.DB)

		for _, containerPath := range b.Containers {
			created, err := ensureContainerTx(tx, ew, actorUUID, containerPath, req.DryRun)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("container %s: %v", containerPath, err))
				result.Success = false
				break
			}
			if created {
				result.ContainersAdded++
			}
		}

		if result.Success {
			for _, task := range b.Tasks {
				if err := applyTaskDocumentTx(tx, ew, actorUUID, task, req.DryRun); err != nil {
					result.TasksFailed++
					result.Success = false
					if conflict := conflictFromError(err); conflict != nil {
						result.Conflicts = append(result.Conflicts, *conflict)
					} else {
						result.Errors = append(result.Errors, fmt.Sprintf("task %s: %v", task.Path, err))
					}
					break
				}
				result.TasksApplied++
			}
		}

		if result.Success && !req.DryRun {
			if err := tx.Commit(); err != nil {
				s.writeError(w, http.StatusBadRequest, err)
				return
			}
		}
	}

	if result.Success && !req.DryRun && b.Manifest.WithAttachments {
		attachmentsDir := filepath.Join(b.Dir, "attachments")
		if _, err := os.Stat(attachmentsDir); err == nil {
			attached, err := reattachFilesDaemon(s.cfg, attachmentsDir)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("attachments: %v", err))
				result.Success = false
			} else {
				result.AttachmentsAdded = attached
			}
		}
	}

	s.writeJSON(w, http.StatusOK, result)
}

func loadTaskDetail(database *db.DB, taskUUID string, includeComments bool, includeRelations bool) (*Task, error) {
	var id, slug, title, state, description, kind string
	var priority int
	var startAt, dueAt, labels, completedAt, archivedAt, deletedAt *string
	var parentTaskUUID, assigneeActorUUID *string
	var createdAt, updatedAt string
	var etag int64
	var projectUUID, createdByUUID, updatedByUUID string

	err := database.QueryRow(`
		SELECT id, slug, title, project_uuid, state, priority,
		       kind, parent_task_uuid, assignee_actor_uuid,
		       start_at, due_at, labels, description, etag,
		       created_at, updated_at, completed_at, archived_at, deleted_at,
		       created_by_actor_uuid, updated_by_actor_uuid
		FROM tasks WHERE uuid = ?
	`, taskUUID).Scan(
		&id, &slug, &title, &projectUUID, &state, &priority,
		&kind, &parentTaskUUID, &assigneeActorUUID,
		&startAt, &dueAt, &labels, &description, &etag,
		&createdAt, &updatedAt, &completedAt, &archivedAt, &deletedAt,
		&createdByUUID, &updatedByUUID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	var createdBySlug, updatedBySlug string
	database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", createdByUUID).Scan(&createdBySlug)
	database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", updatedByUUID).Scan(&updatedBySlug)

	var projectID string
	database.QueryRow("SELECT id FROM containers WHERE uuid = ?", projectUUID).Scan(&projectID)

	var parentTaskID *string
	if parentTaskUUID != nil {
		var ptID string
		if err := database.QueryRow("SELECT id FROM tasks WHERE uuid = ?", *parentTaskUUID).Scan(&ptID); err == nil {
			parentTaskID = &ptID
		}
	}

	var assigneeSlug *string
	if assigneeActorUUID != nil {
		var aSlug string
		if err := database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", *assigneeActorUUID).Scan(&aSlug); err == nil {
			assigneeSlug = &aSlug
		}
	}

	task := &Task{
		ID:             id,
		UUID:           taskUUID,
		ProjectID:      projectID,
		ProjectUUID:    projectUUID,
		Slug:           slug,
		Title:          title,
		State:          state,
		Priority:       priority,
		Kind:           kind,
		ParentTaskID:   parentTaskID,
		ParentTaskUUID: parentTaskUUID,
		AssigneeSlug:   assigneeSlug,
		AssigneeUUID:   assigneeActorUUID,
		StartAt:        startAt,
		DueAt:          dueAt,
		Labels:         labels,
		Description:    description,
		Etag:           etag,
		CreatedAt:      createdAt,
		UpdatedAt:      updatedAt,
		CompletedAt:    completedAt,
		ArchivedAt:     archivedAt,
		DeletedAt:      deletedAt,
		CreatedBy:      createdBySlug,
		UpdatedBy:      updatedBySlug,
	}

	if includeComments {
		rows, err := database.Query(`
			SELECT c.id, c.created_at, c.body, a.slug as actor_slug, a.role as actor_role
			FROM comments c
			LEFT JOIN actors a ON c.actor_uuid = a.uuid
			WHERE c.task_uuid = ? AND c.deleted_at IS NULL
			ORDER BY c.created_at ASC
		`, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query comments: %w", err)
		}

		var comments []Comment
		for rows.Next() {
			var comment Comment
			if err := rows.Scan(&comment.ID, &comment.CreatedAt, &comment.Body, &comment.ActorSlug, &comment.ActorRole); err != nil {
				rows.Close()
				return nil, fmt.Errorf("failed to scan comment: %w", err)
			}
			comments = append(comments, comment)
		}
		rows.Close()

		if len(comments) > 0 {
			task.Comments = comments
		}
	}

	if includeRelations {
		var relations []Relation

		outgoingRows, err := database.Query(`
			SELECT r.kind, r.created_at,
			       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
			       a.id AS created_by_id
			FROM task_relations r
			JOIN tasks t ON r.to_task_uuid = t.uuid
			JOIN actors a ON r.created_by_actor_uuid = a.uuid
			WHERE r.from_task_uuid = ?
			ORDER BY r.kind, t.id
		`, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query outgoing relations: %w", err)
		}

		for outgoingRows.Next() {
			var rel Relation
			if err := outgoingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				outgoingRows.Close()
				return nil, fmt.Errorf("failed to scan relation: %w", err)
			}
			rel.Direction = "outgoing"
			relations = append(relations, rel)
		}
		outgoingRows.Close()

		incomingRows, err := database.Query(`
			SELECT r.kind, r.created_at,
			       t.id AS task_id, t.uuid AS task_uuid, t.slug, t.title,
			       a.id AS created_by_id
			FROM task_relations r
			JOIN tasks t ON r.from_task_uuid = t.uuid
			JOIN actors a ON r.created_by_actor_uuid = a.uuid
			WHERE r.to_task_uuid = ?
			ORDER BY r.kind, t.id
		`, taskUUID)
		if err != nil {
			return nil, fmt.Errorf("failed to query incoming relations: %w", err)
		}

		for incomingRows.Next() {
			var rel Relation
			if err := incomingRows.Scan(&rel.Kind, &rel.CreatedAt, &rel.TaskID, &rel.TaskUUID, &rel.TaskSlug, &rel.TaskTitle, &rel.CreatedByID); err != nil {
				incomingRows.Close()
				return nil, fmt.Errorf("failed to scan relation: %w", err)
			}
			rel.Direction = "incoming"
			relations = append(relations, rel)
		}
		incomingRows.Close()

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

func reattachFilesDaemon(cfg *config.Config, attachmentsDir string) (int, error) {
	count := 0

	entries, err := os.ReadDir(attachmentsDir)
	if err != nil {
		return 0, err
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		taskUUID := entry.Name()
		taskAttachDir := filepath.Join(attachmentsDir, taskUUID)

		files, err := os.ReadDir(taskAttachDir)
		if err != nil {
			return count, fmt.Errorf("failed to read %s: %w", taskAttachDir, err)
		}

		for _, file := range files {
			if file.IsDir() {
				continue
			}

			filePath := filepath.Join(taskAttachDir, file.Name())

			attachCmd := exec.Command("wrkq", "attach", "put", "t:"+taskUUID, filePath)
			attachCmd.Env = os.Environ()
			attachCmd.Env = append(attachCmd.Env, "WRKQ_DB_PATH="+cfg.DBPath)
			if actorIdentifier := cfg.GetActorID(); actorIdentifier != "" {
				attachCmd.Env = append(attachCmd.Env, "WRKQ_ACTOR="+actorIdentifier)
			}

			output, err := attachCmd.CombinedOutput()
			if err != nil {
				return count, fmt.Errorf("wrkq attach put failed for %s: %w\nOutput: %s", file.Name(), err, output)
			}

			count++
		}
	}

	return count, nil
}
