// Package attach handles attachment file I/O and path resolution.
// Files live under attach_dir/tasks/<task_uuid>/<filename>
package attach

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

// Config holds attachment configuration.
type Config struct {
	AttachDir string // Base directory for attachments
	MaxMB     int64  // Maximum attachment size in MB (0 = unlimited)
}

// Metadata represents attachment metadata stored in DB.
type Metadata struct {
	UUID         string
	ID           string // Friendly ID like ATT-00001
	TaskUUID     string
	Filename     string
	RelativePath string
	MimeType     string
	SizeBytes    int64
	Checksum     string
	CreatedAt    string
	CreatedBy    string // Actor UUID
}

// TaskDir returns the canonical directory for a task's attachments.
// Path: attach_dir/tasks/<task_uuid>
func TaskDir(attachDir, taskUUID string) string {
	return filepath.Join(attachDir, "tasks", taskUUID)
}

// RelativePath returns the relative path for an attachment file.
// Relative to attach_dir, e.g., tasks/<task_uuid>/<filename>
func RelativePath(taskUUID, filename string) string {
	return filepath.Join("tasks", taskUUID, filename)
}

// AbsolutePath returns the absolute path for an attachment file.
func AbsolutePath(attachDir, relativePath string) string {
	return filepath.Join(attachDir, relativePath)
}

// EnsureTaskDir creates the task attachment directory if it doesn't exist.
func EnsureTaskDir(attachDir, taskUUID string) error {
	dir := TaskDir(attachDir, taskUUID)
	return os.MkdirAll(dir, 0755)
}

// CopyFile copies a file from src to dst, returning size and checksum.
// If src is "-", reads from stdin.
// If dst is "-", writes to stdout (size/checksum not computed).
func CopyFile(src, dst string) (size int64, checksum string, err error) {
	// Open source
	var srcFile *os.File
	if src == "-" {
		srcFile = os.Stdin
	} else {
		srcFile, err = os.Open(src)
		if err != nil {
			return 0, "", fmt.Errorf("failed to open source: %w", err)
		}
		defer srcFile.Close()
	}

	// Open destination
	var dstFile *os.File
	if dst == "-" {
		dstFile = os.Stdout
	} else {
		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
			return 0, "", fmt.Errorf("failed to create destination directory: %w", err)
		}
		dstFile, err = os.Create(dst)
		if err != nil {
			return 0, "", fmt.Errorf("failed to create destination: %w", err)
		}
		defer dstFile.Close()
	}

	// Copy with checksum computation
	hasher := sha256.New()
	multiWriter := io.MultiWriter(dstFile, hasher)

	size, err = io.Copy(multiWriter, srcFile)
	if err != nil {
		return 0, "", fmt.Errorf("failed to copy file: %w", err)
	}

	checksum = hex.EncodeToString(hasher.Sum(nil))
	return size, checksum, nil
}

// DetectMimeType attempts to detect MIME type from filename extension.
// Falls back to application/octet-stream if unknown.
func DetectMimeType(filename string) string {
	ext := filepath.Ext(filename)
	if ext == "" {
		return "application/octet-stream"
	}

	mimeType := mime.TypeByExtension(ext)
	if mimeType == "" {
		return "application/octet-stream"
	}

	// Strip parameters like charset
	if idx := strings.IndexByte(mimeType, ';'); idx != -1 {
		mimeType = strings.TrimSpace(mimeType[:idx])
	}

	return mimeType
}

// ValidateSize checks if file size is within limits.
func ValidateSize(size int64, maxMB int64) error {
	if maxMB <= 0 {
		return nil // No limit
	}

	maxBytes := maxMB * 1024 * 1024
	if size > maxBytes {
		return fmt.Errorf("attachment size %d bytes exceeds limit of %d MB", size, maxMB)
	}

	return nil
}

// DeleteFile removes an attachment file.
func DeleteFile(attachDir, relativePath string) error {
	absPath := AbsolutePath(attachDir, relativePath)
	if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete attachment: %w", err)
	}
	return nil
}

// DeleteTaskDir removes the entire task attachment directory.
// Used for --purge operations.
func DeleteTaskDir(attachDir, taskUUID string) error {
	dir := TaskDir(attachDir, taskUUID)
	if err := os.RemoveAll(dir); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete task attachment directory: %w", err)
	}
	return nil
}

// GetFileSize returns the size of a file in bytes.
func GetFileSize(path string) (int64, error) {
	if path == "-" {
		return 0, fmt.Errorf("cannot determine size of stdin")
	}

	info, err := os.Stat(path)
	if err != nil {
		return 0, fmt.Errorf("failed to stat file: %w", err)
	}

	return info.Size(), nil
}
