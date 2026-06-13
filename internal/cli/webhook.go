package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

// The webhook command manages GLOBAL webhook subscriptions — URLs stored on the
// singleton root container, inherited by every project via the container chain.
// Register a sink here once and it fires for all projects; per-project overrides
// still use `wrkq container set --webhook-url`.
var webhookCmd = &cobra.Command{
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

var webhookListCmd = &cobra.Command{
	Use:   "list",
	Short: "List global webhook URLs",
	Args:  cobra.NoArgs,
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runWebhookList),
}

var webhookAddCmd = &cobra.Command{
	Use:   "add <url>",
	Short: "Add a global webhook URL",
	Args:  cobra.ExactArgs(1),
	RunE:  appctx.WithApp(appctx.WithActor(), runWebhookAdd),
}

var webhookRemoveCmd = &cobra.Command{
	Use:     "rm <url>",
	Aliases: []string{"remove"},
	Short:   "Remove a global webhook URL",
	Args:    cobra.ExactArgs(1),
	RunE:    appctx.WithApp(appctx.WithActor(), runWebhookRemove),
}

func init() {
	rootCmd.AddCommand(webhookCmd)
	webhookCmd.AddCommand(webhookListCmd)
	webhookCmd.AddCommand(webhookAddCmd)
	webhookCmd.AddCommand(webhookRemoveCmd)
}

func runWebhookList(app *appctx.App, cmd *cobra.Command, args []string) error {
	rootUUID, err := store.RootContainerUUID(app.DB)
	if err != nil {
		return err
	}
	s := store.New(app.DB)
	container, err := s.Containers.GetByUUID(rootUUID)
	if err != nil {
		return err
	}
	urls := decodeWebhookURLs(container.WebhookURLs)
	if !isStdoutTTY(cmd.OutOrStdout()) {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, u := range urls {
			if err := encoder.Encode(map[string]string{"url": u}); err != nil {
				return err
			}
		}
		return nil
	}
	for _, u := range urls {
		fmt.Fprintln(cmd.OutOrStdout(), u)
	}
	return nil
}

func runWebhookAdd(app *appctx.App, cmd *cobra.Command, args []string) error {
	url := strings.TrimSpace(args[0])
	if !isValidWebhookURL(url) {
		return fmt.Errorf("invalid webhook url: %s", url)
	}
	return mutateGlobalWebhooks(app, cmd, []string{url}, nil, "Added")
}

func runWebhookRemove(app *appctx.App, cmd *cobra.Command, args []string) error {
	url := strings.TrimSpace(args[0])
	return mutateGlobalWebhooks(app, cmd, nil, []string{url}, "Removed")
}

func mutateGlobalWebhooks(app *appctx.App, cmd *cobra.Command, add, remove []string, verb string) error {
	rootUUID, err := store.RootContainerUUID(app.DB)
	if err != nil {
		return err
	}
	s := store.New(app.DB)
	container, err := s.Containers.GetByUUID(rootUUID)
	if err != nil {
		return err
	}

	newURLs, changed := applyWebhookDelta(container.WebhookURLs, add, remove)
	if !changed {
		if !isStdoutTTY(cmd.OutOrStdout()) {
			encoder := json.NewEncoder(cmd.OutOrStdout())
			encoder.SetIndent("", "  ")
			return encoder.Encode(map[string]interface{}{
				"changed":      false,
				"webhook_urls": newURLs,
			})
		}
		fmt.Fprintln(cmd.OutOrStdout(), "No change")
		return nil
	}

	payload, err := json.Marshal(newURLs)
	if err != nil {
		return fmt.Errorf("failed to encode webhook urls: %w", err)
	}
	fields := map[string]interface{}{"webhook_urls": string(payload)}
	if _, err := s.Containers.UpdateFieldsWithAttribution(app.Attribution(), rootUUID, fields, 0); err != nil {
		return err
	}

	target := strings.Join(append(add, remove...), ", ")
	if !isStdoutTTY(cmd.OutOrStdout()) {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(map[string]interface{}{
			"changed":      true,
			"target":       target,
			"webhook_urls": newURLs,
			"count":        len(newURLs),
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s global webhook: %s\n", verb, target)
	fmt.Fprintf(cmd.OutOrStdout(), "Global webhook URLs: %d\n", len(newURLs))
	return nil
}

func decodeWebhookURLs(raw *string) []string {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil
	}
	var urls []string
	if err := json.Unmarshal([]byte(*raw), &urls); err != nil {
		return nil
	}
	return urls
}
