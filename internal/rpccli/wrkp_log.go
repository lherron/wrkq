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
	var after, since, before, task, typeList, timeoutStr, stallAfterStr string
	var limit int
	var follow, ndjson, porcelain, pretty, allTypes bool
	cmd := &cobra.Command{
		Use: "log [project]", Short: "Read the merged project timeline", Args: cobra.MaximumNArgs(1),
		Long: `Read the merged project timeline, newest first, or follow it with --follow.

A follow ends when --timeout (total duration) or --stall-after (time with no new
entry) expires. It then writes one terminal record and exits 0: a
{"type":"wrkp.log.terminal",...} line in json/ndjson modes, a "follow ended"
line otherwise, both naming the cursor to resume from with --after. This is
the termination contract of wrkq monitor watch without --until.

Exit codes: 0=read done, or a follow ended on its clock; 1=error; 2=usage error.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			timeout, stallAfter, err := wrkpFollowClocks(cmd, follow, timeoutStr, stallAfterStr)
			if err != nil {
				return err
			}
			var beforeTime time.Time
			if before != "" {
				var err error
				beforeTime, err = time.Parse(time.RFC3339Nano, before)
				if err != nil {
					return fmt.Errorf("invalid --before value: expected RFC3339 timestamp: %w", err)
				}
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			project := sc.selector("", true)
			if len(args) == 1 {
				project = args[0]
			}
			mode := resolveWrkpMode(cmd, pretty, ndjson)
			var styled *style.TimelineWriter
			cursor := after
			// A FORWARD read: --after names an ascending cursor (from `wrkp
			// cursor` or a previous forward page). It reads what changed since
			// that position up to now, then stops -- a bounded --follow. The
			// descending cursor a plain `wrkp log --porcelain` prints pages
			// back into history instead.
			forward := !follow && wrkpCursorAscending(after)
			heldEntries := []timelineEntry{}
			containerSet := ""
			// previousCursor and followIdleNoticed exist only for the
			// empty-window notice below: one detects that a tail has caught up,
			// the other keeps the notice to a single printing per session.
			previousCursor := ""
			followIdleNoticed := false
			containerPath := ""
			delivered := 0
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
			followStarted := time.Now()
			lastEntry := followStarted
			for {
				readStarted := time.Now()
				sentCursor := cursor
				params := query.pageParams(cursor, delivered)
				raw, err := tr.Call(cmd.Context(), "wrkq.container.timelineView", params)
				if err != nil {
					if cmd.Context().Err() != nil {
						return nil
					}
					return wrkpRPCError(err)
				}
				var view wrkpLogView
				if err := json.Unmarshal(raw, &view); err != nil {
					return err
				}
				if mode == "human" && styled == nil {
					styled = style.NewTimelineWriter(cmd.OutOrStdout(), view.Container.Path)
				}
				containerPath = view.Container.Path
				// A bounded --json read is ONE array however many pages it
				// took; it is written when the read stops. A bounded human read
				// is held too: it is fetched newest-first so --limit keeps the
				// most recent entries, but printed oldest-first, in the order
				// --follow prints the same window.
				if !follow && (mode == "json" || (mode == "human" && !forward)) {
					heldEntries = append(heldEntries, view.Entries...)
				} else if err := renderWrkpEntries(cmd, view.Entries, mode, styled); err != nil {
					return err
				}
				delivered += len(view.Entries)
				if len(view.Entries) > 0 {
					lastEntry = time.Now()
				}
				if follow {
					currentSet, err := wrkpSubtreeFingerprint(cmd.Context(), tr, view.Container.UUID)
					if err != nil {
						if cmd.Context().Err() != nil {
							return nil
						}
						return err
					}
					if containerSet != "" && currentSet != containerSet {
						fmt.Fprintln(cmd.ErrOrStderr(), "wrkp: project container set changed; restart with --since to replay moved history")
					}
					containerSet = currentSet
				}
				cursor = view.NextCursor
				// A tail with an empty window prints nothing and waits, which on
				// a terminal is indistinguishable from a hang -- that is exactly
				// how `--since 3h --follow` was read as a stall. Say it once, and
				// only when the read is demonstrably CAUGHT UP: a tail cursor
				// moves whenever the scan advances, so two identical cursors mean
				// the backlog is drained rather than that a sparse page of
				// excluded rows is still being walked.
				if follow && mode == "human" && delivered == 0 && !followIdleNoticed &&
					cursor != "" && cursor == previousCursor {
					fmt.Fprintln(cmd.ErrOrStderr(),
						wrkpEmptyWindowNotice(cmd.Context(), tr, project, containerPath, since, true))
					followIdleNoticed = true
				}
				previousCursor = cursor
				// Once the window closes, drain every held page before stopping.
				// The bounded server tail drops its cursor only after exhaustion.
				if follow && !beforeTime.IsZero() && !readStarted.Before(beforeTime) {
					if cursor == "" {
						return nil
					}
					continue
				}
				if forward {
					if cursor == "" {
						return renderWrkpEntries(cmd, heldEntries, mode, styled)
					}
					// A tail cursor always comes back, so the read is caught up
					// when a page leaves it where it was; a page of only
					// excluded rows still moves it and the read continues.
					if cursor != sentCursor && delivered < deliveredLimit {
						continue
					}
					if porcelain && cursor != "" {
						fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", cursor)
					}
					return renderWrkpEntries(cmd, heldEntries, mode, styled)
				}
				if !follow {
					// A raw-scan page may consume only excluded rows. Keep advancing
					// sparse filtered reads until the delivered limit is full or the
					// fixed fence is drained; --porcelain reports that final position.
					if cursor != "" && delivered < deliveredLimit {
						continue
					}
					if porcelain && cursor != "" {
						fmt.Fprintf(cmd.ErrOrStderr(), "next_cursor=%s\n", cursor)
					}
					if mode == "human" && delivered == 0 {
						fmt.Fprintln(cmd.ErrOrStderr(),
							wrkpEmptyWindowNotice(cmd.Context(), tr, project, containerPath, since, false))
					}
					if mode == "human" {
						slices.Reverse(heldEntries)
					}
					return renderWrkpEntries(cmd, heldEntries, mode, styled)
				}
				if timeout > 0 && time.Since(followStarted) >= timeout {
					return writeWrkpTerminal(cmd, mode, monitorResultTimeout, cursor)
				}
				if stallAfter > 0 && time.Since(lastEntry) >= stallAfter {
					return writeWrkpTerminal(cmd, mode, monitorResultStall, cursor)
				}
				select {
				case <-cmd.Context().Done():
					return nil
				case <-time.After(wrkpFollowDelay(beforeTime)):
				}
			}
		},
	}
	cmd.Flags().StringVar(&after, "after", "", "Opaque cursor: a previous page's, or a forward cursor from `wrkp cursor`")
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

func wrkpFollowDelay(before time.Time) time.Duration {
	if !before.IsZero() && time.Until(before) < monitorPollInterval {
		if remaining := time.Until(before); remaining > 0 {
			return remaining
		}
		return 0
	}
	return monitorPollInterval
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
