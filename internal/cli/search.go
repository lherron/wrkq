package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lherron/wrkq/internal/actors"
	"github.com/lherron/wrkq/internal/cli/appctx"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/search"
	"github.com/lherron/wrkq/internal/search/embed"
	"github.com/lherron/wrkq/internal/search/indexdb"
	"github.com/lherron/wrkq/internal/search/indexer"
	"github.com/spf13/cobra"
)

var searchCmd = &cobra.Command{
	Use:   "search <query> [PATH...]",
	Short: "Search task and comment text",
	Long: `Search task and comment text using a local SQLite sidecar index.
By default, search returns open tasks only. Use --state all to include other
non-deleted states, including archived tasks.`,
	Args: cobra.MinimumNArgs(1),
	RunE: appctx.WithApp(appctx.DefaultOptions(), runSearch),
}

var (
	searchState          string
	searchKind           string
	searchAssignee       string
	searchLimit          int
	searchCandidateLimit int
	searchJSON           bool
	searchNDJSON         bool
	searchExplain        bool
	searchFresh          bool
)

func init() {
	rootCmd.AddCommand(searchCmd)
	searchCmd.Flags().StringVar(&searchState, "state", "", "Filter by state (default open, use all for non-deleted states)")
	searchCmd.Flags().StringVar(&searchKind, "kind", "", "Filter by task kind")
	searchCmd.Flags().StringVar(&searchAssignee, "assignee", "", "Filter by assignee actor")
	searchCmd.Flags().IntVar(&searchLimit, "limit", 20, "Maximum number of task results")
	searchCmd.Flags().IntVar(&searchCandidateLimit, "candidate-limit", 300, "Candidate chunks to retrieve before aggregation")
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Output as JSON")
	searchCmd.Flags().BoolVar(&searchNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	searchCmd.Flags().BoolVar(&searchExplain, "explain", false, "Include ranking diagnostics in JSON output")
	searchCmd.Flags().BoolVar(&searchFresh, "fresh", false, "Fail if the search index is stale")
}

func runSearch(app *appctx.App, cmd *cobra.Command, args []string) error {
	query := args[0]
	paths := applyProjectRootToPaths(app.Config, args[1:], true)

	var assigneeUUID string
	if searchAssignee != "" {
		resolver := actors.NewResolver(app.DB.DB)
		uuid, err := resolver.Resolve(searchAssignee)
		if err != nil {
			return fmt.Errorf("failed to resolve assignee: %w", err)
		}
		assigneeUUID = uuid
	}

	idx, err := openSearchIndex(app.Config)
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()

	svc := search.NewService(app.DB, idx, denseEmbedderFromConfig(app.Config))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	resp, err := svc.Search(ctx, search.Options{
		Query:          query,
		Paths:          paths,
		State:          searchState,
		Kind:           searchKind,
		AssigneeUUID:   assigneeUUID,
		Limit:          searchLimit,
		CandidateLimit: searchCandidateLimit,
		Fresh:          searchFresh,
		Explain:        searchExplain,
	})
	if err != nil {
		return err
	}

	if searchJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(resp)
	}
	if searchNDJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		for _, result := range resp.Results {
			if err := encoder.Encode(result); err != nil {
				return err
			}
		}
		return nil
	}

	if resp.Stale {
		fmt.Fprintf(cmd.ErrOrStderr(), "search index is stale by %d event(s)\n", resp.Status.StaleEventCount)
	}
	for _, result := range resp.Results {
		comment := ""
		if result.CommentID != nil {
			comment = " " + *result.CommentID
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s%s\t%s\t%s\t%.4f\t%s\n", result.TaskID, comment, result.State, result.Path, result.Score, result.Snippet)
	}
	return nil
}

var indexCmd = &cobra.Command{
	Use:   "index",
	Short: "Inspect and rebuild the local search index",
}

var indexStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show local search index status",
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runIndexStatus),
}

var indexRebuildCmd = &cobra.Command{
	Use:   "rebuild",
	Short: "Rebuild the local search index",
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runIndexRebuild),
}

var indexUpdateCmd = &cobra.Command{
	Use:   "update",
	Short: "Index pending canonical changes",
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runIndexUpdate),
}

var indexVacuumCmd = &cobra.Command{
	Use:   "vacuum",
	Short: "Vacuum the local search index",
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runIndexVacuum),
}

var indexPauseCmd = &cobra.Command{
	Use:   "pause",
	Short: "Pause background search indexing",
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runIndexPause),
}

var indexResumeCmd = &cobra.Command{
	Use:   "resume",
	Short: "Resume background search indexing",
	RunE:  appctx.WithApp(appctx.DefaultOptions(), runIndexResume),
}

var (
	indexStatusJSON        bool
	indexRebuildForeground bool
)

func init() {
	rootCmd.AddCommand(indexCmd)
	indexCmd.AddCommand(indexStatusCmd, indexRebuildCmd, indexUpdateCmd, indexVacuumCmd, indexPauseCmd, indexResumeCmd)
	indexStatusCmd.Flags().BoolVar(&indexStatusJSON, "json", false, "Output as JSON")
	indexRebuildCmd.Flags().BoolVar(&indexRebuildForeground, "foreground", false, "Run rebuild in the foreground")
}

func runIndexStatus(app *appctx.App, cmd *cobra.Command, args []string) error {
	idx, err := openSearchIndex(app.Config)
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()

	ix := indexer.New(app.DB, idx, denseEmbedderFromConfig(app.Config))
	ix.BatchSize = app.Config.Search.IndexBatchSize
	status, err := ix.Status()
	if err != nil {
		return err
	}
	if indexStatusJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(status)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "path: %s\n", status.Path)
	fmt.Fprintf(cmd.OutOrStdout(), "status: %s\n", status.Status)
	fmt.Fprintf(cmd.OutOrStdout(), "last_indexed_event_id: %d\n", status.LastIndexedEventID)
	fmt.Fprintf(cmd.OutOrStdout(), "canonical_max_event_id: %d\n", status.CanonicalMaxEventID)
	fmt.Fprintf(cmd.OutOrStdout(), "stale_event_count: %d\n", status.StaleEventCount)
	fmt.Fprintf(cmd.OutOrStdout(), "chunks: %d\n", status.SearchableChunkCount)
	if status.DenseModelID != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "dense_model: %s\n", status.DenseModelID)
		fmt.Fprintf(cmd.OutOrStdout(), "dense_dimension: %d\n", status.DenseDimension)
		fmt.Fprintf(cmd.OutOrStdout(), "dense_vectors: %d\n", status.DenseVectorCount)
	}
	if status.LastError != nil {
		fmt.Fprintf(cmd.OutOrStdout(), "last_error: %s\n", *status.LastError)
	}
	return nil
}

func runIndexRebuild(app *appctx.App, cmd *cobra.Command, args []string) error {
	_ = indexRebuildForeground
	idx, err := openSearchIndex(app.Config)
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()

	ix := indexer.New(app.DB, idx, denseEmbedderFromConfig(app.Config))
	ix.BatchSize = app.Config.Search.IndexBatchSize
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	if err := ix.Rebuild(ctx); err != nil {
		return err
	}
	status, _ := ix.Status()
	fmt.Fprintf(cmd.OutOrStdout(), "rebuilt search index: %d chunks, last event %d\n", status.SearchableChunkCount, status.LastIndexedEventID)
	if status.LastError != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "dense indexing warning: %s\n", *status.LastError)
	}
	return nil
}

func runIndexUpdate(app *appctx.App, cmd *cobra.Command, args []string) error {
	idx, err := openSearchIndex(app.Config)
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()

	ix := indexer.New(app.DB, idx, denseEmbedderFromConfig(app.Config))
	ix.BatchSize = app.Config.Search.IndexBatchSize
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := ix.IndexPending(ctx); err != nil {
		return err
	}
	status, _ := ix.Status()
	fmt.Fprintf(cmd.OutOrStdout(), "updated search index: %d chunks, last event %d\n", status.SearchableChunkCount, status.LastIndexedEventID)
	if status.LastError != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "dense indexing warning: %s\n", *status.LastError)
	}
	return nil
}

func runIndexVacuum(app *appctx.App, cmd *cobra.Command, args []string) error {
	idx, err := openSearchIndex(app.Config)
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()
	if _, err := idx.Exec(`VACUUM`); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "vacuumed search index")
	return nil
}

func runIndexPause(app *appctx.App, cmd *cobra.Command, args []string) error {
	idx, err := openSearchIndex(app.Config)
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()
	if err := idx.SetState("status", "paused"); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "paused search indexing")
	return nil
}

func runIndexResume(app *appctx.App, cmd *cobra.Command, args []string) error {
	idx, err := openSearchIndex(app.Config)
	if err != nil {
		return err
	}
	defer func() { _ = idx.Close() }()
	if err := idx.SetState("status", "ready"); err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), "resumed search indexing")
	return nil
}

func openSearchIndex(cfg *config.Config) (*indexdb.DB, error) {
	if !cfg.Search.Enabled {
		return nil, fmt.Errorf("search is disabled")
	}
	path := cfg.Search.DBPath
	if path == "" {
		path = indexdb.DefaultPath(cfg.DBPath)
	}
	return indexdb.Open(path)
}

func denseEmbedderFromConfig(cfg *config.Config) embed.DenseEmbedder {
	switch strings.ToLower(cfg.Search.DenseProvider) {
	case "", "llama-cpp":
		return &embed.LlamaCPP{
			BaseURL:          cfg.Search.DenseBaseURL,
			Model:            cfg.Search.DenseModel,
			DimensionValue:   cfg.Search.DenseDimension,
			QueryInstruction: cfg.Search.QueryInstruction,
		}
	case "hash":
		return embed.HashEmbedder{Model: "wrkq-hash-test", Dims: cfg.Search.DenseDimension}
	case "none", "off", "disabled":
		return nil
	default:
		return nil
	}
}
