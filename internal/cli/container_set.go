package cli

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/lherron/wrkq/internal/store"
	"github.com/spf13/cobra"
)

var containerSetCmd = &cobra.Command{
	Use:   "set <container>",
	Short: "Update container fields",
	Long: `Update container configuration fields.

Examples:
  wrkq container set inbox --webhook-urls '["http://localhost/hook/{ticket_id}"]'
  wrkq container set P-00001 --webhook-url http://localhost/hook/{ticket_id}
`,
	Args: cobra.ExactArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runContainerSet),
}

var (
	containerSetWebhookURLs string
	containerSetWebhookURL  []string
	containerSetIfMatch     int64
)

func init() {
	containerCmd.AddCommand(containerSetCmd)

	containerSetCmd.Flags().StringVar(&containerSetWebhookURLs, "webhook-urls", "", "Webhook URLs JSON array")
	containerSetCmd.Flags().StringArrayVar(&containerSetWebhookURL, "webhook-url", nil, "Webhook URL (repeatable)")
	containerSetCmd.Flags().Int64Var(&containerSetIfMatch, "if-match", 0, "Conditional update (etag)")
}

func runContainerSet(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	actorUUID := app.ActorUUID

	selector := applyProjectRootToSelector(app.Config, args[0], false)
	containerUUID, containerPath, err := selectors.ResolveContainer(database, selector)
	if err != nil {
		return err
	}

	webhookURLs, hasWebhookURLs, err := collectWebhookURLs(cmd)
	if err != nil {
		return err
	}
	if !hasWebhookURLs {
		return fmt.Errorf("no updates specified")
	}

	payload, err := json.Marshal(webhookURLs)
	if err != nil {
		return fmt.Errorf("failed to encode webhook urls: %w", err)
	}

	fields := map[string]interface{}{
		"webhook_urls": string(payload),
	}

	s := store.New(database)
	_, err = s.Containers.UpdateFields(actorUUID, containerUUID, fields, containerSetIfMatch)
	if err != nil {
		return err
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Updated container: %s\n", containerPath)
	fmt.Fprintf(cmd.OutOrStdout(), "Webhook URLs: %d\n", len(webhookURLs))
	return nil
}

func collectWebhookURLs(cmd *cobra.Command) ([]string, bool, error) {
	var urls []string
	hasWebhookURLs := false

	if cmd.Flags().Changed("webhook-urls") {
		hasWebhookURLs = true
		if err := json.Unmarshal([]byte(containerSetWebhookURLs), &urls); err != nil {
			return nil, false, fmt.Errorf("invalid webhook urls JSON: %w", err)
		}
	}

	if len(containerSetWebhookURL) > 0 {
		hasWebhookURLs = true
		urls = append(urls, containerSetWebhookURL...)
	}

	for i, raw := range urls {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, false, fmt.Errorf("webhook url cannot be empty")
		}
		if !isValidWebhookURL(trimmed) {
			return nil, false, fmt.Errorf("invalid webhook url: %s", trimmed)
		}
		urls[i] = trimmed
	}

	if hasWebhookURLs && urls == nil {
		urls = []string{}
	}

	return urls, hasWebhookURLs, nil
}

func isValidWebhookURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return false
	}
	if parsed.Host == "" {
		return false
	}
	return true
}
