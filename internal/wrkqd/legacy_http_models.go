package wrkqd

import (
	"database/sql"
	"os"
	"path/filepath"

	"github.com/lherron/wrkq/internal/attribution"
)

// Relation is the response shape retained by the legacy wrkqd HTTP routes.
type Relation struct {
	Direction   string `json:"direction"`
	Kind        string `json:"kind"`
	TaskID      string `json:"task_id"`
	TaskUUID    string `json:"task_uuid"`
	TaskSlug    string `json:"task_slug"`
	TaskTitle   string `json:"task_title"`
	CreatedAt   string `json:"created_at"`
	CreatedByID string `json:"created_by_id"`
}

func scopeBind(attr attribution.Attribution) interface{} {
	if attr.ScopeRef == "" {
		return nil
	}
	return attr.ScopeRef
}

func valueOrEmpty(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func taskArtifactDir(taskID string) string {
	root := os.Getenv("PRAESIDIUM_HOME")
	if root == "" {
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			root = filepath.Join(home, "praesidium")
		} else {
			root = "praesidium"
		}
	}
	return filepath.Join(root, "var", "wrkq-artifacts", taskID)
}
