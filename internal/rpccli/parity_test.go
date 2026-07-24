package rpccli

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

// This file keeps the historical filename because active architecture records
// used it as required-test evidence. The implementation is now a production
// contract harness: it builds cmd/wrkq and asserts RPC-backed behavior directly.

func TestProductionCommandContract_ReadWriteCommentAttachment(t *testing.T) {
	if testing.Short() {
		t.Skip("builds production wrkq and wrkqadm binaries; skipped under -short")
	}
	bins := buildProductionBinaries(t)
	dir := seedFixture(t, bins, nil)

	source := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(source, []byte("contract attachment\n"), 0o644); err != nil {
		t.Fatalf("write attachment source: %v", err)
	}
	attachDir := filepath.Join(dir, "attach-store")
	if err := os.MkdirAll(attachDir, 0o755); err != nil {
		t.Fatalf("mkdir attachment store: %v", err)
	}
	attachEnv := []string{"WRKQ_ATTACH_DIR=" + attachDir}

	commands := []struct {
		name       string
		args       []string
		env        []string
		wantStdout []string
	}{
		{
			name:       "touch",
			args:       []string{"touch", "inbox/contract", "-t", "Contract", "-d", "body", "--json"},
			wantStdout: []string{`"id":`, `"slug": "contract"`},
		},
		{
			name:       "cat",
			args:       []string{"cat", "inbox/contract", "--json"},
			wantStdout: []string{`"id": "T-00001"`, `"title": "Contract"`},
		},
		{
			name:       "comment-add",
			args:       []string{"comment", "add", "inbox/contract", "contract comment", "--json"},
			wantStdout: []string{`"id": "C-00001"`},
		},
		{
			name:       "comment-ls",
			args:       []string{"comment", "ls", "inbox/contract", "--json"},
			wantStdout: []string{`"contract comment"`},
		},
		{
			name:       "attach-put",
			args:       []string{"attach", "put", "inbox/contract", source},
			env:        attachEnv,
			wantStdout: []string{`"id": "ATT-00001"`, `"filename": "payload.txt"`},
		},
		{
			name:       "attach-ls",
			args:       []string{"attach", "ls", "inbox/contract", "--json"},
			env:        attachEnv,
			wantStdout: []string{`"id": "ATT-00001"`, `"filename": "payload.txt"`},
		},
	}

	for _, tc := range commands {
		res := runCLIEnv(t, bins.wrkq, dir, tc.args, tc.env)
		if res.exit != 0 {
			t.Fatalf("%s exit=%d stdout=%q stderr=%q", tc.name, res.exit, res.stdout, res.stderr)
		}
		for _, want := range tc.wantStdout {
			if !strings.Contains(res.stdout, want) {
				t.Fatalf("%s stdout missing %q:\n%s", tc.name, want, res.stdout)
			}
		}
	}

	get := runCLIEnv(t, bins.wrkq, dir, []string{"attach", "get", "ATT-00001", "--as", "-"}, attachEnv)
	if get.exit != 0 {
		t.Fatalf("attach-get exit=%d stdout=%q stderr=%q", get.exit, get.stdout, get.stderr)
	}
	if get.stdout != "contract attachment\n" {
		t.Fatalf("attach-get bytes = %q", get.stdout)
	}
}

func TestProductionCommandContract_BoundedMonitorAndWatch(t *testing.T) {
	if testing.Short() {
		t.Skip("builds production wrkq and wrkqadm binaries; skipped under -short")
	}
	bins := buildProductionBinaries(t)
	dir := seedFixture(t, bins, [][]string{{"touch", "inbox/watched", "-t", "Watched"}})

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "watch", args: []string{"watch", "--since", "0", "--ndjson", "--follow=false"}, want: `"event_type"`},
		{name: "monitor-watch", args: []string{"monitor", "watch", "T-00001", "--until", "state=open", "--timeout", "1s", "--ndjson"}, want: `"result":"met"`},
	} {
		res := runCLI(t, bins.wrkq, dir, tc.args)
		if res.exit != 0 {
			t.Fatalf("%s exit=%d stdout=%q stderr=%q", tc.name, res.exit, res.stdout, res.stderr)
		}
		if !strings.Contains(res.stdout, tc.want) {
			t.Fatalf("%s stdout missing %q:\n%s", tc.name, tc.want, res.stdout)
		}
	}
}

func TestProductionCommandContract_MonitorUntilValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("builds production wrkq and wrkqadm binaries; skipped under -short")
	}
	bins := buildProductionBinaries(t)
	dir := seedFixture(t, bins, [][]string{{"touch", "inbox/watched", "-t", "Watched"}})

	for _, tc := range []struct {
		name      string
		args      []string
		wantError string
	}{
		{
			name:      "wait-composed-conditions",
			args:      []string{"monitor", "wait", "T-00001", "--until", "state=blocked|all-terminal", "--timeout", "10ms"},
			wantError: `invalid --until condition "state=blocked|all-terminal"`,
		},
		{
			name:      "wait-unknown-state",
			args:      []string{"monitor", "wait", "T-00001", "--until", "state=blokced", "--timeout", "10ms"},
			wantError: `unknown task state "blokced"`,
		},
		{
			name:      "watch-empty-list-entry",
			args:      []string{"monitor", "watch", "T-00001", "--until", "state=open,", "--timeout", "10ms"},
			wantError: `invalid --until condition "state=open,"`,
		},
	} {
		res := runCLI(t, bins.wrkq, dir, tc.args)
		if res.exit != 2 {
			t.Errorf("%s exit=%d, want 2; stdout=%q stderr=%q", tc.name, res.exit, res.stdout, res.stderr)
		}
		if res.stdout != "" {
			t.Errorf("%s emitted stdout before refusing to arm: %q", tc.name, res.stdout)
		}
		if !strings.Contains(res.stderr, tc.wantError) {
			t.Errorf("%s stderr missing %q:\n%s", tc.name, tc.wantError, res.stderr)
		}
	}

	validList := runCLI(t, bins.wrkq, dir,
		[]string{"monitor", "wait", "T-00001", "--until", "state=blocked,open", "--timeout", "1s"})
	if validList.exit != 0 || !strings.Contains(validList.stdout, `"result":"met"`) {
		t.Fatalf("valid state list exit=%d stdout=%q stderr=%q", validList.exit, validList.stdout, validList.stderr)
	}

	completed := runCLI(t, bins.wrkq, dir, []string{"set", "T-00001", "--state", "completed"})
	if completed.exit != 0 {
		t.Fatalf("complete fixture exit=%d stdout=%q stderr=%q", completed.exit, completed.stdout, completed.stderr)
	}
	allTerminal := runCLI(t, bins.wrkq, dir,
		[]string{"monitor", "watch", "T-00001", "--until", "all-terminal", "--timeout", "1s"})
	if allTerminal.exit != 0 || !strings.Contains(allTerminal.stdout, `"result":"met"`) {
		t.Fatalf("all-terminal exit=%d stdout=%q stderr=%q", allTerminal.exit, allTerminal.stdout, allTerminal.stderr)
	}
}

func TestProductionCommandContract_ProjectRootAndAttribution(t *testing.T) {
	if testing.Short() {
		t.Skip("builds production wrkq and wrkqadm binaries; skipped under -short")
	}
	bins := buildProductionBinaries(t)
	dir := seedFixture(t, bins, [][]string{{"mkdir", "proj"}})

	res := runCLIEnv(t, bins.wrkq, dir,
		[]string{"touch", "task", "-t", "Scoped", "--json"},
		[]string{"WRKQ_PROJECT_ROOT=proj", "ASP_SCOPE_REF=agent:local-human:project:wrkq"},
	)
	if res.exit != 0 {
		t.Fatalf("project-root touch exit=%d stdout=%q stderr=%q", res.exit, res.stdout, res.stderr)
	}
	if !strings.Contains(res.stdout, `"path": "proj/task"`) {
		t.Fatalf("project-root touch did not scope path:\n%s", res.stdout)
	}
	if got := taskUpdatedBy(t, dir, "T-00001"); got != "agent:local-human" {
		t.Fatalf("touch updated_by_principal_ref = %q, want agent:local-human", got)
	}
}

func taskUpdatedBy(t *testing.T, dir, id string) string {
	t.Helper()
	database, err := db.Open(filepath.Join(dir, "wrkq.db"))
	if err != nil {
		t.Fatalf("attribution db open: %v", err)
	}
	defer func() { _ = database.Close() }()

	var got string
	if err := database.QueryRow(
		`SELECT COALESCE(updated_by_principal_ref, '') FROM tasks WHERE id = ?`,
		id,
	).Scan(&got); err != nil {
		t.Fatalf("attribution query: %v", err)
	}
	return got
}

type binaries struct{ wrkq, wrkqadm string }

var (
	productionBins  binaries
	productionOnce  sync.Once
	productionBuilt bool
	sharedHome      string
)

func buildProductionBinaries(t *testing.T) binaries {
	t.Helper()
	productionOnce.Do(func() {
		dir, err := os.MkdirTemp("", "rpccli-contract-bins")
		if err != nil {
			t.Fatalf("mkdtemp: %v", err)
		}
		sharedHome = filepath.Join(dir, "home")
		if err := os.MkdirAll(sharedHome, 0o755); err != nil {
			t.Fatalf("mkdir sharedHome: %v", err)
		}
		root := repoRootFromTest(t)
		build := func(name, pkg string) string {
			out := filepath.Join(dir, name)
			cmd := exec.Command("go", "build", "-tags", "sqlite_fts5", "-o", out, pkg)
			cmd.Dir = root
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("build %s: %v\n%s", pkg, err, b)
			}
			return out
		}
		productionBins = binaries{
			wrkq:    build("wrkq", "./cmd/wrkq"),
			wrkqadm: build("wrkqadm", "./cmd/wrkqadm"),
		}
		productionBuilt = true
	})
	if !productionBuilt {
		t.Fatal("production binaries failed to build")
	}
	return productionBins
}

func seedFixture(t *testing.T, bins binaries, setup [][]string) string {
	return seedFixtureFiles(t, bins, setup, nil)
}

func seedFixtureFiles(t *testing.T, bins binaries, setup [][]string, files map[string]string) string {
	return seedFixtureFilesEnv(t, bins, setup, files, nil)
}

func seedFixtureFilesEnv(t *testing.T, bins binaries, setup [][]string, files map[string]string, seedEnv []string) string {
	t.Helper()
	dir := t.TempDir()
	writeRunFiles(t, dir, files)
	dbPath := filepath.Join(dir, "wrkq.db")
	mustRun(t, bins.wrkqadm, dir, []string{"--db", dbPath, "init"})
	for _, argv := range setup {
		mustRunEnv(t, bins.wrkq, dir, append([]string{"--db", dbPath, "--as", "agent:local-human"}, argv...), seedEnv)
	}
	return dir
}

func writeRunFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if mkErr := os.MkdirAll(filepath.Dir(p), 0o755); mkErr != nil {
			t.Fatalf("write run file mkdir %s: %v", p, mkErr)
		}
		if wErr := os.WriteFile(p, []byte(content), 0o644); wErr != nil {
			t.Fatalf("write run file %s: %v", p, wErr)
		}
	}
}

type cliResult struct {
	exit   int
	stdout string
	stderr string
}

func runCLI(t *testing.T, bin, dir string, args []string) cliResult {
	return runCLIEnv(t, bin, dir, args, nil)
}

func runCLIEnv(t *testing.T, bin, dir string, args []string, extraEnv []string) cliResult {
	return runCLIStdin(t, bin, dir, args, extraEnv, nil)
}

func runCLIStdin(t *testing.T, bin, dir string, args []string, extraEnv []string, stdin []byte) cliResult {
	t.Helper()
	full := append([]string{"--db", filepath.Join(dir, "wrkq.db"), "--as", "agent:local-human"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	cmd.Env = append(hermeticEnv(), extraEnv...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("run %s: %v", bin, err)
		}
	}
	return cliResult{exit: exit, stdout: stdout.String(), stderr: stderr.String()}
}

func mustRun(t *testing.T, bin, dir string, args []string) {
	mustRunEnv(t, bin, dir, args, nil)
}

func mustRunEnv(t *testing.T, bin, dir string, args []string, extraEnv []string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Env = append(hermeticEnv(), extraEnv...)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("seed %s %v: %v\n%s", bin, args, err, b)
	}
}

func hermeticEnv() []string {
	return []string{"HOME=" + sharedHome, "PATH=" + os.Getenv("PATH")}
}

var rfc3339Re = regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})`)
var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
