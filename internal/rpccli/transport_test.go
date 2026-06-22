package rpccli

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/workrpc"
	"github.com/lherron/wrkq/internal/workrpc/bootstrap"
)

// TestInProcessTransport_Lifecycle covers daedalus's required lifecycle test:
// a raw server rejects business calls before initialize; the constructed
// transport initializes successfully; wrkq.task.show succeeds; Close shuts down
// cleanly without leaking the server goroutine.
func TestInProcessTransport_Lifecycle(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)

	// 1. A raw server (no rpc.initialize) rejects business calls. Drive one frame
	// through the real Serve loop over pipes, mirroring server.go's initialize gate.
	assertRejectsBeforeInitialize(t, dbPath)

	before := runtime.NumGoroutine()

	// 2 + 3. Construct transport (performs initialize) and call a business method.
	tr := inProcessTransport(t, dbPath)
	raw, err := tr.Call(context.Background(), "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("wrkq.task.show: %v", err)
	}
	var dto struct {
		ID    string `json:"id"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(raw, &dto); err != nil {
		t.Fatalf("decode dto: %v", err)
	}
	if dto.ID != taskID {
		t.Fatalf("dto id = %q, want %q", dto.ID, taskID)
	}

	// 4. Close shuts down without leaking the server goroutine.
	if err := tr.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := tr.Close(); err != nil { // idempotent
		t.Fatalf("second Close: %v", err)
	}
	assertNoGoroutineLeak(t, before)
}

// TestInProcessTransport_ErrorMapping covers the error-mapping requirement: an
// unknown task surfaces a typed *Error exposing WRKQ_NOT_FOUND and retryable=false
// so the CLI exit-code mapper has what it needs.
func TestInProcessTransport_ErrorMapping(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	tr := inProcessTransport(t, dbPath)
	defer func() { _ = tr.Close() }()

	_, err := tr.Call(context.Background(), "wrkq.task.show", map[string]string{"task": "T-99999999"})
	if err == nil {
		t.Fatal("expected error for unknown task, got nil")
	}
	rpcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *rpccli.Error", err)
	}
	if rpcErr.DomainID != workrpc.CodeWRKQNotFound {
		t.Errorf("DomainID = %q, want %q", rpcErr.DomainID, workrpc.CodeWRKQNotFound)
	}
	if rpcErr.Retryable {
		t.Errorf("Retryable = true, want false for WRKQ_NOT_FOUND")
	}
}

// TestTransportEquivalence_InProcVsSubprocess covers daedalus's transport
// equivalence requirement: the same wrkq.task.show call returns byte-identical
// results through the in-process pipe transport and a real `wrkq rpc --stdio`
// subprocess. (The wrkq.task.show DTO carries no lifecycle-only fields, so no
// normalization is needed here; the initialize handshake — which does carry pid
// and db path — is validated separately inside each transport constructor.)
func TestTransportEquivalence_InProcVsSubprocess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a wrkq binary; skipped under -short")
	}
	dbPath, taskID := migratedDBWithTask(t)
	binPath := buildWrkq(t)

	inproc := inProcessTransport(t, dbPath)
	defer func() { _ = inproc.Close() }()
	inResult, err := inproc.Call(context.Background(), "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("in-process show: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sub, err := NewSubprocess(ctx, binPath, dbPath, nil)
	if err != nil {
		t.Fatalf("NewSubprocess: %v", err)
	}
	defer func() { _ = sub.Close() }()
	subResult, err := sub.Call(ctx, "wrkq.task.show", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess show: %v", err)
	}

	if !jsonEqual(t, inResult, subResult) {
		t.Errorf("transport results differ:\n in-process: %s\n subprocess: %s", inResult, subResult)
	}

	// catView (the cat compatibility projection) must also agree across transports.
	inCat, err := inproc.Call(context.Background(), "wrkq.task.catView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("in-process catView: %v", err)
	}
	subCat, err := sub.Call(ctx, "wrkq.task.catView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess catView: %v", err)
	}
	if !jsonEqual(t, inCat, subCat) {
		t.Errorf("catView transport results differ:\n in-process: %s\n subprocess: %s", inCat, subCat)
	}

	// container.catView (the container compat projection) must also agree.
	inCC, err := inproc.Call(context.Background(), "wrkq.container.catView", map[string]string{"container": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("in-process container.catView: %v", err)
	}
	subCC, err := sub.Call(ctx, "wrkq.container.catView", map[string]string{"container": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("subprocess container.catView: %v", err)
	}
	if !jsonEqual(t, inCC, subCC) {
		t.Errorf("container.catView transport results differ:\n in-process: %s\n subprocess: %s", inCC, subCC)
	}

	// comment.catView must also agree across transports. Seed a comment first via
	// the in-process transport so a known comment id exists.
	if _, err := inproc.Call(context.Background(), "wrkq.comment.add", map[string]any{"task": taskID, "body": "xport ✓"}); err != nil {
		t.Fatalf("seed comment: %v", err)
	}
	inCm, err := inproc.Call(context.Background(), "wrkq.comment.catView", map[string]string{"comment": "C-00001"})
	if err != nil {
		t.Fatalf("in-process comment.catView: %v", err)
	}
	subCm, err := sub.Call(ctx, "wrkq.comment.catView", map[string]string{"comment": "C-00001"})
	if err != nil {
		t.Fatalf("subprocess comment.catView: %v", err)
	}
	if !jsonEqual(t, inCm, subCm) {
		t.Errorf("comment.catView transport results differ:\n in-process: %s\n subprocess: %s", inCm, subCm)
	}

	// relation.listView (Group B compat list projection) must agree across
	// transports. Seed a second task + a relation via the in-process transport.
	t2, err := inproc.Call(context.Background(), "wrkq.task.create",
		map[string]any{"path": "rpccli-test-proj/rel-target", "title": "T2"})
	if err != nil {
		t.Fatalf("seed second task: %v", err)
	}
	var t2dto struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(t2, &t2dto)
	if _, err := inproc.Call(context.Background(), "wrkq.relation.add",
		map[string]any{"fromTask": taskID, "kind": "blocks", "toTask": t2dto.ID}); err != nil {
		t.Fatalf("seed relation: %v", err)
	}
	inRel, err := inproc.Call(context.Background(), "wrkq.relation.listView", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("in-process relation.listView: %v", err)
	}
	subRel, err := sub.Call(ctx, "wrkq.relation.listView", map[string]string{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess relation.listView: %v", err)
	}
	if !jsonEqual(t, inRel, subRel) {
		t.Errorf("relation.listView transport results differ:\n in-process: %s\n subprocess: %s", inRel, subRel)
	}

	// comment.listView (first paginated list view) must agree across transports.
	inCl, err := inproc.Call(context.Background(), "wrkq.comment.listView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("in-process comment.listView: %v", err)
	}
	subCl, err := sub.Call(ctx, "wrkq.comment.listView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess comment.listView: %v", err)
	}
	if !jsonEqual(t, inCl, subCl) {
		t.Errorf("comment.listView transport results differ:\n in-process: %s\n subprocess: %s", inCl, subCl)
	}

	// attachment.listView must agree across transports (empty set is sufficient
	// for transport equivalence; populated requires attachment storage setup).
	inAl, err := inproc.Call(context.Background(), "wrkq.attachment.listView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("in-process attachment.listView: %v", err)
	}
	subAl, err := sub.Call(ctx, "wrkq.attachment.listView", map[string]any{"task": taskID})
	if err != nil {
		t.Fatalf("subprocess attachment.listView: %v", err)
	}
	if !jsonEqual(t, inAl, subAl) {
		t.Errorf("attachment.listView transport results differ:\n in-process: %s\n subprocess: %s", inAl, subAl)
	}

	// task.lsView (the ls mixed task/container compat list projection) must agree
	// across transports. List the seeded project's children.
	inLs, err := inproc.Call(context.Background(), "wrkq.task.lsView", map[string]any{"path": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("in-process task.lsView: %v", err)
	}
	subLs, err := sub.Call(ctx, "wrkq.task.lsView", map[string]any{"path": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("subprocess task.lsView: %v", err)
	}
	if !jsonEqual(t, inLs, subLs) {
		t.Errorf("task.lsView transport results differ:\n in-process: %s\n subprocess: %s", inLs, subLs)
	}

	// task.findListView (the find recursive/filtered compat list projection) must
	// agree across transports. Search the seeded project's tasks recursively.
	inFind, err := inproc.Call(context.Background(), "wrkq.task.findListView", map[string]any{"paths": []string{"rpccli-test-proj"}, "type": "t"})
	if err != nil {
		t.Fatalf("in-process task.findListView: %v", err)
	}
	subFind, err := sub.Call(ctx, "wrkq.task.findListView", map[string]any{"paths": []string{"rpccli-test-proj"}, "type": "t"})
	if err != nil {
		t.Fatalf("subprocess task.findListView: %v", err)
	}
	if !jsonEqual(t, inFind, subFind) {
		t.Errorf("task.findListView transport results differ:\n in-process: %s\n subprocess: %s", inFind, subFind)
	}

	// task.treeView (the tree recursive compat projection) must agree across
	// transports. Walk the seeded project's hierarchy.
	inTree, err := inproc.Call(context.Background(), "wrkq.task.treeView", map[string]any{"path": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("in-process task.treeView: %v", err)
	}
	subTree, err := sub.Call(ctx, "wrkq.task.treeView", map[string]any{"path": "rpccli-test-proj"})
	if err != nil {
		t.Fatalf("subprocess task.treeView: %v", err)
	}
	if !jsonEqual(t, inTree, subTree) {
		t.Errorf("task.treeView transport results differ:\n in-process: %s\n subprocess: %s", inTree, subTree)
	}
}

// assertRejectsBeforeInitialize drives a single wrkq.task.show request through a
// real server's Serve loop without first calling rpc.initialize, and asserts the
// server returns an error rather than executing the business call.
func assertRejectsBeforeInitialize(t *testing.T, dbPath string) {
	t.Helper()
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	api, opts, err := bootstrap.Server(database, &config.Config{DBPath: dbPath, AttachmentsMaxMB: 50})
	if err != nil {
		t.Fatalf("bootstrap.Server: %v", err)
	}
	clientR, clientW := io.Pipe()
	serverR, serverW := io.Pipe()
	srv := workrpc.NewServer(serverW)
	workrpc.RegisterAPI(srv, api, opts)
	go func() {
		_ = srv.Serve(context.Background(), clientR)
		_ = serverW.Close()
		_ = database.Close()
	}()
	defer func() { _ = clientW.Close() }()

	req, _ := json.Marshal(workrpc.Request{
		JSONRPC: "2.0", ID: json.RawMessage("1"),
		Method: "wrkq.task.show", Params: json.RawMessage(`{"task":"T-00001"}`),
	})
	if _, err := clientW.Write(append(req, '\n')); err != nil {
		t.Fatalf("write pre-init frame: %v", err)
	}
	line, err := bufio.NewReader(serverR).ReadBytes('\n')
	if err != nil {
		t.Fatalf("read pre-init response: %v", err)
	}
	var resp workrpc.Response
	if err := json.Unmarshal(line, &resp); err != nil {
		t.Fatalf("decode pre-init response: %v", err)
	}
	if resp.Error == nil {
		t.Fatalf("expected error for business call before initialize, got result: %s", resp.Result)
	}
}

func buildWrkq(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "wrkq")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/wrkq")
	cmd.Dir = repoRootFromTest(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/wrkq: %v\n%s", err, b)
	}
	return out
}

func repoRootFromTest(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	return filepath.Clean(filepath.Join(wd, "..", "..")) // internal/rpccli -> repo root
}

func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	ab, _ := json.Marshal(av)
	bb, _ := json.Marshal(bv)
	return string(ab) == string(bb)
}

func assertNoGoroutineLeak(t *testing.T, before int) {
	t.Helper()
	// Allow the server goroutine a moment to unwind after Close.
	for i := 0; i < 50; i++ {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
}
