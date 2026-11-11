package rpccli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/spf13/cobra"
)

func newExtCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "ext", Short: "Manage subject extensions with independent etag fences"}
	cmd.AddCommand(newExtRecordCmd("put"), newExtRecordCmd("get"), newExtRecordCmd("delete"), newExtListCmd())
	return cmd
}

func extSubject(cmd *cobra.Command, ref string) (map[string]string, error) {
	for prefix, kind := range map[string]string{"T-": "task", "W-": "task", "P-": "container", "R-": "room"} {
		if strings.HasPrefix(ref, prefix) && len(ref) > len(prefix) {
			return map[string]string{kind: ref}, nil
		}
	}
	return nil, &clifunnel.UsageError{Err: fmt.Errorf("subject must be a public T-…, W-…, P-…, or R-… id"), Cmd: cmd}
}

func newExtRecordCmd(verb string) *cobra.Command {
	var body string
	var ifMatch int64
	var create bool
	cmd := &cobra.Command{
		Use: verb + " <subject> <ns> <kind> <id>", Short: map[string]string{"put": "Write an extension", "get": "Read an extension", "delete": "Delete an extension"}[verb], Args: cobra.ExactArgs(4),
		RunE: func(cmd *cobra.Command, args []string) error {
			subject, err := extSubject(cmd, args[0])
			if err != nil {
				return err
			}
			params := map[string]any{"subject": subject, "ns": args[1], "kind": args[2], "id": args[3]}
			if verb != "get" {
				if ifMatch < 0 {
					return &clifunnel.UsageError{Err: fmt.Errorf("--if-match must be nonnegative"), Cmd: cmd}
				}
				if cmd.Flags().Changed("if-match") {
					params["ifMatch"] = ifMatch
				}
			}
			if verb == "put" {
				if !cmd.Flags().Changed("body") {
					return &clifunnel.UsageError{Err: fmt.Errorf("--body is required"), Cmd: cmd}
				}
				if create && cmd.Flags().Changed("if-match") {
					return &clifunnel.UsageError{Err: fmt.Errorf("--create and --if-match are mutually exclusive"), Cmd: cmd}
				}
				value, err := readJSONObjectValue(cmd, body, "--body", &stdinClaims{}, false)
				if err != nil {
					return err
				}
				params["body"] = value
				if create {
					params["ifMatch"] = 0
				}
			}
			method := "wrkq.ext." + verb
			if verb == "get" {
				method = "wrkq.ext.show"
			}
			return callExt(cmd, method, params)
		},
	}
	if verb == "put" {
		cmd.Flags().StringVar(&body, "body", "", "JSON object (inline, @file, or - for stdin)")
		cmd.Flags().BoolVar(&create, "create", false, "Create only; refuse an existing extension")
	}
	if verb != "get" {
		cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Require this extension etag (0 means create only)")
	}
	return refuseRepeatedFlags(cmd)
}

func newExtListCmd() *cobra.Command {
	var ns, kind, cursor string
	var states []string
	var limit int
	cmd := &cobra.Command{Use: "list [<subject>]", Short: "List extensions with subject state filters and cursor pagination", Args: cobra.MaximumNArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
		params := map[string]any{}
		if len(args) == 1 {
			subject, err := extSubject(cmd, args[0])
			if err != nil {
				return err
			}
			params["subject"] = subject
		}
		if ns != "" {
			params["ns"] = ns
		}
		if kind != "" {
			params["kind"] = kind
		}
		if len(states) > 0 {
			params["subjectStates"] = states
		}
		if limit < 1 || limit > 500 {
			return &clifunnel.UsageError{Err: fmt.Errorf("--limit must be between 1 and 500"), Cmd: cmd}
		}
		params["limit"] = limit
		if cursor != "" {
			params["cursor"] = cursor
		}
		return callExt(cmd, "wrkq.ext.listView", params)
	}}
	cmd.Flags().StringVar(&ns, "ns", "", "Filter by namespace")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by extension kind")
	cmd.Flags().StringSliceVar(&states, "states", nil, "Filter by subject task states (comma-separated)")
	cmd.Flags().IntVar(&limit, "limit", 500, "Maximum rows per page (1–500)")
	cmd.Flags().StringVar(&cursor, "cursor", "", "Opaque cursor from the previous page's nextCursor")
	return refuseRepeatedFlags(cmd)
}

func callExt(cmd *cobra.Command, method string, params map[string]any) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	if method == "wrkq.ext.put" || method == "wrkq.ext.delete" {
		principal, err := actorFlag(cmd)
		if err != nil {
			return err
		}
		if principal != "" {
			params["principalRef"] = principal
		}
	}
	ctx := cmd.Context()
	if principal, err := actorFlag(cmd); err != nil {
		return err
	} else if scopeRef := mutationScopeRef(cmd, principal); scopeRef != "" {
		ctx = attribution.WithCallerScope(ctx, scopeRef)
	}
	raw, err := tr.Call(ctx, method, params)
	if err != nil {
		return err
	}
	return encodeJSONIndent(cmd, json.RawMessage(raw))
}

func readJSONObjectValue(cmd *cobra.Command, raw, label string, claims *stdinClaims, nullable bool) (json.RawMessage, error) {
	text, err := readTextValue(raw, label, cmd.InOrStdin(), claims)
	if err != nil {
		return nil, &clifunnel.UsageError{Err: err, Cmd: cmd}
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal([]byte(text), &object); err != nil || (object == nil && !nullable) {
		return nil, &clifunnel.UsageError{Err: fmt.Errorf("%s requires a JSON object", label), Cmd: cmd}
	}
	return json.RawMessage(text), nil
}
