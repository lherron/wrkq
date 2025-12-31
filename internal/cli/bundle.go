package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lherron/wrkq/internal/bundle"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/selectors"
	"github.com/spf13/cobra"
)

var bundleCmd = &cobra.Command{
	Use:   "bundle",
	Short: "Bundle operations for Git-ops workflow",
	Long:  `Commands for creating and managing PR bundles for the Git-ops workflow.`,
}

var bundleCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a bundle of changes for PR workflow",
	Long: `Create a bundle of changes for PR workflow.

Export tasks touched by a specific actor or time window as a reviewable bundle.
Bundles can be committed to git and applied using wrkqadm bundle apply.`,
	RunE: runBundleCreate,
}

var (
	bundleCreateOut             string
	bundleCreateActor           string
	bundleCreateSince           string
	bundleCreateUntil           string
	bundleCreateProject         string
	bundleCreatePathPrefixes    []string
	bundleCreateIncludeRefs     bool
	bundleCreateWithAttachments bool
	bundleCreateNoEvents        bool
	bundleCreateJSON            bool
	bundleCreatePorcelain       bool
	bundleCreateDryRun          bool
)

func init() {
	rootCmd.AddCommand(bundleCmd)
	bundleCmd.AddCommand(bundleCreateCmd)

	bundleCreateCmd.Flags().StringVar(&bundleCreateOut, "out", ".wrkq", "Output directory for bundle")
	bundleCreateCmd.Flags().StringVar(&bundleCreateActor, "actor", "", "Filter by actor (slug or friendly ID)")
	bundleCreateCmd.Flags().StringVar(&bundleCreateSince, "since", "", "Filter by cursor (event:<id> or ts:<rfc3339>) or RFC3339 timestamp")
	bundleCreateCmd.Flags().StringVar(&bundleCreateUntil, "until", "", "Filter by end timestamp (RFC3339)")
	bundleCreateCmd.Flags().StringVar(&bundleCreateProject, "project", "", "Restrict export to a project (path or UUID)")
	bundleCreateCmd.Flags().StringArrayVar(&bundleCreatePathPrefixes, "path-prefix", nil, "Restrict export to path prefix (repeatable)")
	bundleCreateCmd.Flags().BoolVar(&bundleCreateIncludeRefs, "include-refs", false, "Include refs/ stubs for related tasks outside scope")
	bundleCreateCmd.Flags().BoolVar(&bundleCreateWithAttachments, "with-attachments", false, "Include attachment files")
	bundleCreateCmd.Flags().BoolVar(&bundleCreateNoEvents, "no-events", false, "Skip events.ndjson")
	bundleCreateCmd.Flags().BoolVar(&bundleCreateJSON, "json", false, "Output as JSON")
	bundleCreateCmd.Flags().BoolVar(&bundleCreatePorcelain, "porcelain", false, "Machine-readable output")
	bundleCreateCmd.Flags().BoolVar(&bundleCreateDryRun, "dry-run", false, "Show what would be exported without writing")
}

func runBundleCreate(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Override DB path from flag if provided
	if dbPath := cmd.Flag("db").Value.String(); dbPath != "" {
		cfg.DBPath = dbPath
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer database.Close()

	// Prepare bundle creation options
	opts := bundle.CreateOptions{
		OutputDir:       bundleCreateOut,
		Actor:           bundleCreateActor,
		Since:           bundleCreateSince,
		Until:           bundleCreateUntil,
		WithAttachments: bundleCreateWithAttachments,
		WithEvents:      !bundleCreateNoEvents,
		IncludeRefs:     bundleCreateIncludeRefs,
		Version:         "0.1.0",
		Commit:          "",
		BuildDate:       "",
	}

	// Resolve project scope if provided
	if bundleCreateProject != "" {
		projectSelector := applyProjectRootToPath(cfg, bundleCreateProject, false)
		projectUUID, _, err := selectors.ResolveContainer(database, projectSelector)
		if err != nil {
			return fmt.Errorf("failed to resolve project %q: %w", projectSelector, err)
		}
		var projectPath string
		if err := database.QueryRow("SELECT path FROM v_container_paths WHERE uuid = ?", projectUUID).Scan(&projectPath); err != nil {
			return fmt.Errorf("failed to resolve project path: %w", err)
		}
		opts.ProjectUUID = projectUUID
		opts.ProjectPath = projectPath
	}

	// Normalize path prefixes
	for _, prefix := range bundleCreatePathPrefixes {
		trimmed := applyProjectRootToPath(cfg, prefix, false)
		trimmed = strings.Trim(strings.TrimSpace(trimmed), "/")
		if trimmed == "" {
			continue
		}
		// If project scope is set and prefix is relative, anchor it.
		if opts.ProjectPath != "" && !strings.HasPrefix(trimmed, opts.ProjectPath) {
			trimmed = strings.Trim(opts.ProjectPath+"/"+trimmed, "/")
		}
		opts.PathPrefixes = append(opts.PathPrefixes, trimmed)
	}

	// Validate filters
	if opts.Actor == "" && opts.Since == "" && opts.Until == "" && opts.ProjectPath == "" && len(opts.PathPrefixes) == 0 {
		return fmt.Errorf("at least one filter required (--actor, --since, --until, --project, or --path-prefix)")
	}

	if bundleCreateDryRun {
		// TODO: Implement dry-run preview
		fmt.Fprintf(cmd.OutOrStdout(), "Dry run - would create bundle with:\n")
		fmt.Fprintf(cmd.OutOrStdout(), "  Output: %s\n", opts.OutputDir)
		if opts.Actor != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Actor: %s\n", opts.Actor)
		}
		if opts.Since != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Since: %s\n", opts.Since)
		}
		if opts.Until != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Until: %s\n", opts.Until)
		}
		if opts.ProjectPath != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "  Project: %s\n", opts.ProjectPath)
		}
		if len(opts.PathPrefixes) > 0 {
			fmt.Fprintf(cmd.OutOrStdout(), "  Path prefixes: %s\n", strings.Join(opts.PathPrefixes, ", "))
		}
		if opts.IncludeRefs {
			fmt.Fprintf(cmd.OutOrStdout(), "  Include refs: true\n")
		}
		fmt.Fprintf(cmd.OutOrStdout(), "  With attachments: %v\n", opts.WithAttachments)
		fmt.Fprintf(cmd.OutOrStdout(), "  With events: %v\n", opts.WithEvents)
		return nil
	}

	// Create bundle
	b, err := bundle.Create(database.DB, opts)
	if err != nil {
		return fmt.Errorf("failed to create bundle: %w", err)
	}

	// Output results
	if bundleCreateJSON {
		result := map[string]interface{}{
			"bundle_dir":       b.Dir,
			"tasks_count":      len(b.Tasks),
			"containers_count": len(b.Containers),
			"manifest":         b.Manifest,
		}
		encoder := json.NewEncoder(cmd.OutOrStdout())
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	}

	if bundleCreatePorcelain {
		// Tab-separated: tasks_count containers_count bundle_dir
		fmt.Fprintf(cmd.OutOrStdout(), "%d\t%d\t%s\n",
			len(b.Tasks), len(b.Containers), b.Dir)
		return nil
	}

	// Human-readable output
	fmt.Fprintf(cmd.OutOrStdout(), "✓ Bundle created successfully\n")
	fmt.Fprintf(cmd.OutOrStdout(), "  Location: %s\n", b.Dir)
	fmt.Fprintf(cmd.OutOrStdout(), "  Tasks: %d\n", len(b.Tasks))
	fmt.Fprintf(cmd.OutOrStdout(), "  Containers: %d\n", len(b.Containers))

	if b.Manifest.Actor != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Actor: %s\n", b.Manifest.Actor)
	}
	if b.Manifest.Since != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Since: %s\n", b.Manifest.Since)
	}
	if b.Manifest.SinceCursor != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Since cursor: %s\n", b.Manifest.SinceCursor)
	}
	if b.Manifest.Until != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Until: %s\n", b.Manifest.Until)
	}
	if b.Manifest.Project != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Project: %s\n", b.Manifest.Project)
	}
	if len(b.Manifest.PathPrefixes) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Path prefixes: %s\n", strings.Join(b.Manifest.PathPrefixes, ", "))
	}

	if b.Manifest.WithAttachments {
		fmt.Fprintf(cmd.OutOrStdout(), "  Attachments: included\n")
	}
	if b.Manifest.WithEvents {
		fmt.Fprintf(cmd.OutOrStdout(), "  Events: included\n")
	}
	if b.Manifest.IncludeRefs {
		fmt.Fprintf(cmd.OutOrStdout(), "  Refs: included (%d)\n", b.Manifest.RefCount)
	}

	return nil
}
