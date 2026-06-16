package cli

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

const monitorPollInterval = 100 * time.Millisecond

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

type monitorCondition struct {
	kind   string
	states map[string]bool
}

func parseMonitorCondition(raw string) (monitorCondition, error) {
	raw = strings.TrimSpace(raw)
	switch {
	case raw == "":
		return monitorCondition{}, fmt.Errorf("--until condition is required")
	case raw == "all-terminal":
		return monitorCondition{kind: "all-terminal"}, nil
	case strings.HasPrefix(raw, "state="):
		states := strings.Split(strings.TrimPrefix(raw, "state="), ",")
		allowed := make(map[string]bool, len(states))
		for _, state := range states {
			state = strings.TrimSpace(state)
			if state == "" {
				continue
			}
			allowed[state] = true
		}
		if len(allowed) == 0 {
			return monitorCondition{}, fmt.Errorf("state= condition must include at least one state")
		}
		return monitorCondition{kind: "state", states: allowed}, nil
	default:
		return monitorCondition{}, fmt.Errorf("unsupported --until condition %q", raw)
	}
}

func (c monitorCondition) evaluate(database *db.DB, taskUUIDs []string) (bool, []string, error) {
	rows, err := database.Query(`
		SELECT id, state
		FROM tasks
		WHERE uuid IN (`+questionMarks(len(taskUUIDs))+`)
		ORDER BY id
	`, stringsToInterfaces(taskUUIDs)...)
	if err != nil {
		return false, nil, fmt.Errorf("query task state: %w", err)
	}
	defer func() { _ = rows.Close() }()

	unmet := []string{}
	seen := 0
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return false, nil, fmt.Errorf("scan task state: %w", err)
		}
		seen++
		if !c.satisfiedBy(state) {
			unmet = append(unmet, id)
		}
	}
	if err := rows.Err(); err != nil {
		return false, nil, fmt.Errorf("iterate task state: %w", err)
	}
	if seen != len(taskUUIDs) {
		return false, unmet, fmt.Errorf("one or more watched tasks no longer exist")
	}
	return len(unmet) == 0, unmet, nil
}

func (c monitorCondition) satisfiedBy(state string) bool {
	switch c.kind {
	case "all-terminal":
		switch state {
		case "completed", "cancelled", "archived", "deleted":
			return true
		default:
			return false
		}
	case "state":
		return c.states[state]
	default:
		return false
	}
}

// evaluateUntil evaluates a --until condition against current tasks, polling
// until satisfied, timeout, or stall. Shared by monitor watch --until and
// monitor wait.
func evaluateUntil(database *db.DB, opts evaluateUntilOpts) (*evaluateUntilResult, error) {
	condition, err := parseMonitorCondition(opts.Condition)
	if err != nil {
		return nil, exitError(2, err)
	}
	started := time.Now()
	lastProgress := started
	for {
		met, unmet, err := condition.evaluate(database, opts.TaskUUIDs)
		if err != nil {
			return nil, exitError(3, err)
		}
		if met {
			return &evaluateUntilResult{
				Result:   monitorResultMet,
				Reason:   monitorReasonConditionMet,
				Unmet:    []string{},
				ExitCode: 0,
			}, nil
		}
		if opts.Timeout > 0 && time.Since(started) >= opts.Timeout {
			return &evaluateUntilResult{
				Result:   monitorResultTimeout,
				Reason:   monitorReasonTimedOut,
				Unmet:    unmet,
				ExitCode: 1,
			}, nil
		}
		if opts.StallAfter > 0 && time.Since(lastProgress) >= opts.StallAfter {
			return &evaluateUntilResult{
				Result:   monitorResultStall,
				Reason:   monitorReasonStalled,
				Unmet:    unmet,
				ExitCode: 1,
			}, nil
		}
		time.Sleep(monitorPollInterval)
	}
}

func monitorConditionMet(database *db.DB, opts evaluateUntilOpts) (bool, []string, error) {
	condition, err := parseMonitorCondition(opts.Condition)
	if err != nil {
		return false, nil, exitError(2, err)
	}
	met, unmet, err := condition.evaluate(database, opts.TaskUUIDs)
	if err != nil {
		return false, nil, exitError(3, err)
	}
	return met, unmet, nil
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
func isEventIncluded(event watchEvent, filter monitorEventFilter) bool {
	if filter.StateOnly && !isStateChangeEvent(event) {
		return false
	}
	if len(filter.EventTypes) > 0 && !containsString(filter.EventTypes, event.EventType) {
		return false
	}

	if event.ResourceType == "task" && strings.HasPrefix(event.EventType, "task.") {
		if len(filter.TaskUUIDs) == 0 && len(filter.TaskFriendlyIDs) == 0 {
			return true
		}
		return event.ResourceUUID != nil && containsString(filter.TaskUUIDs, *event.ResourceUUID)
	}

	if event.ResourceType == "comment" && strings.HasPrefix(event.EventType, "comment.") {
		if len(filter.TaskUUIDs) == 0 && len(filter.TaskFriendlyIDs) == 0 {
			return true
		}
		taskID := payloadTaskID(event.Payload)
		return containsString(filter.TaskUUIDs, taskID) || containsString(filter.TaskFriendlyIDs, taskID)
	}

	return false
}

// isStateChangeEvent reports whether an event is a lifecycle state change:
//   - task.updated with the "state" key present in the payload
//   - task.archived, task.deleted, task.restored (always lifecycle)
//
// Explicitly excludes title/body/priority/comment-only events.
func isStateChangeEvent(event watchEvent) bool {
	if event.ResourceType != "task" {
		return false
	}
	switch event.EventType {
	case "task.archived", "task.deleted", "task.restored":
		return true
	case "task.updated":
		if event.Payload == nil {
			return false
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal([]byte(*event.Payload), &fields); err != nil {
			return false
		}
		_, ok := fields["state"]
		return ok
	default:
		return false
	}
}

// ──── Polling cursor ──────────────────────────────────────────────────────────

// pollMonitorEvents queries event_log WHERE e.id > cursorID matching filter.
// Returns (matched events as typed lines, new high-water cursor, error).
// The cursor is monotonically advancing; callers must not pass a smaller cursor
// on subsequent calls to avoid duplicate emissions.
func pollMonitorEvents(database *db.DB, cursorID int64, filter monitorEventFilter) ([]monitorEventLine, int64, error) {
	rows, err := database.Query(`
		SELECT e.id, e.timestamp, e.actor_uuid, e.principal_ref, e.scope_ref,
		       e.resource_type, e.resource_uuid, e.event_type, e.etag, e.payload,
		       a.slug as actor_slug, a.id as actor_id,
		       CASE e.resource_type
		           WHEN 'task' THEN (SELECT id FROM tasks WHERE uuid = e.resource_uuid)
		           WHEN 'container' THEN (SELECT id FROM containers WHERE uuid = e.resource_uuid)
		           WHEN 'actor' THEN (SELECT id FROM actors WHERE uuid = e.resource_uuid)
		           WHEN 'comment' THEN (SELECT id FROM comments WHERE uuid = e.resource_uuid)
		           ELSE NULL
		       END as resource_id,
		       CASE e.resource_type
		           WHEN 'comment' THEN (SELECT task_uuid FROM comments WHERE uuid = e.resource_uuid)
		           ELSE NULL
		       END as comment_task_uuid,
		       CASE e.resource_type
		           WHEN 'comment' THEN (SELECT t.id FROM comments c JOIN tasks t ON t.uuid = c.task_uuid WHERE c.uuid = e.resource_uuid)
		           ELSE NULL
		       END as comment_task_id
		FROM event_log e
		LEFT JOIN actors a ON a.uuid = e.actor_uuid
		WHERE e.id > ?
		ORDER BY e.id ASC
	`, cursorID)
	if err != nil {
		return nil, cursorID, fmt.Errorf("query monitor events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []monitorEventLine
	newCursor := cursorID
	for rows.Next() {
		event, commentTaskUUID, commentTaskID, err := scanWatchEventWithCommentTask(rows)
		if err != nil {
			return nil, cursorID, err
		}
		newCursor = event.ID
		normalized := filter
		if event.ResourceType == "comment" {
			if commentTaskUUID != "" && !containsString(normalized.TaskUUIDs, commentTaskUUID) {
				normalized.TaskUUIDs = append(normalized.TaskUUIDs, commentTaskUUID)
			}
			if commentTaskID != "" && !containsString(normalized.TaskFriendlyIDs, commentTaskID) {
				normalized.TaskFriendlyIDs = append(normalized.TaskFriendlyIDs, commentTaskID)
			}
			if payloadTaskID(event.Payload) == "" && commentTaskUUID != "" {
				payload := map[string]string{"task_id": commentTaskUUID}
				payloadBytes, _ := json.Marshal(payload)
				payloadString := string(payloadBytes)
				event.Payload = &payloadString
			}
		}
		if !isEventIncluded(event, normalized) {
			continue
		}
		out = append(out, monitorEventLine{
			Type:         "wrkq.monitor.event",
			ID:           event.ID,
			Timestamp:    event.Timestamp,
			ResourceType: event.ResourceType,
			ResourceUUID: event.ResourceUUID,
			ResourceID:   event.ResourceID,
			EventType:    event.EventType,
			Payload:      event.Payload,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, cursorID, fmt.Errorf("iterate monitor events: %w", err)
	}
	return out, newCursor, nil
}

type eventScanner interface {
	Scan(dest ...interface{}) error
}

func scanWatchEventWithCommentTask(scanner eventScanner) (watchEvent, string, string, error) {
	var event watchEvent
	var actorSlug, actorID, resourceID sql.NullString
	var commentTaskUUID, commentTaskID sql.NullString
	err := scanner.Scan(
		&event.ID,
		&event.Timestamp,
		&event.ActorUUID,
		&event.PrincipalRef,
		&event.ScopeRef,
		&event.ResourceType,
		&event.ResourceUUID,
		&event.EventType,
		&event.ETag,
		&event.Payload,
		&actorSlug,
		&actorID,
		&resourceID,
		&commentTaskUUID,
		&commentTaskID,
	)
	if err != nil {
		return watchEvent{}, "", "", fmt.Errorf("scan monitor event: %w", err)
	}
	if actorSlug.Valid {
		event.ActorSlug = &actorSlug.String
	}
	if actorID.Valid {
		event.ActorID = &actorID.String
	}
	if resourceID.Valid {
		event.ResourceID = &resourceID.String
	}
	return event, valueOrEmpty(commentTaskUUID), valueOrEmpty(commentTaskID), nil
}

// ──── Selector resolution ─────────────────────────────────────────────────────

// resolveMonitorSelectors resolves task selector strings (T-XXXXX / paths) to
// (uuids, friendlyIDs). Returns exitError(2, ...) for any invalid selector so
// callers can fail fast before streaming begins.
func resolveMonitorSelectors(database *db.DB, selectors []string) (uuids []string, friendlyIDs []string, err error) {
	for _, selector := range selectors {
		selector = strings.TrimSpace(selector)
		if selector == "" {
			continue
		}
		uuid, friendlyID, err := resolveMonitorSelector(database, selector)
		if err != nil {
			return nil, nil, exitError(2, err)
		}
		if !containsString(uuids, uuid) {
			uuids = append(uuids, uuid)
		}
		if friendlyID != "" && !containsString(friendlyIDs, friendlyID) {
			friendlyIDs = append(friendlyIDs, friendlyID)
		}
	}
	return uuids, friendlyIDs, nil
}

func resolveMonitorSelector(database *db.DB, selector string) (string, string, error) {
	uuid, friendlyID, err := selectors.ResolveTask(database, selector)
	if err != nil {
		return "", "", fmt.Errorf("invalid task selector %q: %w", selector, err)
	}
	return uuid, friendlyID, nil
}

func monitorInitialCursor(database *db.DB, highWater bool) (int64, error) {
	if !highWater {
		return monitorWatchSince, nil
	}
	var cursor sql.NullInt64
	err := database.QueryRow(`SELECT MAX(id) FROM event_log`).Scan(&cursor)
	if err != nil {
		return 0, fmt.Errorf("query event high-water: %w", err)
	}
	if cursor.Valid {
		return cursor.Int64, nil
	}
	return 0, nil
}

func payloadTaskID(payload *string) string {
	if payload == nil || *payload == "" {
		return ""
	}
	var body map[string]interface{}
	if err := json.Unmarshal([]byte(*payload), &body); err != nil {
		return ""
	}
	value, ok := body["task_id"]
	if !ok {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return fmt.Sprint(typed)
	}
}

func containsString(values []string, needle string) bool {
	for _, value := range values {
		if value == needle {
			return true
		}
	}
	return false
}

func questionMarks(count int) string {
	if count <= 0 {
		return "NULL"
	}
	return strings.TrimRight(strings.Repeat("?,", count), ",")
}

func stringsToInterfaces(values []string) []interface{} {
	out := make([]interface{}, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func parseMonitorDuration(raw string, defaultValue time.Duration) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultValue, nil
	}
	value, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	return value, nil
}

func splitComma(raw string) []string {
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

func cursorForLastEvents(database *db.DB, count int64) (int64, error) {
	if count <= 0 {
		return 0, nil
	}
	var cursor sql.NullInt64
	err := database.QueryRow(`
		SELECT COALESCE(MIN(id), 0) - 1
		FROM (
			SELECT id FROM event_log ORDER BY id DESC LIMIT ?
		)
	`, count).Scan(&cursor)
	if err != nil {
		return 0, fmt.Errorf("query last event cursor: %w", err)
	}
	if cursor.Valid && cursor.Int64 > 0 {
		return cursor.Int64, nil
	}
	return 0, nil
}

// ──── Streaming driver ────────────────────────────────────────────────────────

// monitorStreamUntil drives the event-streaming poll loop. It:
//  1. Evaluates the initial authoritative snapshot (quick exit if already met).
//  2. Advances a monotonic event-id cursor each poll cycle.
//  3. Emits typed monitorEventLine records per relevant event.
//  4. Emits exactly one monitorTerminalLine before return.
//
// Human diagnostics go to stderr; stdout is clean NDJSON.
func monitorStreamUntil(database *db.DB, stdout io.Writer, filter monitorEventFilter, opts evaluateUntilOpts) error {
	encoder := json.NewEncoder(stdout)
	if opts.Condition != "" {
		met, unmet, err := monitorConditionMet(database, opts)
		if err != nil {
			_ = encoder.Encode(buildTerminalLine(monitorResultError, nil))
			return err
		}
		if met {
			if err := encoder.Encode(buildTerminalLine(monitorResultMet, unmet)); err != nil {
				return exitError(3, err)
			}
			return nil
		}
	}

	cursor, err := monitorInitialCursor(database, opts.Condition != "")
	if err != nil {
		_ = encoder.Encode(buildTerminalLine(monitorResultError, nil))
		return exitError(3, err)
	}

	started := time.Now()
	lastEvent := started
	for {
		events, nextCursor, err := pollMonitorEvents(database, cursor, filter)
		if err != nil {
			_ = encoder.Encode(buildTerminalLine(monitorResultError, nil))
			return exitError(3, err)
		}
		cursor = nextCursor
		for _, event := range events {
			if err := encoder.Encode(event); err != nil {
				_ = encoder.Encode(buildTerminalLine(monitorResultError, nil))
				return exitError(3, err)
			}
			lastEvent = time.Now()
		}
		if opts.Condition != "" {
			met, currentUnmet, err := monitorConditionMet(database, opts)
			if err != nil {
				_ = encoder.Encode(buildTerminalLine(monitorResultError, nil))
				return err
			}
			if met {
				if err := encoder.Encode(buildTerminalLine(monitorResultMet, currentUnmet)); err != nil {
					return exitError(3, err)
				}
				return nil
			}
			if opts.Timeout > 0 && time.Since(started) >= opts.Timeout {
				_ = encoder.Encode(buildTerminalLine(monitorResultTimeout, currentUnmet))
				return exitError(1, fmt.Errorf("monitor timeout waiting for %s", opts.Condition))
			}
			if opts.StallAfter > 0 && time.Since(lastEvent) >= opts.StallAfter {
				_ = encoder.Encode(buildTerminalLine(monitorResultStall, currentUnmet))
				return exitError(1, fmt.Errorf("monitor stalled waiting for %s", opts.Condition))
			}
		}
		time.Sleep(monitorPollInterval)
	}
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
	monitorWatchFormat     string
	monitorWatchScope      string
	monitorWatchEventType  string

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
	if monitorWatchRaw {
		return watchEvents(cmd.OutOrStdout(), app.DB, monitorWatchSince, true, true)
	}
	if monitorWatchFormat != "" && monitorWatchFormat != "ndjson" && monitorWatchFormat != "compact" {
		return exitError(2, fmt.Errorf("invalid --format %q: choose ndjson or compact", monitorWatchFormat))
	}

	selectorsIn := make([]string, 0, len(args))
	for _, arg := range args {
		selectorsIn = append(selectorsIn, applyProjectRootToSelector(app.Config, arg, false))
	}
	uuids, friendlyIDs, err := resolveMonitorSelectors(app.DB, selectorsIn)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	if len(uuids) == 0 && monitorWatchUntil != "" {
		err := exitError(2, fmt.Errorf("monitor watch --until requires at least one task selector"))
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}

	timeout, err := parseMonitorDuration(monitorWatchTimeout, 0)
	if err != nil {
		err = exitError(2, err)
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	stallAfter, err := parseMonitorDuration(monitorWatchStallAfter, 0)
	if err != nil {
		err = exitError(2, err)
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	filter := monitorEventFilter{
		TaskUUIDs:       uuids,
		TaskFriendlyIDs: friendlyIDs,
		StateOnly:       monitorWatchStateOnly,
		EventTypes:      splitComma(monitorWatchEventType),
	}
	if monitorWatchScope != "" {
		return exitError(2, fmt.Errorf("--scope is not implemented yet"))
	}
	if monitorWatchLast > 0 {
		cursor, err := cursorForLastEvents(app.DB, monitorWatchLast)
		if err != nil {
			return exitError(3, err)
		}
		monitorWatchSince = cursor
	}
	return monitorStreamUntil(app.DB, cmd.OutOrStdout(), filter, evaluateUntilOpts{
		TaskUUIDs:  uuids,
		Condition:  monitorWatchUntil,
		Timeout:    timeout,
		StallAfter: stallAfter,
	})
}

func runMonitorWait(app *appctx.App, cmd *cobra.Command, args []string) error {
	if strings.TrimSpace(monitorWaitUntil) == "" {
		err := exitError(2, fmt.Errorf("monitor wait requires --until"))
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	selectorsIn := make([]string, 0, len(args))
	for _, arg := range args {
		selectorsIn = append(selectorsIn, applyProjectRootToSelector(app.Config, arg, false))
	}
	uuids, _, err := resolveMonitorSelectors(app.DB, selectorsIn)
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	if len(uuids) == 0 {
		err := exitError(2, fmt.Errorf("monitor wait requires at least one task selector"))
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	timeout, err := parseMonitorDuration(monitorWaitTimeout, 0)
	if err != nil {
		err = exitError(2, err)
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	stallAfter, err := parseMonitorDuration(monitorWaitStallAfter, 0)
	if err != nil {
		err = exitError(2, err)
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	result, err := evaluateUntil(app.DB, evaluateUntilOpts{
		TaskUUIDs:  uuids,
		Condition:  monitorWaitUntil,
		Timeout:    timeout,
		StallAfter: stallAfter,
	})
	if err != nil {
		fmt.Fprintln(cmd.ErrOrStderr(), err)
		return err
	}
	encoder := json.NewEncoder(cmd.OutOrStdout())
	if err := encoder.Encode(buildTerminalLine(result.Result, result.Unmet)); err != nil {
		return exitError(3, err)
	}
	if result.ExitCode != 0 {
		return exitError(result.ExitCode, fmt.Errorf("monitor wait ended: %s", result.Result))
	}
	return nil
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
	monitorWatchCmd.Flags().StringVar(&monitorWatchFormat, "format", "", "Output format: ndjson or compact")
	monitorWatchCmd.Flags().StringVar(&monitorWatchScope, "scope", "", "Filter by scope ref")
	monitorWatchCmd.Flags().StringVar(&monitorWatchEventType, "event-type", "", "Comma-separated event_type filter")

	// wait flags
	monitorWaitCmd.Flags().StringVar(&monitorWaitUntil, "until", "", "Condition: state=<s>[,<s>...] or all-terminal")
	monitorWaitCmd.Flags().StringVar(&monitorWaitTimeout, "timeout", "", "Maximum wait duration (e.g. 30m)")
	monitorWaitCmd.Flags().StringVar(&monitorWaitStallAfter, "stall-after", "", "Exit after this duration with no new events")
}
