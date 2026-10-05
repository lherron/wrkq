package wrkfcli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/spf13/cobra"
)

func ledgerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ledger", Short: "Append and project immutable workflow ledger entries"}
	var appendTask, appendKind, appendAbout, bodySource string
	appendCmd := &cobra.Command{
		Use:   "append --task TASK --kind KIND --about PRINCIPAL --body FILE|-",
		Short: "Append an immutable entry to the workflow ledger",
		Args:  cobra.NoArgs,
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			if appendTask == "" || appendKind == "" || appendAbout == "" || bodySource == "" {
				return fmt.Errorf("--task, --kind, --about, and --body are required")
			}
			body, err := readLedgerBody(cmd, bodySource)
			if err != nil {
				return err
			}
			entry, err := rpcCall[wrkfapi.LedgerEntry](cmd, a, "wrkf.ledger.append", wrkfapi.LedgerAppendParams{
				TaskID: appendTask, Kind: appendKind, AboutPrincipalRef: appendAbout, Body: body,
			})
			if err != nil {
				return err
			}
			if flagJSON {
				return printJSON(cmd, entry)
			}
			return printLedger(cmd, []wrkfapi.LedgerEntry{entry}, "", false)
		}),
	}
	appendCmd.Flags().StringVar(&appendTask, "task", "", "Task selector")
	appendCmd.Flags().StringVar(&appendKind, "kind", "", "Ledger kind")
	appendCmd.Flags().StringVar(&appendAbout, "about", "", "Principal the entry is about")
	appendCmd.Flags().StringVar(&bodySource, "body", "", "JSON body file, - for stdin, or a JSON object")

	var listTask, listPrincipal, listKind, listSince, listUntil, listCursor string
	var listLimit int
	var ndjson bool
	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List ledger entries by task, principal, or kind",
		Args:  cobra.NoArgs,
		RunE: withTransport(func(a *app, cmd *cobra.Command, args []string) error {
			result, err := rpcCall[wrkfapi.LedgerListResult](cmd, a, "wrkf.ledger.list", wrkfapi.LedgerListParams{
				TaskID: listTask, AboutPrincipalRef: listPrincipal, Kind: listKind, Since: listSince, Until: listUntil, Limit: listLimit, Cursor: listCursor,
			})
			if err != nil {
				return err
			}
			return printLedger(cmd, result.Entries, result.NextCursor, ndjson)
		}),
	}
	listCmd.Flags().StringVar(&listTask, "task", "", "Replay one task's ledger")
	listCmd.Flags().StringVar(&listPrincipal, "principal", "", "Project entries about this principal")
	listCmd.Flags().StringVar(&listKind, "kind", "", "Filter by kind")
	listCmd.Flags().StringVar(&listSince, "since", "", "Inclusive ISO-8601 lower timestamp")
	listCmd.Flags().StringVar(&listUntil, "until", "", "Inclusive ISO-8601 upper timestamp")
	listCmd.Flags().IntVar(&listLimit, "limit", 0, "Maximum projection entries (0 is unlimited)")
	listCmd.Flags().StringVar(&listCursor, "cursor", "", "Opaque projection cursor")
	listCmd.Flags().BoolVar(&ndjson, "ndjson", false, "Emit one JSON entry per line")
	cmd.AddCommand(appendCmd, listCmd)
	return cmd
}

func readLedgerBody(cmd *cobra.Command, source string) (json.RawMessage, error) {
	if source == "-" {
		b, err := io.ReadAll(cmd.InOrStdin())
		return json.RawMessage(b), err
	}
	if strings.HasPrefix(strings.TrimSpace(source), "{") {
		return json.RawMessage(source), nil
	}
	b, err := os.ReadFile(source)
	if err != nil {
		return nil, fmt.Errorf("read ledger body %q: %w", source, err)
	}
	return json.RawMessage(b), nil
}

func printLedger(cmd *cobra.Command, entries []wrkfapi.LedgerEntry, nextCursor string, ndjson bool) error {
	if flagJSON {
		return printJSON(cmd, wrkfapi.LedgerListResult{Entries: entries, NextCursor: nextCursor})
	}
	if ndjson {
		for _, entry := range entries {
			b, err := json.Marshal(entry)
			if err != nil {
				return err
			}
			fmt.Fprintln(cmd.OutOrStdout(), string(b))
		}
		return nil
	}
	tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "SEQ\tTS\tINSTANCE\tKIND\tABOUT\tWRITTEN BY")
	for _, entry := range entries {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%s\t%s\t%s\n", entry.Seq, entry.TS, entry.InstanceID, entry.Kind, entry.AboutPrincipalRef, entry.WrittenBy)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	if nextCursor != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "next_cursor\t%s\n", nextCursor)
	}
	return nil
}
