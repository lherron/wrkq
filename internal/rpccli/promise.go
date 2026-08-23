package rpccli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/lherron/wrkq/internal/render"
	"github.com/spf13/cobra"
)

// promiseWire mirrors the public wrkq promise DTO without importing wrkqapi
// across the RPC CLI boundary. Field order is the stable JSON output contract.
type promiseWire struct {
	UUID                  string             `json:"uuid"`
	ID                    string             `json:"id"`
	OwnerPrincipalRef     string             `json:"ownerPrincipalRef"`
	Subject               string             `json:"subject"`
	ReviewQuestion        *string            `json:"reviewQuestion,omitempty"`
	SubjectRef            *promiseSubjectRef `json:"subjectRef"`
	ReviewAt              string             `json:"reviewAt"`
	Ready                 bool               `json:"ready"`
	ReadyFor              *string            `json:"readyFor,omitempty"`
	State                 string             `json:"state"`
	ClosedAt              *string            `json:"closedAt,omitempty"`
	LastReviewedAt        *string            `json:"lastReviewedAt,omitempty"`
	LastReviewNote        *string            `json:"lastReviewNote,omitempty"`
	Meta                  map[string]any     `json:"meta"`
	ETag                  int64              `json:"etag"`
	CreatedAt             string             `json:"createdAt"`
	UpdatedAt             string             `json:"updatedAt"`
	CreatedByPrincipalRef string             `json:"createdByPrincipalRef"`
	UpdatedByPrincipalRef string             `json:"updatedByPrincipalRef"`
}

type promiseSubjectRef struct {
	Type string `json:"type"`
	UUID string `json:"uuid"`
	ID   string `json:"id"`
	Path string `json:"path"`
}

type promiseOutputFlags struct {
	asJSON    bool
	ndjson    bool
	porcelain bool
	pretty    bool
}

func newPromiseCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "promise", Short: "Manage future-attention promises"}
	cmd.AddCommand(newPromiseAddCmd())
	cmd.AddCommand(newPromiseListCmd(false))
	cmd.AddCommand(newPromiseListCmd(true))
	cmd.AddCommand(newPromiseEditCmd())
	cmd.AddCommand(newPromiseReviewCmd("renew"))
	cmd.AddCommand(newPromiseReviewCmd("resolve"))
	cmd.AddCommand(newPromiseReviewCmd("abandon"))
	cmd.AddCommand(newPromiseRetargetCmd(true))
	cmd.AddCommand(newPromiseRetargetCmd(false))
	return cmd
}

func newPromiseAddCmd() *cobra.Command {
	var owner, subject, question, task, container, campaign, reviewAt, reviewIn string
	var meta, metaFile string
	var onBehalf bool
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Record a promise to revisit a subject",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			claims := &stdinClaims{}
			var err error
			if cmd.Flags().Changed("subject") {
				subject, err = readTextValue(subject, "--subject", cmd.InOrStdin(), claims)
				if err != nil {
					return err
				}
			}
			var questionValue *string
			if cmd.Flags().Changed("question") {
				question, err = readNullableTextValue(question, "--question", cmd.InOrStdin(), claims)
				if err != nil {
					return err
				}
				questionValue = &question
			}
			_, _, metaValue, err := readMetaValue(meta, metaFile, cmd.InOrStdin(), claims)
			if err != nil {
				return err
			}
			container, err = promiseContainerAlias(container, campaign)
			if err != nil {
				return err
			}
			if err := requirePromiseReviewTime(reviewAt, reviewIn, true); err != nil {
				return err
			}

			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{
				"subject": subject, "reviewAt": reviewAt, "reviewIn": reviewIn,
			}
			if owner != "" {
				params["ownerPrincipalRef"] = owner
			}
			if onBehalf {
				params["onBehalf"] = true
			}
			if questionValue != nil {
				params["reviewQuestion"] = questionValue
			}
			if task != "" {
				params["task"] = sc.selector(task, false)
			}
			if container != "" {
				params["container"] = sc.selector(container, false)
			}
			if metaValue != nil {
				params["meta"] = metaValue
			}
			if err := addPromisePrincipal(cmd, params); err != nil {
				return err
			}
			raw, err := tr.Call(cmd.Context(), "wrkq.promise.add", params)
			if err != nil {
				return err
			}
			promise, err := decodePromise(raw)
			if err != nil {
				return err
			}
			return renderPromiseSingleton(cmd, promise, output)
		},
	}
	cmd.Flags().StringVar(&owner, "for", "", "Owner principal (bare agent slug or agent:<id>)")
	cmd.Flags().BoolVar(&onBehalf, "on-behalf", false, "Assert the named owner requested this accepted promise")
	cmd.Flags().StringVar(&subject, "subject", "", "Subject snapshot (literal, @file, or - for stdin)")
	cmd.Flags().StringVar(&question, "question", "", "Review question (literal, @file, or - for stdin)")
	cmd.Flags().StringVar(&task, "task", "", "Attach to a task")
	cmd.Flags().StringVar(&container, "container", "", "Attach to a container")
	cmd.Flags().StringVar(&campaign, "campaign", "", "Alias for --container")
	cmd.Flags().StringVar(&reviewAt, "review-at", "", "Absolute review timestamp (forwarded verbatim to the API)")
	cmd.Flags().StringVar(&reviewIn, "in", "", "Relative review duration resolved by the API (for example 7d or 36h)")
	cmd.Flags().StringVar(&meta, "meta", "", "Metadata JSON object")
	cmd.Flags().StringVar(&metaFile, "meta-file", "", "Read metadata JSON from a file or - for stdin")
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func newPromiseListCmd(ready bool) *cobra.Command {
	var owner, state, task, container, campaign string
	var output promiseOutputFlags
	use, short, method := "list", "List promises", "wrkq.promise.list"
	if ready {
		use, short, method = "ready", "List promises ready for review", "wrkq.promise.ready"
	}
	cmd := &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tr, sc, closeFn, err := openMirror(cmd)
			if err != nil {
				return err
			}
			defer closeFn()
			params := map[string]any{}
			if owner != "" {
				params["ownerPrincipalRef"] = owner
			}
			if !ready {
				container, err = promiseContainerAlias(container, campaign)
				if err != nil {
					return err
				}
				if state != "" {
					params["state"] = state
				}
				if task != "" {
					params["task"] = sc.selector(task, false)
				}
				if container != "" {
					params["container"] = sc.selector(container, false)
				}
			}
			if err := addPromisePrincipal(cmd, params); err != nil {
				return err
			}
			raw, err := tr.Call(cmd.Context(), method, params)
			if err != nil {
				return err
			}
			items, err := decodePromiseList(raw)
			if err != nil {
				return err
			}
			return renderPromiseList(cmd, items, output)
		},
	}
	cmd.Flags().StringVar(&owner, "for", "", "Owner principal (bare agent slug or agent:<id>)")
	if !ready {
		cmd.Flags().StringVar(&state, "state", "", "Filter by open, resolved, abandoned, or all")
		cmd.Flags().StringVar(&task, "task", "", "Filter by attached task")
		cmd.Flags().StringVar(&container, "container", "", "Filter by attached container")
		cmd.Flags().StringVar(&campaign, "campaign", "", "Alias for --container")
	}
	addPromiseOutputFlags(cmd, &output, true)
	return cmd
}

func newPromiseEditCmd() *cobra.Command {
	var subject, question, reviewAt, reviewIn, meta, metaFile string
	var ifMatch, etag int64
	var output promiseOutputFlags
	cmd := &cobra.Command{
		Use:   "edit <promise>",
		Short: "Edit an open promise",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			claims := &stdinClaims{}
			params := map[string]any{"promise": args[0]}
			if cmd.Flags().Changed("subject") {
				value, err := readTextValue(subject, "--subject", cmd.InOrStdin(), claims)
				if err != nil {
					return err
				}
				params["subject"] = value
			}
			if cmd.Flags().Changed("question") {
				value, err := readNullableTextValue(question, "--question", cmd.InOrStdin(), claims)
				if err != nil {
					return err
				}
				params["reviewQuestion"] = value
			}
			if err := requirePromiseReviewTime(reviewAt, reviewIn, false); err != nil {
				return err
			}
			if reviewAt != "" {
				params["reviewAt"] = reviewAt
			}
			if reviewIn != "" {
				params["reviewIn"] = reviewIn
			}
			changedMeta, _, metaValue, err := readMetaValue(meta, metaFile, cmd.InOrStdin(), claims)
			if err != nil {
				return err
			}
			if changedMeta {
				params["meta"] = metaValue
			}
			if len(params) == 1 {
				return fmt.Errorf("at least one edit field is required")
			}
			match, err := promiseIfMatch(cmd, ifMatch, etag)
			if err != nil {
				return err
			}
			if match > 0 {
				params["ifMatch"] = match
			}
			return callPromiseMutation(cmd, "wrkq.promise.edit", params, output)
		},
	}
	cmd.Flags().StringVar(&subject, "subject", "", "Subject snapshot (literal, @file, or - for stdin)")
	cmd.Flags().StringVar(&question, "question", "", "Review question (literal, @file, or - for stdin; empty clears)")
	cmd.Flags().StringVar(&reviewAt, "review-at", "", "Absolute review timestamp (forwarded verbatim to the API)")
	cmd.Flags().StringVar(&reviewIn, "in", "", "Relative review duration resolved by the API")
	cmd.Flags().StringVar(&meta, "meta", "", "Metadata JSON object")
	cmd.Flags().StringVar(&metaFile, "meta-file", "", "Read metadata JSON from a file or - for stdin")
	addPromiseETagFlags(cmd, &ifMatch, &etag)
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func newPromiseReviewCmd(verb string) *cobra.Command {
	var reviewAt, reviewIn, note string
	var ifMatch, etag int64
	var output promiseOutputFlags
	short := map[string]string{
		"renew":   "Record a review and choose the next review time",
		"resolve": "Resolve a promise after review",
		"abandon": "Deliberately abandon a promise after review",
	}[verb]
	cmd := &cobra.Command{
		Use:   verb + " <promise>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := requirePromiseReviewTime(reviewAt, reviewIn, verb == "renew"); err != nil {
				return err
			}
			params := map[string]any{"promise": args[0]}
			if reviewAt != "" {
				params["reviewAt"] = reviewAt
			}
			if reviewIn != "" {
				params["reviewIn"] = reviewIn
			}
			if cmd.Flags().Changed("note") {
				value, err := readNullableTextValue(note, "--note", cmd.InOrStdin(), &stdinClaims{})
				if err != nil {
					return err
				}
				params["note"] = value
			}
			match, err := promiseIfMatch(cmd, ifMatch, etag)
			if err != nil {
				return err
			}
			if match > 0 {
				params["ifMatch"] = match
			}
			return callPromiseMutation(cmd, "wrkq.promise."+verb, params, output)
		},
	}
	if verb == "renew" {
		cmd.Flags().StringVar(&reviewAt, "review-at", "", "Absolute review timestamp (forwarded verbatim to the API)")
		cmd.Flags().StringVar(&reviewIn, "in", "", "Relative review duration resolved by the API")
	}
	cmd.Flags().StringVar(&note, "note", "", "Review note (literal, @file, or - for stdin)")
	addPromiseETagFlags(cmd, &ifMatch, &etag)
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func newPromiseRetargetCmd(attach bool) *cobra.Command {
	var task, container, campaign string
	var ifMatch, etag int64
	var output promiseOutputFlags
	verb, short := "detach", "Detach a promise while preserving its subject snapshot"
	if attach {
		verb, short = "attach", "Attach a promise to a task or container"
	}
	cmd := &cobra.Command{
		Use:   verb + " <promise>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			params := map[string]any{"promise": args[0]}
			if attach {
				var err error
				container, err = promiseContainerAlias(container, campaign)
				if err != nil {
					return err
				}
				if (task == "") == (container == "") {
					return fmt.Errorf("exactly one of --task or --container/--campaign is required")
				}
				tr, sc, closeFn, err := openMirror(cmd)
				if err != nil {
					return err
				}
				defer closeFn()
				if task != "" {
					params["task"] = sc.selector(task, false)
				} else {
					params["container"] = sc.selector(container, false)
				}
				match, err := promiseIfMatch(cmd, ifMatch, etag)
				if err != nil {
					return err
				}
				if match > 0 {
					params["ifMatch"] = match
				}
				if err := addPromisePrincipal(cmd, params); err != nil {
					return err
				}
				raw, err := tr.Call(cmd.Context(), "wrkq.promise.attach", params)
				if err != nil {
					return err
				}
				promise, err := decodePromise(raw)
				if err != nil {
					return err
				}
				return renderPromiseSingleton(cmd, promise, output)
			}
			match, err := promiseIfMatch(cmd, ifMatch, etag)
			if err != nil {
				return err
			}
			if match > 0 {
				params["ifMatch"] = match
			}
			return callPromiseMutation(cmd, "wrkq.promise.detach", params, output)
		},
	}
	if attach {
		cmd.Flags().StringVar(&task, "task", "", "Attach to a task")
		cmd.Flags().StringVar(&container, "container", "", "Attach to a container")
		cmd.Flags().StringVar(&campaign, "campaign", "", "Alias for --container")
	}
	addPromiseETagFlags(cmd, &ifMatch, &etag)
	addPromiseOutputFlags(cmd, &output, false)
	return cmd
}

func callPromiseMutation(cmd *cobra.Command, method string, params map[string]any, output promiseOutputFlags) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	if err := addPromisePrincipal(cmd, params); err != nil {
		return err
	}
	raw, err := tr.Call(cmd.Context(), method, params)
	if err != nil {
		return err
	}
	promise, err := decodePromise(raw)
	if err != nil {
		return err
	}
	return renderPromiseSingleton(cmd, promise, output)
}

func addPromisePrincipal(cmd *cobra.Command, params map[string]any) error {
	principal, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	if principal != "" {
		params["principalRef"] = principal
	}
	return nil
}

func promiseContainerAlias(container, campaign string) (string, error) {
	if container != "" && campaign != "" {
		return "", fmt.Errorf("--container and --campaign are aliases; choose one")
	}
	if campaign != "" {
		return campaign, nil
	}
	return container, nil
}

func requirePromiseReviewTime(reviewAt, reviewIn string, required bool) error {
	if reviewAt != "" && reviewIn != "" {
		return fmt.Errorf("--review-at and --in are mutually exclusive")
	}
	if required && reviewAt == "" && reviewIn == "" {
		return fmt.Errorf("exactly one of --review-at or --in is required")
	}
	return nil
}

func addPromiseETagFlags(cmd *cobra.Command, ifMatch, etag *int64) {
	cmd.Flags().Int64Var(ifMatch, "if-match", 0, "Only mutate if the promise etag matches")
	cmd.Flags().Int64Var(etag, "etag", 0, "Alias for --if-match")
}

func promiseIfMatch(cmd *cobra.Command, ifMatch, etag int64) (int64, error) {
	if ifMatch < 0 || etag < 0 {
		return 0, fmt.Errorf("--etag/--if-match must be non-negative")
	}
	if cmd.Flags().Changed("if-match") && cmd.Flags().Changed("etag") && ifMatch != etag {
		return 0, fmt.Errorf("--etag and --if-match disagree")
	}
	if cmd.Flags().Changed("etag") {
		return etag, nil
	}
	return ifMatch, nil
}

func addPromiseOutputFlags(cmd *cobra.Command, flags *promiseOutputFlags, list bool) {
	cmd.Flags().BoolVar(&flags.asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&flags.ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&flags.porcelain, "porcelain", false, "Stable machine-readable output")
	cmd.Flags().BoolVar(&flags.pretty, "pretty", false, "Force human-readable output even when not a TTY")
	_ = list
}

func decodePromise(raw json.RawMessage) (promiseWire, error) {
	var promise promiseWire
	if err := json.Unmarshal(raw, &promise); err != nil {
		return promiseWire{}, err
	}
	return promise, nil
}

func decodePromiseList(raw json.RawMessage) ([]promiseWire, error) {
	var result struct {
		Items []promiseWire `json:"items"`
	}
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result.Items, nil
}

func renderPromiseSingleton(cmd *cobra.Command, promise promiseWire, flags promiseOutputFlags) error {
	mode, stable, err := resolvePromiseOutputMode(cmd, flags, false)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	renderer := render.NewRenderer(out, render.Options{Porcelain: stable})
	switch mode {
	case "json":
		return renderer.RenderJSON(promise)
	case "ndjson":
		return renderer.RenderNDJSON([]interface{}{promise})
	case "yaml":
		return renderer.RenderYAML(promise)
	case "tsv", "table", "human":
		headers, rows := promiseTable([]promiseWire{promise})
		if mode == "tsv" {
			return renderer.RenderTSV(headers, rows)
		}
		return renderer.RenderTable(headers, rows)
	case "raw":
		return renderer.RenderList([]string{promise.ID})
	default:
		return fmt.Errorf("unsupported promise output mode %q", mode)
	}
}

func renderPromiseList(cmd *cobra.Command, promises []promiseWire, flags promiseOutputFlags) error {
	mode, stable, err := resolvePromiseOutputMode(cmd, flags, true)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	renderer := render.NewRenderer(out, render.Options{Porcelain: stable})
	switch mode {
	case "json":
		return renderer.RenderJSON(promises)
	case "ndjson":
		items := make([]interface{}, 0, len(promises))
		for i := range promises {
			items = append(items, promises[i])
		}
		return renderer.RenderNDJSON(items)
	case "yaml":
		return renderer.RenderYAML(promises)
	case "tsv", "table", "human":
		headers, rows := promiseTable(promises)
		if mode == "tsv" {
			return renderer.RenderTSV(headers, rows)
		}
		return renderer.RenderTable(headers, rows)
	case "raw":
		ids := make([]string, 0, len(promises))
		for _, promise := range promises {
			ids = append(ids, promise.ID)
		}
		return renderer.RenderList(ids)
	default:
		return fmt.Errorf("unsupported promise output mode %q", mode)
	}
}

func promiseTable(promises []promiseWire) ([]string, [][]string) {
	headers := []string{"ID", "Owner", "Subject", "ReviewAt", "State", "ReadyFor", "Attachment", "ETag"}
	rows := make([][]string, 0, len(promises))
	for _, promise := range promises {
		attachment := ""
		if promise.SubjectRef != nil {
			attachment = promise.SubjectRef.ID
			if attachment == "" {
				attachment = promise.SubjectRef.Path
			}
		}
		readyFor := ""
		if promise.ReadyFor != nil {
			readyFor = *promise.ReadyFor
		}
		rows = append(rows, []string{
			promise.ID, promise.OwnerPrincipalRef, promise.Subject, promise.ReviewAt,
			promise.State, readyFor, attachment, fmt.Sprint(promise.ETag),
		})
	}
	return headers, rows
}

func resolvePromiseOutputMode(cmd *cobra.Command, flags promiseOutputFlags, list bool) (string, bool, error) {
	selected := 0
	mode := ""
	if flags.asJSON {
		selected++
		mode = "json"
	}
	if flags.ndjson {
		selected++
		mode = "ndjson"
	}
	if selected > 1 {
		return "", false, errors.New("choose only one output mode")
	}
	if flags.pretty {
		return "human", false, nil
	}
	stable := flags.porcelain
	if mode != "" {
		return mode, stable, nil
	}
	if flags.porcelain {
		if list {
			return "ndjson", true, nil
		}
		return "json", true, nil
	}
	if flag := cmd.Flag("output"); flag != nil && flag.Changed {
		mode = strings.ToLower(strings.TrimSpace(flag.Value.String()))
		if mode == "porcelain" {
			if list {
				return "ndjson", true, nil
			}
			return "json", true, nil
		}
		switch mode {
		case "table", "human", "json", "ndjson", "yaml", "tsv", "raw":
			return mode, false, nil
		default:
			return "", false, fmt.Errorf("invalid output mode %q: choose table, human, json, ndjson, porcelain, yaml, tsv, or raw", flag.Value.String())
		}
	}
	if isStdoutTTY(cmd.OutOrStdout()) {
		return "human", false, nil
	}
	if list {
		return "ndjson", false, nil
	}
	return "json", false, nil
}

func renderPromiseDetail(w io.Writer, promise promiseWire) error {
	attachment := "none"
	if promise.SubjectRef != nil {
		attachment = promise.SubjectRef.Type + ":" + promise.SubjectRef.ID
		if promise.SubjectRef.Path != "" {
			attachment += " (" + promise.SubjectRef.Path + ")"
		}
	}
	ready := "sleeping"
	if promise.Ready {
		ready = "ready"
		if promise.ReadyFor != nil {
			ready += " for " + *promise.ReadyFor
		}
	}
	lines := []string{
		"id: " + promise.ID,
		"uuid: " + promise.UUID,
		"owner: " + promise.OwnerPrincipalRef,
		"subject: " + promise.Subject,
		"review_at: " + promise.ReviewAt,
		"attention: " + ready,
		"state: " + promise.State,
		"attachment: " + attachment,
		fmt.Sprintf("etag: %d", promise.ETag),
		"created_by: " + promise.CreatedByPrincipalRef,
	}
	if promise.ReviewQuestion != nil {
		lines = append(lines, "question: "+*promise.ReviewQuestion)
	}
	if promise.LastReviewedAt != nil {
		lines = append(lines, "last_reviewed_at: "+*promise.LastReviewedAt)
	}
	if promise.LastReviewNote != nil {
		lines = append(lines, "last_review_note: "+*promise.LastReviewNote)
	}
	return render.NewRenderer(w, render.Options{}).RenderList(lines)
}
