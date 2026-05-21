package cli

import (
	"os"
	"path/filepath"
)

func praesidiumHome() string {
	if root := os.Getenv("PRAESIDIUM_HOME"); root != "" {
		return root
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, "praesidium")
	}
	return "praesidium"
}

func taskArtifactDir(taskID string) string {
	return filepath.Join(praesidiumHome(), "var", "wrkq-artifacts", taskID)
}

func ensureTaskArtifactDir(taskID string) (string, error) {
	dir := taskArtifactDir(taskID)
	return dir, os.MkdirAll(dir, 0o755)
}
