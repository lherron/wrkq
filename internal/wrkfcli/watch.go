package wrkfcli

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/workflow"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

const defaultWatchPollInterval = time.Second

type watchFlags struct {
	until        string
	timeout      string
	follow       bool
	pollInterval string
}

type watchSummary struct {
	Type      string                  `json:"type,omitempty"`
	Result    string                  `json:"result"`
	Class     string                  `json:"class"`
	ExitCode  int                     `json:"exitCode"`
	Until     string                  `json:"until"`
	TimedOut  bool                    `json:"timedOut,omitempty"`
	Snapshot  *workflow.WatchSnapshot `json:"snapshot,omitempty"`
	ElapsedMs int64                   `json:"elapsedMs"`
}

type watchEventLine struct {
	Type  string         `json:"type"`
	Event workflow.Event `json:"event"`
}

func watchCmd() *cobra.Command {
	flags := watchFlags{}
	cmd := &cobra.Command{
		Use:   "watch <task|instance|run>",
		Short: "Wait for durable workflow lifecycle state",
		Long: `Wait for a task workflow instance, workflow instance, or workflow run to
reach a durable lifecycle predicate. Selectors are inferred by id prefix:
run_* watches a run, wfi_* watches an instance, and anything else is a task
selector. Follow mode emits NDJSON workflow events followed by one summary row.`,
		Args: cobra.ExactArgs(1),
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			return runWatch(a, cmd, args[0], flags)
		}),
	}
	cmd.Flags().StringVar(&flags.until, "until", workflow.WatchUntilTerminal, "Predicate to wait for: terminal, closed, suspended, or waiting")
	cmd.Flags().StringVar(&flags.timeout, "timeout", "", "Maximum wait duration (for example 30s, 10m)")
	cmd.Flags().BoolVar(&flags.follow, "follow", false, "Emit NDJSON workflow events while waiting")
	cmd.Flags().StringVar(&flags.pollInterval, "poll-interval", defaultWatchPollInterval.String(), "Polling interval")
	return cmd
}

func runWatch(a *app, cmd *cobra.Command, selector string, flags watchFlags) error {
	until, err := workflow.NormalizeWatchUntil(flags.until)
	if err != nil {
		return exitError(2, err)
	}
	timeout, err := parseOptionalDuration(flags.timeout, "timeout")
	if err != nil {
		return exitError(2, err)
	}
	pollInterval, err := parseOptionalDuration(flags.pollInterval, "poll-interval")
	if err != nil {
		return exitError(2, err)
	}
	if pollInterval <= 0 {
		return exitError(2, fmt.Errorf("--poll-interval must be greater than zero"))
	}

	start := time.Now()
	var deadline time.Time
	if timeout > 0 {
		deadline = start.Add(timeout)
	}

	snapshotValue, err := rpcCall[workflow.WatchSnapshot](cmd, a, "wrkf.watch.snapshot", wrkfapi.WatchSnapshotParams{Selector: selector, Until: until})
	if err != nil {
		return exitError(2, err)
	}
	snapshot := &snapshotValue
	eventsCursor := ""
	if flags.follow {
		if err := emitWatchEvents(cmd, a, selector, &eventsCursor); err != nil {
			return exitError(3, err)
		}
	}
	if snapshot.Met {
		return finishWatch(cmd, flags.follow, watchSummaryFor("met", snapshot, start, false))
	}

	for {
		if timeout > 0 && !time.Now().Before(deadline) {
			return finishWatch(cmd, flags.follow, watchSummary{
				Type: "summary", Result: "timeout", Class: "timeout", ExitCode: 1, Until: until,
				TimedOut: true, Snapshot: snapshot, ElapsedMs: time.Since(start).Milliseconds(),
			})
		}

		sleepFor := pollInterval
		if timeout > 0 {
			remaining := time.Until(deadline)
			if remaining < sleepFor {
				sleepFor = remaining
			}
		}
		if sleepFor > 0 {
			time.Sleep(sleepFor)
		}

		if flags.follow {
			if err := emitWatchEvents(cmd, a, selector, &eventsCursor); err != nil {
				return exitError(3, err)
			}
		}
		snapshotValue, err = rpcCall[workflow.WatchSnapshot](cmd, a, "wrkf.watch.snapshot", wrkfapi.WatchSnapshotParams{Selector: selector, Until: until})
		if err != nil {
			return exitError(3, err)
		}
		snapshot = &snapshotValue
		if snapshot.Met {
			return finishWatch(cmd, flags.follow, watchSummaryFor("met", snapshot, start, false))
		}
	}
}

func parseOptionalDuration(raw, name string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("--%s must be a valid duration: %w", name, err)
	}
	return d, nil
}

func emitWatchEvents(cmd *cobra.Command, a *app, selector string, afterCursor *string) error {
	result, err := rpcCall[wrkfapi.WatchEventsResult](cmd, a, "wrkf.watch.events", wrkfapi.WatchEventsParams{
		Selector: selector, AfterCursor: *afterCursor, Limit: 100,
	})
	if err != nil {
		return err
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	for _, event := range result.Events {
		if err := enc.Encode(watchEventLine{Type: "event", Event: event}); err != nil {
			return err
		}
	}
	*afterCursor = result.NextCursor
	return nil
}

func finishWatch(cmd *cobra.Command, follow bool, summary watchSummary) error {
	if follow {
		if err := json.NewEncoder(cmd.OutOrStdout()).Encode(summary); err != nil {
			return exitError(3, err)
		}
	} else if flagJSON {
		if err := printJSON(cmd, summary); err != nil {
			return exitError(3, err)
		}
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "%s class=%s exit=%d target=%s until=%s status=%s\n",
			summary.Result, summary.Class, summary.ExitCode, summary.Snapshot.Target.Selector, summary.Until, summary.Snapshot.Status)
	}
	if summary.ExitCode != 0 {
		return exitErrorReported(summary.ExitCode, fmt.Errorf("watch ended: %s", summary.Result))
	}
	return nil
}

func watchSummaryFor(result string, snapshot *workflow.WatchSnapshot, start time.Time, timedOut bool) watchSummary {
	return watchSummary{
		Type: "summary", Result: result, Class: snapshot.Class, ExitCode: snapshot.ExitCode,
		Until: snapshot.Until, TimedOut: timedOut, Snapshot: snapshot, ElapsedMs: time.Since(start).Milliseconds(),
	}
}
