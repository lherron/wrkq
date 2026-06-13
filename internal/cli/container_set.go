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
  wrkq container set inbox --add-webhook-url http://localhost/hook2
  wrkq container set inbox --remove-webhook-url http://localhost/old-hook
  wrkq container set --all --remove-webhook-url http://localhost/old-hook
`,
	Args: cobra.MaximumNArgs(1),
	RunE: appctx.WithApp(appctx.WithActor(), runContainerSet),
}

var (
	containerSetWebhookURLs   string
	containerSetWebhookURL    []string
	containerSetAddWebhookURL []string
	containerSetRemWebhookURL []string
	containerSetIfMatch       int64
	containerSetAll           bool
)

func init() {
	containerCmd.AddCommand(containerSetCmd)

	containerSetCmd.Flags().StringVar(&containerSetWebhookURLs, "webhook-urls", "", "Webhook URLs JSON array")
	containerSetCmd.Flags().StringArrayVar(&containerSetWebhookURL, "webhook-url", nil, "Webhook URL (repeatable)")
	containerSetCmd.Flags().StringArrayVar(&containerSetAddWebhookURL, "add-webhook-url", nil, "Add webhook URL to existing list (repeatable)")
	containerSetCmd.Flags().StringArrayVar(&containerSetRemWebhookURL, "remove-webhook-url", nil, "Remove webhook URL from existing list (repeatable)")
	containerSetCmd.Flags().Int64Var(&containerSetIfMatch, "if-match", 0, "Conditional update (etag)")
	containerSetCmd.Flags().BoolVar(&containerSetAll, "all", false, "Apply to all containers")
}

func runContainerSet(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB
	attr := app.Attribution()
	s := store.New(database)

	hasAddRemove := len(containerSetAddWebhookURL) > 0 || len(containerSetRemWebhookURL) > 0

	if containerSetAll {
		if !hasAddRemove {
			return fmt.Errorf("--all requires --add-webhook-url or --remove-webhook-url")
		}
		if len(args) > 0 {
			return fmt.Errorf("--all cannot be used with a container argument")
		}

		// Validate URLs being added
		for _, u := range containerSetAddWebhookURL {
			if !isValidWebhookURL(strings.TrimSpace(u)) {
				return fmt.Errorf("invalid webhook url: %s", u)
			}
		}

		containers, err := s.Containers.ListAll(false)
		if err != nil {
			return fmt.Errorf("failed to list containers: %w", err)
		}

		updated := 0
		for _, c := range containers {
			newURLs, changed := applyWebhookDelta(c.WebhookURLs, containerSetAddWebhookURL, containerSetRemWebhookURL)
			if !changed {
				continue
			}
			payload, err := json.Marshal(newURLs)
			if err != nil {
				return fmt.Errorf("failed to encode webhook urls: %w", err)
			}
			fields := map[string]interface{}{"webhook_urls": string(payload)}
			if _, err := s.Containers.UpdateFieldsWithAttribution(attr, c.UUID, fields, 0); err != nil {
				return fmt.Errorf("failed to update container %s: %w", c.ID, err)
			}
			updated++
		}
		fmt.Fprintf(cmd.OutOrStdout(), "Updated %d containers\n", updated)
		return nil
	}

	if len(args) == 0 {
		return fmt.Errorf("container argument required (or use --all)")
	}

	selector := applyProjectRootToSelector(app.Config, args[0], false)
	containerUUID, containerPath, err := selectors.ResolveContainer(database, selector)
	if err != nil {
		return err
	}

	var webhookURLs []string

	if hasAddRemove {
		// Validate URLs being added
		for _, u := range containerSetAddWebhookURL {
			if !isValidWebhookURL(strings.TrimSpace(u)) {
				return fmt.Errorf("invalid webhook url: %s", u)
			}
		}

		container, err := s.Containers.GetByUUID(containerUUID)
		if err != nil {
			return err
		}
		webhookURLs, _ = applyWebhookDelta(container.WebhookURLs, containerSetAddWebhookURL, containerSetRemWebhookURL)
	} else {
		var hasWebhookURLs bool
		webhookURLs, hasWebhookURLs, err = collectWebhookURLs(cmd)
		if err != nil {
			return err
		}
		if !hasWebhookURLs {
			return fmt.Errorf("no updates specified")
		}
	}

	payload, err := json.Marshal(webhookURLs)
	if err != nil {
		return fmt.Errorf("failed to encode webhook urls: %w", err)
	}

	fields := map[string]interface{}{
		"webhook_urls": string(payload),
	}

	_, err = s.Containers.UpdateFieldsWithAttribution(attr, containerUUID, fields, containerSetIfMatch)
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

// applyWebhookDelta adds/removes URLs from an existing webhook_urls JSON string.
// Returns the new list and whether it changed.
func applyWebhookDelta(existing *string, add, remove []string) ([]string, bool) {
	var urls []string
	if existing != nil && *existing != "" {
		_ = json.Unmarshal([]byte(*existing), &urls)
	}

	removeSet := make(map[string]bool, len(remove))
	for _, u := range remove {
		removeSet[strings.TrimSpace(u)] = true
	}

	// Filter out removed URLs
	filtered := urls[:0]
	for _, u := range urls {
		if !removeSet[u] {
			filtered = append(filtered, u)
		}
	}

	// Add new URLs (avoid duplicates)
	existingSet := make(map[string]bool, len(filtered))
	for _, u := range filtered {
		existingSet[u] = true
	}
	for _, u := range add {
		trimmed := strings.TrimSpace(u)
		if !existingSet[trimmed] {
			filtered = append(filtered, trimmed)
			existingSet[trimmed] = true
		}
	}

	changed := len(filtered) != len(urls)
	if !changed {
		for i := range filtered {
			if filtered[i] != urls[i] {
				changed = true
				break
			}
		}
	}

	if filtered == nil {
		filtered = []string{}
	}

	return filtered, changed
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
