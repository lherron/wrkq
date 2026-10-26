package admincli

import (
	"errors"

	"github.com/spf13/cobra"
)

// errLegacyActorMovement is returned by the merge data-mover. wrkq has moved to
// principal-only attribution; the legacy cross-database merge relied on the
// actors table and *_actor_uuid columns to remap identities, which no longer
// exist as a write surface. The merge path is therefore hard-gated rather than
// silently producing actor-less rows.
var errLegacyActorMovement = errors.New("legacy actor data movement is no longer supported")

var mergeAdmCmd = &cobra.Command{
	Use:   "merge",
	Short: "Merge a project database into a canonical database (disabled)",
	Long: `Merge a per-project wrkq database into a canonical database.

This data mover relied on legacy actor-table remapping and is no longer
supported under principal-only attribution.`,
	RunE: runMergeAdm,
}

var (
	mergeSourceDB      string
	mergeDestDB        string
	mergeProject       string
	mergePathPrefix    string
	mergeReportPath    string
	mergeDryRun        bool
	mergeSrcAttachDir  string
	mergeDestAttachDir string
)

func init() {
	rootAdmCmd.AddCommand(mergeAdmCmd)

	mergeAdmCmd.Flags().StringVar(&mergeSourceDB, "source", "", "Source database path")
	mergeAdmCmd.Flags().StringVar(&mergeDestDB, "dest", "", "Destination database path (overrides --db)")
	mergeAdmCmd.Flags().StringVar(&mergeProject, "project", "", "Source project selector (slug, path, ID, or UUID)")
	mergeAdmCmd.Flags().StringVar(&mergePathPrefix, "path-prefix", "", "Destination path prefix override")
	mergeAdmCmd.Flags().BoolVar(&mergeDryRun, "dry-run", false, "Validate without writing")
	mergeAdmCmd.Flags().StringVar(&mergeReportPath, "report", "", "Write JSON report to path")
	mergeAdmCmd.Flags().StringVar(&mergeSrcAttachDir, "source-attach-dir", "", "Source attachments directory (defaults to WRKQ_ATTACH_DIR)")
	mergeAdmCmd.Flags().StringVar(&mergeDestAttachDir, "dest-attach-dir", "", "Destination attachments directory (defaults to WRKQ_ATTACH_DIR)")
}

func runMergeAdm(cmd *cobra.Command, args []string) error {
	return exitError(1, errLegacyActorMovement)
}
