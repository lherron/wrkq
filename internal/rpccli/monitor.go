package rpccli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/domain"
	"github.com/spf13/cobra"
)

// The legacy `--ndjson` flag on monitor watch is accepted for surface parity but is
// a no-op (the monitor feed is always typed NDJSON); it is bound below.

// monitor mirrors `wrkq monitor watch|wait` over the bounded-polling read models
// wrkq.monitor.eventsView + wrkq.monitor.stateView (T-05115). The SERVER owns
// selector resolution, resource_id hydration, comment→task backfill,
// isEventIncluded / isStateChangeEvent / event-type filtering, and the single
// authoritative --until condition snapshot. The CLIENT (here) owns the blocking
// poll loop, the timeout/stall clocks, the monotonic high-water cursor across
// polls, exactly-ONE terminal NDJSON record, the exit codes, and stdout/stderr.
// This is daedalus's bounded-polling-as-RPC-v1-streaming arch record: NO server
// push/subscribe in v1; the loop lives caller-side.

const monitorPollInterval = 100 * time.Millisecond

// ──── typed NDJSON schemas (mirrors legacy internal/cli, daedalus T-04798) ─────

type monitorTerminalResult string

const (
	monitorResultMet     monitorTerminalResult = "met"
	monitorResultTimeout monitorTerminalResult = "timeout"
	monitorResultStall   monitorTerminalResult = "stall"
	monitorResultError   monitorTerminalResult = "error"
)

type monitorTerminalReason string

const (
	monitorReasonConditionMet monitorTerminalReason = "condition_met"
	monitorReasonTimedOut     monitorTerminalReason = "timed_out"
	monitorReasonStalled      monitorTerminalReason = "stalled"
	monitorReasonStreamError  monitorTerminalReason = "stream_error"
)

// monitorTerminalLine is the terminal record emitted exactly once as the last
// stdout line before exit. Schema locked by daedalus (T-04798).
type monitorTerminalLine struct {
	Type   string                `json:"type"`
	Result monitorTerminalResult `json:"result"`
	Reason monitorTerminalReason `json:"reason"`
	Unmet  []string              `json:"unmet"`
}

// monitorEventLine is the per-event record emitted for each relevant event. The
// data fields come from the server's WrkqMonitorEvent; the `type` discriminator is
// stamped here (client-owned), matching legacy monitorEventLine byte-for-byte.
type monitorEventLine struct {
	Type         string  `json:"type"`
	ID           int64   `json:"id"`
	Timestamp    string  `json:"timestamp"`
	ResourceType string  `json:"resource_type"`
	ResourceUUID *string `json:"resource_uuid,omitempty"`
	ResourceID   *string `json:"resource_id,omitempty"`
	EventType    string  `json:"event_type"`
	Payload      *string `json:"payload,omitempty"`
}

// serverMonitorEvent decodes one WrkqMonitorEvent row from the server projection.
type serverMonitorEvent struct {
	ID           int64   `json:"id"`
	Timestamp    string  `json:"timestamp"`
	ResourceType string  `json:"resource_type"`
	ResourceUUID *string `json:"resource_uuid"`
	ResourceID   *string `json:"resource_id"`
	EventType    string  `json:"event_type"`
	Payload      *string `json:"payload"`
}

func buildMonitorTerminalLine(result monitorTerminalResult, unmet []string) monitorTerminalLine {
	var reason monitorTerminalReason
	switch result {
	case monitorResultMet:
		reason = monitorReasonConditionMet
	case monitorResultTimeout:
		reason = monitorReasonTimedOut
	case monitorResultStall:
		reason = monitorReasonStalled
	default:
		reason = monitorReasonStreamError
	}
	if unmet == nil {
		unmet = []string{}
	}
	return monitorTerminalLine{Type: "wrkq.monitor.terminal", Result: result, Reason: reason, Unmet: unmet}
}

// ──── commands ─────────────────────────────────────────────────────────────────

func newMonitorCmd() *cobra.Command {
	monitor := &cobra.Command{
		Use:   "monitor",
		Short: "Observe task state and events (Monitor-tool surface)",
		Long: `Stream and query task events for agent observation via the Claude Monitor tool.

Subcommands:
  watch [TASK...]   Stream task events (Monitor feed)
  wait  [TASK...]   Block until condition is met, then exit

Examples:
  wrkq monitor watch T-04466 --state-only --until state=completed --timeout 30m
  wrkq monitor wait T-1 T-2 T-3 --until all-terminal --stall-after 30m
  wrkq monitor wait EN-00012 --until terminal --timeout 10m

Exit codes: 0=condition met, or a bounded follow with no --until ended on its
--timeout/--stall-after clock; 1=--until left unmet by timeout/stall;
2=selector error; 3=stream error.
`,
	}
	monitor.AddCommand(newMonitorWatchCmd())
	monitor.AddCommand(newMonitorWaitCmd())
	return monitor
}

func newMonitorWatchCmd() *cobra.Command {
	var until, timeoutStr, stallAfterStr, format, scope, eventType string
	var stateOnly, raw, ndjson bool
	var since, last int64
	cmd := &cobra.Command{
		Use:   "watch [TASK...]",
		Short: "Stream task events (Monitor feed)",
		Long: `Stream typed NDJSON events for watched tasks. Emits per-event lines and
exactly one terminal line before exit. With no --until, follows until --timeout
or --stall-after expires, if either is given, and otherwise indefinitely.

The feed starts at the current high-water mark: it tails what happens NEXT. Use
--since <id> to start from an earlier event (--since 0 replays the whole log) or
--last N to replay the last N events before following.

Selectors: T-XXXXX friendly IDs or container paths (e.g. inbox/my-task), plus
R-XXXXX rooms and EN-XXXXX envelopes. A task selector also carries that task's
room, so state changes and the conversation stream on ONE selector; --state-only
still emits only task lifecycle changes. An EN- selector that is a fan-out group
head covers every envelope of that group.
Invalid selectors fail with exit code 2 before any streaming.
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if until != "" {
				if err := validateMonitorCondition(until); err != nil {
					return monitorUsageError(cmd.ErrOrStderr(), err)
				}
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			// --raw delegates to the whole-log unfiltered tail (history.tailView),
			// matching legacy runMonitorWatch's raw branch: NDJSON, follow=true.
			if raw {
				return watchTailLoop(cmd.Context(), tr, out, since, true, true)
			}
			// Legacy returns the --format error directly (exit 2) WITHOUT a caller-side
			// stderr line, so only main's "Error:" line appears (single-print).
			if format != "" && format != "ndjson" && format != "compact" {
				return monitorUsageErrorQuiet(errOut, fmt.Errorf("invalid --format %q: choose ndjson or compact", format))
			}

			// Scope each selector through the project-root scoper (legacy semantics);
			// the server resolves the scoped selector and owns the validation error.
			scoped := make([]string, 0, len(args))
			for _, arg := range args {
				scoped = append(scoped, sc.selector(arg, false))
			}
			if len(args) == 0 && until != "" {
				return monitorUsageError(errOut, errors.New("monitor watch --until requires at least one selector"))
			}
			timeout, err := parseMonitorDuration(timeoutStr, 0)
			if err != nil {
				return monitorUsageError(errOut, err)
			}
			stallAfter, err := parseMonitorDuration(stallAfterStr, 0)
			if err != nil {
				return monitorUsageError(errOut, err)
			}
			// Legacy returns the --scope error directly (exit 2) WITHOUT a caller-side
			// stderr line (single-print).
			if scope != "" {
				return monitorUsageErrorQuiet(errOut, errors.New("--scope is not implemented yet"))
			}
			principalRef, err := actorFlag(cmd)
			if err != nil {
				return err
			}

			// --last: replay the last N events. Resolve the start cursor server-side
			// from actual row identity (COALESCE(MIN(id),0)-1 over the last N existing
			// event_log rows) via eventsView lastN — gap-independent, not high_water-N.
			startCursor := since
			// With neither --since nor --last, the feed starts at the current
			// high-water: `watch` is a tail of what happens NEXT, not a replay of
			// the whole event log (T-07620). --since 0 stays an explicit "from the
			// beginning", which is why this tests Changed rather than the value.
			fromHighWater := !cmd.Flags().Changed("since")
			if last > 0 {
				c, err := cursorForLastEvents(cmd.Context(), tr, last)
				if err != nil {
					return monitorStreamError(errOut, out, err)
				}
				startCursor = c
				fromHighWater = false
			}

			return monitorStreamUntil(cmd.Context(), tr, out, errOut, monitorStreamOpts{
				scopedTasks:   scoped,
				stateOnly:     stateOnly,
				eventTypes:    splitMonitorComma(eventType),
				condition:     until,
				timeout:       timeout,
				stallAfter:    stallAfter,
				startCursor:   startCursor,
				fromHighWater: fromHighWater,
				principalRef:  principalRef,
				scopeRef:      wrkcScopeRef(cmd),
			})
		},
	}
	cmd.Flags().StringVar(&until, "until", "", "Condition: state=<s>[,<s>...], all-terminal, acked, or terminal")
	cmd.Flags().StringVar(&timeoutStr, "timeout", "", "Maximum follow duration (e.g. 30m); bounds the follow with or without --until")
	cmd.Flags().StringVar(&stallAfterStr, "stall-after", "", "Exit after this duration with no new events; applies with or without --until")
	cmd.Flags().BoolVar(&stateOnly, "state-only", false, "Only emit lifecycle state-change events")
	cmd.Flags().BoolVar(&raw, "raw", false, "Raw wrkq watch behavior (whole-log unfiltered tail)")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as NDJSON")
	cmd.Flags().Int64Var(&since, "since", 0, "Start from event ID (overrides high-water default)")
	cmd.Flags().Int64Var(&last, "last", 0, "Replay last N events before following")
	cmd.Flags().StringVar(&format, "format", "", "Output format: ndjson or compact")
	cmd.Flags().StringVar(&scope, "scope", "", "Filter by scope ref")
	cmd.Flags().StringVar(&eventType, "event-type", "", "Comma-separated event_type filter")
	return cmd
}

func newMonitorWaitCmd() *cobra.Command {
	var until, timeoutStr, stallAfterStr string
	cmd := &cobra.Command{
		Use:   "wait [TASK...]",
		Short: "Block until condition is met, then exit",
		Long: `Block until the --until condition is satisfied for watched tasks, then exit.
Shares the exact condition evaluator and exit-code contract with monitor watch --until.

Useful for scripted sequencing: wrkq monitor wait T-00001 --until state=completed
An envelope group waits with: wrkq monitor wait EN-00012 --until terminal
`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(until) == "" {
				return monitorUsageError(cmd.ErrOrStderr(), errors.New("monitor wait requires --until"))
			}
			if err := validateMonitorCondition(until); err != nil {
				return monitorUsageError(cmd.ErrOrStderr(), err)
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()

			out := cmd.OutOrStdout()
			errOut := cmd.ErrOrStderr()

			scoped := make([]string, 0, len(args))
			for _, arg := range args {
				scoped = append(scoped, sc.selector(arg, false))
			}
			if len(args) == 0 {
				return monitorUsageError(errOut, errors.New("monitor wait requires at least one selector"))
			}
			timeout, err := parseMonitorDuration(timeoutStr, 0)
			if err != nil {
				return monitorUsageError(errOut, err)
			}
			stallAfter, err := parseMonitorDuration(stallAfterStr, 0)
			if err != nil {
				return monitorUsageError(errOut, err)
			}
			principalRef, err := actorFlag(cmd)
			if err != nil {
				return err
			}

			result, unmet, exitCode, err := monitorWaitLoop(cmd.Context(), tr, monitorStreamOpts{
				scopedTasks:  scoped,
				condition:    until,
				timeout:      timeout,
				stallAfter:   stallAfter,
				principalRef: principalRef,
				scopeRef:     wrkcScopeRef(cmd),
			})
			if err != nil {
				// Selector / condition validation error → exit 2; stream error → exit 3.
				code := 2
				if exitCode != 0 {
					code = exitCode
				}
				fmt.Fprintln(errOut, err)
				monitorExit(errOut, code, err)
				return nil
			}
			encoder := json.NewEncoder(out)
			if encErr := encoder.Encode(buildMonitorTerminalLine(result, unmet)); encErr != nil {
				return monitorStreamErrorExit(errOut, encErr)
			}
			if exitCode != 0 {
				monitorExit(errOut, exitCode, fmt.Errorf("monitor wait ended: %s", result))
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&until, "until", "", "Condition: state=<s>[,<s>...], all-terminal, acked, or terminal")
	cmd.Flags().StringVar(&timeoutStr, "timeout", "", "Maximum wait duration (e.g. 30m)")
	cmd.Flags().StringVar(&stallAfterStr, "stall-after", "", "Exit after this duration with no new events")
	return cmd
}

// ──── stream / wait loops (client-owned) ──────────────────────────────────────

type monitorStreamOpts struct {
	scopedTasks []string
	stateOnly   bool
	eventTypes  []string
	condition   string
	timeout     time.Duration
	stallAfter  time.Duration
	startCursor int64
	// fromHighWater starts the feed at the current MAX(event_log.id) instead of
	// startCursor: the no-flag `monitor watch` default (T-07620). A --until
	// stream always starts there regardless.
	fromHighWater bool
	principalRef  string
	scopeRef      string
}

// monitorStreamUntil drives `monitor watch`: it runs the follow loop and owns the
// stdout terminal line, the stderr lines, and the exit code. The loop itself lives
// in monitorFollowLoop, which RETURNS its outcome instead of exiting, so both
// clocks are reachable from tests.
//
// Exit contract (T-07621, mable ruling): --timeout / --stall-after bound ANY
// follow, with or without --until. Expiry emits the existing terminal line
// (result: timeout|stall). The exit code is 1 only when a --until condition was
// given and is unmet; a bounded follow with no --until ended exactly as asked, so
// it exits 0 with unmet: [].
func monitorStreamUntil(ctx context.Context, tr Transport, out io.Writer, errOut io.Writer, opts monitorStreamOpts) error {
	encoder := json.NewEncoder(out)

	result, unmet, exitCode, err := monitorFollowLoop(ctx, tr, encoder, opts)
	if err != nil {
		if exitCode == 2 {
			return monitorUsageError(errOut, err)
		}
		_ = encoder.Encode(buildMonitorTerminalLine(monitorResultError, nil))
		return monitorStreamErrorExit(errOut, err)
	}
	if encErr := encoder.Encode(buildMonitorTerminalLine(result, unmet)); encErr != nil {
		return monitorStreamErrorExit(errOut, encErr)
	}
	if exitCode != 0 {
		monitorExit(errOut, exitCode, monitorUnmetError(result, opts.condition))
	}
	return nil
}

// monitorUnmetError is the stderr message for a --until stream that ended on a
// clock instead of on its condition.
func monitorUnmetError(result monitorTerminalResult, condition string) error {
	if result == monitorResultStall {
		return fmt.Errorf("monitor stalled waiting for %s", condition)
	}
	return fmt.Errorf("monitor timeout waiting for %s", condition)
}

// monitorClockExitCode maps an expired clock to its exit code: 1 when a --until
// condition is unmet, 0 when the caller only asked for a bounded follow.
func monitorClockExitCode(condition string) int {
	if condition != "" {
		return 1
	}
	return 0
}

// monitorFollowLoop is the event-streaming poll loop for `monitor watch`. It polls
// wrkq.monitor.eventsView for the next bounded ASCENDING page, advances the
// monotonic high-water cursor, emits per-event lines, and (when a --until condition
// is set) re-evaluates wrkq.monitor.stateView each cycle. It returns
// (result, unmet, exitCode, err) and NEVER writes the terminal line or exits;
// monitorStreamUntil owns both. eventsView + stateView are TWO snapshots, so the
// terminal evaluation is intentionally race-tolerant (a met condition may land
// before the matching event is paged in; tests assert race-tolerantly).
func monitorFollowLoop(ctx context.Context, tr Transport, encoder *json.Encoder, opts monitorStreamOpts) (monitorTerminalResult, []string, int, error) {
	// Eager selector validation BEFORE any streaming, matching legacy
	// resolveMonitorSelectors: the server resolves the scoped selectors and a bad
	// one returns WRKQ_VALIDATION (exit 2). Legacy prints the raw error caller-side
	// THEN main adds "Error:" (double-print), and fails before the feed starts. A
	// probe eventsView at the current high-water surfaces that resolution error
	// without emitting any events.
	if len(opts.scopedTasks) > 0 {
		if _, _, perr := monitorPollEvents(ctx, tr, opts, int64(1)<<62); perr != nil {
			return monitorResultError, nil, monitorErrExitCode(perr), monitorStripError(perr)
		}
	}

	// Initial authoritative snapshot: quick exit if already met.
	if opts.condition != "" {
		met, unmet, code, err := monitorConditionSnapshot(ctx, tr, opts)
		if err != nil {
			return monitorResultError, nil, code, err
		}
		if met {
			return monitorResultMet, unmet, 0, nil
		}
	}

	cursor, err := monitorInitialCursor(ctx, tr, opts)
	if err != nil {
		return monitorResultError, nil, 3, err
	}

	started := time.Now()
	lastEvent := started
	for {
		events, nextCursor, err := monitorPollEvents(ctx, tr, opts, cursor)
		if err != nil {
			return monitorResultError, nil, 3, monitorStripError(err)
		}
		cursor = nextCursor
		for _, e := range events {
			if encErr := encoder.Encode(monitorEventLine{
				Type:         "wrkq.monitor.event",
				ID:           e.ID,
				Timestamp:    e.Timestamp,
				ResourceType: e.ResourceType,
				ResourceUUID: e.ResourceUUID,
				ResourceID:   e.ResourceID,
				EventType:    e.EventType,
				Payload:      e.Payload,
			}); encErr != nil {
				return monitorResultError, nil, 3, encErr
			}
			lastEvent = time.Now()
		}

		// unmet is the condition's snapshot when there is one; a plain follow has
		// nothing to be unmet, so its terminal line carries unmet: [].
		unmet := []string{}
		if opts.condition != "" {
			met, snapshotUnmet, code, err := monitorConditionSnapshot(ctx, tr, opts)
			if err != nil {
				return monitorResultError, nil, code, err
			}
			if met {
				return monitorResultMet, snapshotUnmet, 0, nil
			}
			unmet = snapshotUnmet
		}
		// Both clocks bound ANY follow, conditional or not (T-07621).
		if opts.timeout > 0 && time.Since(started) >= opts.timeout {
			return monitorResultTimeout, unmet, monitorClockExitCode(opts.condition), nil
		}
		if opts.stallAfter > 0 && time.Since(lastEvent) >= opts.stallAfter {
			return monitorResultStall, unmet, monitorClockExitCode(opts.condition), nil
		}
		time.Sleep(monitorPollInterval)
	}
}

// monitorWaitLoop drives `monitor wait`: re-evaluates the single stateView snapshot
// each cycle until met, timeout, or stall. Returns (result, unmet, exitCode, err).
func monitorWaitLoop(ctx context.Context, tr Transport, opts monitorStreamOpts) (monitorTerminalResult, []string, int, error) {
	started := time.Now()
	lastProgress := started
	for {
		met, unmet, code, err := monitorConditionSnapshot(ctx, tr, opts)
		if err != nil {
			return monitorResultError, nil, code, err
		}
		if met {
			return monitorResultMet, []string{}, 0, nil
		}
		if opts.timeout > 0 && time.Since(started) >= opts.timeout {
			return monitorResultTimeout, unmet, 1, nil
		}
		if opts.stallAfter > 0 && time.Since(lastProgress) >= opts.stallAfter {
			return monitorResultStall, unmet, 1, nil
		}
		time.Sleep(monitorPollInterval)
	}
}

// ──── RPC helpers ──────────────────────────────────────────────────────────────

// monitorConditionSnapshot calls wrkq.monitor.stateView once. Returns
// (met, unmet, exitCode, err): a validation error → code 2, a stream error → 3.
func monitorConditionSnapshot(ctx context.Context, tr Transport, opts monitorStreamOpts) (bool, []string, int, error) {
	raw, err := tr.Call(ctx, "wrkq.monitor.stateView", map[string]any{
		"tasks":        opts.scopedTasks,
		"condition":    opts.condition,
		"principalRef": opts.principalRef,
		"scopeRef":     opts.scopeRef,
	})
	if err != nil {
		return false, nil, monitorErrExitCode(err), monitorStripError(err)
	}
	var res struct {
		Met   bool     `json:"met"`
		Unmet []string `json:"unmet"`
	}
	if jerr := json.Unmarshal(raw, &res); jerr != nil {
		return false, nil, 3, jerr
	}
	return res.Met, res.Unmet, 0, nil
}

// monitorPollEvents calls wrkq.monitor.eventsView for the next bounded page and
// returns the decoded events + the new high-water cursor.
func monitorPollEvents(ctx context.Context, tr Transport, opts monitorStreamOpts, cursor int64) ([]serverMonitorEvent, int64, error) {
	params := map[string]any{"cursor": cursor}
	if len(opts.scopedTasks) > 0 {
		params["tasks"] = opts.scopedTasks
	}
	if opts.stateOnly {
		params["stateOnly"] = true
	}
	if len(opts.eventTypes) > 0 {
		params["eventTypes"] = opts.eventTypes
	}
	raw, err := tr.Call(ctx, "wrkq.monitor.eventsView", params)
	if err != nil {
		return nil, cursor, err
	}
	var res struct {
		Items     []serverMonitorEvent `json:"items"`
		HighWater int64                `json:"high_water"`
	}
	if jerr := json.Unmarshal(raw, &res); jerr != nil {
		return nil, cursor, jerr
	}
	return res.Items, res.HighWater, nil
}

// monitorInitialCursor resolves the event id the stream starts AFTER. A --until
// flow starts at the current high-water so it only emits NEW events; so does an
// unconditional follow that named neither --since nor --last (T-07620: the
// no-flag default is a tail, not a replay of the whole event log). An explicit
// --since/--last starts at the cursor the caller asked for.
func monitorInitialCursor(ctx context.Context, tr Transport, opts monitorStreamOpts) (int64, error) {
	if opts.condition != "" || opts.fromHighWater {
		return monitorHighWater(ctx, tr)
	}
	return opts.startCursor, nil
}

// monitorHighWater returns the current MAX(event_log.id) by reading a zero-cursor
// tail page's high_water (the tail returns the last scanned id). An empty log
// yields 0. Used to start a --until stream from "now" so only NEW events emit.
func monitorHighWater(ctx context.Context, tr Transport) (int64, error) {
	// A bounded page from cursor 0 advances high_water to the last scanned id; for
	// the high-water "start from now" we want the absolute MAX. Page forward until a
	// short page (fewer than the cap) is returned.
	cursor := int64(0)
	for {
		raw, err := tr.Call(ctx, "wrkq.history.tailView", map[string]any{"cursor": cursor})
		if err != nil {
			return 0, monitorStripError(err)
		}
		var res struct {
			Items     []json.RawMessage `json:"items"`
			HighWater int64             `json:"high_water"`
		}
		if jerr := json.Unmarshal(raw, &res); jerr != nil {
			return 0, jerr
		}
		if res.HighWater <= cursor {
			return res.HighWater, nil
		}
		cursor = res.HighWater
		if len(res.Items) == 0 {
			return cursor, nil
		}
	}
}

// cursorForLastEvents resolves the start cursor for `--last N` by asking the
// SERVER to compute it from actual row identity — the legacy predicate
// COALESCE(MIN(id),0)-1 over the last N EXISTING event_log rows. This is
// gap-independent; the previous high_water-N arithmetic under-replayed when the
// last N rows spanned an event_log id gap (daedalus #10262, T-05115). The server
// returns the cursor as high_water (empty page); the stream then replays forward
// from it applying selectors/filters, exactly as legacy does.
func cursorForLastEvents(ctx context.Context, tr Transport, n int64) (int64, error) {
	raw, err := tr.Call(ctx, "wrkq.monitor.eventsView", map[string]any{"lastN": n})
	if err != nil {
		return 0, monitorStripError(err)
	}
	var res struct {
		HighWater int64 `json:"high_water"`
	}
	if jerr := json.Unmarshal(raw, &res); jerr != nil {
		return 0, jerr
	}
	return res.HighWater, nil
}

// ──── error / exit helpers ─────────────────────────────────────────────────────

// monitorStripError unwraps an RPC *Error to its raw message (the server already
// includes any legacy wrapping; the domain-code prefix is stripped). Non-RPC errors
// pass through unchanged.
func monitorStripError(err error) error {
	if re, ok := err.(*Error); ok {
		return errors.New(re.Message)
	}
	return err
}

// monitorErrExitCode maps an RPC error's domain code to the legacy exit code: a
// validation error (selector/condition) → 2, anything else → 3.
func monitorErrExitCode(err error) int {
	if re, ok := err.(*Error); ok {
		if re.DomainID == "WRKQ_VALIDATION" {
			return 2
		}
	}
	return 3
}

// monitorUsageError reproduces legacy's selector/usage failure stderr: the raw
// error line, then main's "Error: <err>" line, then exit 2. Returns nil so the
// mirror's main does not add a second "Error:" line. Used for the selector /
// --until-no-selector / duration / missing-until paths that legacy prints
// caller-side BEFORE returning the exit error (double-print).
func monitorUsageError(errOut io.Writer, err error) error {
	fmt.Fprintln(errOut, err)
	monitorExit(errOut, 2, err)
	return nil
}

// monitorUsageErrorQuiet reproduces legacy's --format / --scope failure: the error
// is returned directly (exit 2) with NO caller-side stderr line, so ONLY main's
// "Error: <err>" line appears (single-print).
func monitorUsageErrorQuiet(errOut io.Writer, err error) error {
	monitorExit(errOut, 2, err)
	return nil
}

// monitorStreamError reproduces legacy's stream-error path for the pre-loop --last
// cursor failure: exit 3 with main's "Error:" line. The terminal line is emitted by
// the caller when it owns the stdout encoder; here (pre-loop) no terminal is due.
func monitorStreamError(errOut io.Writer, _ io.Writer, err error) error {
	monitorExit(errOut, 3, err)
	return nil
}

// monitorStreamErrorExit emits exit 3 with main's "Error:" line (the terminal line
// is already written by the caller).
func monitorStreamErrorExit(errOut io.Writer, err error) error {
	monitorExit(errOut, 3, err)
	return nil
}

// monitorExit reproduces the legacy `cmd/wrkq` main behavior for a cliExitError that
// was NOT pre-reported: print "Error: <err>" to stderr and os.Exit(code). The mirror
// command returns nil after calling this so the mirror main never adds its own line.
func monitorExit(errOut io.Writer, code int, err error) {
	if f, ok := errOut.(*os.File); ok {
		fmt.Fprintf(f, "Error: %v\n", err)
	} else {
		fmt.Fprintf(errOut, "Error: %v\n", err)
	}
	os.Exit(code)
}

// ──── small helpers (mirror legacy internal/cli) ───────────────────────────────

func parseMonitorDuration(raw string, def time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	return value, nil
}

func validateMonitorCondition(raw string) error {
	raw = strings.TrimSpace(raw)
	switch raw {
	case "all-terminal":
		return nil
	// Envelope conditions (T-07612 rev 5.1). terminal = acked|failed, so a
	// failed obligation releases a waiter instead of hanging it. The
	// SERVER owns the selector/condition agreement check.
	case "acked", "terminal":
		return nil
	}
	if !strings.HasPrefix(raw, "state=") {
		return fmt.Errorf("invalid --until condition %q: expected state=<s>[,<s>...], all-terminal, acked, or terminal", raw)
	}

	stateList := strings.TrimPrefix(raw, "state=")
	if stateList == "" {
		return fmt.Errorf("invalid --until condition %q: state list must include at least one task state", raw)
	}
	states := strings.Split(stateList, ",")
	for _, state := range states {
		if state == "" {
			return fmt.Errorf("invalid --until condition %q: state list contains an empty entry", raw)
		}
		if _, err := domain.ParseState(state); err != nil {
			return fmt.Errorf("invalid --until condition %q: unknown task state %q", raw, state)
		}
	}
	return nil
}

func splitMonitorComma(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
