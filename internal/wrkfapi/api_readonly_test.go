package wrkfapi_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/wrkfapi"
	"github.com/lherron/wrkq/internal/workflow"
)

// newTestAPI spins up a temp migrated DB and returns a wrkfapi.API ready for use.
func newTestAPI(t *testing.T) *wrkfapi.API {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test db: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("failed to migrate test db: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	svc := workflow.NewService(database)
	return wrkfapi.New(svc)
}

// TestTaskInspect_DTO verifies TaskInspect returns a *workflow.Instance
// with the frozen field shape from contract §5.
func TestTaskInspect_DTO(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()

	inst, err := api.TaskInspect(ctx, "T-00001")
	if err != nil {
		// Expected for a non-existent task — errors_test.go covers the typed error.
		return
	}
	// Frozen field names — verify they compile against the real struct.
	_ = inst.ID
	_ = inst.TaskRef
	_ = inst.TemplateID
	_ = inst.TemplateVersion
	_ = inst.TemplateHash
	_ = inst.Status
	_ = inst.Phase
	_ = inst.Outcome
	_ = inst.Revision
	_ = inst.ContextHash
	_ = inst.TaskDocEtag
	_ = inst.TaskDocHash
	_ = inst.CreatedAt
	_ = inst.UpdatedAt
}

// TestTimeline_DTO verifies Timeline returns []workflow.Event with frozen fields.
func TestTimeline_DTO(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()

	events, err := api.Timeline(ctx, "T-00001")
	if err != nil {
		return
	}
	for _, ev := range events {
		_ = ev.ID
		_ = ev.InstanceID
		_ = ev.Seq
		_ = ev.Type
		_ = ev.Actor
		_ = ev.Role
		_ = ev.ObservedRevision
		_ = ev.NextRevision
		_ = ev.ContextHash
		_ = ev.CreatedAt
	}
}

// TestListTemplates_DTO verifies WorkflowListResult carries []TemplateSummary
// with all frozen fields from contract §5.
func TestListTemplates_DTO(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()

	result, err := api.ListTemplates(ctx)
	if err != nil {
		t.Fatalf("ListTemplates returned error on empty DB: %v", err)
	}

	// Frozen shape assertion: WorkflowListResult has Templates []TemplateSummary
	var _ wrkfapi.WorkflowListResult = result
	_ = result.Templates // []wrkfapi.TemplateSummary

	// On a fresh DB there are no templates — verify the zero case is valid.
	if len(result.Templates) == 0 {
		return
	}

	for _, tmpl := range result.Templates {
		var _ wrkfapi.TemplateSummary = tmpl
		// Frozen field names from §5:
		// TemplateSummary { id, version, hash, kind, description, installedAt, installedBy }
		_ = tmpl.ID
		_ = tmpl.Version
		_ = tmpl.Hash
		_ = tmpl.Kind
		_ = tmpl.Description
		_ = tmpl.InstalledAt
		_ = tmpl.InstalledBy
	}
}

// TestSuggest_DTO verifies SuggestResult carries the frozen fields from §5.
// SuggestResult { transition, required: []EvidenceRequirementSpec, missing: []string,
//
//	checks: []string, warnings: []string }
func TestSuggest_DTO(t *testing.T) {
	api := newTestAPI(t)
	ctx := context.Background()

	result, err := api.Suggest(ctx, "T-00001", "some_transition")
	if err != nil {
		// Expected on empty DB — errors_test.go covers the typed error.
		return
	}

	var _ wrkfapi.SuggestResult = result
	_ = result.Transition                      // string
	_ = result.Required                        // []workflow.EvidenceRequirementSpec
	_ = result.Missing                         // []string
	_ = result.Checks                          // []string
	_ = result.Warnings                        // []string
	_ = ([]workflow.EvidenceRequirementSpec)(result.Required) // frozen element type
}
