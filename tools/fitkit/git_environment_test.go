package fitkit

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scratchRepositoryWithLinkedWorktree builds a throwaway repository plus one
// linked worktree, and returns the main checkout root and the linked worktree's
// git dir — the value git exports as GIT_DIR to a hook running there.
func scratchRepositoryWithLinkedWorktree(t *testing.T) (mainRoot string, linkedGitDir string) {
	t.Helper()
	root := t.TempDir()
	mainRoot = filepath.Join(root, "main")
	if err := os.MkdirAll(mainRoot, 0o755); err != nil {
		t.Fatalf("mkdir main: %v", err)
	}
	runGit(t, mainRoot, "init", "--quiet", "--initial-branch=main")
	runGit(t, mainRoot, "config", "user.name", "fitkit")
	runGit(t, mainRoot, "config", "user.email", "fitkit@example.invalid")
	runGit(t, mainRoot, "commit", "--quiet", "--allow-empty", "-m", "baseline")
	linkedRoot := filepath.Join(root, "linked")
	runGit(t, mainRoot, "worktree", "add", "--quiet", "--detach", linkedRoot, "HEAD")
	return mainRoot, filepath.Join(mainRoot, ".git", "worktrees", "linked")
}

func readConfig(t *testing.T, mainRoot string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(mainRoot, ".git", "config"))
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	return string(content)
}

// TestAmbientGitDirPoisonsASharedConfig is the control. Without it, the guarded
// case below cannot tell "the scrub works" from "nothing here would have gone
// wrong anyway". Everything it damages is a throwaway repository.
func TestAmbientGitDirPoisonsASharedConfig(t *testing.T) {
	victim, linkedGitDir := scratchRepositoryWithLinkedWorktree(t)
	if strings.Contains(readConfig(t, victim), "bare = true") {
		t.Fatal("fixture repository started out bare")
	}
	target := t.TempDir()

	// The unscrubbed shape: -C names the temp dir, GIT_DIR overrules it.
	cmd := exec.Command("git", "-C", target, "init", "--quiet")
	cmd.Env = append(os.Environ(), "GIT_DIR="+linkedGitDir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}

	if _, err := os.Stat(filepath.Join(target, ".git")); !os.IsNotExist(err) {
		t.Fatalf("expected the temp dir to be left without a .git, got err=%v", err)
	}
	if !strings.Contains(readConfig(t, victim), "bare = true") {
		t.Fatal("expected the ambient GIT_DIR to write core.bare into the shared config")
	}
}

func TestGitCommandLeavesASharedConfigUntouched(t *testing.T) {
	victim, linkedGitDir := scratchRepositoryWithLinkedWorktree(t)
	before := readConfig(t, victim)
	target := t.TempDir()

	t.Setenv("GIT_DIR", linkedGitDir)
	t.Setenv("GIT_WORK_TREE", filepath.Dir(victim))
	t.Setenv("GIT_INDEX_FILE", filepath.Join(linkedGitDir, "index"))

	if output, err := gitCommand("-C", target, "init", "--quiet").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, output)
	}

	if got := readConfig(t, victim); got != before {
		t.Fatalf("shared config changed:\nbefore:\n%s\nafter:\n%s", before, got)
	}
	if _, err := os.Stat(filepath.Join(target, ".git")); err != nil {
		t.Fatalf("expected the temp dir to become a repository, got %v", err)
	}
}

func TestEnvironWithoutGitOverrides(t *testing.T) {
	scrubbed := environWithoutGitOverrides([]string{
		"GIT_DIR=/repo/.git",
		"GIT_WORK_TREE=/repo",
		"GIT_INDEX_FILE=/repo/.git/index",
		"GITHUB_TOKEN=keep",
		"PATH=/usr/bin",
	})

	want := []string{"GITHUB_TOKEN=keep", "PATH=/usr/bin"}
	if strings.Join(scrubbed, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got %v, want %v", scrubbed, want)
	}
}
