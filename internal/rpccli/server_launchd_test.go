package rpccli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const launchdPrintFixture = `gui/501/com.praesidium.wrkq-server = {
	active count = 1
	path = /Users/lherron/Library/LaunchAgents/com.praesidium.wrkq-server.plist
	type = LaunchAgent
	state = running

	program = /Users/lherron/.local/bin/wrkqd
	arguments = {
		/Users/lherron/.local/bin/wrkqd
		--addr
		100.117.215.92:7171
		--db
		/Users/lherron/praesidium/var/db/wrkq.db
	}

	working directory = /Users/lherron/praesidium

	inherited environment = {
		SSH_AUTH_SOCK => /var/run/com.apple.launchd.UHAHYeWMxL/Listeners
	}

	default environment = {
		PATH => /usr/bin:/bin:/usr/sbin:/sbin
	}

	environment = {
		WRKQD_ADDR => 127.0.0.1:9999
		XPC_SERVICE_NAME => com.praesidium.wrkq-server
	}

	domain = gui/501 [100015]
	pid = 65229
	last exit code = (never exited)
	properties = keepalive | runatload | inferred program | managed LWCR | has LWCR
}
`

func TestParseLaunchdPrintReadsJobShape(t *testing.T) {
	owner := parseLaunchdPrint(launchdPrintFixture)

	if got, want := owner.plistPath, "/Users/lherron/Library/LaunchAgents/com.praesidium.wrkq-server.plist"; got != want {
		t.Fatalf("plistPath=%q want %q", got, want)
	}
	if got, want := owner.programPath(), "/Users/lherron/.local/bin/wrkqd"; got != want {
		t.Fatalf("programPath=%q want %q", got, want)
	}
	if owner.pid == nil || *owner.pid != 65229 {
		t.Fatalf("pid=%v want 65229", owner.pid)
	}
	if got, want := owner.lastExitCode, "(never exited)"; got != want {
		t.Fatalf("lastExitCode=%q want %q", got, want)
	}
	if !strings.Contains(owner.properties, "has LWCR") {
		t.Fatalf("properties=%q want the LWCR markers", owner.properties)
	}
	// Only the job's own environment block is read; inherited and default
	// blocks belong to launchd, not the job.
	if got, want := owner.environment["WRKQD_ADDR"], "127.0.0.1:9999"; got != want {
		t.Fatalf("environment[WRKQD_ADDR]=%q want %q", got, want)
	}
	if _, ok := owner.environment["SSH_AUTH_SOCK"]; ok {
		t.Fatal("inherited environment leaked into the job environment")
	}
	if _, ok := owner.environment["PATH"]; ok {
		t.Fatal("default environment leaked into the job environment")
	}
}

func TestLaunchdEndpointPrefersProgramArguments(t *testing.T) {
	owner := parseLaunchdPrint(launchdPrintFixture)
	addr, unixPath := owner.endpoint()
	if got, want := addr, "100.117.215.92:7171"; got != want {
		t.Fatalf("addr=%q want %q (the address the job actually binds)", got, want)
	}
	if unixPath != "" {
		t.Fatalf("unixPath=%q want empty", unixPath)
	}
}

func TestLaunchdEndpointFallsBackToJobEnvironment(t *testing.T) {
	owner := parseLaunchdPrint(`gui/501/x = {
	program = /Users/lherron/.local/bin/wrkq
	arguments = {
		/Users/lherron/.local/bin/wrkq
		server
		serve
	}
	environment = {
		WRKQD_ADDR => 100.64.0.1:7171
	}
	pid = 42
}
`)
	addr, _ := owner.endpoint()
	if got, want := addr, "100.64.0.1:7171"; got != want {
		t.Fatalf("addr=%q want %q", got, want)
	}
}

func TestLaunchdProbeOptionsDoesNotOverrideExplicitFlags(t *testing.T) {
	owner := parseLaunchdPrint(launchdPrintFixture)
	opts := &serverOptions{addr: "127.0.0.1:7171"}
	if got := launchdProbeOptions(opts, owner); got != opts {
		t.Fatalf("explicit --addr was overridden by the launchd job endpoint")
	}
	probe := launchdProbeOptions(&serverOptions{}, owner)
	if got, want := probe.addr, "100.117.215.92:7171"; got != want {
		t.Fatalf("probe addr=%q want %q", got, want)
	}
}

// fakeLaunchctl records the command sequence and answers `print` from a
// caller-supplied liveness function so unload timing can be simulated.
type fakeLaunchctl struct {
	calls     []string
	loaded    func(printCall int) bool
	prints    int
	bootstrap func(attempt int) (string, error)
	attempts  int
}

func (f *fakeLaunchctl) run(args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "print":
		f.prints++
		if f.loaded == nil || f.loaded(f.prints) {
			return "gui/501/label = {\n}\n", nil
		}
		return "Could not find service", errors.New("exit status 113")
	case "bootout":
		return "", nil
	case "bootstrap":
		f.attempts++
		if f.bootstrap == nil {
			return "", nil
		}
		return f.bootstrap(f.attempts)
	}
	return "", nil
}

func installFakeLaunchctl(t *testing.T, fake *fakeLaunchctl) {
	t.Helper()
	previousRunner, previousInterval := runLaunchctl, launchdPollInterval
	runLaunchctl = fake.run
	launchdPollInterval = time.Millisecond
	t.Cleanup(func() {
		runLaunchctl = previousRunner
		launchdPollInterval = previousInterval
	})
}

func testOwner(t *testing.T) *launchdOwner {
	t.Helper()
	plist := filepath.Join(t.TempDir(), "com.praesidium.wrkq-server.plist")
	if err := os.WriteFile(plist, []byte("<plist/>"), 0o644); err != nil {
		t.Fatalf("write plist: %v", err)
	}
	return &launchdOwner{
		label:         "com.praesidium.wrkq-server",
		domain:        "gui/501",
		serviceTarget: "gui/501/com.praesidium.wrkq-server",
		plistPath:     plist,
	}
}

func TestRelaunchWaitsForBootoutBeforeBootstrapping(t *testing.T) {
	// bootout is asynchronous: launchctl returns while the job is still
	// unloading, and bootstrapping into that window fails and leaves the
	// daemon down.
	fake := &fakeLaunchctl{loaded: func(call int) bool { return call <= 3 }}
	installFakeLaunchctl(t, fake)

	if err := relaunchLaunchdJob(testOwner(t)); err != nil {
		t.Fatalf("relaunchLaunchdJob: %v", err)
	}

	bootoutAt, bootstrapAt := -1, -1
	for i, call := range fake.calls {
		switch {
		case strings.HasPrefix(call, "bootout"):
			bootoutAt = i
		case strings.HasPrefix(call, "bootstrap") && bootstrapAt < 0:
			bootstrapAt = i
		}
	}
	if bootoutAt < 0 || bootstrapAt < 0 {
		t.Fatalf("expected bootout and bootstrap, got %v", fake.calls)
	}
	if bootstrapAt < bootoutAt {
		t.Fatalf("bootstrap ran before bootout: %v", fake.calls)
	}
	for _, call := range fake.calls[bootoutAt+1 : bootstrapAt] {
		if !strings.HasPrefix(call, "print") {
			t.Fatalf("unexpected call between bootout and bootstrap: %q (%v)", call, fake.calls)
		}
	}
	if fake.prints < 4 {
		t.Fatalf("bootstrapped after %d print polls; it must poll until the job is gone (%v)", fake.prints, fake.calls)
	}
}

func TestRelaunchDoesNotBootstrapWhenUnloadNeverCompletes(t *testing.T) {
	fake := &fakeLaunchctl{loaded: func(int) bool { return true }}
	installFakeLaunchctl(t, fake)
	previousTimeout := launchdUnloadTimeout
	launchdUnloadTimeout = 20 * time.Millisecond
	t.Cleanup(func() { launchdUnloadTimeout = previousTimeout })

	err := relaunchLaunchdJob(testOwner(t))
	if err == nil {
		t.Fatal("expected an error when the job never unloads")
	}
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "bootstrap") {
			t.Fatalf("bootstrapped into a half-unloaded job: %v", fake.calls)
		}
	}
}

func TestBootstrapRetriesTransientIOError(t *testing.T) {
	fake := &fakeLaunchctl{
		loaded: func(call int) bool { return call == 1 },
		bootstrap: func(attempt int) (string, error) {
			if attempt == 1 {
				return "Bootstrap failed: 5: Input/output error", errors.New("exit status 5")
			}
			return "", nil
		},
	}
	installFakeLaunchctl(t, fake)

	if err := relaunchLaunchdJob(testOwner(t)); err != nil {
		t.Fatalf("relaunchLaunchdJob: %v", err)
	}
	if fake.attempts < 2 {
		t.Fatalf("bootstrap attempts=%d want a retry after the transient I/O error", fake.attempts)
	}
}

func TestRelaunchRefusesWithoutAReadablePlist(t *testing.T) {
	fake := &fakeLaunchctl{}
	installFakeLaunchctl(t, fake)

	owner := testOwner(t)
	owner.plistPath = filepath.Join(t.TempDir(), "missing.plist")
	err := relaunchLaunchdJob(owner)
	if err == nil || !strings.Contains(err.Error(), "not readable") {
		t.Fatalf("err=%v want a refusal naming the unreadable plist", err)
	}
	for _, call := range fake.calls {
		if strings.HasPrefix(call, "bootout") {
			t.Fatalf("booted the job out with no way to bootstrap it again: %v", fake.calls)
		}
	}
}

func TestInspectLaunchdBinaryDetectsRebuildUnderLiveDaemon(t *testing.T) {
	previous := runCodesign
	runCodesign = func(target string) (string, error) {
		if strings.HasPrefix(target, "+") {
			return "Executable=/x/wrkqd\nCDHash=2543145f7fe318c352536101ea83a27589cbddf4\n", nil
		}
		return "Executable=/x/wrkqd\nCDHash=50bb22e21de88e37a5fd877ff0860aa015deab57\n", nil
	}
	t.Cleanup(func() { runCodesign = previous })

	pid := 4242
	identity := inspectLaunchdBinary(&launchdOwner{program: "/x/wrkqd", pid: &pid})
	if !identity.comparable {
		t.Fatalf("identity is not comparable: %+v", identity)
	}
	if !identity.stale() {
		t.Fatalf("expected a stale binary: on-disk %q running %q", identity.onDisk, identity.running)
	}
}

func TestInspectLaunchdBinaryIsNotStaleWhenHashesMatch(t *testing.T) {
	previous := runCodesign
	runCodesign = func(string) (string, error) {
		return "CDHash=58d7dd79a06fa14ba06bd254ba57a06e30e28a86\n", nil
	}
	t.Cleanup(func() { runCodesign = previous })

	pid := 1
	if inspectLaunchdBinary(&launchdOwner{program: "/x/wrkqd", pid: &pid}).stale() {
		t.Fatal("identical cdhashes reported as stale")
	}
}

func TestInspectLaunchdBinaryUnknownWithoutAPID(t *testing.T) {
	previous := runCodesign
	runCodesign = func(string) (string, error) { return "CDHash=abc\n", nil }
	t.Cleanup(func() { runCodesign = previous })

	identity := inspectLaunchdBinary(&launchdOwner{program: "/x/wrkqd"})
	if identity.comparable || identity.stale() {
		t.Fatalf("a job with no pid must not report drift: %+v", identity)
	}
}

func TestLaunchdFailureDetailNamesTheCodesigningKill(t *testing.T) {
	owner := parseLaunchdPrint(`gui/501/label = {
	path = /Users/lherron/Library/LaunchAgents/com.praesidium.wrkq-server.plist
	last exit code = 9
	last exit reason = namespace = SIGNAL, code = 9, OS_REASON_CODESIGNING
}
`)
	owner.serviceTarget = "gui/501/com.praesidium.wrkq-server"
	detail := launchdFailureDetail(owner)
	if !strings.Contains(detail, "OS_REASON_CODESIGNING") || !strings.Contains(detail, "code requirement") {
		t.Fatalf("detail=%q want the codesigning explanation", detail)
	}
}

// `wrkq server health` reports a daemon that answered as healthy. A 401 still
// proves the daemon is serving — the canonical node runs per-node bearer
// tokens, so an operator shell without one gets 401 from a perfectly healthy
// daemon — and is flagged as auth "unauthorized" with a stderr note; a token
// (flag or WRKQD_TOKEN_FILE) the daemon accepts reports plain ok; a 5xx fails.
func TestServerHealthTreatsAnyAnswerAsServingAndFlagsARejectedToken(t *testing.T) {
	installFakeLaunchctl(t, &fakeLaunchctl{loaded: func(int) bool { return false }})
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte("file-token\n"), 0o600); err != nil {
		t.Fatalf("write token file: %v", err)
	}
	requireToken := func(token string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer "+token {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}

	for _, tc := range []struct {
		name       string
		handler    http.HandlerFunc
		flags      []string
		tokenFile  string
		wantErr    string
		wantStdout string
		wantStderr string
	}{
		{name: "401 without a token is serving but unauthorized", handler: requireToken("node-token"),
			wantStdout: `{"auth":"unauthorized","status":"ok"}`, wantStderr: "rejected this caller's token"},
		{name: "--token the daemon accepts is plain ok", handler: requireToken("node-token"), flags: []string{"--token", "node-token"},
			wantStdout: `{"status":"ok"}`},
		{name: "WRKQD_TOKEN_FILE is the fallback token", handler: requireToken("file-token"), tokenFile: tokenFile,
			wantStdout: `{"status":"ok"}`},
		{name: "5xx fails", handler: func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusInternalServerError) },
			wantErr: "500"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("WRKQD_ADDR", "")
			t.Setenv("WRKQD_UNIX", "")
			t.Setenv("WRKQD_TOKEN", "")
			t.Setenv("WRKQD_TOKEN_FILE", tc.tokenFile)
			server := httptest.NewServer(tc.handler)
			t.Cleanup(server.Close)

			cmd := NewRootCmdFor("wrkq")
			cmd.SetArgs(append([]string{"server", "health", "--addr", strings.TrimPrefix(server.URL, "http://")}, tc.flags...))
			var stdout, stderr bytes.Buffer
			cmd.SetOut(&stdout)
			cmd.SetErr(&stderr)
			err := cmd.Execute()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one naming %q (stdout %q)", err, tc.wantErr, stdout.String())
				}
				return
			}
			if err != nil {
				t.Fatalf("server health: %v\nstderr: %s", err, stderr.String())
			}
			if got := strings.TrimSpace(stdout.String()); got != tc.wantStdout {
				t.Fatalf("stdout = %s, want %s", got, tc.wantStdout)
			}
			if tc.wantStderr == "" && stderr.Len() != 0 {
				t.Fatalf("stderr = %q, want none", stderr.String())
			}
			if !strings.Contains(stderr.String(), tc.wantStderr) {
				t.Fatalf("stderr = %q, want it to contain %q", stderr.String(), tc.wantStderr)
			}
		})
	}
}
