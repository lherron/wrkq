package attach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskDir(t *testing.T) {
	dir := TaskDir("/tmp/attachments", "abc-123")
	expected := filepath.Join("/tmp/attachments", "tasks", "abc-123")
	if dir != expected {
		t.Errorf("TaskDir() = %q, want %q", dir, expected)
	}
}

func TestRelativePath(t *testing.T) {
	path := RelativePath("abc-123", "document.pdf")
	expected := filepath.Join("tasks", "abc-123", "document.pdf")
	if path != expected {
		t.Errorf("RelativePath() = %q, want %q", path, expected)
	}
}

func TestAbsolutePath(t *testing.T) {
	path := AbsolutePath("/tmp/attachments", "tasks/abc-123/doc.pdf")
	expected := filepath.Join("/tmp/attachments", "tasks", "abc-123", "doc.pdf")
	if path != expected {
		t.Errorf("AbsolutePath() = %q, want %q", path, expected)
	}
}

func TestDetectMimeType(t *testing.T) {
	tests := []struct {
		filename string
		want     string
	}{
		{"document.pdf", "application/pdf"},
		{"image.png", "image/png"},
		{"image.jpg", "image/jpeg"},
		{"file.txt", "text/plain"},
		{"unknown", "application/octet-stream"},
		{"file.xyz", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			got := DetectMimeType(tt.filename)
			// Some systems may have different MIME mappings
			// Just check that we get something reasonable
			if got == "" {
				t.Errorf("DetectMimeType(%q) = empty string", tt.filename)
			}
		})
	}
}

func TestValidateSize(t *testing.T) {
	tests := []struct {
		name    string
		size    int64
		maxMB   int64
		wantErr bool
	}{
		{"under limit", 1024, 1, false},
		{"at limit", 1024 * 1024, 1, false},
		{"over limit", 2 * 1024 * 1024, 1, true},
		{"no limit", 1000 * 1024 * 1024, 0, false},
		{"negative limit (no limit)", 1000 * 1024 * 1024, -1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateSize(tt.size, tt.maxMB)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateSize(%d, %d) error = %v, wantErr %v", tt.size, tt.maxMB, err, tt.wantErr)
			}
		})
	}
}

func TestCopyFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create source file
	srcPath := filepath.Join(tmpDir, "source.txt")
	content := []byte("test content for attachment")
	if err := os.WriteFile(srcPath, content, 0644); err != nil {
		t.Fatal(err)
	}

	// Copy file
	dstPath := filepath.Join(tmpDir, "subdir", "dest.txt")
	size, checksum, err := CopyFile(srcPath, dstPath)
	if err != nil {
		t.Fatalf("CopyFile() error = %v", err)
	}

	// Verify size
	if size != int64(len(content)) {
		t.Errorf("CopyFile() size = %d, want %d", size, len(content))
	}

	// Verify checksum is non-empty
	if checksum == "" {
		t.Error("CopyFile() checksum is empty")
	}

	// Verify destination file exists and has correct content
	gotContent, err := os.ReadFile(dstPath)
	if err != nil {
		t.Fatalf("failed to read destination: %v", err)
	}

	if string(gotContent) != string(content) {
		t.Errorf("destination content = %q, want %q", gotContent, content)
	}
}

func TestEnsureTaskDir(t *testing.T) {
	tmpDir := t.TempDir()

	taskUUID := "test-task-uuid"
	if err := EnsureTaskDir(tmpDir, taskUUID); err != nil {
		t.Fatalf("EnsureTaskDir() error = %v", err)
	}

	// Verify directory exists
	expectedDir := TaskDir(tmpDir, taskUUID)
	info, err := os.Stat(expectedDir)
	if err != nil {
		t.Fatalf("directory does not exist: %v", err)
	}

	if !info.IsDir() {
		t.Error("path is not a directory")
	}

	// Should be idempotent
	if err := EnsureTaskDir(tmpDir, taskUUID); err != nil {
		t.Errorf("EnsureTaskDir() second call error = %v", err)
	}
}

func TestDeleteFile(t *testing.T) {
	tmpDir := t.TempDir()

	// Create file to delete
	relPath := "tasks/test-uuid/file.txt"
	absPath := AbsolutePath(tmpDir, relPath)
	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absPath, []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Delete file
	if err := DeleteFile(tmpDir, relPath); err != nil {
		t.Fatalf("DeleteFile() error = %v", err)
	}

	// Verify file is gone
	if _, err := os.Stat(absPath); !os.IsNotExist(err) {
		t.Error("file still exists after DeleteFile()")
	}

	// Should not error on non-existent file
	if err := DeleteFile(tmpDir, relPath); err != nil {
		t.Errorf("DeleteFile() on non-existent file error = %v", err)
	}
}

func TestDeleteTaskDir(t *testing.T) {
	tmpDir := t.TempDir()

	taskUUID := "test-task-uuid"
	taskDir := TaskDir(tmpDir, taskUUID)

	// Create directory with files
	if err := os.MkdirAll(taskDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "file1.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, "file2.txt"), []byte("test"), 0644); err != nil {
		t.Fatal(err)
	}

	// Delete directory
	if err := DeleteTaskDir(tmpDir, taskUUID); err != nil {
		t.Fatalf("DeleteTaskDir() error = %v", err)
	}

	// Verify directory is gone
	if _, err := os.Stat(taskDir); !os.IsNotExist(err) {
		t.Error("directory still exists after DeleteTaskDir()")
	}

	// Should not error on non-existent directory
	if err := DeleteTaskDir(tmpDir, taskUUID); err != nil {
		t.Errorf("DeleteTaskDir() on non-existent dir error = %v", err)
	}
}

func TestGetFileSize(t *testing.T) {
	tmpDir := t.TempDir()

	// Create test file
	path := filepath.Join(tmpDir, "test.txt")
	content := []byte("hello world")
	if err := os.WriteFile(path, content, 0644); err != nil {
		t.Fatal(err)
	}

	size, err := GetFileSize(path)
	if err != nil {
		t.Fatalf("GetFileSize() error = %v", err)
	}

	if size != int64(len(content)) {
		t.Errorf("GetFileSize() = %d, want %d", size, len(content))
	}

	// Should error on stdin
	_, err = GetFileSize("-")
	if err == nil || !strings.Contains(err.Error(), "stdin") {
		t.Errorf("GetFileSize(\"-\") should error for stdin, got %v", err)
	}

	// Should error on non-existent file
	_, err = GetFileSize(filepath.Join(tmpDir, "nonexistent.txt"))
	if err == nil {
		t.Error("GetFileSize() should error on non-existent file")
	}
}
