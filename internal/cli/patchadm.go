package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/patch"
	"github.com/spf13/cobra"
)

var patchAdmCmd = &cobra.Command{
	Use:   "patch",
	Short: "Manage RFC 6902 JSON patches",
	Long: `Commands for creating, validating, and applying RFC 6902 JSON patches
between wrkq state snapshots.

Patches represent semantic changes that can be reviewed, versioned, and
applied in a controlled manner.`,
}

// Create command
var patchCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a patch between two snapshots",
	Long: `Create computes an RFC 6902 JSON Patch that transforms the base snapshot
into the target snapshot.

The patch is normalized to remove redundant operations and uses UUID paths
(not friendly IDs) for all references.`,
	RunE: runPatchCreate,
}

var (
	patchCreateFrom              string
	patchCreateTo                string
	patchCreateOut               string
	patchCreateAllowNonCanonical bool
	patchCreateJSON              bool
)

func init() {
	rootAdmCmd.AddCommand(patchAdmCmd)
	patchAdmCmd.AddCommand(patchCreateCmd)
	patchAdmCmd.AddCommand(patchValidateCmd)
	patchAdmCmd.AddCommand(patchApplyCmd)
	patchAdmCmd.AddCommand(patchRebaseCmd)
	patchAdmCmd.AddCommand(patchSummarizeCmd)

	// Create flags
	patchCreateCmd.Flags().StringVar(&patchCreateFrom, "from", "", "Base snapshot file (required)")
	patchCreateCmd.Flags().StringVar(&patchCreateTo, "to", "", "Target snapshot file (required)")
	patchCreateCmd.Flags().StringVar(&patchCreateOut, "out", "", "Output patch file (required)")
	patchCreateCmd.Flags().BoolVar(&patchCreateAllowNonCanonical, "allow-noncanonical", false, "Skip canonicalization check")
	patchCreateCmd.Flags().BoolVar(&patchCreateJSON, "json", false, "Output result as JSON")
	patchCreateCmd.MarkFlagRequired("from")
	patchCreateCmd.MarkFlagRequired("to")
	patchCreateCmd.MarkFlagRequired("out")

	// Validate flags
	patchValidateCmd.Flags().StringVar(&patchValidatePatch, "patch", "", "Patch file (required)")
	patchValidateCmd.Flags().StringVar(&patchValidateBase, "base", "", "Base snapshot file (required)")
	patchValidateCmd.Flags().BoolVar(&patchValidateStrict, "strict", false, "Exit 4 on any violation")
	patchValidateCmd.Flags().BoolVar(&patchValidateJSON, "json", false, "Output result as JSON")
	patchValidateCmd.MarkFlagRequired("patch")
	patchValidateCmd.MarkFlagRequired("base")

	// Apply flags
	patchApplyCmd.Flags().StringVar(&patchApplyPatch, "patch", "", "Patch file (required)")
	patchApplyCmd.Flags().StringVar(&patchApplyIfMatch, "if-match", "", "Require snapshot_rev to match")
	patchApplyCmd.Flags().BoolVar(&patchApplyDryRun, "dry-run", false, "Validate without writing")
	patchApplyCmd.Flags().BoolVar(&patchApplyStrict, "strict", false, "Enable strict validation")
	patchApplyCmd.Flags().BoolVar(&patchApplyJSON, "json", false, "Output result as JSON")
	patchApplyCmd.MarkFlagRequired("patch")

	// Rebase flags
	patchRebaseCmd.Flags().StringVar(&patchRebasePatch, "patch", "", "Patch file to rebase (required)")
	patchRebaseCmd.Flags().StringVar(&patchRebaseOldBase, "old-base", "", "Original base snapshot (required)")
	patchRebaseCmd.Flags().StringVar(&patchRebaseNewBase, "new-base", "", "New base snapshot to rebase onto (required)")
	patchRebaseCmd.Flags().StringVar(&patchRebaseOut, "out", "", "Output rebased patch file (required)")
	patchRebaseCmd.Flags().BoolVar(&patchRebaseStrictIDs, "strict-ids", false, "Fail on malformed friendly IDs")
	patchRebaseCmd.Flags().BoolVar(&patchRebaseJSON, "json", false, "Output result as JSON")
	patchRebaseCmd.MarkFlagRequired("patch")
	patchRebaseCmd.MarkFlagRequired("old-base")
	patchRebaseCmd.MarkFlagRequired("new-base")
	patchRebaseCmd.MarkFlagRequired("out")

	// Summarize flags
	patchSummarizeCmd.Flags().StringVar(&patchSummarizePatch, "patch", "", "Patch file to summarize (required)")
	patchSummarizeCmd.Flags().StringVar(&patchSummarizeBase, "base", "", "Base snapshot for context (optional)")
	patchSummarizeCmd.Flags().StringVar(&patchSummarizeFormat, "format", "text", "Output format: text, markdown, json")
	patchSummarizeCmd.MarkFlagRequired("patch")
}

func runPatchCreate(cmd *cobra.Command, args []string) error {
	opts := patch.CreateOptions{
		FromPath:          patchCreateFrom,
		ToPath:            patchCreateTo,
		OutputPath:        patchCreateOut,
		AllowNonCanonical: patchCreateAllowNonCanonical,
	}

	result, err := patch.Create(opts)
	if err != nil {
		return exitError(1, err)
	}

	if patchCreateJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return exitError(1, fmt.Errorf("failed to encode result: %w", err))
		}
	} else {
		fmt.Printf("✓ Created patch: %s\n", result.OutputPath)
		fmt.Printf("  operations: %d (add: %d, replace: %d, remove: %d)\n",
			result.OpCount, result.AddCount, result.ReplaceCount, result.RemoveCount)
	}

	return nil
}

// Validate command
var patchValidateCmd = &cobra.Command{
	Use:   "validate",
	Short: "Validate a patch against a base snapshot",
	Long: `Validate loads a patch and base snapshot, applies the patch in memory,
and checks domain invariants.

In strict mode (--strict), any violation causes exit code 4.`,
	RunE: runPatchValidate,
}

var (
	patchValidatePatch  string
	patchValidateBase   string
	patchValidateStrict bool
	patchValidateJSON   bool
)

func runPatchValidate(cmd *cobra.Command, args []string) error {
	opts := patch.ValidateOptions{
		PatchPath: patchValidatePatch,
		BasePath:  patchValidateBase,
		Strict:    patchValidateStrict,
	}

	result, err := patch.Validate(opts)
	if err != nil {
		return exitError(1, err)
	}

	if patchValidateJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return exitError(1, fmt.Errorf("failed to encode result: %w", err))
		}
	} else {
		if result.Valid {
			fmt.Println("✓ Patch is valid")
		} else {
			fmt.Println("✗ Patch validation failed:")
			for _, e := range result.Errors {
				fmt.Printf("  - %s\n", e)
			}
		}
	}

	if !result.Valid && patchValidateStrict {
		return exitError(4, fmt.Errorf("validation failed"))
	}

	return nil
}

// Apply command
var patchApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Apply a patch to the database",
	Long: `Apply loads a patch, applies it to the current database state,
validates the result, and commits the changes.

Use --if-match to require the current snapshot_rev to match before applying.
Use --dry-run to validate without writing changes.`,
	RunE: runPatchApply,
}

var (
	patchApplyPatch   string
	patchApplyIfMatch string
	patchApplyDryRun  bool
	patchApplyStrict  bool
	patchApplyJSON    bool
)

func runPatchApply(cmd *cobra.Command, args []string) error {
	// Load configuration
	cfg, err := config.Load()
	if err != nil {
		return exitError(1, fmt.Errorf("failed to load config: %w", err))
	}

	// Override DB path from flag if provided
	dbPathFlag := cmd.Flag("db").Value.String()
	if dbPathFlag != "" {
		cfg.DBPath = dbPathFlag
	}

	// Open database
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		return exitError(1, fmt.Errorf("failed to open database: %w", err))
	}
	defer database.Close()

	opts := patch.ApplyOptions{
		PatchPath: patchApplyPatch,
		IfMatch:   patchApplyIfMatch,
		DryRun:    patchApplyDryRun,
		Strict:    patchApplyStrict,
	}

	result, err := patch.Apply(database.DB, opts)
	if err != nil {
		// Check if this is a snapshot_rev mismatch (conflict)
		if patchApplyIfMatch != "" {
			return exitError(4, err)
		}
		return exitError(1, err)
	}

	if patchApplyJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return exitError(1, fmt.Errorf("failed to encode result: %w", err))
		}
	} else {
		if result.DryRun {
			fmt.Println("✓ Patch would apply successfully (dry run)")
		} else {
			fmt.Println("✓ Patch applied successfully")
			if result.SnapshotRev != "" {
				fmt.Printf("  new snapshot_rev: %s\n", result.SnapshotRev)
			}
		}
		fmt.Printf("  operations: %d (add: %d, replace: %d, remove: %d)\n",
			result.OpCount, result.AddCount, result.ReplaceCount, result.RemoveCount)
	}

	return nil
}

// Rebase command
var patchRebaseCmd = &cobra.Command{
	Use:   "rebase",
	Short: "Rebase a patch from old base to new base",
	Long: `Rebase transforms a patch created against an old base snapshot to apply
cleanly against a new base snapshot.

When the new base contains friendly IDs that conflict with IDs introduced
by the patch, the conflicting IDs are automatically renumbered to maintain
uniqueness. The --json flag includes a code_rewrites map showing any ID
changes.

Use --strict-ids to fail if any friendly ID doesn't match the standard
pattern (e.g., T-00123).`,
	RunE: runPatchRebase,
}

var (
	patchRebasePatch     string
	patchRebaseOldBase   string
	patchRebaseNewBase   string
	patchRebaseOut       string
	patchRebaseStrictIDs bool
	patchRebaseJSON      bool
)

func runPatchRebase(cmd *cobra.Command, args []string) error {
	opts := patch.RebaseOptions{
		PatchPath:   patchRebasePatch,
		OldBasePath: patchRebaseOldBase,
		NewBasePath: patchRebaseNewBase,
		OutputPath:  patchRebaseOut,
		StrictIDs:   patchRebaseStrictIDs,
	}

	result, err := patch.Rebase(opts)
	if err != nil {
		// Exit 4 for invalid patch or strict ID failure
		if patchRebaseStrictIDs {
			return exitError(4, err)
		}
		return exitError(1, err)
	}

	if patchRebaseJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return exitError(1, fmt.Errorf("failed to encode result: %w", err))
		}
	} else {
		fmt.Printf("✓ Rebased patch: %s\n", result.OutputPath)
		fmt.Printf("  operations: %d (add: %d, replace: %d, remove: %d)\n",
			result.OpCount, result.AddCount, result.ReplaceCount, result.RemoveCount)

		if result.CodeRewrites != nil && len(result.CodeRewrites) > 0 {
			fmt.Println("  ID rewrites:")
			for resourceType, rewrites := range result.CodeRewrites {
				for uuid, rewrite := range rewrites {
					fmt.Printf("    %s %s: %s → %s\n", resourceType, uuid[:8], rewrite.From, rewrite.To)
				}
			}
		}
	}

	return nil
}

// Summarize command
var patchSummarizeCmd = &cobra.Command{
	Use:   "summarize",
	Short: "Generate a human-friendly summary of a patch",
	Long: `Summarize generates a human or agent-friendly summary of a patch
for use in PR descriptions or review.

The optional --base flag provides a snapshot for enriched context
(titles, paths) in the output.

Output formats:
  text     - Simple one-line summary
  markdown - Table with Entity, Op, ID, Path/Title columns
  json     - Structured JSON with counts and details`,
	RunE: runPatchSummarize,
}

var (
	patchSummarizePatch  string
	patchSummarizeBase   string
	patchSummarizeFormat string
)

func runPatchSummarize(cmd *cobra.Command, args []string) error {
	opts := patch.SummarizeOptions{
		PatchPath: patchSummarizePatch,
		BasePath:  patchSummarizeBase,
		Format:    patchSummarizeFormat,
	}

	result, err := patch.Summarize(opts)
	if err != nil {
		return exitError(1, err)
	}

	if patchSummarizeFormat == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(result); err != nil {
			return exitError(1, fmt.Errorf("failed to encode result: %w", err))
		}
	} else {
		fmt.Print(result.Summary)
		if !strings.HasSuffix(result.Summary, "\n") {
			fmt.Println()
		}
	}

	return nil
}
