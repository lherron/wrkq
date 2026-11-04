//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// Exercise the real RPC handlers: a follower must acquire before reading,
// drain even limit-1 pages before parking, and bound a parked wait by its clock.
func TestWrkpFollowCommitWait(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	for range 5 {
		if out, err := runCampaignCLI(t, dbPath, "comment", "add", taskID, "-m", "backlog"); err != nil {
			t.Fatalf("seed: %v %s", err, out)
		}
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatal(err)
	}
	server := workrpc.NewServer(nil)
	workrpc.RegisterAPI(server, api, opts)
	var mu sync.Mutex
	var calls []string
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		calls = append(calls, req.Method)
		mu.Unlock()
		resp, _ := server.HandleRequest(r.Context(), req)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer httpServer.Close()
	t.Setenv("WRKQD_TOKEN_FILE", "")
	t.Setenv("WRKQD_TOKEN", "test-token")
	for _, clock := range []string{"--timeout", "--stall-after"} {
		t.Run(clock, func(t *testing.T) {
			mu.Lock()
			calls = nil
			mu.Unlock()
			cmd := NewWrkpRootCmd()
			cmd.SetArgs([]string{"--db", "rpc://" + strings.TrimPrefix(httpServer.URL, "http://"), "log", "rpccli-test-proj", "--follow", "--ndjson", "--limit", "1", clock, "1200ms"})
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			started := time.Now()
			if err := cmd.ExecuteContext(t.Context()); err != nil {
				t.Fatalf("follow: %v %s", err, stderr.String())
			}
			if elapsed := time.Since(started); elapsed > 3*time.Second {
				t.Fatalf("clock overshot: %s", elapsed)
			}
			if strings.Count(stdout.String(), "backlog") != 5 || strings.Count(stdout.String(), `"type":"wrkp.log.terminal"`) != 1 {
				t.Fatalf("lost backlog or terminal: %s", stdout.String())
			}
			mu.Lock()
			observed := append([]string(nil), calls...)
			mu.Unlock()
			reads, waits := 0, 0
			for _, method := range observed {
				switch method {
				case "wrkq.commit.wait":
					waits++
				case "wrkq.container.timelineView":
					if waits == 0 {
						t.Fatalf("read before token acquisition: %v", observed)
					}
					reads++
				}
			}
			if waits < 2 || reads > 8 {
				t.Fatalf("idle polling or backlog not drained: reads=%d waits=%d calls=%v", reads, waits, observed)
			}
		})
	}
}
