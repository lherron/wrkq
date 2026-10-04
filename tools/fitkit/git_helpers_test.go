package fitkit

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

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
