package wrkqd

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRemovedRESTRoutesAnswer404 pins the T-12856 removal: wrkqd serves only
// /v1/health and /v1/rpc, so the retired REST routes are unknown paths.
func TestRemovedRESTRoutesAnswer404(t *testing.T) {
	mux := http.NewServeMux()
	(&daemonServer{}).registerRoutes(mux)
	for _, route := range []string{
		"/v1/containers/tree",
		"/v1/tasks/list", "/v1/tasks/get", "/v1/tasks/create", "/v1/tasks/update", "/v1/tasks/archive", "/v1/tasks/restore",
		"/v1/comments/list", "/v1/comments/create",
		"/v1/relations/list", "/v1/relations/create", "/v1/relations/delete",
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, route, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST %s = %d; want 404", route, rec.Code)
		}
	}
}
