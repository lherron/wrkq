package rpccli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/lherron/wrkq/internal/webhooksub"
	"github.com/spf13/cobra"
)

// containerCatModel mirrors the legacy `runContainerCat` local `Container` struct
// (internal/rpccli/container.go) field-for-field and tag-for-tag. The server-owned
// wrkq.container.catView returns this exact shape; the mirror decodes the raw RPC
// result into this struct so every render mode (json/ndjson/porcelain/markdown/raw)
// is produced from the SAME projection, byte-identical to legacy. Field ORDER here
// is load-bearing: legacy encodes the struct directly (struct order, not alpha), so
// json/ndjson/porcelain must preserve it.
type containerCatModel struct {
	ID          string                    `json:"id"`
	UUID        string                    `json:"uuid"`
	Slug        string                    `json:"slug"`
	Title       string                    `json:"title"`
	Description string                    `json:"description"`
	Kind        string                    `json:"kind"`
	ParentID    *string                   `json:"parent_id,omitempty"`
	ParentUUID  *string                   `json:"parent_uuid,omitempty"`
	ParentPath  *string                   `json:"parent_path,omitempty"`
	Path        string                    `json:"path"`
	WebhookURLs []webhooksub.Subscription `json:"webhook_urls,omitempty"`
	SortIndex   int                       `json:"sort_index"`
	Etag        int64                     `json:"etag"`
	CreatedAt   string                    `json:"created_at"`
	UpdatedAt   string                    `json:"updated_at"`
	ArchivedAt  *string                   `json:"archived_at,omitempty"`
	CreatedBy   string                    `json:"created_by"`
	UpdatedBy   string                    `json:"updated_by"`
	Promises    []promiseWire             `json:"promises"`
}

// newContainerCmd mirrors `wrkq container`. `cat` is RPC-backed via the
// server-owned wrkq.container.catView compat projection; `set` uses the dedicated
// wrkq.container.webhookSet compatibility mutation for per-container webhook URL
// updates.
func newContainerCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "container", Short: "Manage containers"}
	cmd.AddCommand(newContainerCatCmd())
	cmd.AddCommand(newContainerSetCmd())
	return cmd
}

func newContainerCatCmd() *cobra.Command {
	var asJSON, ndjson, porcelain, noFrontmatter bool
	cmd := &cobra.Command{
		Use:     "cat <path|id>",
		Aliases: []string{"show"},
		Short:   "Print container details",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerCat(cmd, args, asJSON, ndjson, porcelain, noFrontmatter)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output as JSON")
	cmd.Flags().BoolVar(&ndjson, "ndjson", false, "Output as newline-delimited JSON")
	cmd.Flags().BoolVar(&porcelain, "porcelain", false, "Machine-readable output")
	cmd.Flags().BoolVar(&noFrontmatter, "no-frontmatter", false, "Print body only without front matter")
	return cmd
}

func newContainerSetCmd() *cobra.Command {
	var webhookURLsJSON string
	var webhookURL []string
	var addWebhookURL []string
	var removeWebhookURL []string
	var webhookEvents []string
	var ifMatch int64
	var all bool

	cmd := &cobra.Command{
		Use:   "set <container>",
		Short: "Update container fields",
		Long: `Update container configuration fields.

A webhook subscription is either a bare URL (receives every event family) or a
URL narrowed to one or more event classes: task, workflow, container — or an
exact event name. --webhook-events narrows every URL passed with --webhook-url /
--add-webhook-url in the same invocation; --webhook-urls takes the stored JSON
form directly and may mix bare and narrowed entries.

Examples:
  wrkq container set inbox --webhook-urls '["http://localhost/hook/{ticket_id}"]'
  wrkq container set inbox --webhook-urls '[{"url":"http://localhost/hook","events":["container"]}]'
  wrkq container set P-00001 --webhook-url http://localhost/hook/{ticket_id}
  wrkq container set inbox --webhook-url http://localhost/hook --webhook-events container
  wrkq container set inbox --add-webhook-url http://localhost/hook2
  wrkq container set inbox --add-webhook-url http://localhost/hook2 --webhook-events task,workflow
  wrkq container set inbox --remove-webhook-url http://localhost/old-hook
  wrkq container set --all --remove-webhook-url http://localhost/old-hook
`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContainerSet(cmd, args, containerSetOptions{
				webhookURLsJSON:  webhookURLsJSON,
				webhookURL:       webhookURL,
				addWebhookURL:    addWebhookURL,
				removeWebhookURL: removeWebhookURL,
				webhookEvents:    webhookEvents,
				ifMatch:          ifMatch,
				all:              all,
			})
		},
	}
	cmd.Flags().StringVar(&webhookURLsJSON, "webhook-urls", "", `Webhook subscriptions JSON array; entries are "url" or {"url":...,"events":[...]}`)
	cmd.Flags().StringArrayVar(&webhookURL, "webhook-url", nil, "Webhook URL (repeatable)")
	cmd.Flags().StringArrayVar(&addWebhookURL, "add-webhook-url", nil, "Add webhook URL to existing list (repeatable)")
	cmd.Flags().StringArrayVar(&removeWebhookURL, "remove-webhook-url", nil, "Remove webhook URL from existing list, matched by URL (repeatable)")
	cmd.Flags().StringSliceVar(&webhookEvents, "webhook-events", nil, "Narrow --webhook-url/--add-webhook-url to these event classes (task, workflow, container) or exact event names")
	cmd.Flags().Int64Var(&ifMatch, "if-match", 0, "Conditional update (etag)")
	cmd.Flags().BoolVar(&all, "all", false, "Apply to all containers")
	return cmd
}

type containerSetOptions struct {
	webhookURLsJSON  string
	webhookURL       []string
	addWebhookURL    []string
	removeWebhookURL []string
	webhookEvents    []string
	ifMatch          int64
	all              bool
}

func runContainerSet(cmd *cobra.Command, args []string, opts containerSetOptions) error {
	hasAddRemove := len(opts.addWebhookURL) > 0 || len(opts.removeWebhookURL) > 0

	events, err := normalizeWebhookEvents(opts.webhookEvents)
	if err != nil {
		return err
	}
	if len(events) > 0 && len(opts.webhookURL) == 0 && len(opts.addWebhookURL) == 0 {
		return fmt.Errorf("--webhook-events requires --webhook-url or --add-webhook-url")
	}

	if opts.all {
		if !hasAddRemove {
			return fmt.Errorf("--all requires --add-webhook-url or --remove-webhook-url")
		}
		if len(args) > 0 {
			return fmt.Errorf("--all cannot be used with a container argument")
		}
		adds, err := containerWebhookSubscriptions(opts.addWebhookURL, events)
		if err != nil {
			return err
		}
		return runContainerSetRPC(cmd, map[string]any{
			"all":               true,
			"addWebhookUrls":    adds,
			"removeWebhookUrls": opts.removeWebhookURL,
		}, true)
	}

	if len(args) == 0 {
		return fmt.Errorf("container argument required (or use --all)")
	}

	params := map[string]any{
		"expectEtag": opts.ifMatch,
	}
	replace := false
	if hasAddRemove {
		adds, err := containerWebhookSubscriptions(opts.addWebhookURL, events)
		if err != nil {
			return err
		}
		params["addWebhookUrls"] = adds
		params["removeWebhookUrls"] = opts.removeWebhookURL
	} else {
		urls, hasWebhookURLs, err := collectContainerWebhookURLs(cmd, opts.webhookURLsJSON, opts.webhookURL, events)
		if err != nil {
			return err
		}
		if !hasWebhookURLs {
			return fmt.Errorf("no updates specified")
		}
		replace = true
		params["replace"] = true
		params["webhookUrls"] = urls
	}
	params["replace"] = replace

	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	params["container"] = sc.selector(args[0], false)
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	if actor != "" {
		params["actor"] = actor
	}
	raw, err := tr.Call(cmd.Context(), "wrkq.container.webhookSet", params)
	if err != nil {
		return errors.New(rpcMessage(err))
	}
	return renderContainerSetResult(cmd, raw, false)
}

func runContainerSetRPC(cmd *cobra.Command, params map[string]any, all bool) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	if actor != "" {
		params["actor"] = actor
	}
	raw, err := tr.Call(cmd.Context(), "wrkq.container.webhookSet", params)
	if err != nil {
		return errors.New(rpcMessage(err))
	}
	return renderContainerSetResult(cmd, raw, all)
}

// collectContainerWebhookURLs builds the REPLACEMENT subscription list from
// --webhook-urls (the stored JSON form, mixing bare strings and
// {"url":...,"events":[...]} objects) and --webhook-url (bare URLs, narrowed by
// --webhook-events when given). Validation mirrors the server so a bad list is
// rejected before the RPC.
func collectContainerWebhookURLs(
	cmd *cobra.Command,
	webhookURLsJSON string,
	webhookURL []string,
	events []string,
) ([]webhooksub.Subscription, bool, error) {
	var subs []webhooksub.Subscription
	hasWebhookURLs := false

	if cmd.Flags().Changed("webhook-urls") {
		hasWebhookURLs = true
		parsed, err := webhooksub.DecodeStrict(webhookURLsJSON)
		if err != nil {
			return nil, false, fmt.Errorf("invalid webhook urls JSON: %w", err)
		}
		subs = parsed
	}

	if len(webhookURL) > 0 {
		hasWebhookURLs = true
		fromFlags, err := containerWebhookSubscriptions(webhookURL, events)
		if err != nil {
			return nil, false, err
		}
		subs = append(subs, fromFlags...)
	}

	for i, sub := range subs {
		trimmed := strings.TrimSpace(sub.URL)
		if trimmed == "" {
			return nil, false, fmt.Errorf("webhook url cannot be empty")
		}
		if !isValidContainerWebhookURL(trimmed) {
			return nil, false, fmt.Errorf("invalid webhook url: %s", trimmed)
		}
		normalized, err := normalizeWebhookEvents(sub.Events)
		if err != nil {
			return nil, false, err
		}
		subs[i] = webhooksub.Subscription{URL: trimmed, Events: normalized}
	}

	if hasWebhookURLs && subs == nil {
		subs = []webhooksub.Subscription{}
	}
	return subs, hasWebhookURLs, nil
}

// containerWebhookSubscriptions pairs each bare URL flag value with the shared
// --webhook-events narrowing.
func containerWebhookSubscriptions(urls []string, events []string) ([]webhooksub.Subscription, error) {
	subs := make([]webhooksub.Subscription, 0, len(urls))
	for _, raw := range urls {
		trimmed := strings.TrimSpace(raw)
		if !isValidContainerWebhookURL(trimmed) {
			return nil, fmt.Errorf("invalid webhook url: %s", raw)
		}
		subs = append(subs, webhooksub.Subscription{URL: trimmed, Events: events})
	}
	return subs, nil
}

// normalizeWebhookEvents trims event names and rejects empty ones. Unknown names
// are NOT rejected: the dispatcher matches class names (task/workflow/container,
// plus */all) and exact event names alike, so narrowing the vocabulary here
// would make legal subscriptions unwritable.
func normalizeWebhookEvents(events []string) ([]string, error) {
	var out []string
	for _, ev := range events {
		trimmed := strings.TrimSpace(ev)
		if trimmed == "" {
			return nil, fmt.Errorf("webhook event cannot be empty")
		}
		out = append(out, trimmed)
	}
	return out, nil
}

func renderContainerSetResult(cmd *cobra.Command, raw json.RawMessage, all bool) error {
	out := cmd.OutOrStdout()
	if !isStdoutTTY(out) {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err != nil {
			return err
		}
		fmt.Fprintln(out, buf.String())
		return nil
	}

	if all {
		var res struct {
			Updated int `json:"updated"`
		}
		if err := json.Unmarshal(raw, &res); err != nil {
			return err
		}
		fmt.Fprintf(out, "Updated %d containers\n", res.Updated)
		return nil
	}

	var res struct {
		ContainerPath string `json:"container_path"`
		Count         int    `json:"count"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return err
	}
	fmt.Fprintf(out, "Updated container: %s\n", res.ContainerPath)
	fmt.Fprintf(out, "Webhook URLs: %d\n", res.Count)
	return nil
}

func isValidContainerWebhookURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	return parsed.Host != ""
}

func runContainerCat(cmd *cobra.Command, args []string, asJSON, ndjson, porcelain, noFrontmatter bool) error {
	tr, sc, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	// Legacy: applyProjectRootToSelector(arg, false).
	sref := sc.selector(args[0], false)
	raw, err := tr.Call(cmd.Context(), "wrkq.container.catView", map[string]string{"container": sref})
	if err != nil {
		if re, ok := err.(*Error); ok && re.DomainID == "WRKQ_NOT_FOUND" {
			// Legacy container cat surfaces the raw selectors.ResolveContainer error
			// ("container not found: <ref>"), not the generic "path not found".
			return fmt.Errorf("container not found: %s", sref)
		}
		return err
	}

	var c containerCatModel
	if err := json.Unmarshal(raw, &c); err != nil {
		return err
	}

	out := cmd.OutOrStdout()

	// Legacy emits JSON for --json/--ndjson/--porcelain, or for a non-TTY stdout
	// when --no-frontmatter is not set. Indent is applied unless ndjson/porcelain.
	jsonMode := asJSON || ndjson || porcelain || (!isStdoutTTY(out) && !noFrontmatter)
	if jsonMode {
		enc := json.NewEncoder(out)
		if !ndjson && !porcelain {
			enc.SetIndent("", "  ")
		}
		return enc.Encode(&c)
	}

	// Markdown output. With --no-frontmatter this collapses to "raw" body-only:
	// just the description (if any).
	return renderContainerMarkdown(out, &c, noFrontmatter)
}

// renderContainerMarkdown reproduces legacy runContainerCat's markdown branch
// byte-for-byte: a YAML front matter block (suppressed by noFrontmatter) followed
// by the description. With noFrontmatter set it is the "raw" body-only mode.
func renderContainerMarkdown(out io.Writer, c *containerCatModel, noFrontmatter bool) error {
	if !noFrontmatter {
		fmt.Fprintln(out, "---")
		fmt.Fprintf(out, "id: %s\n", c.ID)
		fmt.Fprintf(out, "uuid: %s\n", c.UUID)
		fmt.Fprintf(out, "slug: %s\n", c.Slug)
		fmt.Fprintf(out, "title: %s\n", c.Title)
		fmt.Fprintf(out, "kind: %s\n", c.Kind)
		fmt.Fprintf(out, "path: %s\n", c.Path)
		if c.ParentID != nil {
			fmt.Fprintf(out, "parent_id: %s\n", *c.ParentID)
		}
		if c.ParentUUID != nil {
			fmt.Fprintf(out, "parent_uuid: %s\n", *c.ParentUUID)
		}
		if c.ParentPath != nil {
			fmt.Fprintf(out, "parent_path: %s\n", *c.ParentPath)
		}
		if len(c.WebhookURLs) > 0 {
			webhooksJSON, _ := json.Marshal(c.WebhookURLs)
			fmt.Fprintf(out, "webhook_urls: %s\n", string(webhooksJSON))
		}
		fmt.Fprintf(out, "sort_index: %d\n", c.SortIndex)
		fmt.Fprintf(out, "etag: %d\n", c.Etag)
		fmt.Fprintf(out, "created_at: %s\n", c.CreatedAt)
		fmt.Fprintf(out, "updated_at: %s\n", c.UpdatedAt)
		if c.ArchivedAt != nil {
			fmt.Fprintf(out, "archived_at: %s\n", *c.ArchivedAt)
		}
		fmt.Fprintf(out, "created_by: %s\n", c.CreatedBy)
		fmt.Fprintf(out, "updated_by: %s\n", c.UpdatedBy)
		fmt.Fprintln(out, "---")
		fmt.Fprintln(out)
	}

	if c.Description != "" {
		fmt.Fprintln(out, c.Description)
	}
	if len(c.Promises) > 0 {
		return renderAttachedPromises(out, c.Promises)
	}
	return nil
}
