package rpccli

// wrkp_log.go — `wrkp log`: read the merged project timeline newest-first,
// page forward from a cursor, or follow appends until a clock ends the follow.
// Also the advisory pieces a read needs: the subtree fingerprint a follow
// watches and the notice that explains an empty window.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/lherron/wrkq/internal/style"
	workrpcclient "github.com/lherron/wrkq/pkg/client"
	"github.com/spf13/cobra"
)

type wrkpLogView struct {
	Container struct {
		UUID string `json:"uuid"`
		Path string `json:"path"`
	} `json:"container"`
	Entries    []timelineEntry `json:"entries"`
	NextCursor string          `json:"nextCursor,omitempty"`
}

type wrkpContainerTaskCounts struct {
	Items []struct {
		UUID string `json:"uuid"`
		Path string `json:"path"`
	} `json:"items"`
}

func newWrkpLogCmd() *cobra.Command {
	var since, before, task, typeList, timeoutStr, stallAfterStr string
	var afters []string
	var limit int
	var follow, ndjson, porcelain, pretty, allTypes bool
	cmd := &cobra.Command{
		Use: "log [project]...", Short: "Read the merged project timeline", Args: cobra.ArbitraryArgs,
		Long: `Read the merged project timeline, newest first, or follow it with --follow.

A follow ends when --timeout (total duration) or --stall-after (time with no new
entry) expires. It then writes one terminal record and exits 0: a
{"type":"wrkp.log.terminal",...} line in json/ndjson modes, a "follow ended"
line otherwise, both naming the cursor to resume from with --after. This is
the termination contract of wrkq monitor watch without --until.

Several projects: wrkp log --follow <project> <project>... --ndjson follows
them all in one process on one commit wait. Each entry line gains a "project"
key (the container path), --after is repeated as <project>=<cursor> (a project
without one starts at its head; wrkp cursor <project>... prints these), and the
terminal record carries "cursors": {"<project>": "<cursor>"} in place of
"cursor". --task, --before, --limit and --since are single-project only.

Exit codes: 0=read done, or a follow ended on its clock; 1=error; 2=usage error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			timeout, stallAfter, err := wrkpFollowClocks(cmd, follow, timeoutStr, stallAfterStr)
			if err != nil {
				return err
			}
			if len(args) > 1 {
				return runWrkpMultiFollow(cmd, args, afters, typeList, allTypes, follow, ndjson, pretty, timeout, stallAfter)
			}
			// One project keeps --after's single-value meaning: the last one given.
			after := ""
			if len(afters) > 0 {
				after = afters[len(afters)-1]
			}
			var beforeTime time.Time
			if before != "" {
				var err error
				beforeTime, err = time.Parse(time.RFC3339Nano, before)
				if err != nil {
					return fmt.Errorf("invalid --before value: expected RFC3339 timestamp: %w", err)
				}
			}
			tr, sc, cfg, closeFn, err := openMirrorConfig(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			project := sc.selector("", true)
			if len(args) == 1 {
				project = args[0]
			}
			mode := resolveWrkpMode(cmd, pretty, ndjson)
			// A FORWARD read: --after names an ascending cursor (from `wrkp
			// cursor` or a previous forward page). It reads what changed since
			// that position up to now, then stops -- a bounded --follow. The
			// descending cursor a plain `wrkp log --porcelain` prints pages
			// back into history instead.
			forward := !follow && wrkpCursorAscending(after)
			deliveredLimit := limit
			if deliveredLimit <= 0 {
				deliveredLimit = wrkpDefaultLimit
			}
			query := wrkpLogQuery{
				project: project, since: since, before: before, typeList: typeList,
				allTypes: allTypes, follow: follow, forward: forward, deliveredLimit: deliveredLimit,
			}
			if task != "" {
				query.task = sc.selector(task, false)
			}
			stream := &wrkpStream{arg: project, query: query, cursor: after}
			if follow {
				return (&wrkpFollowRun{
					cmd: cmd, tr: tr, remote: cfg.RemoteEndpoint != "", mode: mode, since: since,
					beforeTime: beforeTime, timeout: timeout, stallAfter: stallAfter,
					streams: []*wrkpStream{stream},
				}).run()
			}
			return readWrkpBounded(cmd, tr, stream, mode, forward, porcelain, since)
		},
	}
	cmd.Flags().StringArrayVar(&afters, "after", nil, "Opaque cursor: a previous page's, or a forward cursor from `wrkp cursor`; with several projects, repeated <project>=<cursor>")
	cmd.Flags().StringVar(&since, "since", "", "RFC3339 time or duration")
	cmd.Flags().StringVar(&before, "before", "", "Exclusive RFC3339 server-time upper bound")
	cmd.Flags().StringVar(&typeList, "type", "", "Comma-separated exact or trailing-glob types")
	cmd.Flags().BoolVar(&allTypes, "all-types", false, "Include turn.* facts hidden by default")
	cmd.Flags().StringVar(&task, "task", "", "Task selector")
	cmd.Flags().IntVar(&limit, "limit", 0, "Maximum delivered entries")
	cmd.Flags().BoolVar(&follow, "follow", false, "Follow newly appended matching entries")
	cmd.Flags().StringVar(&timeoutStr, "timeout", "", "End a --follow after this duration (e.g. 30m)")
	cmd.Flags().StringVar(&stallAfterStr, "stall-after", "", "End a --follow after this long with no new entry")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output entries as NDJSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Write the next cursor to stderr")
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force the styled timeline even when not a TTY")
	return cmd
}

// wrkpStream is one project's timeline read: its fixed query plus the position
// and advisory state a read keeps for it. A single-project read has one; a
// multi-project follow has one per project, all on one commit wait.
type wrkpStream struct {
	arg    string // the project as given: the key of --after and terminal cursors
	query  wrkpLogQuery
	tagged bool // multi-project: each entry line names its project
	cursor string
	// sentCursor is the cursor the last page was read from; the drain is
	// caught up when a page leaves the cursor where it was.
	sentCursor string
	path       string
	delivered  int
	styled     *style.TimelineWriter
	// containerSet and fingerprintAt are the advisory subtree fingerprint.
	containerSet  string
	fingerprintAt time.Time
	// previousCursor and idleNoticed exist only for the empty-window notice:
	// one detects that a tail has caught up, the other keeps the notice to a
	// single printing per session.
	previousCursor string
	idleNoticed    bool
}

// fetch reads the stream's next page from its current cursor.
func (s *wrkpStream) fetch(ctx context.Context, tr Transport) (wrkpLogView, error) {
	var view wrkpLogView
	raw, err := tr.Call(ctx, "wrkq.container.timelineView", s.query.pageParams(s.cursor, s.delivered))
	if err != nil {
		return view, s.named(wrkpRPCError(err))
	}
	if err := json.Unmarshal(raw, &view); err != nil {
		return view, err
	}
	return view, nil
}

// named keeps an error's type; in a multi-project follow it also names the
// project, so one refused argument is identifiable among several.
func (s *wrkpStream) named(err error) error {
	if !s.tagged {
		return err
	}
	var rpcErr *Error
	if errors.As(err, &rpcErr) {
		annotated := *rpcErr
		annotated.Message = s.arg + ": " + annotated.Message
		return &annotated
	}
	return fmt.Errorf("%s: %w", s.arg, err)
}

func (s *wrkpStream) render(cmd *cobra.Command, entries []timelineEntry, mode string) error {
	if s.tagged {
		return renderWrkpTaggedEntries(cmd, s.path, entries)
	}
	return renderWrkpEntries(cmd, entries, mode, s.styled)
}

// readWrkpBounded is a read that stops: newest-first history (paged back by a
// descending cursor), or a forward read from an ascending --after up to now.
func readWrkpBounded(cmd *cobra.Command, tr Transport, s *wrkpStream, mode string, forward, porcelain bool, since string) error {
	heldEntries := []timelineEntry{}
	for {
		if cmd.Context().Err() != nil {
			return nil
		}
		s.sentCursor = s.cursor
		view, err := s.fetch(cmd.Context(), tr)
		if err != nil {
			if cmd.Context().Err() != nil {
				return nil
			}
			return err
		}
		if mode == "human" && s.styled == nil {
			s.styled = style.NewTimelineWriter(cmd.OutOrStdout(), view.Container.Path)
		}
		s.path = view.Container.Path
		// A bounded --json read is ONE array however many pages it took; it
		// is written when the read stops. A bounded human read is held too:
		// it is fetched newest-first so --limit keeps the most recent
		// entries, but printed oldest-first, in the order --follow prints the
		// same window.
		if mode == "json" || (mode == "human" && !forward) {
			heldEntries = append(heldEntries, view.Entries...)
		} else if err := s.render(cmd, view.Entries, mode); err != nil {
			return err
		}
		s.delivered += len(view.Entries)
		s.cursor = view.NextCursor
		if forward {
			if s.cursor == "" {
				return renderWrkpEntries(cmd, heldEntries, mode, s.styled)
			}
			// A tail cursor always comes back, so the read is caught up
			// when a page leaves it where it was; a page of only
			// excluded rows still moves it and the read continues.
			if s.cursor != s.sentCursor && s.delivered < s.query.deliveredLimit {
				continue
			}
			if porcelain && s.cursor != "" {
				fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", s.cursor)
			}
			return renderWrkpEntries(cmd, heldEntries, mode, s.styled)
		}
		// A raw-scan page may consume only excluded rows. Keep advancing
		// sparse filtered reads until the delivered limit is full or the
		// fixed fence is drained; --porcelain reports that final position.
		if s.cursor != "" && s.delivered < s.query.deliveredLimit {
			continue
		}
		if porcelain && s.cursor != "" {
			fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", s.cursor)
		}
		if mode == "human" && s.delivered == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(),
				wrkpEmptyWindowNotice(cmd.Context(), tr, s.query.project, s.path, since, false))
		}
		if mode == "human" {
			slices.Reverse(heldEntries)
		}
		return renderWrkpEntries(cmd, heldEntries, mode, s.styled)
	}
}

// wrkpFollowRun is one follow process over one or more streams. However many
// projects it follows, it holds ONE commit wait: a wake reads every project
// and drains each until its cursor stops moving, then parks again.
type wrkpFollowRun struct {
	cmd                 *cobra.Command
	tr                  Transport
	remote              bool
	mode, since         string
	beforeTime          time.Time
	timeout, stallAfter time.Duration
	streams             []*wrkpStream
	multi               bool
}

func (r *wrkpFollowRun) run() error {
	cmd, tr := r.cmd, r.tr
	ctx := cmd.Context()
	followStarted := time.Now()
	lastEntry := followStarted
	maxWait := 25 * time.Second
	// Local pipe calls cannot cancel a parked request, and separate
	// local writers do not share the process commit signal. Keep
	// these waits short to preserve local cancellation and clocks.
	if !r.remote {
		maxWait = monitorPollInterval
	}
	// Take the ephemeral wake token before the first authoritative read.
	// The timeline cursors alone remain the replay positions.
	token, err := workrpcclient.WaitForCommit(ctx, tr, "", wrkpFollowDelay(r.beforeTime, min(maxWait, clockSlack(followStarted, lastEntry, r.timeout, r.stallAfter))))
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return wrkpRPCError(err)
	}
	pending := r.streams
	for {
		if ctx.Err() != nil {
			return nil
		}
		readStarted := time.Now()
		// Read every pending stream before writing any of them, so a project
		// the server refuses fails the command before another's entry is out.
		views := make([]wrkpLogView, len(pending))
		for i, s := range pending {
			s.sentCursor = s.cursor
			if views[i], err = s.fetch(ctx, tr); err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return err
			}
		}
		for i, s := range pending {
			view := views[i]
			if r.mode == "human" && s.styled == nil {
				s.styled = style.NewTimelineWriter(cmd.OutOrStdout(), view.Container.Path)
			}
			s.path = view.Container.Path
			if err := s.render(cmd, view.Entries, r.mode); err != nil {
				return err
			}
			s.delivered += len(view.Entries)
			if len(view.Entries) > 0 {
				lastEntry = time.Now()
			}
			// The fingerprint is advisory and reads the whole container
			// tree, so a tail refreshes it on a slow clock, not per poll:
			// per-poll, a dozen idle followers drove wrkqd to ~5 cores.
			if s.containerSet == "" || time.Since(s.fingerprintAt) >= wrkpFingerprintInterval {
				s.fingerprintAt = time.Now()
				currentSet, err := wrkpSubtreeFingerprint(ctx, tr, view.Container.UUID)
				if err != nil {
					if ctx.Err() != nil {
						return nil
					}
					return s.named(err)
				}
				if s.containerSet != "" && currentSet != s.containerSet {
					if s.tagged {
						fmt.Fprintf(cmd.ErrOrStderr(), "wrkp: project %s container set changed; restart it alone with --since to replay moved history\n", s.path)
					} else {
						fmt.Fprintln(cmd.ErrOrStderr(), "wrkp: project container set changed; restart with --since to replay moved history")
					}
				}
				s.containerSet = currentSet
			}
			s.cursor = view.NextCursor
			// A tail with an empty window prints nothing and waits, which on
			// a terminal is indistinguishable from a hang -- that is exactly
			// how `--since 3h --follow` was read as a stall. Say it once, and
			// only when the read is demonstrably CAUGHT UP: a tail cursor
			// moves whenever the scan advances, so two identical cursors mean
			// the backlog is drained rather than that a sparse page of
			// excluded rows is still being walked.
			if r.mode == "human" && s.delivered == 0 && !s.idleNoticed &&
				s.cursor != "" && s.cursor == s.previousCursor {
				fmt.Fprintln(cmd.ErrOrStderr(),
					wrkpEmptyWindowNotice(ctx, tr, s.query.project, s.path, r.since, true))
				s.idleNoticed = true
			}
			s.previousCursor = s.cursor
		}
		// Once the window closes, drain every held page before stopping.
		// The bounded server tail drops its cursor only after exhaustion.
		if !r.beforeTime.IsZero() && !readStarted.Before(r.beforeTime) {
			if !slices.ContainsFunc(pending, func(s *wrkpStream) bool { return s.cursor != "" }) {
				return nil
			}
			continue
		}
		if r.timeout > 0 && time.Since(followStarted) >= r.timeout {
			return r.terminal(monitorResultTimeout)
		}
		if r.stallAfter > 0 && time.Since(lastEntry) >= r.stallAfter {
			return r.terminal(monitorResultStall)
		}
		// Drain all advancing pages (including filtered empty pages)
		// before parking; a commit token never replaces a view cursor.
		advancing := []*wrkpStream{}
		for _, s := range pending {
			if s.cursor != "" && s.cursor != s.sentCursor {
				advancing = append(advancing, s)
			}
		}
		if len(advancing) > 0 {
			pending = advancing
			continue
		}
		// Against a remote wrkqd the commit token is authoritative: an
		// unchanged token (timeout, capacity refusal) means nothing was
		// committed in wrkqd, so park again instead of re-reading the
		// timeline. Writes outside wrkqd (wrkqadm, migrations, restores)
		// never move the token, so a read is still forced every
		// wrkpFollowForcedReadInterval (ADR 0004 unsupported list).
		// Locally, other processes' writes never move the token, so
		// every wait return still reads.
		for {
			prev := token
			slack := wrkpFollowDelay(r.beforeTime, min(maxWait, clockSlack(followStarted, lastEntry, r.timeout, r.stallAfter)))
			if forcedIn := wrkpFollowForcedReadInterval - time.Since(readStarted); forcedIn < slack {
				slack = forcedIn
			}
			token, err = workrpcclient.WaitForCommit(ctx, tr, token, slack)
			if err != nil {
				if ctx.Err() != nil {
					return nil
				}
				return wrkpRPCError(err)
			}
			if !r.remote || token == "" || token != prev || slack <= 0 {
				break
			}
			if r.timeout > 0 && time.Since(followStarted) >= r.timeout {
				return r.terminal(monitorResultTimeout)
			}
			if r.stallAfter > 0 && time.Since(lastEntry) >= r.stallAfter {
				return r.terminal(monitorResultStall)
			}
			if ctx.Err() != nil {
				return nil
			}
		}
		// A wake reads every project, whichever one committed.
		pending = r.streams
	}
}

// terminal writes the record that ends a follow on its clock: the one cursor
// for a single project, or every project's cursor in a multi-project follow.
func (r *wrkpFollowRun) terminal(result monitorTerminalResult) error {
	if !r.multi {
		return writeWrkpTerminal(r.cmd, r.mode, result, r.streams[0].cursor)
	}
	cursors := make(map[string]string, len(r.streams))
	for _, s := range r.streams {
		cursors[s.arg] = s.cursor
	}
	line := wrkpTerminalLine{monitorTerminalLine: buildMonitorTerminalLine(result, nil), Cursors: cursors}
	line.Type = "wrkp.log.terminal"
	return json.NewEncoder(r.cmd.OutOrStdout()).Encode(line)
}

// runWrkpMultiFollow is `wrkp log --follow a b ... --ndjson`: client-side
// fan-in over the per-project timeline (the server's subtree timeline is per
// project by invariant), one stream per project, one commit wait for all.
func runWrkpMultiFollow(cmd *cobra.Command, projects, afters []string, typeList string, allTypes, follow, ndjson, pretty bool, timeout, stallAfter time.Duration) error {
	usage := func(format string, args ...any) error {
		return &clifunnel.UsageError{Err: fmt.Errorf(format, args...), Cmd: cmd}
	}
	explicitNDJSON := ndjson
	if outF := cmd.Flag("output"); outF != nil && outF.Changed && outF.Value.String() == "ndjson" {
		explicitNDJSON = true
	}
	if !follow || !explicitNDJSON || pretty || resolveWrkpMode(cmd, pretty, ndjson) != "ndjson" {
		return usage("more than one project is accepted only with --follow and --ndjson")
	}
	for _, name := range []string{"task", "before", "limit", "since"} {
		if cmd.Flags().Changed(name) {
			return usage("--%s reads one project; it cannot be used with several", name)
		}
	}
	streams := make([]*wrkpStream, 0, len(projects))
	byArg := map[string]*wrkpStream{}
	for _, project := range projects {
		if byArg[project] != nil {
			return usage("project %s is named twice", project)
		}
		s := &wrkpStream{arg: project, tagged: true, query: wrkpLogQuery{
			project: project, typeList: typeList, allTypes: allTypes, follow: true, deliveredLimit: wrkpDefaultLimit,
		}}
		byArg[project] = s
		streams = append(streams, s)
	}
	seen := map[string]bool{}
	for _, raw := range afters {
		project, cursor, ok := strings.Cut(raw, "=")
		if !ok || project == "" || cursor == "" {
			return usage("--after %q: with several projects each --after is <project>=<cursor>", raw)
		}
		s := byArg[project]
		if s == nil {
			return usage("--after names %s, which is not one of the followed projects", project)
		}
		if seen[project] {
			return usage("--after is given twice for %s", project)
		}
		seen[project] = true
		s.cursor = cursor
	}
	tr, _, cfg, closeFn, err := openMirrorConfig(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	return (&wrkpFollowRun{
		cmd: cmd, tr: tr, remote: cfg.RemoteEndpoint != "", mode: "ndjson",
		timeout: timeout, stallAfter: stallAfter, streams: streams, multi: true,
	}).run()
}

// wrkpLogQuery is the fixed part of one `wrkp log` read; pageParams turns it
// into each page's timelineView request.
type wrkpLogQuery struct {
	project, since, before, task, typeList string
	allTypes, follow, forward              bool
	deliveredLimit                         int
}

// pageParams builds the timelineView request for the page at cursor, with
// delivered entries already written.
func (q wrkpLogQuery) pageParams(cursor string, delivered int) map[string]any {
	params := map[string]any{"container": q.project, "scope": "subtree", "entriesOnly": true, "tail": q.follow || q.forward}
	if cursor != "" {
		params["cursor"] = cursor
	} else if !q.follow {
		// A log reads newest-first: --limit N is "the N most recent",
		// and the newest entry is the first one delivered. --follow
		// stays ascending because it follows APPENDS, which only
		// arrive at the newest end. Later pages take their direction
		// from the cursor, which is authoritative, so the flag is
		// sent only on the opening page.
		params["order"] = "desc"
	}
	if q.since != "" {
		params["since"] = q.since
	}
	if q.before != "" {
		params["before"] = q.before
	}
	if q.task != "" {
		params["task"] = q.task
	}
	// --limit is a delivered BUDGET for a bounded read and a per-poll
	// page size under --follow, where the read never ends and a
	// decreasing budget would collapse to the server default.
	if q.follow {
		params["limit"] = wrkpPageLimit(q.deliveredLimit)
	} else {
		params["limit"] = wrkpPageLimit(q.deliveredLimit - delivered)
	}
	if q.typeList != "" {
		params["types"] = splitCommaValues(q.typeList)
	}
	if q.allTypes {
		params["allTypes"] = true
	}
	return params
}

// wrkpFollowClocks parses --timeout and --stall-after. They bound a follow, so
// either one without --follow is a usage error rather than a silent no-op.
func wrkpFollowClocks(cmd *cobra.Command, follow bool, timeoutStr, stallAfterStr string) (time.Duration, time.Duration, error) {
	if !follow && (timeoutStr != "" || stallAfterStr != "") {
		return 0, 0, &clifunnel.UsageError{Err: errors.New("--timeout and --stall-after bound a --follow; add --follow or drop them"), Cmd: cmd}
	}
	timeout, err := parseMonitorDuration(timeoutStr, 0)
	if err != nil {
		return 0, 0, &clifunnel.UsageError{Err: fmt.Errorf("--timeout: %w", err), Cmd: cmd}
	}
	stallAfter, err := parseMonitorDuration(stallAfterStr, 0)
	if err != nil {
		return 0, 0, &clifunnel.UsageError{Err: fmt.Errorf("--stall-after: %w", err), Cmd: cmd}
	}
	return timeout, stallAfter, nil
}

// wrkpTerminalLine is monitor watch's terminal record under wrkp's own type,
// plus the cursor a caller resumes from with --after.
type wrkpTerminalLine struct {
	monitorTerminalLine
	Cursor string `json:"cursor,omitempty"`
	// Cursors replaces Cursor in a multi-project follow, keyed by the
	// project as given, so each value resumes as --after <project>=<cursor>.
	Cursors map[string]string `json:"cursors,omitempty"`
}

// writeWrkpTerminal ends a follow that ran out its clock: one terminal record on
// stdout with the data, exit 0 (the follow ended exactly as asked).
func writeWrkpTerminal(cmd *cobra.Command, mode string, result monitorTerminalResult, cursor string) error {
	line := wrkpTerminalLine{monitorTerminalLine: buildMonitorTerminalLine(result, nil), Cursor: cursor}
	line.Type = "wrkp.log.terminal"
	if mode == "json" || mode == "ndjson" {
		return json.NewEncoder(cmd.OutOrStdout()).Encode(line)
	}
	resume := ""
	if cursor != "" {
		resume = "; resume with: wrkp log --follow --after " + cursor
	}
	_, err := fmt.Fprintf(cmd.OutOrStdout(), "follow ended: %s (%s)%s\n", line.Result, line.Reason, resume)
	return err
}

// The advisory subtree fingerprint retains its own clock while a tail waits
// for commits. Waits also end at --before so the finite window can drain.
const wrkpFingerprintInterval = 30 * time.Second

// wrkpFollowForcedReadInterval bounds how long a remote follower trusts an
// unchanged commit token: it re-reads at least this often, so writes made
// outside wrkqd surface within it. A var only so tests can shorten it.
var wrkpFollowForcedReadInterval = 5 * time.Minute

func wrkpFollowDelay(before time.Time, idle time.Duration) time.Duration {
	if !before.IsZero() && time.Until(before) < idle {
		if remaining := time.Until(before); remaining > 0 {
			return remaining
		}
		return 0
	}
	return idle
}

// wrkpDefaultLimit matches the server's own default page size, and
// wrkpMaxPageLimit matches its hard per-page cap. The cap belongs to one PAGE,
// not to the read: --limit is what the CALLER asked for, and the paging loop
// below already stitches pages together, so a --limit above the cap is
// satisfied by more pages rather than refused. Before T-08328 the whole --limit
// went to the server verbatim, so `--limit 5000` was rejected outright and a
// caller who piped the output saw an empty stream and the pipeline's exit code.
const (
	wrkpDefaultLimit = 100
	wrkpMaxPageLimit = 1000
)

func wrkpPageLimit(remaining int) int {
	if remaining > wrkpMaxPageLimit {
		return wrkpMaxPageLimit
	}
	return remaining
}

// wrkpSubtreeFingerprint is advisory only: the timeline cursor remains the
// authority. It lets follow readers notice that a move changed the mutable
// subtree whose production-time stamps are being filtered at each poll.
func wrkpSubtreeFingerprint(ctx context.Context, tr Transport, projectUUID string) (string, error) {
	raw, err := tr.Call(ctx, "wrkq.container.taskCounts", map[string]any{"includeArchived": true})
	if err != nil {
		return "", wrkpRPCError(err)
	}
	var counts wrkpContainerTaskCounts
	if err := json.Unmarshal(raw, &counts); err != nil {
		return "", err
	}
	projectPath := ""
	for _, item := range counts.Items {
		if item.UUID == projectUUID {
			projectPath = item.Path
			break
		}
	}
	if projectPath == "" {
		return "", fmt.Errorf("project container %s is absent from task counts", projectUUID)
	}
	members := make([]string, 0)
	for _, item := range counts.Items {
		if item.Path == projectPath || strings.HasPrefix(item.Path, projectPath+"/") {
			members = append(members, item.UUID+"\x00"+item.Path)
		}
	}
	sort.Strings(members)
	return strings.Join(members, "\x01"), nil
}

// wrkpEmptyWindowNotice explains a read that delivered nothing. An empty window
// is silent on BOTH paths -- the bounded read exits 0 with no output at all, and
// the tail simply waits -- and that silence is read as a broken command rather
// than as an answer. `wrkp log --since 3h --follow` on a project whose newest
// entry was ten hours old was reported as a performance regression for exactly
// this reason: the command was correct and fast, and said so in no way.
//
// The notice names the window that was asked for and, when the timeline does
// hold older entries, WHEN the newest one was. That second fact is what
// separates "your window is empty" from "the timeline is dead", and it is the
// question a reader asks next.
func wrkpEmptyWindowNotice(ctx context.Context, tr Transport, project, containerPath, since string, following bool) string {
	where := containerPath
	if where == "" {
		where = project
	}
	notice := "no entries"
	if where != "" {
		notice += " in " + where
	}
	if since != "" {
		notice += " since " + since
	}
	if newest := wrkpNewestEntryStamp(ctx, tr, project); newest != "" {
		notice += "; newest is " + newest
	}
	if following {
		notice += " — following for new ones (ctrl-c to stop)"
	}
	return "wrkp: " + notice
}

// wrkpNewestEntryStamp reports when the project last had a deliverable entry,
// in the reader's own zone, for the empty-window notice. It is one descending
// limit-1 read issued ONLY on the empty path, so a read that delivered anything
// pays nothing for it.
//
// Its failure is never the caller's error -- the read it annotates already
// succeeded -- so every failure degrades to the empty string and the notice
// falls back to naming the window alone. A descending page can also legitimately
// deliver nothing when its newest raw rows are all excluded from this container;
// that too degrades quietly, and the surviving wording claims only that the
// WINDOW is empty, never that the timeline is.
func wrkpNewestEntryStamp(ctx context.Context, tr Transport, project string) string {
	raw, err := tr.Call(ctx, "wrkq.container.timelineView", map[string]any{
		"container": project, "scope": "subtree", "entriesOnly": true,
		"order": "desc", "limit": 1,
	})
	if err != nil {
		return ""
	}
	var view wrkpLogView
	if err := json.Unmarshal(raw, &view); err != nil || len(view.Entries) == 0 {
		return ""
	}
	newest, ok := style.ParseTimestamp(view.Entries[0].Timestamp)
	if !ok {
		return ""
	}
	elapsed := style.NowUTC().Sub(newest)
	if elapsed < 0 {
		elapsed = 0
	}
	return fmt.Sprintf("%s (%s ago)",
		style.FormatLocalTime(newest),
		style.FormatDuration(elapsed))
}
