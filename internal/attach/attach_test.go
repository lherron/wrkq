package attach

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestAttachmentMimeTypeComesFromExtensionWithParametersStripped(t *testing.T) {
	// Rows cover each branch of DetectMimeType: no extension, an extension the
	// mime table does not know, a plain mapping, and a mapping whose
	// "; charset=..." parameter must be stripped. Only extensions in Go's
	// built-in mime table are used so system mime.types files cannot change
	// the expected value.
	tests := []struct {
		filename string
		want     string
	}{
		{"document.pdf", "application/pdf"},
		{"image.png", "image/png"},
		{"image.jpg", "image/jpeg"},
		{"page.html", "text/html"},
		{"unknown", "application/octet-stream"},
		{"file.wrkqnotamimetype", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.filename, func(t *testing.T) {
			if got := DetectMimeType(tt.filename); got != tt.want {
				t.Errorf("DetectMimeType(%q) = %q, want %q", tt.filename, got, tt.want)
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

func TestCopiedAttachmentKeepsBytesAndReportsSizeAndSHA256(t *testing.T) {
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

	// The checksum is the SHA-256 of the copied bytes, hex encoded.
	sum := sha256.Sum256(content)
	if want := hex.EncodeToString(sum[:]); checksum != want {
		t.Errorf("CopyFile() checksum = %q, want sha256 %q", checksum, want)
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
