//go:build wrkq_local

package workflow

import (
	"context"

	"database/sql"

	"time"

	"github.com/lherron/wrkq/internal/db"
)

type Service struct {
	db  *db.DB
	now nowFunc
}

// TemplateVersionInfo combines an immutable workflow definition with the
// operator-managed metadata for that installed version.
type TemplateVersionInfo struct {
	Template       *Template `json:"template"`
	Hash           string    `json:"hash"`
	DiscontinuedAt string    `json:"discontinuedAt,omitempty"`
	DiscontinuedBy string    `json:"discontinuedBy,omitempty"`
}

type taskDoc struct {
	UUID          string
	ID            string
	ProjectID     string
	Slug          string
	Title         string
	Description   string
	Specification string
	State         string
	Priority      int
	Kind          string
	Labels        string
	Meta          string
	ETag          int64
	UpdatedAt     string
}

type queryer interface {
	QueryRow(query string, args ...interface{}) *sql.Row
}

type rowsQueryer interface {
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

type workflowEventMetadata struct {
	ID            string
	Seq           int64
	SchemaVersion string
	Type          string
	CreatedAt     string
	Payload       interface{}
}

// instanceScanner is satisfied by both *sql.Row and *sql.Rows.
type instanceScanner interface {
	Scan(dest ...any) error
}

type RoleBindOptions struct {
	TaskSelector string
	InstanceID   string
	Role         string
	PrincipalRef string
	DeliveryRef  string
	Lane         string
	BindingMode  string
}

type evalContext struct {
	Evidence    []Evidence
	Obligations []Obligation
	Checks      map[string]CheckRun
	Facts       map[string]interface{}
	Task        *taskDoc
	State       State
}

type HookExecutionOptions struct {
	Context        context.Context
	TimeoutCeiling time.Duration
}
