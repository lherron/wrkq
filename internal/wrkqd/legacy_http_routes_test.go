package wrkqd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// legacyClient drives the legacy /v1 REST routes through the daemon's real
// mux, attributed to one canonical principal.
type legacyClient struct {
	t       *testing.T
	handler http.Handler
}

func newLegacyClient(t *testing.T) *legacyClient {
	t.Helper()
	database, _ := setupTestEnv(t)
	mux := http.NewServeMux()
	(&daemonServer{db: database}).registerRoutes(mux)
	return &legacyClient{t: t, handler: mux}
}

func (c *legacyClient) do(method, route, body string) (int, map[string]interface{}) {
	c.t.Helper()
	req := httptest.NewRequest(method, route, bytes.NewBufferString(body))
	req.Header.Set("X-Wrkq-Principal-Ref", "agent:tester")
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	var payload map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		c.t.Fatalf("%s %s: decode %q: %v", method, route, rec.Body.String(), err)
	}
	return rec.Code, payload
}

// post requires the given status and returns the decoded payload.
func (c *legacyClient) post(route, body string, wantStatus int) map[string]interface{} {
	c.t.Helper()
	status, payload := c.do(http.MethodPost, route, body)
	if status != wantStatus {
		c.t.Fatalf("POST %s %s: status %d payload %v; want %d", route, body, status, payload, wantStatus)
	}
	return payload
}

func (c *legacyClient) task(route, body string) map[string]interface{} {
	c.t.Helper()
	task, ok := c.post(route, body, http.StatusOK)["task"].(map[string]interface{})
	if !ok {
		c.t.Fatalf("POST %s: no task in payload", route)
	}
	return task
}

func requireField(t *testing.T, what string, got map[string]interface{}, key string, want interface{}) {
	t.Helper()
	if got[key] != want {
		t.Fatalf("%s: %s = %#v; want %#v (payload %v)", what, key, got[key], want, got)
	}
}

func listOf(t *testing.T, payload map[string]interface{}, key string) []interface{} {
	t.Helper()
	if payload[key] == nil {
		return nil
	}
	items, ok := payload[key].([]interface{})
	if !ok {
		t.Fatalf("%s is %T, want a list (payload %v)", key, payload[key], payload)
	}
	return items
}

// TestLegacyHTTPRoutesRequestGuards pins the shared preamble every legacy
// route answers with: 405 for a wrong method, 400 for an undecodable body,
// 404 for a selector that names no task, and 400 without attribution.
func TestLegacyHTTPRoutesRequestGuards(t *testing.T) {
	c := newLegacyClient(t)

	for _, route := range []string{
		"/v1/tasks/list", "/v1/tasks/get", "/v1/tasks/create", "/v1/tasks/update",
		"/v1/tasks/archive", "/v1/tasks/restore", "/v1/comments/list", "/v1/comments/create",
		"/v1/relations/list", "/v1/relations/create", "/v1/relations/delete", "/v1/containers/tree",
	} {
		status, payload := c.do(http.MethodGet, route, "")
		if status != http.StatusMethodNotAllowed || payload["message"] != "method not allowed" {
			t.Fatalf("GET %s: status %d payload %v; want 405 method not allowed", route, status, payload)
		}
		if route == "/v1/containers/tree" {
			continue // an empty tree request body is accepted
		}
		if status, _ := c.do(http.MethodPost, route, "{not json"); status != http.StatusBadRequest {
			t.Fatalf("POST %s with a bad body: status %d; want 400", route, status)
		}
	}

	for _, route := range []string{"/v1/tasks/get", "/v1/tasks/update", "/v1/tasks/archive", "/v1/tasks/restore"} {
		c.post(route, `{"selector":"T-99999","fields":{"title":"x"}}`, http.StatusNotFound)
	}
	for _, route := range []string{"/v1/comments/list", "/v1/comments/create", "/v1/relations/list"} {
		c.post(route, `{"task":"T-99999","body":"x"}`, http.StatusNotFound)
	}
	c.post("/v1/tasks/get", `{}`, http.StatusBadRequest)
	c.post("/v1/comments/create", `{"task":"T-99999","body":"  "}`, http.StatusBadRequest)

	req := httptest.NewRequest(http.MethodPost, "/v1/tasks/create", bytes.NewBufferString(`{"path":"inbox/x"}`))
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("create without attribution: status %d; want 400", rec.Code)
	}
}

// TestLegacyHTTPRoutesTaskLifecycle characterizes the legacy task, comment,
// and relation routes end to end over one database.
func TestLegacyHTTPRoutesTaskLifecycle(t *testing.T) {
	c := newLegacyClient(t)

	alpha := c.task("/v1/tasks/create", `{"path":"inbox/alpha","fields":{"title":"Alpha","priority":2,"labels":["x","y"]}}`)
	requireField(t, "created alpha", alpha, "title", "Alpha")
	requireField(t, "created alpha", alpha, "state", "open")
	requireField(t, "created alpha", alpha, "priority", float64(2))
	requireField(t, "created alpha", alpha, "labels", `["x","y"]`)
	requireField(t, "created alpha", alpha, "created_by_principal_ref", "agent:tester")
	beta := c.task("/v1/tasks/create", `{"path":"inbox/beta"}`)
	requireField(t, "created beta", beta, "title", "beta")
	requireField(t, "created beta", beta, "priority", float64(3))
	alphaID, betaID := alpha["id"].(string), beta["id"].(string)

	updated := c.task("/v1/tasks/update", `{"selector":"`+alphaID+`","fields":{"title":"Alpha 2","priority":1,"ignored":"x"}}`)
	requireField(t, "updated alpha", updated, "title", "Alpha 2")
	requireField(t, "updated alpha", updated, "priority", float64(1))
	requireField(t, "updated alpha", updated, "etag", alpha["etag"].(float64)+1)
	c.post("/v1/tasks/update", `{"selector":"`+alphaID+`","fields":{"ignored":"x"}}`, http.StatusBadRequest)

	comment := c.post("/v1/comments/create", `{"task":"`+alphaID+`","body":"  first note  ","meta":{"k":"v"}}`, http.StatusOK)["comment"].(map[string]interface{})
	requireField(t, "created comment", comment, "body", "first note")
	comments := listOf(t, c.post("/v1/comments/list", `{"task":"`+alphaID+`"}`, http.StatusOK), "comments")
	if len(comments) != 1 {
		t.Fatalf("comments = %v; want one", comments)
	}
	listed := comments[0].(map[string]interface{})
	requireField(t, "listed comment", listed, "body", "first note")
	requireField(t, "listed comment", listed, "task_id", alphaID)
	requireField(t, "listed comment", listed, "meta", `{"k":"v"}`)
	requireField(t, "listed comment", listed, "created_by_principal_ref", "agent:tester")

	if got := listOf(t, c.post("/v1/relations/list", `{"task":"`+alphaID+`"}`, http.StatusOK), "relations"); got != nil {
		t.Fatalf("relations before any = %v; want null", got)
	}
	c.post("/v1/relations/create", `{"from":"`+alphaID+`","kind":"blocks","to":"`+alphaID+`"}`, http.StatusBadRequest)
	c.post("/v1/relations/create", `{"from":"`+alphaID+`","kind":"bogus","to":"`+betaID+`"}`, http.StatusBadRequest)
	requireField(t, "relation create", c.post("/v1/relations/create", `{"from":"`+alphaID+`","kind":"blocks","to":"`+betaID+`"}`, http.StatusOK), "ok", true)
	for task, want := range map[string][2]string{alphaID: {"outgoing", betaID}, betaID: {"incoming", alphaID}} {
		relations := listOf(t, c.post("/v1/relations/list", `{"task":"`+task+`"}`, http.StatusOK), "relations")
		if len(relations) != 1 {
			t.Fatalf("relations of %s = %v; want one", task, relations)
		}
		rel := relations[0].(map[string]interface{})
		requireField(t, "relation of "+task, rel, "direction", want[0])
		requireField(t, "relation of "+task, rel, "task_id", want[1])
		requireField(t, "relation of "+task, rel, "kind", "blocks")
		requireField(t, "relation of "+task, rel, "created_by_id", "agent:tester")
	}

	detail := c.task("/v1/tasks/get", `{"selector":"`+alphaID+`"}`)
	if len(listOf(t, detail, "comments")) != 1 || len(listOf(t, detail, "relations")) != 1 {
		t.Fatalf("detail = %v; want one comment and one relation", detail)
	}
	bare := c.task("/v1/tasks/get", `{"selector":"`+alphaID+`","include_comments":false,"include_relations":false}`)
	if bare["comments"] != nil || bare["relations"] != nil {
		t.Fatalf("detail without includes = %v; want no comments or relations", bare)
	}

	requireField(t, "relation delete", c.post("/v1/relations/delete", `{"from":"`+alphaID+`","kind":"blocks","to":"`+betaID+`"}`, http.StatusOK), "ok", true)
	requireField(t, "relation re-delete", c.post("/v1/relations/delete", `{"from":"`+alphaID+`","kind":"blocks","to":"`+betaID+`"}`, http.StatusNotFound), "message", "relation not found")

	c.post("/v1/tasks/restore", `{"selector":"`+alphaID+`"}`, http.StatusBadRequest)
	archived := c.task("/v1/tasks/archive", `{"selector":"`+alphaID+`"}`)
	requireField(t, "archived alpha", archived, "state", "archived")
	c.post("/v1/tasks/restore", `{"selector":"`+alphaID+`","state":"deleted"}`, http.StatusBadRequest)
	c.post("/v1/tasks/restore", `{"selector":"`+alphaID+`","ifMatch":1}`, http.StatusConflict)
	restored := c.task("/v1/tasks/restore", `{"selector":"`+alphaID+`","state":"in_progress","fields":{"title":"Alpha 3"}}`)
	requireField(t, "restored alpha", restored, "state", "in_progress")
	requireField(t, "restored alpha", restored, "title", "Alpha 3")
	requireField(t, "restored alpha", restored, "updated_by_principal_ref", "agent:tester")

	tasks := listOf(t, c.post("/v1/tasks/list", `{"filter":"all"}`, http.StatusOK), "tasks")
	if len(tasks) != 2 {
		t.Fatalf("tasks/list = %v; want both tasks", tasks)
	}
	tree := c.post("/v1/containers/tree", ``, http.StatusOK)
	requireField(t, "tree", tree, "path", ".")
}
