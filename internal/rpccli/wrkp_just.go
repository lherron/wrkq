package rpccli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
)

// `wrkp just` is the run.settled producer: it runs `just` and, when the
// justfile opts in, posts one project fact per top-level run with the recipe,
// repo, exit status and duration. `just` has no cursor of its own, so its runs
// reach a cursor-driven reader through the project timeline like any other
// foreign fact (T-08917, Lance 2026-09-25).
//
// It is reached through the `just` shim (tools/just/just), which sits ahead of
// the real binary on PATH and passes its location in WRKP_JUST_BIN. A justfile
// opts in with one comment line:
//
//	# wrkp: run.settled
//
// Every other invocation -- no marker, a listing or other non-run subcommand,
// a recipe nested inside an observed run -- is replaced by the real `just`
// with exec, so the wrapper costs one process start and nothing else.
//
// OBSERVABILITY NEVER CHANGES THE RUN. The exit status is just's, stdio is
// inherited, and a failed post is one `wrkp just:` line on stderr.

// The vocabulary. The first segment names the subject: a run. Attribute order
// is the render order.
const (
	wrkpJustEventType = "run.settled"
	wrkpJustSource    = "wrkp-just"
	wrkpJustBinEnv    = "WRKP_JUST_BIN"
	wrkpJustDepthEnv  = "WRKP_JUST_DEPTH"
	wrkpJustTimeout   = 5 * time.Second
)

var (
	wrkpJustMarker      = regexp.MustCompile(`(?m)^[ \t]*#[ \t]*wrkp:[ \t]*run\.settled\b`)
	wrkpJustScopeTask   = regexp.MustCompile(`:task:(T-\d{5})(?:/|$)`)
	wrkpJustAssignment  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]*=`)
	wrkpJustValueOption = map[string]int{
		"--alias-style": 1, "--ceiling": 1, "--chooser": 1, "--color": 1, "--command-color": 1,
		"--cygpath": 1, "--dotenv-command": 1, "-F": 1, "--dotenv-filename": 1, "-E": 1,
		"--dotenv-path": 1, "--dump-format": 1, "--evaluate-format": 1, "--group": 1,
		"--indentation": 1, "--jobs": 1, "-f": 1, "--justfile": 1, "--justfile-name": 1,
		"--list-heading": 1, "--list-prefix": 1, "--set": 2, "--shell": 1, "--shell-arg": 1,
		"--tempdir": 1, "--timestamp-format": 1, "-d": 1, "--working-directory": 1,
	}
	// Subcommands and modes that run no recipe. They are never observed.
	wrkpJustNonRun = map[string]bool{
		"--changelog": true, "--choose": true, "--clean": true, "-c": true, "--command": true,
		"--completions": true, "--dump": true, "-e": true, "--edit": true, "--evaluate": true,
		"--fmt": true, "--groups": true, "--init": true, "--json": true, "-l": true, "--list": true,
		"--man": true, "-s": true, "--show": true, "--summary": true, "--usage": true,
		"--variables": true, "-h": true, "--help": true, "-V": true, "--version": true,
		"-n": true, "--dry-run": true, "-g": true, "--global-justfile": true,
	}
)

// wrkpJustInvocation is what the argument list says about the run.
type wrkpJustInvocation struct {
	run          bool
	recipe       string
	justfile     string
	workdir      string
	justfileName string
}

func parseWrkpJustArgs(args []string) wrkpJustInvocation {
	inv := wrkpJustInvocation{run: true}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			if i+1 < len(args) && inv.recipe == "" {
				inv.recipe = args[i+1]
			}
			break
		}
		if strings.HasPrefix(arg, "-") && len(arg) > 1 {
			name, value, inline := strings.Cut(arg, "=")
			if wrkpJustNonRun[name] {
				inv.run = false
				return inv
			}
			n := wrkpJustValueOption[name]
			if n > 0 && !inline && i+n < len(args) {
				value = args[i+n]
				i += n
			}
			switch name {
			case "-f", "--justfile":
				inv.justfile = value
			case "-d", "--working-directory":
				inv.workdir = value
			case "--justfile-name":
				inv.justfileName = value
			}
			continue
		}
		if inv.recipe == "" && wrkpJustAssignment.MatchString(arg) {
			continue
		}
		if inv.recipe == "" {
			inv.recipe = arg
		}
	}
	if inv.recipe == "" {
		inv.recipe = "(default)"
	}
	return inv
}

// findWrkpJustfile mirrors just's own search: an explicit --justfile, else the
// first directory from the working directory upward holding a file named
// `justfile` (any case) or `.justfile`.
func findWrkpJustfile(inv wrkpJustInvocation) (string, error) {
	if inv.justfile != "" {
		return filepath.Abs(inv.justfile)
	}
	dir := inv.workdir
	if dir == "" {
		var err error
		if dir, err = os.Getwd(); err != nil {
			return "", err
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		entries, err := os.ReadDir(dir)
		if err == nil {
			for _, entry := range entries {
				name := entry.Name()
				if entry.IsDir() {
					continue
				}
				if strings.EqualFold(name, "justfile") || name == ".justfile" ||
					(inv.justfileName != "" && name == inv.justfileName) {
					return filepath.Join(dir, name), nil
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("no justfile found")
		}
		dir = parent
	}
}

func wrkpJustOptedIn(justfile string) bool {
	content, err := os.ReadFile(justfile)
	return err == nil && wrkpJustMarker.Match(content)
}

func resolveWrkpRealJust() (string, error) {
	if bin := strings.TrimSpace(os.Getenv(wrkpJustBinEnv)); bin != "" {
		return bin, nil
	}
	return exec.LookPath("just")
}

func newWrkpJustCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "just [-- just-args...]",
		Short: "Run just, posting run.settled when the justfile opts in",
		// Every argument belongs to just, flags included.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 && args[0] == "--" {
				args = args[1:]
			}
			justBin, err := resolveWrkpRealJust()
			if err != nil {
				return fmt.Errorf("wrkp just: cannot find the just binary: %w", err)
			}
			inv := parseWrkpJustArgs(args)
			justfile := ""
			if inv.run && os.Getenv(wrkpJustDepthEnv) == "" {
				if found, findErr := findWrkpJustfile(inv); findErr == nil && wrkpJustOptedIn(found) {
					justfile = found
				}
			}
			if justfile == "" {
				// Not observed: become just.
				return syscall.Exec(justBin, append([]string{"just"}, args...), os.Environ())
			}
			code := runWrkpJustObserved(cmd, justBin, args, inv, justfile)
			if code == 0 {
				return nil
			}
			return exitErrorReported(code, fmt.Errorf("just exited %d", code))
		},
	}
}

func runWrkpJustObserved(cmd *cobra.Command, justBin string, args []string, inv wrkpJustInvocation, justfile string) int {
	child := exec.Command(justBin, args...)
	child.Stdin, child.Stdout, child.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Recipes that call just again run unobserved: the fact is the top run.
	child.Env = append(os.Environ(), wrkpJustDepthEnv+"=1")

	// A terminal ^C reaches the whole foreground group, just included, so
	// wrkp only has to survive it (ExecuteWrkp already holds SIGINT). A TERM
	// or HUP aimed at this process is passed on.
	forwarded := make(chan os.Signal, 2)
	signal.Notify(forwarded, syscall.SIGTERM, syscall.SIGHUP)
	defer signal.Stop(forwarded)

	started := time.Now()
	if err := child.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "wrkp just: %s\n", err)
		return 127
	}
	done := make(chan error, 1)
	go func() { done <- child.Wait() }()
	var waitErr error
	for waiting := true; waiting; {
		select {
		case sig := <-forwarded:
			_ = child.Process.Signal(sig)
		case waitErr = <-done:
			waiting = false
		}
	}
	duration := time.Since(started)

	code, signalName := 0, ""
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			fmt.Fprintf(os.Stderr, "wrkp just: %s\n", waitErr)
			return 1
		}
		code = exitErr.ExitCode()
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			code = 128 + int(status.Signal())
			signalName = status.Signal().String()
		}
	}
	if err := postWrkpJustSettled(cmd, inv, justfile, args, code, signalName, started, duration); err != nil {
		fmt.Fprintf(os.Stderr, "wrkp just: %s\n", strings.Join(strings.Fields(err.Error()), " "))
	}
	return code
}

func postWrkpJustSettled(cmd *cobra.Command, inv wrkpJustInvocation, justfile string, args []string, code int, signalName string, started time.Time, duration time.Duration) error {
	// A fresh context: the command's own is cancelled by the ^C that may have
	// ended the run, and the fact of that run is still worth posting.
	ctx, cancel := context.WithTimeout(context.Background(), wrkpJustTimeout)
	defer cancel()

	dir := filepath.Dir(justfile)
	repo, err := runWrkpGitCommand(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		repo = dir
	}
	principal, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	tr, _, closeFn, err := openConfiguredTransport(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	project, err := resolveWrkpGitProject(ctx, tr, repo, "")
	if err != nil {
		return err
	}
	host, _ := os.Hostname()
	node := strings.SplitN(host, ".", 2)[0]
	repoName := filepath.Base(repo)

	outcome := "ok"
	if code != 0 {
		outcome = fmt.Sprintf("failed (exit %d)", code)
		if signalName != "" {
			outcome = "interrupted (" + signalName + ")"
		}
	}
	attributes := []wrkpAttribute{
		{"source", wrkpJustSource}, {"node", node}, {"recipe", clampWrkpJust(inv.recipe)},
		{"repo", repoName}, {"status", strconv.Itoa(code)},
		{"duration_ms", strconv.FormatInt(duration.Milliseconds(), 10)},
	}
	if signalName != "" {
		attributes = append(attributes, wrkpAttribute{"signal", signalName})
	}
	attributes = append(attributes,
		wrkpAttribute{"argv", clampWrkpJust(strings.Join(args, " "))},
		wrkpAttribute{"justfile", clampWrkpJust(justfile)},
		wrkpAttribute{"started_at", started.UTC().Format(time.RFC3339)},
	)
	took := duration.Round(100 * time.Millisecond).String()
	if duration < time.Second {
		took = duration.Round(time.Millisecond).String()
	}
	summary := truncateWrkpGitSummary(fmt.Sprintf("just %s %s in %s (%s)",
		inv.recipe, outcome, took, repoName), 512)

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	params := map[string]any{
		"project": project.Slug, "type": wrkpJustEventType, "summary": summary,
		"attributes":     encodeWrkpAttributes(attributes),
		"idempotencyKey": "run.settled:" + hex.EncodeToString(nonce),
		"occurredAt":     time.Now().UTC().Format(time.RFC3339),
	}
	if principal != "" {
		params["principalRef"] = principal
	}
	scopeRef := wrkcScopeRef(cmd)
	if scopeRef != "" {
		params["scopeRef"] = scopeRef
	}
	// A run inside a task seat threads under that task when the task belongs
	// to the run's project; otherwise it sits at the project's top level.
	if match := wrkpJustScopeTask.FindStringSubmatch(scopeRef); match != nil {
		if task := wrkpGitLinkableTask(ctx, tr, project.Slug, []string{match[1]}); task != "" {
			params["task"] = task
			delete(params, "project")
		}
	}
	raw, err := tr.Call(ctx, "wrkq.projectEvent.post", params)
	if err != nil {
		return wrkpRPCError(err)
	}
	var result struct {
		UUID string `json:"uuid"`
	}
	return json.Unmarshal(raw, &result)
}

func clampWrkpJust(value string) string {
	return truncateWrkpGitSummary(value, 1024)
}
