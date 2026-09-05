//go:build wrkq_local

package rpccli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

type wrkpGitFakeTransport struct {
	projects []projectEntry
	tasks    map[string]string
	posts    []map[string]any
	keys     map[string]string
	nextID   int
}

func (f *wrkpGitFakeTransport) Call(_ context.Context, method string, raw any) (json.RawMessage, error) {
	switch method {
	case "wrkq.project.listView":
		return json.Marshal(map[string]any{"items": f.projects})
	case "wrkq.task.catView":
		params := raw.(map[string]any)
		id := params["task"].(string)
		path, ok := f.tasks[id]
		if !ok {
			return nil, errors.New("task not found")
		}
		return json.Marshal(map[string]any{"id": id, "path": path})
	case "wrkq.projectEvent.post":
		params := raw.(map[string]any)
		f.posts = append(f.posts, params)
		if f.keys == nil {
			f.keys = map[string]string{}
		}
		key := params["idempotencyKey"].(string)
		fid, exists := f.keys[key]
		if !exists {
			f.nextID++
			fid = fmt.Sprintf("PE-%05d", f.nextID)
			f.keys[key] = fid
		}
		return json.Marshal(map[string]any{"id": f.nextID, "fid": fid, "created": !exists})
	default:
		return nil, fmt.Errorf("unexpected method %s", method)
	}
}

func (f *wrkpGitFakeTransport) Close() error { return nil }

func wrkpGitTestCommand(input string) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	cmd := &cobra.Command{Use: "test"}
	cmd.SetContext(context.Background())
	cmd.Flags().String("project", "", "")
	cmd.Flags().String("scope-ref", "", "")
	cmd.Flags().Bool("json", false, "")
	out, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	cmd.SetIn(strings.NewReader(input))
	cmd.SetOut(out)
	cmd.SetErr(stderr)
	return cmd, out, stderr
}

func wrkpGitTestDeps(repo string, tr Transport) wrkpGitDependencies {
	return wrkpGitDependencies{
		principal: func(*cobra.Command) (string, error) { return "agent:cody", nil },
		open:      func(*cobra.Command) (Transport, func(), error) { return tr, func() {}, nil },
		workdir:   func() (string, error) { return repo, nil },
		hostname:  func() (string, error) { return "max3.example", nil },
		now:       func() time.Time { return time.Date(2026, 9, 5, 12, 0, 0, 0, time.UTC) },
		git:       runWrkpGitCommand,
	}
}

func wrkpGitTestRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	wrkpGitMust(t, repo, "init", "-q", "-b", "main")
	wrkpGitMust(t, repo, "config", "user.name", "Cody")
	wrkpGitMust(t, repo, "config", "user.email", "cody@example.test")
	return repo
}

func wrkpGitTestCommit(t *testing.T, repo, file, body string) string {
	t.Helper()
	path := filepath.Join(repo, file)
	if err := os.WriteFile(path, []byte(body+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wrkpGitMust(t, repo, "add", file)
	wrkpGitMust(t, repo, "commit", "-q", "-m", body)
	return strings.TrimSpace(wrkpGitMust(t, repo, "rev-parse", "HEAD"))
}

func wrkpGitMust(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := runWrkpGitCommand(context.Background(), repo, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

func wrkpGitProjectFixture(slug, id, root string) projectEntry {
	return projectEntry{Type: "project", ID: id, Slug: slug, Path: slug, Root: &root}
}

func TestWrkpGitG1NeverBlocks(t *testing.T) {
	repo := wrkpGitTestRepo(t)
	wrkpGitTestCommit(t, repo, "one.txt", "initial")
	root := repo
	registered := &wrkpGitFakeTransport{projects: []projectEntry{wrkpGitProjectFixture("repo", "P-00001", root)}, tasks: map[string]string{}}
	refLine := "refs/heads/main 1111111111111111111111111111111111111111 refs/heads/main 0000000000000000000000000000000000000000\n"

	cases := []struct {
		name string
		deps wrkpGitDependencies
		push bool
	}{
		{
			name: "no-principal",
			deps: func() wrkpGitDependencies {
				d := wrkpGitTestDeps(repo, registered)
				d.principal = func(*cobra.Command) (string, error) { return "", nil }
				return d
			}(),
		},
		{
			name: "unregistered-repo",
			deps: wrkpGitTestDeps(repo, &wrkpGitFakeTransport{tasks: map[string]string{}}),
		},
		{
			name: "server-unreachable",
			deps: func() wrkpGitDependencies {
				d := wrkpGitTestDeps(repo, registered)
				d.open = func(*cobra.Command) (Transport, func(), error) { return nil, nil, errors.New("server unreachable") }
				return d
			}(),
		},
	}
	for _, tc := range cases {
		for _, push := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/push=%t", tc.name, push), func(t *testing.T) {
				input := ""
				if push {
					input = refLine
				}
				cmd, _, stderr := wrkpGitTestCommand(input)
				runWrkpGitNonBlocking(cmd, func() error {
					if push {
						return runWrkpGitPush(cmd, tc.deps, "origin", "example", nil)
					}
					return runWrkpGitCommit(cmd, tc.deps)
				})
				lines := strings.Split(strings.TrimSpace(stderr.String()), "\n")
				if len(lines) != 1 || !strings.HasPrefix(lines[0], "wrkp git:") {
					t.Fatalf("stderr = %q, want exactly one wrkp git line", stderr.String())
				}
			})
		}
	}
	if len(registered.posts) != 0 {
		t.Fatalf("failure cases posted %d events", len(registered.posts))
	}
}

func TestWrkpGitG2ExactAttribution(t *testing.T) {
	repo := wrkpGitTestRepo(t)
	wrkpGitTestCommit(t, repo, "one.txt", "ship T-00001")
	tr := &wrkpGitFakeTransport{
		projects: []projectEntry{wrkpGitProjectFixture("repo", "P-00001", repo)},
		tasks:    map[string]string{"T-00001": "repo/inbox/task"},
	}
	cmd, _, stderr := wrkpGitTestCommand("")
	if err := runWrkpGitCommit(cmd, wrkpGitTestDeps(repo, tr)); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 || len(tr.posts) != 1 {
		t.Fatalf("stderr=%q posts=%d", stderr.String(), len(tr.posts))
	}
	if got := tr.posts[0]["principalRef"]; got != "agent:cody" {
		t.Fatalf("principalRef = %v", got)
	}
	if got := tr.posts[0]["source"]; got != "lefthook" {
		t.Fatalf("source = %v", got)
	}
	if got := tr.posts[0]["node"]; got != "max3" {
		t.Fatalf("node = %v", got)
	}
}

func TestWrkpGitG3IdempotentByFullIdentity(t *testing.T) {
	repo := wrkpGitTestRepo(t)
	c1 := wrkpGitTestCommit(t, repo, "one.txt", "one")
	c2 := wrkpGitTestCommit(t, repo, "two.txt", "two")
	c3 := wrkpGitTestCommit(t, repo, "three.txt", "three")
	tr := &wrkpGitFakeTransport{projects: []projectEntry{wrkpGitProjectFixture("repo", "P-00001", repo)}, tasks: map[string]string{}}
	deps := wrkpGitTestDeps(repo, tr)

	for i := 0; i < 2; i++ {
		cmd, _, _ := wrkpGitTestCommand("")
		if err := runWrkpGitCommit(cmd, deps); err != nil {
			t.Fatal(err)
		}
	}
	line := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", c3, c1)
	for i := 0; i < 2; i++ {
		cmd, _, _ := wrkpGitTestCommand(line)
		if err := runWrkpGitPush(cmd, deps, "origin", "git@example/repo", nil); err != nil {
			t.Fatal(err)
		}
	}
	cmd, _, _ := wrkpGitTestCommand(fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", c3, c2))
	if err := runWrkpGitPush(cmd, deps, "origin", "git@example/repo", nil); err != nil {
		t.Fatal(err)
	}
	for _, remoteSHA := range []string{c1, c2} {
		cmd, _, _ = wrkpGitTestCommand(fmt.Sprintf("(delete) %040d refs/heads/old %s\n", 0, remoteSHA))
		if err := runWrkpGitPush(cmd, deps, "origin", "git@example/repo", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := len(tr.keys), 5; got != want {
		t.Fatalf("unique keys = %d, want %d (%v)", got, want, tr.keys)
	}
	if !strings.Contains(tr.posts[2]["idempotencyKey"].(string), ":git@example/repo:") {
		t.Fatalf("push key omits url: %s", tr.posts[2]["idempotencyKey"])
	}
}

func TestWrkpGitG4TaskLinkageExactOneSameProject(t *testing.T) {
	tr := &wrkpGitFakeTransport{tasks: map[string]string{
		"T-00001": "repo/inbox/own",
		"T-00002": "other/inbox/foreign",
	}}
	if got := wrkpGitLinkableTask(context.Background(), tr, "repo", []string{"T-00001"}); got != "T-00001" {
		t.Fatalf("own exact-one link = %q", got)
	}
	for _, tasks := range [][]string{{}, {"T-00002"}, {"T-00001", "T-00002"}} {
		if got := wrkpGitLinkableTask(context.Background(), tr, "repo", tasks); got != "" {
			t.Fatalf("tasks %v unexpectedly link %q", tasks, got)
		}
	}
	if got := wrkpGitTaskIDs("T-00001 x T-00002 then T-00001"); !reflect.DeepEqual(got, []string{"T-00001", "T-00002"}) {
		t.Fatalf("payload tasks = %v", got)
	}

	repo := wrkpGitTestRepo(t)
	local := wrkpGitTestCommit(t, repo, "one.txt", "T-00001")
	shape, err := wrkpGitPushShape(context.Background(), wrkpGitTestDeps(repo, tr), repo, "origin", wrkpGitRef{
		LocalRef: "refs/heads/main", LocalSHA: local, RemoteRef: "refs/heads/main", RemoteSHA: strings.Repeat("a", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if shape.tasks != nil || shape.commits != nil || shape.forced != nil {
		t.Fatalf("unknown range = tasks:%v commits:%v forced:%v", shape.tasks, shape.commits, shape.forced)
	}
}

func TestWrkpGitG5PushShapes(t *testing.T) {
	repo := wrkpGitTestRepo(t)
	c1 := wrkpGitTestCommit(t, repo, "one.txt", "one")
	c2 := wrkpGitTestCommit(t, repo, "two.txt", "two T-00001")
	wrkpGitMust(t, repo, "checkout", "-q", "-b", "side", c1)
	side := wrkpGitTestCommit(t, repo, "side.txt", "side")
	wrkpGitMust(t, repo, "checkout", "-q", "main")
	deps := wrkpGitTestDeps(repo, &wrkpGitFakeTransport{})
	zeros := strings.Repeat("0", 40)

	fast, err := wrkpGitPushShape(context.Background(), deps, repo, "origin", wrkpGitRef{"refs/heads/main", c2, "refs/heads/main", c1})
	if err != nil || fast.commits != 1 || fast.forced != false || !strings.HasPrefix(fast.summary, "push main ") {
		t.Fatalf("fast-forward: %+v err=%v", fast, err)
	}
	newBranch, err := wrkpGitPushShape(context.Background(), deps, repo, "origin", wrkpGitRef{"refs/heads/main", c2, "refs/heads/new", zeros})
	if err != nil || newBranch.commits != 2 || newBranch.forced != false || !strings.Contains(newBranch.summary, "(new branch, 2 commits)") {
		t.Fatalf("new branch: %+v err=%v", newBranch, err)
	}
	deleted, err := wrkpGitPushShape(context.Background(), deps, repo, "origin", wrkpGitRef{"(delete)", zeros, "refs/heads/old", c1})
	if err != nil || deleted.commits != 0 || deleted.forced != false || deleted.summary != "delete old → origin" {
		t.Fatalf("delete: %+v err=%v", deleted, err)
	}
	forced, err := wrkpGitPushShape(context.Background(), deps, repo, "origin", wrkpGitRef{"refs/heads/main", c2, "refs/heads/main", side})
	if err != nil || forced.forced != true || !strings.HasPrefix(forced.summary, "force-push main ") {
		t.Fatalf("force: %+v err=%v", forced, err)
	}
	unknown, err := wrkpGitPushShape(context.Background(), deps, repo, "origin", wrkpGitRef{"refs/heads/main", c2, "refs/heads/main", strings.Repeat("f", 40)})
	if err != nil || unknown.commits != nil || unknown.forced != nil || !strings.Contains(unknown.summary, "(unknown count)") {
		t.Fatalf("unknown: %+v err=%v", unknown, err)
	}
	cmd, _, _ := wrkpGitTestCommand("")
	deps.open = func(*cobra.Command) (Transport, func(), error) {
		t.Fatal("zero stdin opened transport")
		return nil, nil, nil
	}
	if err := runWrkpGitPush(cmd, deps, "origin", "example", nil); err != nil {
		t.Fatal(err)
	}
}

func TestWrkpGitG6OneRowPerRef(t *testing.T) {
	repo := wrkpGitTestRepo(t)
	local := wrkpGitTestCommit(t, repo, "one.txt", "one")
	tr := &wrkpGitFakeTransport{projects: []projectEntry{wrkpGitProjectFixture("repo", "P-00001", repo)}, tasks: map[string]string{}}
	zeros := strings.Repeat("0", 40)
	input := fmt.Sprintf("refs/heads/one %s refs/heads/one %s\nrefs/heads/two %s refs/heads/two %s\n", local, zeros, local, zeros)
	cmd, _, _ := wrkpGitTestCommand(input)
	if err := runWrkpGitPush(cmd, wrkpGitTestDeps(repo, tr), "origin", "example", nil); err == nil {
		// Deliberately empty: the assertion below is the row cardinality contract.
	} else {
		t.Fatal(err)
	}
	if len(tr.posts) != 2 || tr.posts[0]["idempotencyKey"] == tr.posts[1]["idempotencyKey"] {
		t.Fatalf("posts = %#v", tr.posts)
	}
}

func TestWrkpGitG7ProjectResolution(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realRoot := filepath.Join(home, "repos", "repo")
	if err := os.MkdirAll(realRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(home, "repo-link")
	if err := os.Symlink(realRoot, symlink); err != nil {
		t.Fatal(err)
	}
	tildeRoot := "~/repos/repo"
	tr := &wrkpGitFakeTransport{projects: []projectEntry{wrkpGitProjectFixture("repo", "P-00001", tildeRoot)}}
	for _, top := range []string{realRoot, symlink} {
		project, err := resolveWrkpGitProject(context.Background(), tr, top, "")
		if err != nil || project.Slug != "repo" {
			t.Fatalf("resolve %s = %+v, %v", top, project, err)
		}
	}
	if _, err := resolveWrkpGitProject(context.Background(), tr, home, ""); err == nil || !strings.Contains(err.Error(), "not a registered project root") {
		t.Fatalf("unregistered error = %v", err)
	}
	project, err := resolveWrkpGitProject(context.Background(), tr, home, "P-00001")
	if err != nil || project.Slug != "repo" {
		t.Fatalf("override = %+v, %v", project, err)
	}
}

func TestWrkpGitG8WrkqHookPostsLastAndOnlyAfterGate(t *testing.T) {
	prePush, err := os.ReadFile(filepath.Join("..", "..", "tools", "hooks", "pre-push"))
	if err != nil {
		t.Fatal(err)
	}
	source := string(prePush)
	positions := []int{
		strings.Index(source, "refs=$(cat)"),
		strings.Index(source, "\njust verify\n"),
		strings.LastIndex(source, `wrkp git push "$@"`),
		strings.LastIndex(source, "exit $rc"),
	}
	for i, position := range positions {
		if position < 0 || (i > 0 && position <= positions[i-1]) {
			t.Fatalf("hook order %v does not prove capture → gate → post → exit", positions)
		}
	}
	postCommit, err := os.Stat(filepath.Join("..", "..", "tools", "hooks", "post-commit"))
	if err != nil {
		t.Fatal(err)
	}
	if postCommit.Mode()&0o111 == 0 {
		t.Fatal("tracked post-commit entrypoint is not executable")
	}
}
