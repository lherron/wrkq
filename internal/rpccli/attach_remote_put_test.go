//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// remoteAttachServer is a wrkqd stand-in that records every RPC method it is
// asked for, so a test can assert WHICH transfer path `attach put` took rather
// than only that it succeeded.
type remoteAttachServer struct {
	locator string

	mu      sync.Mutex
	methods []string
}

func (s *remoteAttachServer) called(method string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range s.methods {
		if m == method {
			return true
		}
	}
	return false
}

func (s *remoteAttachServer) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.methods...)
}

// startRemoteAttachServer boots an in-process RPC server behind an httptest
// listener and returns an `rpc://host:port` locator for it. Attachment storage is
// the server's own dir, exactly as a real remote daemon owns it.
func startRemoteAttachServer(t *testing.T, dbPath, attachDir string) *remoteAttachServer {
	t.Helper()

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	cfg.DBPath = dbPath
	cfg.AttachDir = attachDir
	api, opts, err := bootstrap.Server(database, cfg)
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	rpcServer := workrpc.NewServer(nil)
	workrpc.RegisterAPI(rpcServer, api, opts)

	srv := &remoteAttachServer{}
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req workrpc.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		srv.mu.Lock()
		srv.methods = append(srv.methods, req.Method)
		srv.mu.Unlock()
		resp, ok := rpcServer.HandleRequest(r.Context(), req)
		if !ok {
			t.Errorf("unexpected rpc exit")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(httpServer.Close)

	srv.locator = "rpc://" + strings.TrimPrefix(httpServer.URL, "http://")
	return srv
}

func runAttachCLI(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), stderr.String(), err
}

// TestAttachPutRemoteFileUsesByteTransfer proves the T-08374 fix: against a
// REMOTE endpoint, `attach put <task> <file>` must stream the caller-local file
// over chunked wrkq.attachment.addBytes and must NOT send the host path to
// wrkq.attachment.add. The recorded method list is the biting assertion — client
// and server share a filesystem in-test, so the host-path call would also have
// SUCCEEDED, and a success-only check would not distinguish the two paths.
func TestAttachPutRemoteFileUsesByteTransfer(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	attachDir := t.TempDir()
	remote := startRemoteAttachServer(t, dbPath, attachDir)

	srcDir := t.TempDir()
	srcPath := filepath.Join(srcDir, "payload.txt")
	content := "remote attachment bytes\n"
	if err := os.WriteFile(srcPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write source: %v", err)
	}

	stdout, stderr, err := runAttachCLI(t,
		"--db", remote.locator, "--project", "rpccli-test-proj",
		"attach", "put", taskID, srcPath,
	)
	if err != nil {
		t.Fatalf("remote attach put: %v\n%s", err, stderr)
	}

	if remote.called("wrkq.attachment.add") {
		t.Fatalf("remote attach put sent the host PATH via wrkq.attachment.add; methods=%v", remote.seen())
	}
	if !remote.called("wrkq.attachment.addBytes") {
		t.Fatalf("remote attach put did not use the byte path; methods=%v", remote.seen())
	}

	// --name defaults to the basename, so a remote put takes the same arguments a
	// local one does (the byte path requires a filename server-side).
	if !strings.Contains(stdout, `"filename": "payload.txt"`) {
		t.Fatalf("remote attach put stdout missing basename-derived filename:\n%s", stdout)
	}

	// The bytes landed intact in the SERVER's attach dir, and read back whole.
	getOut, getErr, err := runAttachCLI(t,
		"--db", remote.locator, "--project", "rpccli-test-proj",
		"attach", "get", "ATT-00001", "--as", "-",
	)
	if err != nil {
		t.Fatalf("remote attach get: %v\n%s", err, getErr)
	}
	if getOut != content {
		t.Fatalf("round-tripped bytes = %q want %q", getOut, content)
	}
}

// TestAttachPutRemoteMissingFileFailsCallerSide proves the error now tells the
// truth: a source path that is missing HERE fails before the server is consulted,
// naming the caller's own path. Previously the server reported "no such file or
// directory" about a path it alone had looked for.
func TestAttachPutRemoteMissingFileFailsCallerSide(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	remote := startRemoteAttachServer(t, dbPath, t.TempDir())

	missing := filepath.Join(t.TempDir(), "not-here.txt")
	_, stderr, err := runAttachCLI(t,
		"--db", remote.locator, "--project", "rpccli-test-proj",
		"attach", "put", taskID, missing,
	)
	if err == nil {
		t.Fatalf("remote attach put accepted a missing source file\n%s", stderr)
	}
	if !strings.Contains(err.Error(), missing) {
		t.Fatalf("error does not name the caller's path %q: %v", missing, err)
	}
	if remote.called("wrkq.attachment.add") || remote.called("wrkq.attachment.addBytes") {
		t.Fatalf("a missing local file still reached the server; methods=%v", remote.seen())
	}
}
