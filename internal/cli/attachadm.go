package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/lherron/wrkq/internal/config"
	"github.com/lherron/wrkq/internal/db"
	"github.com/spf13/cobra"
)

var attachAdmCmd = &cobra.Command{
	Use:   "attach",
	Short: "Administrative attachment operations",
	Long:  `Administrative commands for attachment management. These expose filesystem paths and should not be used by agents.`,
}

var attachPathCmd = &cobra.Command{
	Use:   "path <ATTACHMENT-ID|relative_path>",
	Short: "Get absolute filesystem path for an attachment",
	Long: `Resolves an attachment ID or relative path to its absolute filesystem path.

This command exposes raw host filesystem paths and is intended for exporters
and administrative tools. Agents should use 'wrkq attach get/put' instead.`,
	Args: cobra.ExactArgs(1),
	RunE: runAttachPath,
}

var (
	attachPathJSON      bool
	attachPathPorcelain bool
)

type attachPathOutput struct {
	AttachmentID string `json:"attachment_id,omitempty"`
	TaskUUID     string `json:"task_uuid,omitempty"`
	Filename     string `json:"filename,omitempty"`
	RelativePath string `json:"relative_path,omitempty"`
	AbsolutePath string `json:"absolute_path"`
	Exists       bool   `json:"exists"`
	SizeBytes    *int64 `json:"size_bytes,omitempty"`
}

func init() {
	rootAdmCmd.AddCommand(attachAdmCmd)
	attachAdmCmd.AddCommand(attachPathCmd)

	attachPathCmd.Flags().BoolVar(&attachPathJSON, "json", false, "Output as JSON")
	attachPathCmd.Flags().BoolVar(&attachPathPorcelain, "porcelain", false, "Machine-readable output")
}

func runAttachPath(cmd *cobra.Command, args []string) error {
	identifier := args[0]

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

	// Try to resolve as attachment ID first
	var relativePath, taskUUID, filename, attachmentID string
	var sizeBytes *int64

	// Check if it looks like an attachment ID (ATT-xxxxx format)
	if len(identifier) > 4 && identifier[0:4] == "ATT-" {
		// Query by friendly ID
		var size int64
		err = database.QueryRow(`
			SELECT a.id, a.task_uuid, a.filename, a.relative_path, a.size_bytes
			FROM attachments a
			WHERE a.id = ?
		`, identifier).Scan(&attachmentID, &taskUUID, &filename, &relativePath, &size)

		if err != nil {
			return fmt.Errorf("attachment not found: %s", identifier)
		}
		sizeBytes = &size
	} else {
		// Assume it's a relative path
		relativePath = identifier

		// Try to look it up in the database to get additional metadata
		var size int64
		err = database.QueryRow(`
			SELECT a.id, a.task_uuid, a.filename, a.size_bytes
			FROM attachments a
			WHERE a.relative_path = ?
		`, relativePath).Scan(&attachmentID, &taskUUID, &filename, &size)

		if err == nil {
			sizeBytes = &size
		}
		// If not found in DB, that's okay - we can still resolve the path
	}

	// Construct absolute path
	absolutePath := filepath.Join(cfg.AttachDir, relativePath)

	// Check if file exists
	exists := false
	if info, err := os.Stat(absolutePath); err == nil {
		exists = true
		// If we don't have size from DB, get it from filesystem
		if sizeBytes == nil {
			size := info.Size()
			sizeBytes = &size
		}
	}

	// Build output
	output := attachPathOutput{
		AttachmentID: attachmentID,
		TaskUUID:     taskUUID,
		Filename:     filename,
		RelativePath: relativePath,
		AbsolutePath: absolutePath,
		Exists:       exists,
		SizeBytes:    sizeBytes,
	}

	// Render output
	if attachPathJSON {
		encoder := json.NewEncoder(cmd.OutOrStdout())
		if !attachPathPorcelain {
			encoder.SetIndent("", "  ")
		}
		return encoder.Encode(output)
	}

	if attachPathPorcelain {
		fmt.Fprintln(cmd.OutOrStdout(), absolutePath)
		return nil
	}

	// Human-readable output
	fmt.Fprintln(cmd.OutOrStdout(), absolutePath)

	return nil
}
