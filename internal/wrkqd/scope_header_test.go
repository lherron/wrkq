package wrkqd

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// Drive authenticated HTTP RPC through the real registry and SQLite store.
// Each call must emit an event, so missing attribution cannot pass vacuously.
func TestRPCMutationScopeHeader(t *testing.T) {
	for _, header := range []string{"", "cody@inbox:primary", "agent:cody:project:inbox:task:primary", "agent:cody:project:inbox:task:primary/lane:main"} {
		t.Run(header, func(t *testing.T) {
			database, _ := setupTestEnv(t)
			// setupTestEnv seeds P-00001 explicitly; reserve its generated ID.
			if _, err := database.Exec("INSERT INTO container_seq(id) VALUES (1)"); err != nil {
				t.Fatal(err)
			}
			cfg := &config.Config{DBPath: database.Path(), AttachmentsMaxMB: 50}
			api, opts, err := bootstrap.Server(database, cfg)
			if err != nil {
				t.Fatal(err)
			}
			rpc := workrpc.NewServer(nil)
			workrpc.RegisterAPI(rpc, api, opts)
			daemon := &daemonServer{db: database, cfg: cfg, token: "secret", workrpc: rpc}
			server := httptest.NewServer(daemon.withAuth(daemon.handleWorkRPC))
			defer server.Close()
			call := func(method string, params any, scopeHeader, wantScope string, wantError bool) map[string]any {
				t.Helper()
				var before int64
				if err := database.QueryRow("SELECT COALESCE(MAX(id), 0) FROM event_log").Scan(&before); err != nil {
					t.Fatal(err)
				}
				p, _ := json.Marshal(params)
				body, _ := json.Marshal(workrpc.Request{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: method, Params: p})
				req, err := http.NewRequest(http.MethodPost, server.URL+"/v1/rpc", bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				req.Header.Set("Authorization", "Bearer secret")
				req.Header.Set("X-Wrkq-Principal-Ref", "agent:cody")
				req.Header.Set("X-Wrkq-Scope-Ref", scopeHeader)
				response, err := server.Client().Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = response.Body.Close() }()
				var frame struct {
					Result map[string]any `json:"result"`
					Error  *struct {
						Message string         `json:"message"`
						Data    map[string]any `json:"data"`
					} `json:"error"`
				}
				if err := json.NewDecoder(response.Body).Decode(&frame); err != nil {
					t.Fatal(err)
				}
				if wantError {
					if frame.Error == nil || frame.Error.Data["code"] != "WRKQ_VALIDATION" {
						t.Fatalf("%s: want WRKQ_VALIDATION, got %+v", method, frame)
					}
				} else if frame.Error != nil {
					t.Fatalf("%s: %+v", method, frame.Error)
				}
				rows, err := database.Query("SELECT event_type, principal_ref, scope_ref FROM event_log WHERE id > ?", before)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = rows.Close() }()
				count := 0
				for rows.Next() {
					var event, principal string
					var stored sql.NullString
					if err := rows.Scan(&event, &principal, &stored); err != nil {
						t.Fatal(err)
					}
					if principal != "agent:cody" || stored.String != wantScope || stored.Valid != (wantScope != "") {
						t.Errorf("%s/%s attribution = %s/%+v, want agent:cody/%q", method, event, principal, stored, wantScope)
					}
					count++
				}
				if err := rows.Err(); err != nil {
					t.Fatal(err)
				}
				if (!wantError && method != "rpc.initialize" && count == 0) || (wantError && count != 0) {
					t.Fatalf("%s events=%d, wantError=%v", method, count, wantError)
				}
				return frame.Result
			}
			call("rpc.initialize", map[string]any{"protocolVersion": "2026-06-30"}, "", "", false)
			want := ""
			if header != "" {
				want = "cody@inbox:primary"
			}
			mutate := func(method string, p map[string]any) map[string]any { return call(method, p, header, want, false) }
			task := mutate("wrkq.task.create", map[string]any{"path": "inbox/alpha", "title": "Alpha", "requesterScopeRef": "agent:mable:project:inbox", "requesterPrincipalRef": "agent:mable"})
			taskID := task["id"].(string)
			var creator, requester sql.NullString
			if err := database.QueryRow("SELECT created_by_scope_ref, requester_scope_ref FROM tasks WHERE id = ?", taskID).Scan(&creator, &requester); err != nil {
				t.Fatal(err)
			}
			if creator.String != want || creator.Valid != (want != "") || requester.String != "agent:mable:project:inbox" {
				t.Fatalf("creator=%+v requester=%+v", creator, requester)
			}
			other := mutate("wrkq.task.create", map[string]any{"path": "inbox/beta", "title": "Beta"})
			mutate("wrkq.task.update", map[string]any{"task": other["id"], "patch": map[string]any{"title": "Header fallback"}})
			mutate("wrkq.comment.add", map[string]any{"task": other["id"], "body": "Header fallback"})
			mutate("wrkq.relation.add", map[string]any{"fromTask": taskID, "toTask": other["id"], "kind": "blocks"})
			mutate("wrkq.container.create", map[string]any{"path": "inbox/dest"})
			mutate("wrkq.task.move", map[string]any{"task": taskID, "targetPath": "inbox/dest"})
			mutate("wrkq.task.delete", map[string]any{"task": taskID})
			mutate("wrkq.container.create", map[string]any{"path": "inbox/empty"})
			mutate("wrkq.container.move", map[string]any{"container": "inbox/empty", "destination": "inbox/renamed"})
			mutate("wrkq.container.delete", map[string]any{"path": "inbox/renamed"})
			for _, terminal := range []string{"resolve", "abandon"} {
				promise := mutate("wrkq.promise.add", map[string]any{"subject": "Scope proof", "reviewIn": "1h"})
				mutate("wrkq.promise.renew", map[string]any{"promise": promise["id"], "reviewIn": "2h"})
				mutate("wrkq.promise."+terminal, map[string]any{"promise": promise["id"], "note": "done"})
			}
			call("wrkq.task.update", map[string]any{"task": other["id"], "patch": map[string]any{"title": "Explicit"}, "scopeRef": "agent:cody:project:inbox:task:explicit"}, "mable@inbox:primary", "agent:cody:project:inbox:task:explicit", false)
			for _, bad := range []string{"nonsense", "agent:cody", "agent:cody:project:", "mable@inbox:primary"} {
				call("wrkq.task.create", map[string]any{"path": "inbox/invalid", "title": "Invalid"}, bad, "", true)
			}
		})
	}
}
