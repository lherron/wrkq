package rpccli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spf13/cobra"
)

const updateTestRevision = "0123456789abcdef0123456789abcdef01234567"

func selfUpdateTestSetup(t *testing.T, download string) (string, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var handshakes, fetches atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-node-token" {
			t.Error("request did not use the configured node token")
		}
		switch r.URL.Path {
		case "/v1/rpc":
			handshakes.Add(1)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"future","protocolSchemaHash":"sha256:future","server":{"revision":"` + updateTestRevision + `"}}}`))
		case "/v1/client/wrkq":
			fetches.Add(1)
			if download == "" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"code":"client_unavailable"}`))
				return
			}
			w.Header().Set("X-Wrkq-Revision", updateTestRevision)
			_, _ = w.Write([]byte(download))
		default:
			t.Errorf("unexpected route %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	home := t.TempDir()
	endpoint := strings.TrimPrefix(srv.URL, "http://")
	t.Setenv("HOME", home)
	t.Setenv("WRKQ_DB", "rpc://"+endpoint)
	t.Setenv("WRKQ_DB_PATH", "")
	t.Setenv("WRKQ_DB_PATH_FILE", "")
	t.Setenv("WRKQD_TOKEN", "test-node-token")
	t.Setenv("WRKQD_TOKEN_FILE", "")
	t.Setenv("WRKQ_CLIENT_SELF_UPDATE", "1")
	if value, present := os.LookupEnv("WRKQ_SELF_UPDATED"); present {
		t.Setenv("WRKQ_SELF_UPDATED", value)
		if err := os.Unsetenv("WRKQ_SELF_UPDATED"); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(home)
	configDir := filepath.Join(home, ".config", "wrkq")
	binDir := filepath.Join(home, ".local", "bin")
	for _, dir := range []string{configDir, binDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yaml"), []byte("client_self_update_endpoint: "+endpoint+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(binDir, "wrkq")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nprintf '%s\\n' '{\"commit\":\"fffffff\"}'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	realBin, err := filepath.EvalSymlinks(bin)
	if err != nil {
		t.Fatal(err)
	}
	return realBin, &handshakes, &fetches
}

func TestSelfUpdateExecsBeforeRunE(t *testing.T) {
	download := "#!/bin/sh\nprintf '%s\\n' '{\"commit\":\"0123456\"}'\n"
	bin, handshakes, fetches := selfUpdateTestSetup(t, download)
	execCalled := new(int)
	root := newRootCmdFor("wrkq", clientUpdater{
		executable: func() (string, error) { return bin, nil },
		reexec: func(path string, argv, env []string) error {
			if path != bin || !slices.Equal(argv, os.Args) || env[len(env)-1] != "WRKQ_SELF_UPDATED="+updateTestRevision {
				t.Fatal("re-exec did not preserve invocation and set the loop guard")
			}
			if installed, err := os.ReadFile(bin); err != nil || string(installed) != download {
				t.Fatalf("re-exec did not see the replacement: %v", err)
			}
			panic(execCalled) // A successful syscall.Exec cannot return to Cobra.
		},
	})
	root.SetErr(new(bytes.Buffer))
	root.AddCommand(&cobra.Command{Use: "probe", RunE: func(*cobra.Command, []string) error {
		t.Fatal("RunE consumed the command before re-exec")
		return nil
	}})
	root.SetArgs([]string{"probe"})
	defer func() {
		if got := recover(); got != execCalled {
			t.Fatalf("expected re-exec, got %v", got)
		}
		if handshakes.Load() != 1 || fetches.Load() != 1 {
			t.Fatalf("handshakes = %d, fetches = %d", handshakes.Load(), fetches.Load())
		}
	}()
	_ = root.Execute()
}

func TestSelfUpdateFailedPreRunPreservesAgentContext(t *testing.T) {
	bin, handshakes, fetches := selfUpdateTestSetup(t, "")
	root := newRootCmdFor("wrkq", clientUpdater{
		executable: func() (string, error) { return bin, nil },
		reexec: func(string, []string, []string) error {
			t.Fatal("refused download attempted to re-exec")
			return nil
		},
	})
	var stdout, stderr bytes.Buffer
	root.SetOut(&stdout)
	root.SetErr(&stderr)
	root.SetArgs([]string{"agent-context", "--scope", "cody@wrkq", "--json"})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	var got agentContextOutput
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil || got.Scope == nil ||
		got.DBReachable || !strings.Contains(got.DBError, "rpc protocol mismatch") {
		t.Fatalf("agent-context lost its diagnostic: %s (%v)", &stdout, err)
	}
	if handshakes.Load() != 2 || fetches.Load() != 1 || !strings.Contains(stderr.String(), "client_unavailable") {
		t.Fatalf("handshakes = %d, fetches = %d, stderr = %q", handshakes.Load(), fetches.Load(), &stderr)
	}
}
