package cli

import (
	"encoding/json"
	"fmt"

	"github.com/lherron/wrkq/internal/bundle"
	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
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
	bundleCreateOut            string
	bundleCreateActor          string
	bundleCreateSince          string
	bundleCreateUntil          string
	bundleCreateWithAttachments bool
	bundleCreateNoEvents       bool
	bundleCreateJSON           bool
	bundleCreatePorcelain      bool
	bundleCreateDryRun         bool
)

func init() {
	rootCmd.AddCommand(bundleCmd)
	bundleCmd.AddCommand(bundleCreateCmd)

	bundleCreateCmd.Flags().StringVar(&bundleCreateOut, "out", ".wrkq", "Output directory for bundle")
	bundleCreateCmd.Flags().StringVar(&bundleCreateActor, "actor", "", "Filter by actor (slug or friendly ID)")
	bundleCreateCmd.Flags().StringVar(&bundleCreateSince, "since", "", "Filter by start timestamp (RFC3339)")
	bundleCreateCmd.Flags().StringVar(&bundleCreateUntil, "until", "", "Filter by end timestamp (RFC3339)")
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
		Version:         "0.1.0",
		Commit:          "",
		BuildDate:       "",
	}

	// Validate filters
	if opts.Actor == "" && opts.Since == "" && opts.Until == "" {
		return fmt.Errorf("at least one filter required (--actor, --since, or --until)")
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
	if b.Manifest.Until != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "  Until: %s\n", b.Manifest.Until)
	}

	if b.Manifest.WithAttachments {
		fmt.Fprintf(cmd.OutOrStdout(), "  Attachments: included\n")
	}
	if b.Manifest.WithEvents {
		fmt.Fprintf(cmd.OutOrStdout(), "  Events: included\n")
	}

	return nil
}
