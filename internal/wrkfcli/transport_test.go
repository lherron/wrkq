//go:build wrkq_local

package wrkfcli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

func TestConfiguredTransportWrkfReadParityLocalAndRemote(t *testing.T) {
	t.Setenv("WRKF_HOOK_CATALOG", explicitEmptyHookCatalog(t))
	dbPath := filepath.Join(t.TempDir(), "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatal(err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		resp, ok := rpcServer.HandleRequest(r.Context(), req)
		if !ok {
			t.Error("unexpected rpc exit")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(httpServer.Close)
	oldDB := flagDB
	t.Cleanup(func() { flagDB = oldDB })
	t.Setenv("WRKQD_TOKEN", "")
	t.Setenv("WRKQD_TOKEN_FILE", "")

	flagDB = dbPath
	local, _, closeLocal, err := openConfiguredTransport(nil)
	if err != nil {
		t.Fatalf("open local: %v", err)
	}
	localResult, err := local.Call(t.Context(), "wrkf.workflow.list", map[string]any{})
	if err != nil {
		t.Fatalf("local workflow.list: %v", err)
	}
	closeLocal()

	flagDB = "rpc://" + strings.TrimPrefix(httpServer.URL, "http://")
	remote, cfg, closeRemote, err := openConfiguredTransport(nil)
	if err != nil {
		t.Fatalf("open remote: %v", err)
	}
	defer closeRemote()
	if cfg.DBPath != "" || cfg.RemoteEndpoint == "" {
		t.Fatalf("remote locator not normalized: %#v", cfg)
	}
	remoteResult, err := remote.Call(t.Context(), "wrkf.workflow.list", map[string]any{})
	if err != nil {
		t.Fatalf("remote workflow.list: %v", err)
	}
	if string(remoteResult) != string(localResult) {
		t.Fatalf("workflow.list differs by transport:\nlocal  %s\nremote %s", localResult, remoteResult)
	}
}

func TestConfiguredTransportRejectsHookCatalogOverrideForRemote(t *testing.T) {
	oldDB, oldHook := flagDB, flagHookCatalog
	t.Cleanup(func() {
		flagDB = oldDB
		flagHookCatalog = oldHook
	})
	flagDB = "rpc://127.0.0.1:17171"
	flagHookCatalog = "/caller/workspace/hook-catalog.json"
	_, _, _, err := openConfiguredTransport(nil)
	if err == nil || !strings.Contains(err.Error(), "canonical-node configuration") {
		t.Fatalf("remote hook catalog override error=%v, want hard refusal", err)
	}
}

func TestTaskInstancesCLIJSONParityAndInspectCompatibility(t *testing.T) {
	hookCatalog := explicitEmptyHookCatalog(t)
	dbPath := filepath.Join(t.TempDir(), "wrkq.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })

	actorUUID := "00000000-0000-4000-8000-0000000000a0"
	projectUUID := "a3000000-0000-4000-8000-000000000001"
	taskUUID := "b3000000-0000-4000-8000-000000000001"
	if _, err := database.Exec(`
		INSERT INTO containers (uuid, slug, title, parent_uuid, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, 'instances-cli-project', 'Instances CLI Project',
		        (SELECT uuid FROM containers WHERE kind = 'root'), 'project', ?, ?)
	`, projectUUID, actorUUID, actorUUID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`
		INSERT INTO tasks (uuid, slug, title, project_uuid, state, priority, kind, created_by_actor_uuid, updated_by_actor_uuid)
		VALUES (?, 'instances-cli-task', 'Instances CLI Task', ?, 'open', 2, 'task', ?, ?)
	`, taskUUID, projectUUID, actorUUID, actorUUID); err != nil {
		t.Fatal(err)
	}
	var taskID string
	if err := database.QueryRow(`SELECT id FROM tasks WHERE uuid = ?`, taskUUID).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	svc := workflow.NewService(database)
	instance, err := svc.AttachTask(taskID, workflow.BuiltinSimpleTaskV2TemplateRef, "agent:cli-parity")
	if err != nil {
		t.Fatal(err)
	}

	t.Setenv("WRKF_HOOK_CATALOG", hookCatalog)
	api, opts, err := bootstrap.Server(database, &config.Config{
		DBPath:              dbPath,
		DefaultPrincipalRef: "agent:cli-parity",
		AttachmentsMaxMB:    50,
	})
	if err != nil {
		t.Fatal(err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		resp, ok := rpcServer.HandleRequest(r.Context(), req)
		if !ok {
			t.Error("unexpected rpc exit")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(httpServer.Close)

	local := runWrkfCLI(t, hookCatalog, "--db", dbPath, "--json", "task", "instances", taskID)
	remote := runWrkfCLI(t, hookCatalog, "--db", "rpc://"+strings.TrimPrefix(httpServer.URL, "http://"), "--json", "task", "instances", taskID)
	if string(remote) != string(local) {
		t.Fatalf("task instances differs by transport:\nlocal  %s\nremote %s", local, remote)
	}
	var envelope map[string][]map[string]any
	if err := json.Unmarshal(local, &envelope); err != nil {
		t.Fatalf("instances JSON: %v\n%s", err, local)
	}
	if len(envelope["instances"]) != 1 || envelope["instances"][0]["id"] != instance.ID {
		t.Fatalf("instances envelope = %#v, want %s", envelope, instance.ID)
	}

	inspect := runWrkfCLI(t, hookCatalog, "--db", dbPath, "--json", "task", "inspect", taskID)
	var singleton map[string]any
	if err := json.Unmarshal(inspect, &singleton); err != nil {
		t.Fatalf("inspect JSON: %v\n%s", err, inspect)
	}
	if singleton["id"] != instance.ID {
		t.Fatalf("inspect id = %v, want %s", singleton["id"], instance.ID)
	}
	if _, wrapped := singleton["instance"]; wrapped {
		t.Fatalf("CLI inspect was wrapped in-place: %s", inspect)
	}
	if _, listed := singleton["instances"]; listed {
		t.Fatalf("CLI inspect changed to list shape: %s", inspect)
	}
}

func runWrkfCLI(t *testing.T, hookCatalog string, args ...string) []byte {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "go", append([]string{"run", "-tags", "sqlite_fts5,wrkq_local", "./cmd/wrkf"}, args...)...)
	cmd.Dir = filepath.Clean(filepath.Join(wd, "..", ".."))
	cmd.Env = append(os.Environ(),
		"WRKF_HOOK_CATALOG="+hookCatalog,
		"WRKF_PRINCIPAL_REF=agent:cli-parity",
		"WRKQD_TOKEN=",
		"WRKQD_TOKEN_FILE=",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("wrkf %v: %v\n%s", args, err, out)
	}
	return out
}