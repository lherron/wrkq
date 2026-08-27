package fitkit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestS6HookRunsVerifyUsesLocalProvenanceAndRealSmoke(t *testing.T) {
	requireS6Dependencies(t)
	tempRoot := newS6Repository(t, "#!/usr/bin/env bash\nset -euo pipefail\njust verify\n")

	// Regression bar for T-05623: the guard is now first-party wrkq code. A repo
	// with the local provenance manifest and a real installed hook should pass
	// without requiring the old vendored pin file.
	payload, output, err := runS6Guard(tempRoot)
	if err != nil {
		t.Fatalf("expected local-provenance guard to pass, got %v:\n%s", err, output)
	}

	if payload["id"] != "fit:s6/hook-runs-verify" {
		t.Fatalf("expected guard id in JSON, got %#v", payload["id"])
	}
	result, ok := payload["result"].(map[string]any)
	if !ok || result["level"] != "PRESENT" || result["exercise"] != "EXERCISED" {
		t.Fatalf("expected passing exercised guard result, got %#v", payload["result"])
	}
	smoke, ok := payload["smoke"].(map[string]any)
	if !ok || smoke["expected"] != "FAIL" || smoke["observed"] != "FAIL" {
		t.Fatalf("expected real negative-smoke result in JSON, got %#v", payload["smoke"])
	}
	if payload["provenanceIdentifier"] != "fit:s6/hook-runs-verify@wrkq-local" {
		t.Fatalf("expected local provenance identifier, got %#v", payload["provenanceIdentifier"])
	}
	if _, hasPin := payload["pin"]; hasPin {
		t.Fatalf("expected local provenance JSON instead of vendored pin output, got pin=%#v", payload["pin"])
	}
}

func TestS6HookRunsVerifyPassesInRealLinkedWorktree(t *testing.T) {
	requireS6Dependencies(t)
	mainRoot := newS6Repository(t, "#!/usr/bin/env bash\nset -euo pipefail\njust verify\n")
	linkedRoot := filepath.Join(t.TempDir(), "linked")
	runGit(t, mainRoot, "worktree", "add", "--detach", linkedRoot, "HEAD")
	t.Cleanup(func() {
		_ = gitCommand("-C", mainRoot, "worktree", "remove", "--force", linkedRoot).Run()
	})

	mainHooks := strings.TrimSpace(runGit(t, mainRoot, "rev-parse", "--git-path", "hooks"))
	linkedHooks := strings.TrimSpace(runGit(t, linkedRoot, "rev-parse", "--git-path", "hooks"))
	if cleanGitPath(mainRoot, mainHooks) != cleanGitPath(linkedRoot, linkedHooks) {
		t.Fatalf("expected linked worktree to share main hooks path, main=%q linked=%q", mainHooks, linkedHooks)
	}

	payload, output, err := runS6Guard(linkedRoot)
	if err != nil {
		t.Fatalf("expected guard to pass in real linked worktree, got %v:\n%s", err, output)
	}
	detail, ok := payload["detail"].(map[string]any)
	if !ok || detail["prePushHookPresent"] != true || detail["prePushRunsVerify"] != true {
		t.Fatalf("expected linked worktree to inspect the shared installed hook, got %#v", payload["detail"])
	}
}

func TestS6HookRunsVerifyRejectsMissingHookInMainCheckout(t *testing.T) {
	requireS6Dependencies(t)
	mainRoot := newS6Repository(t, "")

	payload, output, err := runS6Guard(mainRoot)
	if err == nil {
		t.Fatalf("expected missing main-checkout hook to fail:\n%s", output)
	}
	assertS6DiagnosticCode(t, payload, "hook.pre-push.missing")
}

func TestS6HookRunsVerifyRejectsTamperedHookInMainCheckout(t *testing.T) {
	requireS6Dependencies(t)
	mainRoot := newS6Repository(t, "#!/usr/bin/env bash\necho verify intentionally skipped\n")

	payload, output, err := runS6Guard(mainRoot)
	if err == nil {
		t.Fatalf("expected tampered main-checkout hook to fail:\n%s", output)
	}
	assertS6DiagnosticCode(t, payload, "hook.pre-push.verify.missing")
}

func TestS6HookRunsVerifyNeverPerturbsHooksOutsideSmokeRoot(t *testing.T) {
	requireS6Dependencies(t)
	// Regression bar: Git exports GIT_DIR/GIT_WORK_TREE when it runs a hook, and
	// those override `git -C <root>` during repository discovery. The guard used
	// to inherit them, so its negative smoke resolved the *invoking* repo's hooks
	// path and rewrote a real installed pre-push hook — replacing `just verify`
	// with a comment while still reporting ok. Every later `just verify` then
	// failed with hook.pre-push.verify.missing.
	goodHook := "#!/usr/bin/env bash\nset -euo pipefail\njust verify\n"
	mainRoot := newS6Repository(t, goodHook)
	decoyRoot := newS6Repository(t, goodHook)
	decoyHook := filepath.Join(cleanGitPath(decoyRoot, strings.TrimSpace(runGit(t, decoyRoot, "rev-parse", "--git-path", "hooks"))), "pre-push")

	payload, output, err := runS6GuardWithEnv(mainRoot, []string{
		"GIT_DIR=" + filepath.Join(decoyRoot, ".git"),
		"GIT_WORK_TREE=" + decoyRoot,
	})
	if err != nil {
		t.Fatalf("expected guard to evaluate --root despite ambient GIT_DIR, got %v:\n%s", err, output)
	}
	if detail, ok := payload["detail"].(map[string]any); !ok || detail["prePushRunsVerify"] != true {
		t.Fatalf("expected guard to inspect the --root hook, got %#v", payload["detail"])
	}

	for name, path := range map[string]string{"decoy": decoyHook, "root": filepath.Join(mainRoot, ".git/hooks/pre-push")} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatalf("reading %s hook: %v", name, readErr)
		}
		if string(got) != goodHook {
			t.Fatalf("guard perturbed the %s repository's installed hook outside its smoke root:\n%s", name, got)
		}
	}
}

func requireS6Dependencies(t *testing.T) {
	t.Helper()
	for _, binary := range []string{"git", "node"} {
		if _, err := exec.LookPath(binary); err != nil {
			t.Skipf("%s is required to exercise the fitkit entrypoint: %v", binary, err)
		}
	}
}

func newS6Repository(t *testing.T, hook string) string {
	t.Helper()
	sourceRoot := repoRoot(t)
	tempRoot := filepath.Join(t.TempDir(), "main")
	if err := os.MkdirAll(tempRoot, 0o755); err != nil {
		t.Fatal(err)
	}

	copyFile(t,
		filepath.Join(sourceRoot, "tools/fitkit/s6-hook-runs-verify.mjs"),
		filepath.Join(tempRoot, "tools/fitkit/s6-hook-runs-verify.mjs"),
	)
	writeFile(t, filepath.Join(tempRoot, "tools/fitkit/s6-hook-runs-verify.provenance.json"), `{
  "guardId": "fit:s6/hook-runs-verify",
  "ownerProject": "wrkq",
  "localSourceFiles": ["tools/fitkit/s6-hook-runs-verify.mjs"],
  "checkedSurfaces": ["Justfile", ".git/hooks/pre-push"],
  "reason": "first-party wrkq guard replacing the vendored archagent artifact"
}`)
	writeFile(t, filepath.Join(tempRoot, "Justfile"), "verify:\n  go test ./...\n")
	runGit(t, tempRoot, "init", "--quiet")
	runGit(t, tempRoot, "add", "Justfile", "tools/fitkit/s6-hook-runs-verify.mjs", "tools/fitkit/s6-hook-runs-verify.provenance.json")
	runGit(t, tempRoot, "-c", "user.name=fitkit", "-c", "user.email=fitkit@example.invalid", "commit", "--quiet", "-m", "fixture")
	if hook != "" {
		hooks := strings.TrimSpace(runGit(t, tempRoot, "rev-parse", "--git-path", "hooks"))
		writeFile(t, filepath.Join(cleanGitPath(tempRoot, hooks), "pre-push"), hook)
	}
	return tempRoot
}

func runGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := gitCommand(append([]string{"-C", root}, args...)...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

// gitCommand runs git against the repository the ARGUMENTS name, and only that
// one.
//
// Git's GIT_* variables outrank every argument that identifies a repository: an
// ambient GIT_DIR beats -C, beats the process working directory, and beats even
// the directory argument to `git init`, which then re-initializes the ambient
// repository and reports success. A test that inherits the environment is not
// running against its temp dir at all.
//
// That is how wrkq lost git for every seat on 2026-08-27: `just verify` runs
// this package from the pre-push hook, git exports an absolute GIT_DIR to hooks
// in a linked worktree, and `runGit(t, tempRoot, "init", "--quiet")` wrote
// core.bare=true into the wrkq checkout's own config — the file a main checkout
// SHARES with every linked worktree. Every git command in every seat then failed
// with "fatal: this operation must be run in a work tree" (T-07635).
func gitCommand(args ...string) *exec.Cmd {
	cmd := exec.Command("git", args...)
	cmd.Env = environWithoutGitOverrides(os.Environ())
	return cmd
}

func environWithoutGitOverrides(environ []string) []string {
	scrubbed := make([]string, 0, len(environ))
	for _, entry := range environ {
		if strings.HasPrefix(entry, "GIT_") {
			continue
		}
		scrubbed = append(scrubbed, entry)
	}
	return scrubbed
}

func cleanGitPath(root, path string) string {
	var cleaned string
	if filepath.IsAbs(path) {
		cleaned = filepath.Clean(path)
	} else {
		cleaned = filepath.Clean(filepath.Join(root, path))
	}
	if resolved, err := filepath.EvalSymlinks(cleaned); err == nil {
		return resolved
	}
	return cleaned
}

func runS6Guard(root string) (map[string]any, []byte, error) {
	return runS6GuardWithEnv(root, nil)
}

func runS6GuardWithEnv(root string, extraEnv []string) (map[string]any, []byte, error) {
	cmd := exec.Command("node", "tools/fitkit/s6-hook-runs-verify.mjs", "--root", root, "--json")
	cmd.Dir = root
	if len(extraEnv) > 0 {
		cmd.Env = append(os.Environ(), extraEnv...)
	}
	output, err := cmd.CombinedOutput()
	var payload map[string]any
	if jsonErr := json.Unmarshal(output, &payload); jsonErr != nil {
		return nil, output, jsonErr
	}
	return payload, output, err
}

func assertS6DiagnosticCode(t *testing.T, payload map[string]any, want string) {
	t.Helper()
	diagnostic, ok := payload["diagnostic"].(map[string]any)
	if !ok || diagnostic["code"] != want {
		t.Fatalf("expected diagnostic %q, got %#v", want, payload["diagnostic"])
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src string, dst string) {
	t.Helper()
	bytes, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dst, strings.ReplaceAll(string(bytes), "\r\n", "\n"))
}
