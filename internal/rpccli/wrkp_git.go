package rpccli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

var wrkpGitTaskPattern = regexp.MustCompile(`\bT-\d{5}\b`)

type wrkpGitProject struct {
	ID   string
	Slug string
	Root string
}

type wrkpGitRef struct {
	LocalRef  string
	LocalSHA  string
	RemoteRef string
	RemoteSHA string
}

type wrkpGitDependencies struct {
	principal func(*cobra.Command) (string, error)
	open      func(*cobra.Command) (Transport, func(), error)
	workdir   func() (string, error)
	hostname  func() (string, error)
	now       func() time.Time
	git       func(context.Context, string, ...string) (string, error)
}

func defaultWrkpGitDependencies() wrkpGitDependencies {
	return wrkpGitDependencies{
		principal: actorFlag,
		open: func(cmd *cobra.Command) (Transport, func(), error) {
			tr, _, closeFn, err := openConfiguredTransport(cmd)
			return tr, closeFn, err
		},
		workdir:  os.Getwd,
		hostname: os.Hostname,
		now:      time.Now,
		git:      runWrkpGitCommand,
	}
}

func newWrkpGitCmd() *cobra.Command {
	deps := defaultWrkpGitDependencies()
	gitCmd := &cobra.Command{Use: "git", Short: "Post best-effort facts from Git hooks"}
	gitCmd.AddCommand(newWrkpGitCommitCmd(deps), newWrkpGitPushCmd(deps))
	return gitCmd
}

func newWrkpGitCommitCmd(deps wrkpGitDependencies) *cobra.Command {
	return &cobra.Command{
		Use: "commit", Short: "Post the current HEAD as a git.commit fact",
		Run: func(cmd *cobra.Command, args []string) {
			runWrkpGitNonBlocking(cmd, func() error {
				if len(args) != 0 {
					return fmt.Errorf("commit accepts no arguments")
				}
				return runWrkpGitCommit(cmd, deps)
			})
		},
	}
}

func newWrkpGitPushCmd(deps wrkpGitDependencies) *cobra.Command {
	var payloadExtra []string
	cmd := &cobra.Command{
		Use: "push <remote> <url>", Short: "Post pre-push ref transitions as git.push facts",
		Run: func(cmd *cobra.Command, args []string) {
			runWrkpGitNonBlocking(cmd, func() error {
				if len(args) != 2 {
					return fmt.Errorf("push requires <remote> <url>")
				}
				extra, err := parseWrkpGitPayloadExtra(payloadExtra)
				if err != nil {
					return err
				}
				return runWrkpGitPush(cmd, deps, args[0], args[1], extra)
			})
		},
	}
	cmd.Flags().StringArrayVar(&payloadExtra, "payload-extra", nil, "Add key=value to each push payload")
	return cmd
}

func runWrkpGitNonBlocking(cmd *cobra.Command, run func() error) {
	if err := run(); err != nil {
		message := strings.Join(strings.Fields(err.Error()), " ")
		fmt.Fprintf(cmd.ErrOrStderr(), "wrkp git: %s\n", message)
	}
}

func runWrkpGitCommit(cmd *cobra.Command, deps wrkpGitDependencies) error {
	principal, tr, closeFn, project, repo, node, err := prepareWrkpGit(cmd, deps)
	if err != nil {
		return err
	}
	defer closeFn()

	sha, err := deps.git(cmd.Context(), repo, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	shortSHA, err := deps.git(cmd.Context(), repo, "rev-parse", "--short", sha)
	if err != nil {
		return err
	}
	branch, err := deps.git(cmd.Context(), repo, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		branch = "(detached)"
	}
	metadata, err := deps.git(cmd.Context(), repo, "show", "-s", "--format=%s%x00%an <%ae>%x00%cI%x00%P%x00%B", sha)
	if err != nil {
		return err
	}
	parts := strings.SplitN(metadata, "\x00", 5)
	if len(parts) != 5 {
		return fmt.Errorf("unexpected git commit metadata for %s", shortSHA)
	}
	subject, author, committedAt := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), strings.TrimSpace(parts[2])
	parents := strings.Fields(parts[3])
	tasks := wrkpGitTaskIDs(parts[4])
	files, insertions, deletions, err := wrkpGitCommitStats(cmd.Context(), deps, repo, sha)
	if err != nil {
		return err
	}
	payload := map[string]any{
		"sha": sha, "branch": branch, "subject": subject, "author": author,
		"committerDate": committedAt, "filesChanged": files, "insertions": insertions,
		"deletions": deletions, "parents": parents, "tasks": tasks,
	}
	summary := truncateWrkpGitSummary(fmt.Sprintf("commit %s on %s: %s", shortSHA, branch, subject), 512)
	task := wrkpGitLinkableTask(cmd.Context(), tr, project.Slug, tasks)
	return postWrkpGitFact(cmd, tr, principal, project.Slug, task, node, "git.commit", summary, payload, "git.commit:"+sha, committedAt)
}

func runWrkpGitPush(cmd *cobra.Command, deps wrkpGitDependencies, remote, url string, extra map[string]any) error {
	refs, err := readWrkpGitRefs(cmd.InOrStdin())
	if err != nil {
		return err
	}
	if len(refs) == 0 {
		return nil
	}
	principal, tr, closeFn, project, repo, node, err := prepareWrkpGit(cmd, deps)
	if err != nil {
		return err
	}
	defer closeFn()

	for _, ref := range refs {
		shape, err := wrkpGitPushShape(cmd.Context(), deps, repo, remote, ref)
		if err != nil {
			return err
		}
		payload := map[string]any{
			"remote": remote, "url": url, "localRef": ref.LocalRef, "localSha": ref.LocalSHA,
			"remoteRef": ref.RemoteRef, "remoteSha": ref.RemoteSHA, "commits": shape.commits,
			"forced": shape.forced, "tasks": shape.tasks,
		}
		for key, value := range extra {
			if _, reserved := payload[key]; reserved {
				return fmt.Errorf("--payload-extra cannot replace %q", key)
			}
			payload[key] = value
		}
		task := ""
		if shape.tasks != nil {
			task = wrkpGitLinkableTask(cmd.Context(), tr, project.Slug, shape.tasks)
		}
		key := strings.Join([]string{"git.push", remote, url, ref.RemoteRef, ref.RemoteSHA, ref.LocalSHA}, ":")
		if err := postWrkpGitFact(cmd, tr, principal, project.Slug, task, node, "git.push", shape.summary, payload, key, deps.now().UTC().Format(time.RFC3339)); err != nil {
			return err
		}
	}
	return nil
}

func prepareWrkpGit(cmd *cobra.Command, deps wrkpGitDependencies) (string, Transport, func(), wrkpGitProject, string, string, error) {
	principal, err := deps.principal(cmd)
	if err != nil {
		return "", nil, nil, wrkpGitProject{}, "", "", err
	}
	if principal == "" {
		return "", nil, nil, wrkpGitProject{}, "", "", fmt.Errorf("no resolvable principal; skipping")
	}
	repo, err := deps.workdir()
	if err != nil {
		return "", nil, nil, wrkpGitProject{}, "", "", err
	}
	toplevel, err := deps.git(cmd.Context(), repo, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", nil, nil, wrkpGitProject{}, "", "", err
	}
	tr, closeFn, err := deps.open(cmd)
	if err != nil {
		return "", nil, nil, wrkpGitProject{}, "", "", err
	}
	project, err := resolveWrkpGitProject(cmd.Context(), tr, toplevel, wrkpProjectOverride(cmd))
	if err != nil {
		closeFn()
		return "", nil, nil, wrkpGitProject{}, "", "", err
	}
	host, err := deps.hostname()
	if err != nil {
		closeFn()
		return "", nil, nil, wrkpGitProject{}, "", "", err
	}
	node := strings.SplitN(host, ".", 2)[0]
	return principal, tr, closeFn, project, toplevel, node, nil
}

func wrkpProjectOverride(cmd *cobra.Command) string {
	value, _ := cmd.Flags().GetString("project")
	return strings.TrimSpace(value)
}

func resolveWrkpGitProject(ctx context.Context, tr Transport, toplevel, override string) (wrkpGitProject, error) {
	projects, err := listWrkpGitProjects(ctx, tr)
	if err != nil {
		return wrkpGitProject{}, err
	}
	if override != "" {
		for _, project := range projects {
			if override == project.ID || override == project.Slug {
				return project, nil
			}
		}
		return wrkpGitProject{}, fmt.Errorf("project %q was not found; skipping", override)
	}
	canonicalTop, err := canonicalWrkpGitRoot(toplevel)
	if err != nil {
		return wrkpGitProject{}, err
	}
	matches := make([]wrkpGitProject, 0, 1)
	for _, project := range projects {
		if project.Root == "" {
			continue
		}
		root, rootErr := canonicalWrkpGitRoot(project.Root)
		if rootErr == nil && root == canonicalTop {
			matches = append(matches, project)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return wrkpGitProject{}, fmt.Errorf("%s matches multiple registered project roots; skipping", toplevel)
	}
	return wrkpGitProject{}, fmt.Errorf("%s is not a registered project root; skipping", toplevel)
}

func listWrkpGitProjects(ctx context.Context, tr Transport) ([]wrkpGitProject, error) {
	projects := []wrkpGitProject{}
	cursor := ""
	for {
		params := map[string]any{"limit": 100}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := tr.Call(ctx, "wrkq.project.listView", params)
		if err != nil {
			return nil, wrkpRPCError(err)
		}
		var view struct {
			Items []projectEntry `json:"items"`
			Next  string         `json:"next_cursor"`
		}
		if err := json.Unmarshal(raw, &view); err != nil {
			return nil, err
		}
		for _, item := range view.Items {
			root := ""
			if item.Root != nil {
				root = *item.Root
			}
			projects = append(projects, wrkpGitProject{ID: item.ID, Slug: item.Slug, Root: root})
		}
		if view.Next == "" {
			return projects, nil
		}
		cursor = view.Next
	}
}

func canonicalWrkpGitRoot(raw string) (string, error) {
	if raw == "~" || strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		if raw == "~" {
			raw = home
		} else {
			raw = filepath.Join(home, strings.TrimPrefix(raw, "~/"))
		}
	}
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func wrkpGitCommitStats(ctx context.Context, deps wrkpGitDependencies, repo, sha string) (int, int, int, error) {
	raw, err := deps.git(ctx, repo, "show", "--format=", "--numstat", sha)
	if err != nil {
		return 0, 0, 0, err
	}
	files, insertions, deletions := 0, 0, 0
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		files++
		if n, convErr := strconv.Atoi(fields[0]); convErr == nil {
			insertions += n
		}
		if n, convErr := strconv.Atoi(fields[1]); convErr == nil {
			deletions += n
		}
	}
	return files, insertions, deletions, nil
}

type wrkpGitPushResult struct {
	summary string
	commits any
	forced  any
	tasks   []string
}

func wrkpGitPushShape(ctx context.Context, deps wrkpGitDependencies, repo, remote string, ref wrkpGitRef) (wrkpGitPushResult, error) {
	branch := strings.TrimPrefix(ref.RemoteRef, "refs/heads/")
	if branch == ref.RemoteRef {
		branch = strings.TrimPrefix(ref.LocalRef, "refs/heads/")
	}
	if allZeroGitSHA(ref.LocalSHA) {
		return wrkpGitPushResult{summary: fmt.Sprintf("delete %s → %s", branch, remote), commits: 0, forced: false, tasks: []string{}}, nil
	}
	localShort, err := deps.git(ctx, repo, "rev-parse", "--short", ref.LocalSHA)
	if err != nil {
		return wrkpGitPushResult{}, err
	}
	if allZeroGitSHA(ref.RemoteSHA) {
		count, tasks, err := wrkpGitRange(ctx, deps, repo, ref.LocalSHA)
		if err != nil {
			return wrkpGitPushResult{}, err
		}
		return wrkpGitPushResult{
			summary: fmt.Sprintf("push %s (new branch, %d commits) → %s", branch, count, remote),
			commits: count, forced: false, tasks: tasks,
		}, nil
	}
	if _, err := deps.git(ctx, repo, "cat-file", "-e", ref.RemoteSHA+"^{commit}"); err != nil {
		remoteShort := shortWrkpGitSHA(ref.RemoteSHA)
		return wrkpGitPushResult{
			summary: fmt.Sprintf("push %s %s..%s (unknown count) → %s", branch, remoteShort, localShort, remote),
			commits: nil, forced: nil, tasks: nil,
		}, nil
	}
	remoteShort, err := deps.git(ctx, repo, "rev-parse", "--short", ref.RemoteSHA)
	if err != nil {
		return wrkpGitPushResult{}, err
	}
	count, tasks, err := wrkpGitRange(ctx, deps, repo, ref.RemoteSHA+".."+ref.LocalSHA)
	if err != nil {
		return wrkpGitPushResult{}, err
	}
	_, ancestorErr := deps.git(ctx, repo, "merge-base", "--is-ancestor", ref.RemoteSHA, ref.LocalSHA)
	forced := false
	if ancestorErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(ancestorErr, &exitErr) || exitErr.ExitCode() != 1 {
			return wrkpGitPushResult{}, ancestorErr
		}
		forced = true
	}
	verb := "push"
	if forced {
		verb = "force-push"
	}
	return wrkpGitPushResult{
		summary: fmt.Sprintf("%s %s %s..%s (%d commits) → %s", verb, branch, remoteShort, localShort, count, remote),
		commits: count, forced: forced, tasks: tasks,
	}, nil
}

func wrkpGitRange(ctx context.Context, deps wrkpGitDependencies, repo, revision string) (int, []string, error) {
	rawCount, err := deps.git(ctx, repo, "rev-list", "--count", revision)
	if err != nil {
		return 0, nil, err
	}
	count, err := strconv.Atoi(strings.TrimSpace(rawCount))
	if err != nil {
		return 0, nil, fmt.Errorf("invalid git commit count %q", rawCount)
	}
	messages, err := deps.git(ctx, repo, "log", "--format=%B%x00", revision)
	if err != nil {
		return 0, nil, err
	}
	return count, wrkpGitTaskIDs(messages), nil
}

func readWrkpGitRefs(r io.Reader) ([]wrkpGitRef, error) {
	refs := []wrkpGitRef{}
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 4 {
			return nil, fmt.Errorf("invalid pre-push ref line %q", line)
		}
		refs = append(refs, wrkpGitRef{LocalRef: fields[0], LocalSHA: fields[1], RemoteRef: fields[2], RemoteSHA: fields[3]})
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return refs, nil
}

func wrkpGitTaskIDs(message string) []string {
	found := wrkpGitTaskPattern.FindAllString(message, -1)
	seen := map[string]bool{}
	result := make([]string, 0, len(found))
	for _, task := range found {
		if !seen[task] {
			seen[task] = true
			result = append(result, task)
		}
	}
	return result
}

func wrkpGitLinkableTask(ctx context.Context, tr Transport, projectSlug string, tasks []string) string {
	if len(tasks) != 1 {
		return ""
	}
	raw, err := tr.Call(ctx, "wrkq.task.catView", map[string]any{"task": tasks[0], "includeComments": false})
	if err != nil {
		return ""
	}
	var task struct {
		ID   string `json:"id"`
		Path string `json:"path"`
	}
	if json.Unmarshal(raw, &task) != nil || strings.SplitN(task.Path, "/", 2)[0] != projectSlug {
		return ""
	}
	return task.ID
}

func postWrkpGitFact(cmd *cobra.Command, tr Transport, principal, project, task, node, eventType, summary string, payload map[string]any, key, occurredAt string) error {
	payloadRaw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	params := map[string]any{
		"project": project, "type": eventType, "source": "lefthook", "node": node,
		"summary": summary, "payload": json.RawMessage(payloadRaw), "idempotencyKey": key,
		"occurredAt": occurredAt, "principalRef": principal,
	}
	if task != "" {
		params["task"] = task
	}
	if scopeRef := wrkcScopeRef(cmd); scopeRef != "" {
		params["scopeRef"] = scopeRef
	}
	raw, err := tr.Call(cmd.Context(), "wrkq.projectEvent.post", params)
	if err != nil {
		return wrkpRPCError(err)
	}
	var result struct {
		FID     string `json:"fid"`
		Created bool   `json:"created"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return err
	}
	if wrkpJSON(cmd) {
		return encodeJSONIndent(cmd, result)
	}
	if result.Created {
		fmt.Fprintln(cmd.OutOrStdout(), result.FID)
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s (existing)\n", result.FID)
	}
	return nil
}

func parseWrkpGitPayloadExtra(values []string) (map[string]any, error) {
	result := map[string]any{}
	for _, raw := range values {
		key, value, ok := strings.Cut(raw, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("--payload-extra requires key=value")
		}
		var decoded any
		if json.Unmarshal([]byte(value), &decoded) != nil {
			decoded = value
		}
		result[key] = decoded
	}
	return result, nil
}

func truncateWrkpGitSummary(value string, maxRunes int) string {
	if utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	return string(runes[:maxRunes])
}

func allZeroGitSHA(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r != '0' {
			return false
		}
	}
	return true
}

func shortWrkpGitSHA(value string) string {
	if len(value) <= 7 {
		return value
	}
	return value[:7]
}

func runWrkpGitCommand(ctx context.Context, dir string, args ...string) (string, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = dir
	var stdout, stderr strings.Builder
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if message := strings.TrimSpace(stderr.String()); message != "" {
			return "", fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), message, err)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}
