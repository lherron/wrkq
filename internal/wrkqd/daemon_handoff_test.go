package wrkqd

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

func TestServeDaemonInheritsListenerAndChangesInstance(t *testing.T) {
	database, dbPath := setupTestEnv(t)
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	listener, err := net.ListenTCP("tcp", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	file, err := listener.File()
	if err != nil {
		t.Fatal(err)
	}
	_ = listener.Close()
	opts := DaemonOptions{Addr: "127.0.0.1:0", DBPath: dbPath, Token: "test"}
	first := startInheritedDaemonForTest(t, file, opts)
	if _, ok := os.LookupEnv("WRKQD_LISTEN_FD"); ok {
		t.Fatal("inherited fd remained in the environment")
	}
	id := probeDaemonInstance(t, "http://"+addr, nil)
	if _, err := os.Stat(dbPath + "-shm"); err != nil {
		t.Fatalf("SQLite shared memory was absent while serving: %v", err)
	}
	file = first(syscall.SIGHUP)
	if file == nil {
		t.Fatal("SIGHUP returned no listener")
	}
	if _, err := os.Stat(dbPath + "-shm"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("SQLite shared memory still present after ServeDaemon returned: %v", err)
	}
	second := startInheritedDaemonForTest(t, file, opts)
	if next := probeDaemonInstance(t, "http://"+addr, nil); next == id {
		t.Fatalf("successive daemon starts shared instance id %q", next)
	}
	if file := second(syscall.SIGTERM); file != nil {
		_ = file.Close()
		t.Fatal("SIGTERM returned a handoff listener")
	}
}

func startInheritedDaemonForTest(t *testing.T, file *os.File, opts DaemonOptions) func(os.Signal) *os.File {
	t.Helper()
	// Transfer ownership with a duplicate, as exec would.
	fd, err := syscall.Dup(int(file.Fd()))
	if err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	t.Setenv("WRKQD_LISTEN_FD", strconv.Itoa(fd))
	return startDaemonForTest(t, opts)
}

func TestServeDaemonHandoffPreservesUnixPath(t *testing.T) {
	_, dbPath := setupTestEnv(t)
	// Darwin's sockaddr_un has room for only 103 path bytes.
	dir, err := os.MkdirTemp("/tmp", "wrkqd-handoff.")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "wrkqd.sock")
	opts := DaemonOptions{Unix: path, DBPath: dbPath, Token: "test"}
	dialer := &net.Dialer{}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "unix", path)
	}}
	t.Cleanup(transport.CloseIdleConnections)
	stop := startDaemonForTest(t, opts)
	before := probeDaemonInstance(t, "http://unix", transport)
	file := stop(syscall.SIGHUP)
	if file == nil {
		t.Fatal("SIGHUP returned no listener")
	}
	beforeSocket, err := os.Stat(path)
	if err != nil || beforeSocket.Mode()&os.ModeSocket == 0 {
		t.Fatalf("socket path after SIGHUP: %v, %v", beforeSocket, err)
	}
	stop = startInheritedDaemonForTest(t, file, opts)
	if after := probeDaemonInstance(t, "http://unix", transport); after == before {
		t.Fatal("unix dial reached the old daemon instance")
	}
	if afterSocket, err := os.Stat(path); err != nil || !os.SameFile(beforeSocket, afterSocket) {
		t.Fatalf("inheriting the socket replaced its path: %v, %v", afterSocket, err)
	}
	if file := stop(syscall.SIGTERM); file != nil {
		_ = file.Close()
		t.Fatal("SIGTERM returned a handoff listener")
	}
}

func probeDaemonInstance(t *testing.T, url string, transport *http.Transport) string {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	if transport != nil {
		client.Transport = transport
	}
	var id string
	for _, tc := range []struct {
		path  string
		token string
		code  int
	}{
		{"/v1/health", "", http.StatusUnauthorized},
		{"/v1/health", "test", http.StatusOK},
		{"/v1/rpc", "test", http.StatusMethodNotAllowed},
		{"/missing", "", http.StatusNotFound},
	} {
		req, err := http.NewRequest(http.MethodGet, url+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Close = true
		req.Header.Set("Authorization", "Bearer "+tc.token)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		got := resp.Header.Get("X-Wrkqd-Instance")
		if resp.StatusCode != tc.code || got == "" || (id != "" && got != id) {
			t.Fatalf("%s: status %d, instance %q; want status %d and consistent instance (previous %q)", tc.path, resp.StatusCode, got, tc.code, id)
		}
		id = got
	}
	return id
}
