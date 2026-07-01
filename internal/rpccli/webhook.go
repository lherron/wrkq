package rpccli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"
)

// newWebhookCmd mirrors `wrkq webhook` (list / add / rm) — the GLOBAL webhook
// subscriptions stored on the singleton root container and inherited by every
// project. This is a DEDICATED RPC family (wrkq.webhook.add / remove / listView,
// T-05119 daedalus #10211): root resolution, URL validation, the idempotent
// add/remove delta, the webhook_urls write + attribution, and the
// container.updated event are ALL server-owned. The mirror owns only TTY vs
// non-TTY output rendering.
//
// No project scoping: legacy webhook always targets the root, so the mirror never
// touches --project. No --if-match flag: legacy has no CAS here, so the mirror
// never sends expectEtag and the server defaults to legacy last-writer-wins. The
// server method DOES accept an optional expectEtag for raw RPC callers, but the
// mirror deliberately omits it to stay byte-for-byte with legacy.
func newWebhookCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "webhook",
		Short: "Manage global webhook subscriptions (apply to all projects)",
		Long: `Manage global webhook subscriptions.

Global webhooks live on the internal root container and are inherited by every
project, so a sink registered here fires for all projects without per-project
duplication. Per-project or per-container webhooks are managed with
'wrkq container set --webhook-url'.

Examples:
  wrkq webhook list
  wrkq webhook add http://127.0.0.1:18451/api/webhooks/wrkq
  wrkq webhook rm  http://127.0.0.1:18451/api/webhooks/wrkq`,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List global webhook URLs",
		Args:  cobra.NoArgs,
		RunE:  func(c *cobra.Command, _ []string) error { return runWebhookList(c) },
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "add <url>",
		Short: "Add a global webhook URL",
		Args:  cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runWebhookMutate(c, "wrkq.webhook.add", args[0], "Added")
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:     "rm <url>",
		Aliases: []string{"remove"},
		Short:   "Remove a global webhook URL",
		Args:    cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return runWebhookMutate(c, "wrkq.webhook.remove", args[0], "Removed")
		},
	})
	return cmd
}

func runWebhookList(cmd *cobra.Command) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	raw, cerr := tr.Call(cmd.Context(), "wrkq.webhook.listView", map[string]any{})
	if cerr != nil {
		return errors.New(rpcMessage(cerr))
	}
	var rows []struct {
		URL string `json:"url"`
	}
	if uerr := json.Unmarshal(raw, &rows); uerr != nil {
		return uerr
	}

	out := cmd.OutOrStdout()
	if !isStdoutTTY(out) {
		// Legacy non-TTY: one compact {"url":<value>} JSON line per URL.
		enc := json.NewEncoder(out)
		for _, r := range rows {
			if err := enc.Encode(map[string]string{"url": r.URL}); err != nil {
				return err
			}
		}
		return nil
	}
	for _, r := range rows {
		fmt.Fprintln(out, r.URL)
	}
	return nil
}

func runWebhookMutate(cmd *cobra.Command, method, url, verb string) error {
	tr, _, closeFn, err := openMirror(cmd)
	if err != nil {
		return err
	}
	defer closeFn()

	params := map[string]any{"url": url}
	actor, err := actorFlag(cmd)
	if err != nil {
		return err
	}
	if actor != "" {
		params["actor"] = actor
	}
	raw, cerr := tr.Call(cmd.Context(), method, params)
	if cerr != nil {
		// RPC *Error.Error() prepends a domain code; strip it for legacy wording
		// (e.g. "invalid webhook url: <url>").
		return errors.New(rpcMessage(cerr))
	}

	// The server returns the legacy MUTATION RESULT in MAP-ALPHABETICAL key order
	// already. Decode the changed flag + count + target for the TTY rendering; the
	// non-TTY path re-indents the server bytes verbatim to preserve key order.
	var res struct {
		Changed bool   `json:"changed"`
		Count   int    `json:"count"`
		Target  string `json:"target"`
	}
	if uerr := json.Unmarshal(raw, &res); uerr != nil {
		return uerr
	}

	out := cmd.OutOrStdout()
	if !isStdoutTTY(out) {
		// Legacy non-TTY: indented JSON (2-space) + trailing newline, key order
		// exactly as the server produced it (map-alphabetical).
		var buf bytes.Buffer
		if ierr := json.Indent(&buf, raw, "", "  "); ierr != nil {
			return ierr
		}
		fmt.Fprintln(out, buf.String())
		return nil
	}

	if !res.Changed {
		fmt.Fprintln(out, "No change")
		return nil
	}
	fmt.Fprintf(out, "%s global webhook: %s\n", verb, res.Target)
	fmt.Fprintf(out, "Global webhook URLs: %d\n", res.Count)
	return nil
}
