package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sort"
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
	searchSort           string
	searchReverse        bool
	searchJSON           bool
	searchNDJSON         bool
	searchPorcelain      bool
	searchHuman          bool
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
	searchCmd.Flags().StringVar(&searchSort, "sort", "relevance", "Sort by relevance, updated_at, or created_at")
	searchCmd.Flags().BoolVar(&searchReverse, "reverse", false, "Reverse sort order")
	searchCmd.Flags().BoolVar(&searchJSON, "json", false, "Output as JSON")
	searchCmd.Flags().BoolVar(&searchNDJSON, "ndjson", false, "Output as newline-delimited JSON")
	searchCmd.Flags().BoolVar(&searchPorcelain, "porcelain", false, "Stable machine-readable output")
	searchCmd.Flags().BoolVar(&searchHuman, "human", false, "Force human-readable output")
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
		Sort:           searchSort,
		Reverse:        searchReverse,
	})
	if err != nil {
		return err
	}

	sel, err := resolveOutputMode(cmd, app.Config, outputShapeList, outputResolveOptions{
		Allow:      []outputMode{outputModeHuman, outputModeJSON, outputModeNDJSON},
		DefaultTTY: outputModeHuman,
	})
	if err != nil {
		return err
	}
	if cmd.Flag("json") == nil {
		switch {
		case (searchJSON && searchNDJSON) || (searchJSON && searchHuman) || (searchNDJSON && searchHuman):
			return fmt.Errorf("choose only one output mode")
		case searchJSON:
			sel = outputSelection{Mode: outputModeJSON, Stable: searchPorcelain}
		case searchNDJSON:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: searchPorcelain}
		case searchHuman:
			sel = outputSelection{Mode: outputModeHuman, Stable: searchPorcelain}
		case searchPorcelain:
			sel = outputSelection{Mode: outputModeNDJSON, Stable: true}
		}
	}

	switch sel.Mode {
	case outputModeJSON:
		return writeJSONOutput(cmd.OutOrStdout(), sel, resp)
	case outputModeNDJSON:
		return writeNDJSONOutput(cmd.OutOrStdout(), resp.Results)
	}

	if resp.Stale {
		fmt.Fprintln(cmd.ErrOrStderr(), paint(colStateStop, fmt.Sprintf("search index is stale by %d event(s)", resp.Status.StaleEventCount)))
	}
	renderSearchResults(cmd.OutOrStdout(), query, resp.Results)
	return nil
}

// colHit lifts matched query terms out of the dim snippet — bold amber, the
// same "attention" accent the tree view uses for open tasks.
const colHit = "1;33"

const (
	searchTitleBudget   = 72
	searchSnippetBudget = 150
)

// renderSearchResults prints results in the wrkq tree aesthetic: scaffolding
// (ids, paths, scores) recedes in dim, the title leads in plain text, and the
// state badge carries color. Each result is a small three-line card —
//
//	T-01752  C-02933  Title of the task                         <open>
//	                  path/to/container  ·  0.016
//	                  …matched snippet with the query highlighted…
//
// The comment column is always reserved (blank for task hits) so titles and
// state badges line up whether or not a row matched on a comment.
func renderSearchResults(w io.Writer, query string, results []search.Result) {
	if len(results) == 0 {
		fmt.Fprintln(w, paint(colDim, fmt.Sprintf("no matches for %q", query)))
		return
	}

	noun := "results"
	if len(results) == 1 {
		noun = "result"
	}
	fmt.Fprintf(w, "%s %s   %s\n\n",
		paint(colDim, "search"),
		query,
		paint(colDim, fmt.Sprintf("%d %s", len(results), noun)))

	// Measure the id / comment-id columns so the title column starts at a
	// fixed offset across every row.
	idW, cidW := 0, 0
	for _, r := range results {
		if n := len(r.TaskID); n > idW {
			idW = n
		}
		if r.CommentID != nil {
			if n := len(*r.CommentID); n > cidW {
				cidW = n
			}
		}
	}

	gutter := idW + 2
	if cidW > 0 {
		gutter += cidW + 2
	}
	indent := strings.Repeat(" ", gutter)
	terms := strings.Fields(strings.ToLower(query))

	for _, r := range results {
		// Primary line: id · comment-id · title · <state>
		var line strings.Builder
		line.WriteString(paint(colDim, padRight(r.TaskID, idW)))
		if cidW > 0 {
			cid := ""
			if r.CommentID != nil {
				cid = *r.CommentID
			}
			line.WriteString("  " + paint(colDim, padRight(cid, cidW)))
		}
		title := firstNonEmpty(r.Title, lastPathSegment(r.Path))
		line.WriteString("  " + truncateRunes(title, searchTitleBudget))
		if r.State != "" {
			line.WriteString("  " + paint(stateColor(r.State), "<"+r.State+">"))
		}
		fmt.Fprintln(w, line.String())

		// Secondary line: path · score, aligned under the title.
		fmt.Fprintf(w, "%s%s\n", indent,
			paint(colDim, fmt.Sprintf("%s  ·  %.3f", r.Path, r.Score)))

		// Tertiary line: the matched snippet, dim with highlighted terms.
		if snip := clipSnippet(r.Snippet, searchSnippetBudget); snip != "" {
			fmt.Fprintf(w, "%s%s\n", indent, highlightTerms(snip, terms))
		}
	}
}

// highlightTerms renders text in dim, lifting each occurrence of a query term
// into the bold-amber hit accent. No-ops to plain dim when color is disabled.
func highlightTerms(text string, terms []string) string {
	if !colorEnabled || len(terms) == 0 {
		return paint(colDim, text)
	}
	lower := strings.ToLower(text)
	var spans [][2]int
	for _, t := range terms {
		if t == "" {
			continue
		}
		for from := 0; ; {
			i := strings.Index(lower[from:], t)
			if i < 0 {
				break
			}
			s := from + i
			spans = append(spans, [2]int{s, s + len(t)})
			from = s + len(t)
		}
	}
	if len(spans) == 0 {
		return paint(colDim, text)
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i][0] < spans[j][0] })

	dim := "\033[" + colDim + "m"
	hit := "\033[" + colHit + "m"
	const reset = "\033[0m"
	var b strings.Builder
	b.WriteString(dim)
	pos := 0
	for _, sp := range spans {
		if sp[0] < pos { // overlapping match already covered
			continue
		}
		b.WriteString(text[pos:sp[0]])
		b.WriteString(reset + hit + text[sp[0]:sp[1]] + reset + dim)
		pos = sp[1]
	}
	b.WriteString(text[pos:])
	b.WriteString(reset)
	return b.String()
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func truncateRunes(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(r[:n-1]) + "…"
}

// clipSnippet limits a snippet to n runes, backing off to the last word
// boundary and appending an ellipsis so the display cut lands cleanly.
func clipSnippet(s string, n int) string {
	if n <= 0 {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if sp := strings.LastIndexByte(cut, ' '); sp > n/2 {
		cut = cut[:sp]
	}
	cut = strings.TrimRight(cut, " ,.;:—-")
	return cut + "…"
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func lastPathSegment(p string) string {
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
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

	embedder := denseEmbedderFromConfig(app.Config)
	ix := indexer.New(app.DB, idx, embedder)
	ix.BatchSize = app.Config.Search.IndexBatchSize
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()

	// Single-attempt pre-flight: if llama-server is down, try one launchctl
	// kickstart of com.praesidium.llama-server. If it doesn't come back, fail
	// the index — per the "single attempt, if it fails the index fails"
	// contract. No-op when the embedder is HashEmbedder or nil.
	if err := embed.EnsureLlamaReady(ctx, embedder, 60*time.Second); err != nil {
		return fmt.Errorf("dense embedder unavailable: %w", err)
	}

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

	embedder := denseEmbedderFromConfig(app.Config)
	ix := indexer.New(app.DB, idx, embedder)
	ix.BatchSize = app.Config.Search.IndexBatchSize
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	if err := embed.EnsureLlamaReady(ctx, embedder, 60*time.Second); err != nil {
		return fmt.Errorf("dense embedder unavailable: %w", err)
	}

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
