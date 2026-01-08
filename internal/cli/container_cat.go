package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var containerCatCmd = &cobra.Command{
	Use:     "cat <path|id>",
	Aliases: []string{"show"},
	Short:   "Print container details",
	Long: `Prints a container's metadata as markdown with YAML front matter.

Examples:
  wrkq container cat inbox
  wrkq container cat P-00001
  wrkq container cat inbox --json
`,
	Args: cobra.ExactArgs(1),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runContainerCat),
}

var (
	containerCatJSON       bool
	containerCatNDJSON     bool
	containerCatPorcelain  bool
	containerCatNoFrontmatter bool
)

func init() {
	containerCmd.AddCommand(containerCatCmd)
	containerCatCmd.Flags().BoolVar(&containerCatJSON, "json", false, "Output as JSON")
	containerCatCmd.Flags().BoolVar(&containerCatNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	containerCatCmd.Flags().BoolVar(&containerCatPorcelain, "porcelain", false, "Machine-readable output")
	containerCatCmd.Flags().BoolVar(&containerCatNoFrontmatter, "no-frontmatter", false, "Print body only without front matter")
}

func runContainerCat(app *appctx.App, cmd *cobra.Command, args []string) error {
	database := app.DB

	type Container struct {
		ID          string   `json:"id"`
		UUID        string   `json:"uuid"`
		Slug        string   `json:"slug"`
		Title       string   `json:"title"`
		Description string   `json:"description"`
		Kind        string   `json:"kind"`
		ParentID    *string  `json:"parent_id,omitempty"`
		ParentUUID  *string  `json:"parent_uuid,omitempty"`
		ParentPath  *string  `json:"parent_path,omitempty"`
		Path        string   `json:"path"`
		WebhookURLs []string `json:"webhook_urls,omitempty"`
		SortIndex   int      `json:"sort_index"`
		Etag        int64    `json:"etag"`
		CreatedAt   string   `json:"created_at"`
		UpdatedAt   string   `json:"updated_at"`
		ArchivedAt  *string  `json:"archived_at,omitempty"`
		CreatedBy   string   `json:"created_by"`
		UpdatedBy   string   `json:"updated_by"`
	}

	selector := applyProjectRootToSelector(app.Config, args[0], false)
	containerUUID, _, err := selectors.ResolveContainer(database, selector)
	if err != nil {
		return err
	}

	// Get full path from view
	var containerPath string
	err = database.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", containerUUID).Scan(&containerPath)
	if err != nil {
		return fmt.Errorf("failed to get container path: %w", err)
	}

	var id, slug, title, description, kind string
	var parentUUID, archivedAt, webhookURLsRaw *string
	var sortIndex int
	var etag int64
	var createdAt, updatedAt string
	var createdByUUID, updatedByUUID string

	err = database.QueryRow(`
		SELECT id, slug, title, description, kind,
		       parent_uuid, webhook_urls, sort_index, etag,
		       created_at, updated_at, archived_at,
		       created_by_actor_uuid, updated_by_actor_uuid
		FROM containers WHERE uuid = ?
	`, containerUUID).Scan(
		&id, &slug, &title, &description, &kind,
		&parentUUID, &webhookURLsRaw, &sortIndex, &etag,
		&createdAt, &updatedAt, &archivedAt,
		&createdByUUID, &updatedByUUID,
	)
	if err != nil {
		return fmt.Errorf("failed to get container: %w", err)
	}

	// Get actor slugs
	var createdBySlug, updatedBySlug string
	database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", createdByUUID).Scan(&createdBySlug)
	database.QueryRow("SELECT slug FROM actors WHERE uuid = ?", updatedByUUID).Scan(&updatedBySlug)

	// Get parent info if parent exists
	var parentID, parentPath *string
	if parentUUID != nil {
		var pID string
		if err := database.QueryRow("SELECT id FROM containers WHERE uuid = ?", *parentUUID).Scan(&pID); err == nil {
			parentID = &pID
		}
		// Parent path is everything before the last segment
		if idx := lastIndexSlash(containerPath); idx >= 0 {
			pp := containerPath[:idx]
			parentPath = &pp
		}
	}

	// Parse webhook URLs
	var webhookURLs []string
	if webhookURLsRaw != nil && *webhookURLsRaw != "" {
		if err := json.Unmarshal([]byte(*webhookURLsRaw), &webhookURLs); err != nil {
			// Ignore parse errors, just leave empty
			webhookURLs = nil
		}
	}

	container := Container{
		ID:          id,
		UUID:        containerUUID,
		Slug:        slug,
		Title:       title,
		Description: description,
		Kind:        kind,
		ParentID:    parentID,
		ParentUUID:  parentUUID,
		ParentPath:  parentPath,
		Path:        containerPath,
		WebhookURLs: webhookURLs,
		SortIndex:   sortIndex,
		Etag:        etag,
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		ArchivedAt:  archivedAt,
		CreatedBy:   createdBySlug,
		UpdatedBy:   updatedBySlug,
	}

	// JSON output
	if containerCatJSON || containerCatNDJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if containerCatJSON && !containerCatPorcelain {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(container)
	}

	// Markdown output
	if !containerCatNoFrontmatter {
		fmt.Fprintln(cmd.OutOrStdout(), "---")
		fmt.Fprintf(cmd.OutOrStdout(), "id: %s\n", container.ID)
		fmt.Fprintf(cmd.OutOrStdout(), "uuid: %s\n", container.UUID)
		fmt.Fprintf(cmd.OutOrStdout(), "slug: %s\n", container.Slug)
		fmt.Fprintf(cmd.OutOrStdout(), "title: %s\n", container.Title)
		fmt.Fprintf(cmd.OutOrStdout(), "kind: %s\n", container.Kind)
		fmt.Fprintf(cmd.OutOrStdout(), "path: %s\n", container.Path)
		if container.ParentID != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "parent_id: %s\n", *container.ParentID)
		}
		if container.ParentUUID != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "parent_uuid: %s\n", *container.ParentUUID)
		}
		if container.ParentPath != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "parent_path: %s\n", *container.ParentPath)
		}
		if len(container.WebhookURLs) > 0 {
			webhooksJSON, _ := json.Marshal(container.WebhookURLs)
			fmt.Fprintf(cmd.OutOrStdout(), "webhook_urls: %s\n", string(webhooksJSON))
		}
		fmt.Fprintf(cmd.OutOrStdout(), "sort_index: %d\n", container.SortIndex)
		fmt.Fprintf(cmd.OutOrStdout(), "etag: %d\n", container.Etag)
		fmt.Fprintf(cmd.OutOrStdout(), "created_at: %s\n", container.CreatedAt)
		fmt.Fprintf(cmd.OutOrStdout(), "updated_at: %s\n", container.UpdatedAt)
		if container.ArchivedAt != nil {
			fmt.Fprintf(cmd.OutOrStdout(), "archived_at: %s\n", *container.ArchivedAt)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "created_by: %s\n", container.CreatedBy)
		fmt.Fprintf(cmd.OutOrStdout(), "updated_by: %s\n", container.UpdatedBy)
		fmt.Fprintln(cmd.OutOrStdout(), "---")
		fmt.Fprintln(cmd.OutOrStdout())
	}

	// Print description
	if container.Description != "" {
		fmt.Fprintln(cmd.OutOrStdout(), container.Description)
	}

	return nil
}

func lastIndexSlash(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}
