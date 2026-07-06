package rpccli

import (
	"bytes"
	"encoding/json"
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

func copyFixture(t *testing.T, base string) string {
	t.Helper()
	dst := t.TempDir()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(filepath.Join(base, "wrkq.db"+suffix))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			t.Fatalf("copy fixture read: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dst, "wrkq.db"+suffix), b, 0o644); err != nil {
			t.Fatalf("copy fixture write: %v", err)
		}
	}
	return dst
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

func normalize(s string) string {
	return rfc3339Re.ReplaceAllString(s, "<TS>")
}

func snapshot(t *testing.T, dir string) string {
	t.Helper()
	database, err := db.Open(filepath.Join(dir, "wrkq.db"))
	if err != nil {
		t.Fatalf("snapshot open: %v", err)
	}
	defer func() { _ = database.Close() }()
	rows, err := database.Query(`
		SELECT id, slug, state, priority, kind,
		       project_uuid,
		       COALESCE(parent_task_uuid, ''), COALESCE(assignee_principal_ref, ''),
		       COALESCE(description, ''), COALESCE(specification, ''),
		       COALESCE(labels, ''), COALESCE(meta, ''),
		       COALESCE(requested_by_project_id, ''), COALESCE(assigned_project_id, ''),
		       COALESCE(resolution, ''),
		       COALESCE(start_at, ''), COALESCE(due_at, ''),
		       etag,
		       CASE WHEN acknowledged_at IS NOT NULL AND acknowledged_at != '' THEN 'ack' ELSE '-' END,
		       CASE WHEN completed_at    IS NOT NULL AND completed_at    != '' THEN 'done' ELSE '-' END
		FROM tasks ORDER BY id`)
	if err != nil {
		t.Fatalf("snapshot query: %v", err)
	}
	defer func() { _ = rows.Close() }()
	var b strings.Builder
	for rows.Next() {
		var id, slug, state, kind, projectUUID, description, specification, labels, meta string
		var parentTaskUUID, assignee, requestedBy, assignedProject, resolution string
		var startAt, dueAt, ackd, done string
		var prio, etag int
		if err := rows.Scan(&id, &slug, &state, &prio, &kind, &projectUUID, &parentTaskUUID, &assignee, &description, &specification, &labels, &meta, &requestedBy, &assignedProject, &resolution, &startAt, &dueAt, &etag, &ackd, &done); err != nil {
			t.Fatalf("snapshot scan: %v", err)
		}
		b.WriteString(id)
		b.WriteByte('|')
		b.WriteString(slug)
		b.WriteByte('|')
		b.WriteString(state)
		b.WriteString("|p=")
		b.WriteString(jsonNumber(prio))
		b.WriteString("|kind=")
		b.WriteString(kind)
		b.WriteString("|project=")
		b.WriteString(projectUUID)
		b.WriteString("|parent=")
		b.WriteString(parentTaskUUID)
		b.WriteString("|assignee=")
		b.WriteString(assignee)
		b.WriteString("|desc=")
		b.WriteString(description)
		b.WriteString("|spec=")
		b.WriteString(specification)
		b.WriteString("|labels=")
		b.WriteString(labels)
		b.WriteString("|meta=")
		b.WriteString(meta)
		b.WriteString("|requested=")
		b.WriteString(requestedBy)
		b.WriteString("|assigned_project=")
		b.WriteString(assignedProject)
		b.WriteString("|resolution=")
		b.WriteString(resolution)
		b.WriteString("|start=")
		b.WriteString(startAt)
		b.WriteString("|due=")
		b.WriteString(dueAt)
		b.WriteString("|etag=")
		b.WriteString(jsonNumber(etag))
		b.WriteByte('|')
		b.WriteString(ackd)
		b.WriteByte('|')
		b.WriteString(done)
		b.WriteByte('\n')
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("snapshot rows: %v", err)
	}
	return b.String()
}

func jsonNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}
