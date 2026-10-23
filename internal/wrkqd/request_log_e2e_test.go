package wrkqd

// request_log_e2e_test.go: the logging L3 request-line contract, end to end.
// Foundry's fitkit probes read logs only on their own host and wrkqd's log
// lives on mini, so this gate is what holds the line shape (Lance, 2026-10-23).
//
// A built wrkqd serves a scratch DB on a scratch loopback port with stdout and
// stderr in separate files, exactly as launchd splits wrkqd.log/wrkqd.err.log.
// Calls come from a built wrkq CLI and from pkg/client in-process, both over
// the rpc:// HTTP transport. Nothing touches a live daemon or the ambient env.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/useragent"
	"github.com/lherron/wrkq/internal/wrkqapi"
	"github.com/lherron/wrkq/pkg/client"
)

const (
	e2eToken        = "request-log-e2e"
	e2eSampleWindow = 2 * time.Second
)

type e2eLine struct {
	Level     string         `json:"level"`
	Component string         `json:"component"`
	Event     string         `json:"event"`
	Data      map[string]any `json:"data"`
	TS        string         `json:"ts"`
}

func TestRequestLogE2E(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs wrkqd")
	}
	database, _ := setupTestEnv(t)
	dbPath := database.Path()
	catalog := os.Getenv("WRKF_HOOK_CATALOG")
	if err := database.Close(); err != nil {
		t.Fatalf("close seed db: %v", err)
	}

	bin := t.TempDir()
	buildE2EBinary(t, bin, "wrkqd")
	buildE2EBinary(t, bin, "wrkq")

	home := t.TempDir()
	addr := freeLoopbackAddr(t)
	stdoutPath := filepath.Join(home, "wrkqd.log")
	stderrPath := filepath.Join(home, "wrkqd.err.log")
	startE2EDaemon(t, filepath.Join(bin, "wrkqd"), addr, dbPath, catalog, home, stdoutPath, stderrPath)

	// 1. One real CLI call: exactly one line for its op, full L3 shape.
	runE2ECLI(t, filepath.Join(bin, "wrkq"), addr, home, "projects", "--json")
	ok := waitForLines(t, stdoutPath, func(l e2eLine) bool { return l.Data["op"] == "wrkq.project.listView" }, 1)
	if len(ok) != 1 {
		t.Fatalf("want exactly 1 request line for wrkq.project.listView, got %d: %+v", len(ok), ok)
	}
	line := ok[0]
	assertField(t, line.Level, "info", "level")
	assertField(t, line.Component, "http", "component")
	assertField(t, line.Event, "http.request", "event")
	assertField(t, line.Data["listener"], "main", "listener")
	assertField(t, line.Data["route"], "/v1/rpc", "route")
	assertField(t, line.Data["path"], "/v1/rpc", "path")
	assertField(t, line.Data["method"], "POST", "method")
	assertField(t, line.Data["remote"], "127.0.0.1", "remote")
	if c, _ := line.Data["client"].(string); !regexp.MustCompile(`^wrkq/`).MatchString(c) {
		t.Fatalf("client %q does not match ^wrkq/", line.Data["client"])
	}
	if s, _ := line.Data["status"].(float64); s != 200 {
		t.Fatalf("status = %v, want 200", line.Data["status"])
	}
	if _, isNum := line.Data["durationMs"].(float64); !isNum {
		t.Fatalf("durationMs = %v, want a number", line.Data["durationMs"])
	}
	if _, err := time.Parse("2006-01-02T15:04:05.000Z", line.TS); err != nil {
		t.Fatalf("ts %q: %v", line.TS, err)
	}

	// 2. A JSON-RPC not-found inside HTTP 200 logs at warn with op and code.
	tr, err := client.NewRemote(addr, e2eToken, client.WrkqProfile)
	if err != nil {
		t.Fatalf("pkg/client NewRemote: %v", err)
	}
	t.Cleanup(func() { _ = tr.Close() })
	_, err = tr.Call(context.Background(), "wrkq.task.show", wrkqapi.TaskShowParams{Task: "T-99999"})
	var rpcErr *client.Error
	if !errors.As(err, &rpcErr) {
		t.Fatalf("wrkq.task.show T-99999: want an rpc error, got %v", err)
	}
	notFound := waitForLines(t, stdoutPath, func(l e2eLine) bool { return l.Data["op"] == "wrkq.task.show" }, 1)
	if len(notFound) != 1 {
		t.Fatalf("want exactly 1 line for wrkq.task.show, got %d: %+v", len(notFound), notFound)
	}
	assertField(t, notFound[0].Level, "warn", "level")
	assertField(t, notFound[0].Data["status"], float64(200), "status")
	assertField(t, notFound[0].Data["code"], float64(-32004), "code")
	if m, _ := notFound[0].Data["message"].(string); m == "" {
		t.Fatalf("not-found line carries no message: %+v", notFound[0])
	}

	// 3. A burst of a sampled poll op logs its first success, then one line
	// carrying `suppressed` once the window has passed.
	me := useragent.String()
	sampled := func(l e2eLine) bool {
		return l.Data["op"] == "wrkq.container.taskCounts" && l.Data["client"] == me
	}
	const burst = 10
	start := time.Now()
	for i := 0; i < burst; i++ {
		taskCounts(t, tr)
	}
	if time.Since(start) >= e2eSampleWindow {
		t.Fatalf("burst took %s, longer than the %s sample window", time.Since(start), e2eSampleWindow)
	}
	time.Sleep(time.Until(start.Add(e2eSampleWindow + 200*time.Millisecond)))
	taskCounts(t, tr)
	polls := waitForLines(t, stdoutPath, sampled, 2)
	if len(polls) != 2 {
		t.Fatalf("want 2 sampled lines for %d+1 taskCounts calls, got %d: %+v", burst, len(polls), polls)
	}
	var carrying []e2eLine
	for _, l := range polls {
		if _, has := l.Data["suppressed"]; has {
			carrying = append(carrying, l)
		}
	}
	if len(carrying) != 1 {
		t.Fatalf("want exactly 1 line carrying suppressed, got %d: %+v", len(carrying), polls)
	}
	assertField(t, carrying[0].Data["suppressed"], float64(burst-1), "suppressed")

	// Request lines are the stdout half only; stderr keeps diagnostics.
	if errLog, _ := os.ReadFile(stderrPath); strings.Contains(string(errLog), `"http.request"`) {
		t.Fatalf("request lines leaked into stderr:\n%s", errLog)
	}
}

func taskCounts(t *testing.T, tr client.Transport) {
	t.Helper()
	if _, err := tr.Call(context.Background(), "wrkq.container.taskCounts", wrkqapi.ContainerTaskCountsParams{}); err != nil {
		t.Fatalf("wrkq.container.taskCounts: %v", err)
	}
}

func assertField(t *testing.T, got, want any, name string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s = %#v, want %#v", name, got, want)
	}
}

// hermeticEnv is a minimal environment: no WRKQ_*/WRKQD_*/HRC_*/GIT_* leaks
// in from the seat running the tests.
func hermeticEnv(home string, extra ...string) []string {
	env := []string{"HOME=" + home, "PATH=" + os.Getenv("PATH"), "TMPDIR=" + os.TempDir()}
	return append(env, extra...)
}

func buildE2EBinary(t *testing.T, dir, name string) {
	t.Helper()
	cmd := exec.Command("go", "build", "-tags", "sqlite_fts5,wrkq_local",
		"-ldflags", "-X github.com/lherron/wrkq/internal/useragent.Version=e2e",
		"-o", filepath.Join(dir, name), "./cmd/"+name)
	cmd.Dir = filepath.Join("..", "..")
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build ./cmd/%s: %v\n%s", name, err, out)
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func startE2EDaemon(t *testing.T, bin, addr, dbPath, catalog, home, stdoutPath, stderrPath string) {
	t.Helper()
	stdout, err := os.Create(stdoutPath)
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.Create(stderrPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "--addr", addr, "--db", dbPath, "--token", e2eToken,
		"--poll-sample-window", e2eSampleWindow.String())
	cmd.Dir = home
	cmd.Env = hermeticEnv(home, "WRKF_HOOK_CATALOG="+catalog)
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("start wrkqd: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Signal(os.Interrupt)
		done := make(chan struct{})
		go func() { _ = cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = cmd.Process.Kill()
			<-done
		}
		_ = stdout.Close()
		_ = stderr.Close()
		if t.Failed() {
			errLog, _ := os.ReadFile(stderrPath)
			t.Logf("wrkqd stderr:\n%s", errLog)
		}
	})

	healthClient := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(15 * time.Second)
	for {
		req, _ := http.NewRequest(http.MethodGet, "http://"+addr+"/v1/health", nil)
		req.Header.Set("Authorization", "Bearer "+e2eToken)
		resp, err := healthClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		if time.Now().After(deadline) {
			errLog, _ := os.ReadFile(stderrPath)
			t.Fatalf("wrkqd never answered /v1/health at %s: %v\n%s", addr, err, errLog)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func runE2ECLI(t *testing.T, bin, addr, home string, args ...string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = home
	cmd.Env = hermeticEnv(home, "WRKQ_DB=rpc://"+addr, "WRKQD_TOKEN="+e2eToken)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("wrkq %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// waitForLines returns the request lines matching keep, waiting briefly for
// at least want of them: a line is written after its response, so a client
// can return before the line lands. It then settles once more so a duplicate
// line would still be counted.
func waitForLines(t *testing.T, path string, keep func(e2eLine) bool, want int) []e2eLine {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := readLines(t, path, keep)
		if len(got) >= want || time.Now().After(deadline) {
			time.Sleep(200 * time.Millisecond)
			return readLines(t, path, keep)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func readLines(t *testing.T, path string, keep func(e2eLine) bool) []e2eLine {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var out []e2eLine
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var l e2eLine
		if err := json.Unmarshal(sc.Bytes(), &l); err != nil {
			t.Fatalf("stdout carries a non-JSON line %q: %v", sc.Text(), err)
		}
		if l.Event != "http.request" {
			t.Fatalf("stdout carries a non-request line: %s", sc.Text())
		}
		if keep(l) {
			out = append(out, l)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(fmt.Errorf("scan %s: %w", path, err))
	}
	return out
}
