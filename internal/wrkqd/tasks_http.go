package wrkqd

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/domain"
	"github.com/lherron/wrkq/internal/events"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooks"
)

type taskGetRequest struct {
	Selector         string `json:"selector"`
	IncludeComments  *bool  `json:"include_comments,omitempty"`
	IncludeRelations *bool  `json:"include_relations,omitempty"`
}

func (s *daemonServer) handleTasksGet(w http.ResponseWriter, r *http.Request) {
	var req taskGetRequest
	if !s.decodePost(w, r, &req) {
		return
	}

	if req.Selector == "" {
		s.writeError(w, http.StatusBadRequest, fmt.Errorf("selector required"))
		return
	}

	taskUUID, ok := s.resolveTask(w, req.Selector)
	if !ok {
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

	s.writeTaskDetail(w, taskUUID, includeComments, includeRelations)
}

type taskCreateRequest struct {
	Path      string                 `json:"path"`
	Fields    map[string]interface{} `json:"fields,omitempty"`
	ForceUUID string                 `json:"force_uuid,omitempty"`
}

func (s *daemonServer) handleTasksCreate(w http.ResponseWriter, r *http.Request) {
	var req taskCreateRequest
	if !s.decodePost(w, r, &req) {
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

	s.writeTaskDetail(w, result.UUID, true, true)
}

type taskUpdateRequest struct {
	Selector string                 `json:"selector"`
	Fields   map[string]interface{} `json:"fields,omitempty"`
	IfMatch  int64                  `json:"ifMatch,omitempty"`
}

func (s *daemonServer) handleTasksUpdate(w http.ResponseWriter, r *http.Request) {
	var req taskUpdateRequest
	if !s.decodePost(w, r, &req) {
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

	taskUUID, ok := s.resolveTask(w, req.Selector)
	if !ok {
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

	s.writeTaskDetail(w, taskUUID, true, true)
}

type taskArchiveRequest struct {
	Selector string `json:"selector"`
	IfMatch  int64  `json:"ifMatch,omitempty"`
}

func (s *daemonServer) handleTasksArchive(w http.ResponseWriter, r *http.Request) {
	var req taskArchiveRequest
	if !s.decodePost(w, r, &req) {
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

	taskUUID, ok := s.resolveTask(w, req.Selector)
	if !ok {
		return
	}

	svc := store.New(s.db)
	if _, err := svc.Tasks.ArchiveWithViaAttribution(attr, taskUUID, req.IfMatch, "api"); err != nil {
		s.writeError(w, http.StatusBadRequest, err)
		return
	}

	s.writeTaskDetail(w, taskUUID, true, true)
}

type taskRestoreRequest struct {
	Selector string                 `json:"selector"`
	State    string                 `json:"state,omitempty"`
	IfMatch  int64                  `json:"ifMatch,omitempty"`
	Fields   map[string]interface{} `json:"fields,omitempty"`
}

func (s *daemonServer) handleTasksRestore(w http.ResponseWriter, r *http.Request) {
	var req taskRestoreRequest
	if !s.decodePost(w, r, &req) {
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

	taskUUID, ok := s.resolveTask(w, req.Selector)
	if !ok {
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

	s.writeTaskDetail(w, taskUUID, true, true)
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
