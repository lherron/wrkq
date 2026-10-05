package wrkqd

import (
	"database/sql"
	"os"
	"path/filepath"

	"github.com/lherron/wrkq/internal/attribution"
)

// Task mirrors wrkq cat --json output with additional deleted_at metadata.
type Task struct {
	ID                    string     `json:"id"`
	UUID                  string     `json:"uuid"`
	ArtifactDir           string     `json:"artifact_dir"`
	ProjectID             string     `json:"project_id"`
	ProjectUUID           string     `json:"project_uuid"`
	Slug                  string     `json:"slug"`
	Title                 string     `json:"title"`
	State                 string     `json:"state"`
	Priority              int        `json:"priority"`
	Kind                  string     `json:"kind"`
	ParentTaskID          *string    `json:"parent_task_id,omitempty"`
	ParentTaskUUID        *string    `json:"parent_task_uuid,omitempty"`
	AssigneeSlug          *string    `json:"assignee,omitempty"`
	AssigneePrincipalRef  *string    `json:"assignee_principal_ref,omitempty"`
	StartAt               *string    `json:"start_at,omitempty"`
	DueAt                 *string    `json:"due_at,omitempty"`
	Labels                *string    `json:"labels,omitempty"`
	Description           string     `json:"description"`
	Specification         string     `json:"specification"`
	Etag                  int64      `json:"etag"`
	CreatedAt             string     `json:"created_at"`
	UpdatedAt             string     `json:"updated_at"`
	CompletedAt           *string    `json:"completed_at,omitempty"`
	ArchivedAt            *string    `json:"archived_at,omitempty"`
	DeletedAt             *string    `json:"deleted_at,omitempty"`
	CreatedBy             string     `json:"created_by"`
	UpdatedBy             string     `json:"updated_by"`
	CreatedByPrincipalRef string     `json:"created_by_principal_ref,omitempty"`
	UpdatedByPrincipalRef string     `json:"updated_by_principal_ref,omitempty"`
	CreatedByScopeRef     string     `json:"created_by_scope_ref,omitempty"`
	UpdatedByScopeRef     string     `json:"updated_by_scope_ref,omitempty"`
	Comments              []Comment  `json:"comments,omitempty"`
	Relations             []Relation `json:"relations,omitempty"`
}

type Comment struct {
	ID           string `json:"id"`
	CreatedAt    string `json:"created_at"`
	Body         string `json:"body"`
	PrincipalRef string `json:"principal_ref,omitempty"`
	ScopeRef     string `json:"scope_ref,omitempty"`
	Author       string `json:"author,omitempty"`
}

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
