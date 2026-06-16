package cli

// monitor.go — stub surface for T-04798 Phase 1 (RED).
//
// Provides the minimal cobra command group and function signatures so that
// monitor_test.go compiles and runs. All RunE handlers and evaluator functions
// return errMonitorUnimplemented so tests assert the real contract and FAIL.
//
// Phase 2 (GREEN) will replace these stubs with real implementations.

import (
	"errors"
	"io"
	"time"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

// errMonitorUnimplemented is the sentinel returned by all monitor stubs until
// T-04798 Phase 2 (GREEN) is complete.
var errMonitorUnimplemented = errors.New("monitor: not implemented (T-04798 Phase 2)")

// ──── Typed NDJSON schemas (daedalus-approved, T-04798) ──────────────────────

// monitorTerminalResult is the typed result enum for the terminal stdout line.
type monitorTerminalResult string

const (
	monitorResultMet     monitorTerminalResult = "met"
	monitorResultTimeout monitorTerminalResult = "timeout"
	monitorResultStall   monitorTerminalResult = "stall"
	monitorResultError   monitorTerminalResult = "error"
)

// monitorTerminalReason is the typed reason enum for the terminal stdout line.
type monitorTerminalReason string

const (
	monitorReasonConditionMet monitorTerminalReason = "condition_met"
	monitorReasonTimedOut     monitorTerminalReason = "timed_out"
	monitorReasonStalled      monitorTerminalReason = "stalled"
	monitorReasonStreamError  monitorTerminalReason = "stream_error"
)

// monitorTerminalLine is the terminal record emitted exactly once as the last
// stdout line before process exit. Schema locked by daedalus (T-04798).
type monitorTerminalLine struct {
	Type   string                `json:"type"` // "wrkq.monitor.terminal"
	Result monitorTerminalResult `json:"result"`
	Reason monitorTerminalReason `json:"reason"`
	Unmet  []string              `json:"unmet"` // task IDs that never satisfied condition
}

// monitorEventLine is the per-event record emitted for each relevant event.
type monitorEventLine struct {
	Type         string  `json:"type"` // "wrkq.monitor.event"
	ID           int64   `json:"id"`
	Timestamp    string  `json:"timestamp"`
	ResourceType string  `json:"resource_type"`
	ResourceUUID *string `json:"resource_uuid,omitempty"`
	ResourceID   *string `json:"resource_id,omitempty"`
	EventType    string  `json:"event_type"`
	Payload      *string `json:"payload,omitempty"`
}

// buildTerminalLine constructs the typed terminal NDJSON record. Called exactly
// once by monitorStreamUntil before return. Maps result enum to the appropriate
// reason enum value.
func buildTerminalLine(result monitorTerminalResult, unmet []string) monitorTerminalLine {
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
	return monitorTerminalLine{
		Type:   "wrkq.monitor.terminal",
		Result: result,
		Reason: reason,
		Unmet:  unmet,
	}
}

// ──── Condition evaluator (shared by watch --until and wait) ─────────────────

// evaluateUntilOpts holds options for the shared condition evaluator.
type evaluateUntilOpts struct {
	TaskUUIDs  []string
	Condition  string
	Timeout    time.Duration
	StallAfter time.Duration
}

// evaluateUntilResult holds the result of condition evaluation.
type evaluateUntilResult struct {
	Result   monitorTerminalResult
	Reason   monitorTerminalReason
	Unmet    []string // task IDs not yet satisfying the condition
	ExitCode int      // 0=met 1=timeout/stall 2=usage/selector 3=stream-error
}

// evaluateUntil evaluates a --until condition against current tasks, polling
// until satisfied, timeout, or stall. Shared by monitor watch --until and
// monitor wait. Emits exactly one monitorTerminalLine to stdout before return.
//
// This stub always returns errMonitorUnimplemented (T-04798 Phase 2).
func evaluateUntil(database *db.DB, opts evaluateUntilOpts) (*evaluateUntilResult, error) {
	return nil, errMonitorUnimplemented
}

// ──── Event filter ────────────────────────────────────────────────────────────

// monitorEventFilter holds server-side filtering options for the event stream.
type monitorEventFilter struct {
	TaskUUIDs       []string // watched task UUIDs
	TaskFriendlyIDs []string // watched task friendly IDs (e.g. T-00001)
	StateOnly       bool     // only lifecycle state-change events
	EventTypes      []string // explicit event_type filter (empty = all applicable)
}

// isEventIncluded reports whether a raw event_log row should be included given
// the filter. Handles:
//   - task.* events matched by resource_uuid ∈ filter.TaskUUIDs
//   - comment events matched by payload.task_id ∈ filter.TaskUUIDs ∪ filter.TaskFriendlyIDs
//
// If filter.StateOnly is true, also applies isStateChangeEvent.
//
// This stub always returns false (T-04798 Phase 2).
func isEventIncluded(event watchEvent, filter monitorEventFilter) bool {
	return false
}

// isStateChangeEvent reports whether an event is a lifecycle state change:
//   - task.updated with the "state" key present in the payload
//   - task.archived, task.deleted, task.restored (always lifecycle)
//
// Explicitly excludes title/body/priority/comment-only events.
//
// This stub always returns false (T-04798 Phase 2).
func isStateChangeEvent(event watchEvent) bool {
	return false
}

// ──── Polling cursor ──────────────────────────────────────────────────────────

// pollMonitorEvents queries event_log WHERE e.id > cursorID matching filter.
// Returns (matched events as typed lines, new high-water cursor, error).
// The cursor is monotonically advancing; callers must not pass a smaller cursor
// on subsequent calls to avoid duplicate emissions.
//
// This stub always returns errMonitorUnimplemented (T-04798 Phase 2).
func pollMonitorEvents(database *db.DB, cursorID int64, filter monitorEventFilter) ([]monitorEventLine, int64, error) {
	return nil, cursorID, errMonitorUnimplemented
}

// ──── Selector resolution ─────────────────────────────────────────────────────

// resolveMonitorSelectors resolves task selector strings (T-XXXXX / paths) to
// (uuids, friendlyIDs). Returns exitError(2, ...) for any invalid selector so
// callers can fail fast before streaming begins.
//
// This stub always returns errMonitorUnimplemented (T-04798 Phase 2).
func resolveMonitorSelectors(database *db.DB, selectors []string) (uuids []string, friendlyIDs []string, err error) {
	return nil, nil, errMonitorUnimplemented
}

// ──── Streaming driver ────────────────────────────────────────────────────────

// monitorStreamUntil drives the event-streaming poll loop. It:
//  1. Evaluates the initial authoritative snapshot (quick exit if already met).
//  2. Advances a monotonic event-id cursor each poll cycle.
//  3. Emits typed monitorEventLine records per relevant event.
//  4. Emits exactly one monitorTerminalLine before return.
//
// Human diagnostics go to stderr; stdout is clean NDJSON.
//
// This stub always returns errMonitorUnimplemented (T-04798 Phase 2).
func monitorStreamUntil(database *db.DB, stdout io.Writer, filter monitorEventFilter, opts evaluateUntilOpts) error {
	// Poll for events — stub returns errMonitorUnimplemented.
	_, _, err := pollMonitorEvents(database, 0, filter)
	if err != nil {
		// Phase 2: will write terminal line to stdout before returning.
		terminal := buildTerminalLine(monitorResultError, nil)
		_ = stdout
		_ = terminal
		_ = opts
		return err
	}
	return errMonitorUnimplemented
}

// ──── Flags ───────────────────────────────────────────────────────────────────

var (
	monitorWatchUntil      string
	monitorWatchTimeout    string
	monitorWatchStallAfter string
	monitorWatchStateOnly  bool
	monitorWatchRaw        bool
	monitorWatchNDJSON     bool
	monitorWatchSince      int64
	monitorWatchLast       int64

	monitorWaitUntil      string
	monitorWaitTimeout    string
	monitorWaitStallAfter string
)

// ──── Commands ────────────────────────────────────────────────────────────────

var monitorCmd = &cobra.Command{
	Use:   "monitor",
	Short: "Observe task state and events (Monitor-tool surface)",
	Long: `Stream and query task events for agent observation via the Claude Monitor tool.

Subcommands:
  watch [TASK...]   Stream task events (Monitor feed)
  wait  [TASK...]   Block until condition is met, then exit

Examples:
  wrkq monitor watch T-04466 --state-only --until state=completed --timeout 30m
  wrkq monitor wait T-1 T-2 T-3 --until all-terminal --stall-after 30m

Exit codes: 0=condition met, 1=timeout/stall, 2=selector error, 3=stream error.
`,
}

var monitorWatchCmd = &cobra.Command{
	Use:   "watch [TASK...]",
	Short: "Stream task events (Monitor feed)",
	Long: `Stream typed NDJSON events for watched tasks. Emits per-event lines and
exactly one terminal line before exit. With no --until, follows indefinitely.

Task selectors: T-XXXXX friendly IDs or container paths (e.g. inbox/my-task).
Invalid selectors fail with exit code 2 before any streaming.
`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runMonitorWatch),
}

var monitorWaitCmd = &cobra.Command{
	Use:   "wait [TASK...]",
	Short: "Block until condition is met, then exit",
	Long: `Block until the --until condition is satisfied for watched tasks, then exit.
Shares the exact condition evaluator and exit-code contract with monitor watch --until.

Useful for scripted sequencing: wrkq monitor wait T-00001 --until state=completed
`,
	RunE: appctx.WithApp(appctx.DefaultOptions(), runMonitorWait),
}

func runMonitorWatch(app *appctx.App, cmd *cobra.Command, args []string) error {
	return monitorStreamUntil(app.DB, cmd.OutOrStdout(), monitorEventFilter{}, evaluateUntilOpts{})
}

func runMonitorWait(_ *appctx.App, _ *cobra.Command, _ []string) error {
	return errMonitorUnimplemented
}

func init() {
	rootCmd.AddCommand(monitorCmd)
	monitorCmd.AddCommand(monitorWatchCmd)
	monitorCmd.AddCommand(monitorWaitCmd)

	// watch flags
	monitorWatchCmd.Flags().StringVar(&monitorWatchUntil, "until", "", "Condition: state=<s>[,<s>...] or all-terminal")
	monitorWatchCmd.Flags().StringVar(&monitorWatchTimeout, "timeout", "", "Maximum wait duration (e.g. 30m)")
	monitorWatchCmd.Flags().StringVar(&monitorWatchStallAfter, "stall-after", "", "Exit after this duration with no new events")
	monitorWatchCmd.Flags().BoolVar(&monitorWatchStateOnly, "state-only", false, "Only emit lifecycle state-change events")
	monitorWatchCmd.Flags().BoolVar(&monitorWatchRaw, "raw", false, "Raw wrkq watch behavior (whole-log unfiltered tail)")
	monitorWatchCmd.Flags().BoolVar(&monitorWatchNDJSON, "ndjson", false, "Output as NDJSON")
	monitorWatchCmd.Flags().Int64Var(&monitorWatchSince, "since", 0, "Start from event ID (overrides high-water default)")
	monitorWatchCmd.Flags().Int64Var(&monitorWatchLast, "last", 0, "Replay last N events before following")

	// wait flags
	monitorWaitCmd.Flags().StringVar(&monitorWaitUntil, "until", "", "Condition: state=<s>[,<s>...] or all-terminal")
	monitorWaitCmd.Flags().StringVar(&monitorWaitTimeout, "timeout", "", "Maximum wait duration (e.g. 30m)")
	monitorWaitCmd.Flags().StringVar(&monitorWaitStallAfter, "stall-after", "", "Exit after this duration with no new events")
}
