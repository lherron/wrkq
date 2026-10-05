package rpccli

// wrkp is the project-facts CLI: post a foreign fact (and the git/just
// producers that post for you), print a head cursor, show one event, and list
// observed types. Reading the timeline lives in wrkp_log.go and rendering it in
// wrkp_render.go.

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/lherron/wrkq/internal/clifunnel"
	"github.com/lherron/wrkq/internal/style"
	"github.com/spf13/cobra"
)

//go:embed embedded/WRKP-USAGE.md
var wrkpUsageContent string

type wrkpProjectEvent struct {
	UUID           string          `json:"uuid"`
	ProjectUUID    string          `json:"projectUuid"`
	ContainerUUID  string          `json:"containerUuid"`
	CampaignUUID   *string         `json:"campaignUuid"`
	TaskUUID       *string         `json:"taskUuid"`
	Type           string          `json:"type"`
	Attributes     json.RawMessage `json:"attributes"`
	PrincipalRef   *string         `json:"principalRef"`
	ScopeRef       *string         `json:"scopeRef"`
	Summary        string          `json:"summary"`
	IdempotencyKey *string         `json:"idempotencyKey"`
	OccurredAt     string          `json:"occurredAt"`
	CreatedAt      string          `json:"createdAt"`
	Task           *string         `json:"task"`
	Container      *string         `json:"container"`
	Campaign       *string         `json:"campaign"`
}

func NewWrkpRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use: "wrkp", Short: "Post and read foreign project facts",
		SilenceUsage: true, SilenceErrors: true,
	}
	root.PersistentFlags().String("db", "", "Database path or rpc:// locator (overrides WRKQ_DB and WRKQ_DB_PATH)")
	root.PersistentFlags().String("principal-ref", "", "Caller principal for write attribution: agent:<id> or full agent ScopeRef")
	root.PersistentFlags().String("as", "", "Alias for --principal-ref; accepts agent:<id> or a full agent ScopeRef")
	root.PersistentFlags().String("project", "", "Project to operate under (overrides WRKQ_PROJECT_ROOT)")
	root.PersistentFlags().String("output", "", "Output mode: human, json, ndjson, porcelain, yaml, tsv")
	root.PersistentFlags().Bool("json", false, "Output as JSON")
	root.PersistentFlags().String("scope-ref", "", "Caller scope handle (defaults to $HRC_SESSION_REF)")
	root.AddCommand(newWrkpPostCmd(), newWrkpGitCmd(), newWrkpJustCmd(), newWrkpCursorCmd(), newWrkpLogCmd(), newWrkpShowCmd(), newWrkpTypesCmd(), newWrkpInfoCmd(), newVersionCmd())
	applyWrkcHelpTemplates(root)
	return root
}

func ExecuteWrkp() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := clifunnel.Execute(ctx, NewWrkpRootCmd(), os.Args[1:],
		clifunnel.Options{NotFoundHint: wrkpNotFoundHint, OutputModes: wrkpOutputModes})
	if errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}

// wrkpOutputModes is wrkp's --output vocabulary (§9).
var wrkpOutputModes = []string{"human", "json", "ndjson", "porcelain", "yaml", "tsv"}

func newWrkpPostCmd() *cobra.Command {
	var eventType, summary, task, key, occurredAt string
	var attrs []string
	cmd := &cobra.Command{
		Use: "post [project]", Short: "Post one foreign fact", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			claims := &stdinClaims{}
			message, err := readTextValue(summary, "--message", cmd.InOrStdin(), claims)
			if err != nil {
				return err
			}
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			project := ""
			if len(args) == 1 {
				project = args[0]
			} else if task == "" {
				project = sc.selector("", true)
			}
			params := map[string]any{
				"project": project, "task": sc.selector(task, false), "type": eventType,
				"summary":        strings.TrimSuffix(message, "\n"),
				"idempotencyKey": key, "occurredAt": occurredAt,
			}
			if len(attrs) > 0 {
				pairs := make([]wrkpAttribute, 0, len(attrs))
				for _, raw := range attrs {
					name, value, ok := strings.Cut(raw, "=")
					if !ok || name == "" {
						return fmt.Errorf("--attr requires key=value")
					}
					pairs = appendWrkpAttribute(pairs, name, value)
				}
				params["attributes"] = encodeWrkpAttributes(pairs)
			}
			principal, err := actorFlag(cmd)
			if err != nil {
				return err
			}
			if principal != "" {
				params["principalRef"] = principal
			}
			if scopeRef := wrkcScopeRef(cmd); scopeRef != "" {
				params["scopeRef"] = scopeRef
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.projectEvent.post", params)
			if err != nil {
				return wrkpRPCError(err)
			}
			var result struct {
				UUID    string `json:"uuid"`
				Created bool   `json:"created"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			if wrkpJSON(cmd) {
				return encodeJSONIndent(cmd, result)
			}
			if result.Created {
				fmt.Fprintln(cmd.OutOrStdout(), result.UUID)
			} else {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (existing)\n", result.UUID)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&eventType, "type", "", "Dotted event type")
	cmd.Flags().StringVarP(&summary, "message", "m", "", "One-line summary, or - for stdin")
	cmd.Flags().StringArrayVar(&attrs, "attr", nil, "Attribute key=value (repeatable)")
	cmd.Flags().StringVar(&task, "task", "", "Optional task selector")
	cmd.Flags().StringVar(&key, "key", "", "Project-scoped idempotency key")
	cmd.Flags().StringVar(&occurredAt, "occurred-at", "", "Occurrence time (RFC3339)")
	return cmd
}

// wrkpAttribute is one key=value pair. A slice, not a map, because the contract
// stores attributes verbatim in the producer's key order and the timeline renders
// them in that order: it is the producer's only control over what a reader sees
// first. encoding/json sorts map keys, which is exactly the order loss the first
// landing shipped (clod's probe on T-08388: posted zebra/middle/alpha, rendered
// alpha/middle/zebra).
type wrkpAttribute struct {
	Key   string
	Value string
}

// appendWrkpAttribute keeps the first position of a repeated key and takes the
// last value, so `--attr a=1 --attr b=2 --attr a=3` is {a:3, b:2}.
func appendWrkpAttribute(pairs []wrkpAttribute, key, value string) []wrkpAttribute {
	for i := range pairs {
		if pairs[i].Key == key {
			pairs[i].Value = value
			return pairs
		}
	}
	return append(pairs, wrkpAttribute{Key: key, Value: value})
}

// encodeWrkpAttributes writes the pairs as one JSON object in slice order. The
// server stores the bytes it receives, so order survives end to end only if the
// wire carries it.
func encodeWrkpAttributes(pairs []wrkpAttribute) json.RawMessage {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, pair := range pairs {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, _ := json.Marshal(pair.Key)
		value, _ := json.Marshal(pair.Value)
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return json.RawMessage(buf.Bytes())
}

// wrkpCursorAscending peeks at an opaque timeline cursor's direction. Version
// 3 is the descending (newest-first) reader's; every earlier version is
// ascending. An unreadable cursor is left to the server to refuse.
func wrkpCursorAscending(raw string) bool {
	if raw == "" {
		return false
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return false
	}
	var cur struct {
		Version int `json:"v"`
	}
	return json.Unmarshal(decoded, &cur) == nil && cur.Version > 0 && cur.Version < 3
}

// newWrkpCursorCmd prints a forward cursor at the timeline's current head. A
// reader that lists current state captures this FIRST, lists, then reads
// `wrkp log --after CURSOR` from it: a consistent cut, since anything that
// changed during the listing is re-delivered rather than lost.
func newWrkpCursorCmd() *cobra.Command {
	return &cobra.Command{
		Use: "cursor [project]", Short: "Print a forward cursor at the timeline head", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			project := sc.selector("", true)
			if len(args) == 1 {
				project = args[0]
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.container.timelineView", map[string]any{
				"container": project, "scope": "subtree", "entriesOnly": true, "tail": true, "limit": 1,
			})
			if err != nil {
				return wrkpRPCError(err)
			}
			var view wrkpLogView
			if err := json.Unmarshal(raw, &view); err != nil {
				return err
			}
			if view.NextCursor == "" {
				return fmt.Errorf("server returned no head cursor for %s", project)
			}
			if wrkpJSON(cmd) {
				return encodeJSONIndent(cmd, map[string]string{"project": view.Container.Path, "cursor": view.NextCursor})
			}
			fmt.Fprintln(cmd.OutOrStdout(), view.NextCursor)
			return nil
		},
	}
}

// resolveWrkpMode picks the output mode for a wrkp read. Interactive displays
// default to the human porcelain: on a terminal the styled render wins, and a
// pipe falls to the machine format so scripted callers are untouched. --pretty
// forces the styled LAYOUT exactly as it does for `wrkq cat` — it never flips
// style.ColorEnabled, which is what keeps non-TTY --pretty byte-parity provable.
func resolveWrkpMode(cmd *cobra.Command, pretty, ndjson bool) string {
	if pretty {
		return "human"
	}
	if outF := cmd.Flag("output"); outF != nil && outF.Changed {
		switch outF.Value.String() {
		case "human":
			return "human"
		case "json":
			return "json"
		case "ndjson":
			return "ndjson"
		}
	}
	if wrkpJSON(cmd) {
		return "json"
	}
	if ndjson || !isStdoutTTY(cmd.OutOrStdout()) {
		return "ndjson"
	}
	return "human"
}

func newWrkpShowCmd() *cobra.Command {
	var pretty bool
	cmd := &cobra.Command{
		Use: "show <uuid>", Short: "Show one project event", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			raw, err := tr.Call(cmd.Context(), "wrkq.projectEvent.get", map[string]any{"projectEvent": args[0]})
			if err != nil {
				return wrkpRPCError(err)
			}
			var event wrkpProjectEvent
			if err := json.Unmarshal(raw, &event); err != nil {
				return err
			}
			if resolveWrkpMode(cmd, pretty, false) != "human" {
				return encodeJSONIndent(cmd, event)
			}
			style.RenderStyledEvent(cmd.OutOrStdout(), styledProjectEvent(event, sc.selector("", true)))
			return nil
		},
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force the styled card even when not a TTY")
	return cmd
}

func newWrkpTypesCmd() *cobra.Command {
	var pretty bool
	cmd := &cobra.Command{
		Use: "types [project]", Short: "List observed project-event types", Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{}
			if len(args) == 1 {
				params["project"] = args[0]
			} else if root := sc.selector("", true); root != "" {
				params["project"] = root
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.projectEvent.typesView", params)
			if err != nil {
				return wrkpRPCError(err)
			}
			var result struct {
				Items []struct {
					Type          string `json:"type"`
					Count         int64  `json:"count"`
					LastCreatedAt string `json:"lastCreatedAt"`
				} `json:"items"`
			}
			if err := json.Unmarshal(raw, &result); err != nil {
				return err
			}
			// Same mode contract as log and show (T-08224): a terminal gets the
			// styled render, a pipe stays machine-readable, and --pretty forces the
			// LAYOUT without flipping color. `types` previously hard-coded JSON for
			// every path including --output human, which is the defect.
			if resolveWrkpMode(cmd, pretty, false) != "human" {
				return encodeJSONIndent(cmd, result.Items)
			}
			project, _ := params["project"].(string)
			types := make([]style.StyledType, 0, len(result.Items))
			for _, item := range result.Items {
				types = append(types, style.StyledType{
					Type:          item.Type,
					Count:         item.Count,
					LastCreatedAt: item.LastCreatedAt,
				})
			}
			style.RenderStyledTypes(cmd.OutOrStdout(), project, types)
			return nil
		},
	}
	cmd.Flags().BoolVar(&pretty, "pretty", false, "Force the styled list even when not a TTY")
	return cmd
}

func newWrkpInfoCmd() *cobra.Command {
	return &cobra.Command{Use: "info", Short: "Display wrkp usage documentation", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		return renderEmbeddedUsage(cmd, wrkpUsageContent, wrkpJSON(cmd))
	}}
}

func wrkpJSON(cmd *cobra.Command) bool {
	value, _ := cmd.Flags().GetBool("json")
	return value
}

// wrkpRPCError keeps the public domain code and stable validation reason in
// CLI diagnostics. Hooks use both to distinguish permanent input failures from
// transport errors, and the generic mirror helper intentionally strips them.
func wrkpRPCError(err error) error {
	var rpcErr *Error
	if !errors.As(err, &rpcErr) {
		return err
	}
	// Keep the typed error so its domain code stays a field (CLI standard §4);
	// only the message gains the server's reason.
	annotated := *rpcErr
	if len(rpcErr.Data) > 0 {
		var data struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(rpcErr.Data, &data) == nil && data.Reason != "" {
			annotated.Message += " (" + data.Reason + ")"
		}
	}
	return &annotated
}

func splitCommaValues(raw string) []string {
	result := []string{}
	for _, value := range strings.Split(raw, ",") {
		if value = strings.TrimSpace(value); value != "" {
			result = append(result, value)
		}
	}
	return result
}
