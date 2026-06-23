package rpccli

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// buildWrkqFTS builds the wrkq binary WITH the sqlite_fts5 tag so the subprocess
// server's search sidecar has the FTS5 module (the default buildWrkq omits tags).
func buildWrkqFTS(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "wrkq")
	cmd := exec.Command("go", "build", "-tags", "sqlite_fts5", "-o", out, "./cmd/wrkq")
	cmd.Dir = repoRootFromTest(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build -tags sqlite_fts5 ./cmd/wrkq: %v\n%s", err, b)
	}
	return out
}

// searchEnv returns the env that enables a DETERMINISTIC, hermetic search host:
// search ON, the `none` dense provider (pure lexical/FTS — no llama dependency, no
// non-determinism), and a SHARED sidecar path so the in-proc + subprocess servers
// operate on the SAME derived index. Used by both the in-proc bootstrap (via
// config.Load env) and the subprocess (passed as extraEnv).
func searchEnv(sidecarPath string) []string {
	return []string{
		"WRKQ_SEARCH_ENABLED=1",
		"WRKQ_SEARCH_DENSE_PROVIDER=none",
		"WRKQ_SEARCH_DB_PATH=" + sidecarPath,
	}
}

// TestTransportEquivalence_SearchIndex covers the search + index family across
// both transports (T-05114): with a shared deterministic sidecar, an index rebuild
// then search/status return byte-identical results through the in-process pipe and
// a real `wrkq rpc --stdio` subprocess — proving the SERVER owns the sidecar +
// pipeline and the seam is transport-agnostic.
func TestTransportEquivalence_SearchIndex(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a wrkq binary; skipped under -short")
	}
	dbPath, taskID := migratedDBWithTask(t)
	_ = taskID
	binPath := buildWrkqFTS(t)
	sidecar := t.TempDir() + "/search.sqlite"

	for _, kv := range searchEnv(sidecar) {
		k, v, _ := cut(kv, '=')
		t.Setenv(k, v)
	}

	inproc := inProcessTransport(t, dbPath)
	defer func() { _ = inproc.Close() }()

	// Rebuild first so the shared sidecar is populated deterministically.
	if _, err := inproc.Call(context.Background(), "wrkq.index.rebuild", map[string]any{}); err != nil {
		t.Fatalf("in-process index.rebuild: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sub, err := NewSubprocess(ctx, binPath, dbPath, searchEnv(sidecar))
	if err != nil {
		t.Fatalf("NewSubprocess: %v", err)
	}
	defer func() { _ = sub.Close() }()

	// index.status must agree across transports (the sidecar is shared + fresh).
	inStatus, err := inproc.Call(context.Background(), "wrkq.index.status", map[string]any{})
	if err != nil {
		t.Fatalf("in-process index.status: %v", err)
	}
	subStatus, err := sub.Call(ctx, "wrkq.index.status", map[string]any{})
	if err != nil {
		t.Fatalf("subprocess index.status: %v", err)
	}
	if !jsonEqual(t, inStatus, subStatus) {
		t.Errorf("index.status transport results differ:\n in-process: %s\n subprocess: %s", inStatus, subStatus)
	}

	// search.listView must agree across transports. The seeded task's title/desc
	// carry "task"/"smoke"; query a lexical term that FTS/lexical both match.
	searchParams := map[string]any{"query": "smoke", "state": "all"}
	inSearch, err := inproc.Call(context.Background(), "wrkq.search.listView", searchParams)
	if err != nil {
		t.Fatalf("in-process search.listView: %v", err)
	}
	subSearch, err := sub.Call(ctx, "wrkq.search.listView", searchParams)
	if err != nil {
		t.Fatalf("subprocess search.listView: %v", err)
	}
	if !jsonEqual(t, inSearch, subSearch) {
		t.Errorf("search.listView transport results differ:\n in-process: %s\n subprocess: %s", inSearch, subSearch)
	}

	// index.pause / resume map-shaped acks must agree across transports.
	inPause, err := inproc.Call(context.Background(), "wrkq.index.pause", map[string]any{})
	if err != nil {
		t.Fatalf("in-process index.pause: %v", err)
	}
	subPause, err := sub.Call(ctx, "wrkq.index.pause", map[string]any{})
	if err != nil {
		t.Fatalf("subprocess index.pause: %v", err)
	}
	if !jsonEqual(t, inPause, subPause) {
		t.Errorf("index.pause transport results differ:\n in-process: %s\n subprocess: %s", inPause, subPause)
	}
	if _, err := inproc.Call(context.Background(), "wrkq.index.resume", map[string]any{}); err != nil {
		t.Fatalf("in-process index.resume: %v", err)
	}
}

// TestSearchDisabled_TransportError proves that with search disabled (default
// config, no search env), the server reports the legacy "search is disabled"
// message through the transport — the mirror never opens the sidecar itself.
func TestSearchDisabled_TransportError(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	t.Setenv("WRKQ_SEARCH_ENABLED", "0")

	tr := inProcessTransport(t, dbPath)
	defer func() { _ = tr.Close() }()

	_, err := tr.Call(context.Background(), "wrkq.search.listView", map[string]any{"query": "x"})
	if err == nil {
		t.Fatal("expected error when search disabled")
	}
	rpcErr, ok := err.(*Error)
	if !ok {
		t.Fatalf("error type = %T, want *rpccli.Error", err)
	}
	if rpcErr.DomainID != "WRKQ_VALIDATION" {
		t.Errorf("DomainID = %q, want WRKQ_VALIDATION", rpcErr.DomainID)
	}
	if rpcErr.Message != "search is disabled" {
		t.Errorf("Message = %q, want %q", rpcErr.Message, "search is disabled")
	}
}

// cut splits s at the first occurrence of sep (a tiny strings.Cut for byte sep).
func cut(s string, sep byte) (before, after string, found bool) {
	for i := 0; i < len(s); i++ {
		if s[i] == sep {
			return s[:i], s[i+1:], true
		}
	}
	return s, "", false
}
