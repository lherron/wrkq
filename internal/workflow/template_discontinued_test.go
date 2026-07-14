package workflow

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lherron/wrkq/internal/db"
)

func TestTemplateDiscontinueGuardOverrideReinstateAndReadback(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskV2TemplateRef, "agent:installer"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate(v2): %v", err)
	}

	if err := svc.DiscontinueTemplate("wrkq-simple-task", "2", "agent:operator"); err != nil {
		t.Fatalf("DiscontinueTemplate: %v", err)
	}
	info, err := svc.ShowTemplateVersion(BuiltinSimpleTaskV2TemplateRef)
	if err != nil {
		t.Fatalf("ShowTemplateVersion: %v", err)
	}
	if info.DiscontinuedAt == "" || info.DiscontinuedBy != "agent:operator" {
		t.Fatalf("discontinued metadata = %+v, want timestamp and agent:operator", info)
	}

	rows, err := svc.ListTemplates()
	if err != nil {
		t.Fatalf("ListTemplates: %v", err)
	}
	if len(rows) != 1 || rows[0]["discontinuedAt"] != info.DiscontinuedAt || rows[0]["discontinuedBy"] != "agent:operator" {
		t.Fatalf("list discontinued metadata = %#v, want show metadata", rows)
	}

	_, err = svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:attacher")
	assertWorkflowValidation(t, err, "non-discontinued template version", "--attach-discontinued")

	inst, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:attacher", AttachTaskOptions{AttachDiscontinued: true})
	if err != nil {
		t.Fatalf("AttachTask override: %v", err)
	}
	if inst.TemplateVersion != "2" {
		t.Fatalf("override instance template = %s@%s, want wrkq-simple-task@2", inst.TemplateID, inst.TemplateVersion)
	}

	err = svc.DiscontinueTemplate("wrkq-simple-task", "2", "agent:operator")
	assertWorkflowValidation(t, err, "non-discontinued template version", "already discontinued")

	// Installing a newer version is intentionally not registry policy: it does
	// not alter the discontinued marker on any older version.
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskV3TemplateRef, "agent:installer"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate(v3): %v", err)
	}
	info, err = svc.ShowTemplateVersion(BuiltinSimpleTaskV2TemplateRef)
	if err != nil || info.DiscontinuedAt == "" {
		t.Fatalf("v2 discontinued state after v3 install = %+v, %v", info, err)
	}

	if err := svc.ReinstateTemplate("wrkq-simple-task", "2"); err != nil {
		t.Fatalf("ReinstateTemplate: %v", err)
	}
	info, err = svc.ShowTemplateVersion(BuiltinSimpleTaskV2TemplateRef)
	if err != nil {
		t.Fatalf("ShowTemplateVersion after reinstate: %v", err)
	}
	if info.DiscontinuedAt != "" || info.DiscontinuedBy != "" {
		t.Fatalf("reinstate left discontinued metadata: %+v", info)
	}

	err = svc.ReinstateTemplate("wrkq-simple-task", "2")
	assertWorkflowValidation(t, err, "discontinued template version", "already non-discontinued")

	secondTaskUUID := "ffffffff-ffff-4fff-ffff-000000000002"
	if _, err := svc.db.Exec(`
		INSERT INTO tasks (
			uuid, slug, title, specification, project_uuid, state, priority, kind,
			created_by_actor_uuid, updated_by_actor_uuid
		)
		SELECT ?, 'act-task-reinstated', 'Action Task Reinstated', specification,
		       project_uuid, 'open', priority, kind, created_by_actor_uuid, updated_by_actor_uuid
		FROM tasks WHERE uuid = ?
	`, secondTaskUUID, taskUUID); err != nil {
		t.Fatalf("insert second task: %v", err)
	}
	if _, err := svc.AttachTask(secondTaskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:attacher"); err != nil {
		t.Fatalf("AttachTask after reinstate: %v", err)
	}
}

func TestTemplateDiscontinueMigrationColumns(t *testing.T) {
	svc, _ := actionFixture(t)
	rows, err := svc.db.Query(`PRAGMA table_info(workflow_templates)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()
	want := map[string]bool{"discontinued_at": false, "discontinued_by": false}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			t.Fatalf("scan table_info: %v", err)
		}
		if _, ok := want[name]; ok {
			want[name] = true
		}
	}
	for column, found := range want {
		if !found {
			t.Errorf("workflow_templates missing %s", column)
		}
	}
}

func TestTemplateDiscontinueSerializesWithAttachAcrossConnections(t *testing.T) {
	attachSvc, firstTaskUUID := actionFixture(t)
	if _, _, err := attachSvc.EnsureBuiltinTemplate(BuiltinSimpleTaskV2TemplateRef, "agent:installer"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate: %v", err)
	}
	database2, err := db.Open(attachSvc.db.Path())
	if err != nil {
		t.Fatalf("db.Open(second connection): %v", err)
	}
	t.Cleanup(func() { _ = database2.Close() })
	curationSvc := NewService(database2)

	// Attach owns the IMMEDIATE transaction first. Discontinue must wait for
	// that attach to commit, so both calls may succeed only in attach-then-mark
	// order.
	attachEntered := make(chan struct{})
	attachRelease := make(chan struct{})
	var attachOnce sync.Once
	attachSvc.now = func() time.Time {
		attachOnce.Do(func() {
			close(attachEntered)
			<-attachRelease
		})
		return time.Date(2026, 7, 14, 12, 0, 0, 0, time.UTC)
	}
	attachDone := make(chan error, 1)
	go func() {
		_, err := attachSvc.AttachTask(firstTaskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:attacher")
		attachDone <- err
	}()
	<-attachEntered
	discontinueDone := make(chan error, 1)
	go func() { discontinueDone <- curationSvc.DiscontinueTemplate("wrkq-simple-task", "2", "agent:curator") }()
	select {
	case err := <-discontinueDone:
		t.Fatalf("discontinue bypassed in-flight attach transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(attachRelease)
	if err := <-attachDone; err != nil {
		t.Fatalf("attach-first interleaving: %v", err)
	}
	if err := <-discontinueDone; err != nil {
		t.Fatalf("discontinue after attach: %v", err)
	}

	if err := curationSvc.ReinstateTemplate("wrkq-simple-task", "2"); err != nil {
		t.Fatalf("reinstate between interleavings: %v", err)
	}
	secondTaskUUID := "ffffffff-ffff-4fff-ffff-000000000003"
	seedSiblingTask(t, attachSvc, firstTaskUUID, secondTaskUUID, "act-task-discontinue-first")

	// Discontinue owns the IMMEDIATE transaction first. The attach waits, then
	// observes the committed marker and must fail validation.
	discontinueEntered := make(chan struct{})
	discontinueRelease := make(chan struct{})
	var discontinueOnce sync.Once
	curationSvc.now = func() time.Time {
		discontinueOnce.Do(func() {
			close(discontinueEntered)
			<-discontinueRelease
		})
		return time.Date(2026, 7, 14, 12, 1, 0, 0, time.UTC)
	}
	discontinueDone = make(chan error, 1)
	go func() { discontinueDone <- curationSvc.DiscontinueTemplate("wrkq-simple-task", "2", "agent:curator") }()
	<-discontinueEntered
	attachDone = make(chan error, 1)
	go func() {
		_, err := attachSvc.AttachTask(secondTaskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:attacher")
		attachDone <- err
	}()
	select {
	case err := <-attachDone:
		t.Fatalf("attach bypassed in-flight discontinue transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(discontinueRelease)
	if err := <-discontinueDone; err != nil {
		t.Fatalf("discontinue-first interleaving: %v", err)
	}
	assertWorkflowValidation(t, <-attachDone, "non-discontinued template version", "--attach-discontinued")
}

func TestSameVersionSupersedePreservesDiscontinuedMarkerAndExistingInstance(t *testing.T) {
	svc, taskUUID := actionFixture(t)
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskV2TemplateRef, "agent:installer"); err != nil {
		t.Fatalf("EnsureBuiltinTemplate: %v", err)
	}
	if _, err := svc.AttachTask(taskUUID, BuiltinSimpleTaskV2TemplateRef, "agent:attacher"); err != nil {
		t.Fatalf("AttachTask before discontinue: %v", err)
	}
	if err := svc.DiscontinueTemplate("wrkq-simple-task", "2", "agent:curator"); err != nil {
		t.Fatalf("DiscontinueTemplate: %v", err)
	}
	before, err := svc.ShowTemplateVersion(BuiltinSimpleTaskV2TemplateRef)
	if err != nil {
		t.Fatalf("ShowTemplateVersion before supersede: %v", err)
	}
	if _, _, err := svc.EnsureBuiltinTemplate(BuiltinSimpleTaskV2TemplateRef, "agent:installer"); err != nil {
		t.Fatalf("idempotent built-in install: %v", err)
	}
	idempotent, err := svc.ShowTemplateVersion(BuiltinSimpleTaskV2TemplateRef)
	if err != nil || idempotent.DiscontinuedAt != before.DiscontinuedAt || idempotent.DiscontinuedBy != before.DiscontinuedBy {
		t.Fatalf("idempotent install changed marker: before=%+v after=%+v err=%v", before, idempotent, err)
	}

	changed := *before.Template
	changed.Description += " (same-version amendment)"
	raw, err := json.Marshal(changed)
	if err != nil {
		t.Fatalf("marshal changed template: %v", err)
	}
	tpl, canonical, err := ParseTemplate(raw)
	if err != nil {
		t.Fatalf("ParseTemplate changed: %v", err)
	}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:installer", nil, true); err != nil {
		t.Fatalf("same-version supersede: %v", err)
	}
	after, err := svc.ShowTemplateVersion(BuiltinSimpleTaskV2TemplateRef)
	if err != nil {
		t.Fatalf("ShowTemplateVersion after supersede: %v", err)
	}
	if after.Hash == before.Hash {
		t.Fatalf("same-version supersede did not change hash")
	}
	if after.DiscontinuedAt != before.DiscontinuedAt || after.DiscontinuedBy != before.DiscontinuedBy {
		t.Fatalf("same-version supersede changed marker: before=%+v after=%+v", before, after)
	}
	catalog := &HookCatalog{SchemaVersion: "1", Hooks: map[string]HookSpec{}}
	if _, err := svc.installTemplateCanonical(tpl, canonical, Hash(canonical), "agent:installer", catalog, false); err != nil {
		t.Fatalf("same-version catalog amendment: %v", err)
	}
	afterCatalog, err := svc.ShowTemplateVersion(BuiltinSimpleTaskV2TemplateRef)
	if err != nil || afterCatalog.DiscontinuedAt != before.DiscontinuedAt || afterCatalog.DiscontinuedBy != before.DiscontinuedBy {
		t.Fatalf("catalog amendment changed marker: before=%+v after=%+v err=%v", before, afterCatalog, err)
	}
	if _, err := svc.Next(taskUUID, "triager"); err != nil {
		t.Fatalf("existing instance became inoperable after template discontinue/supersede: %v", err)
	}
}

func seedSiblingTask(t *testing.T, svc *Service, sourceUUID, taskUUID, slug string) {
	t.Helper()
	if _, err := svc.db.Exec(`
		INSERT INTO tasks (
			uuid, slug, title, specification, project_uuid, state, priority, kind,
			created_by_actor_uuid, updated_by_actor_uuid
		)
		SELECT ?, ?, 'Sibling Action Task', specification, project_uuid, 'open',
		       priority, kind, created_by_actor_uuid, updated_by_actor_uuid
		FROM tasks WHERE uuid = ?
	`, taskUUID, slug, sourceUUID); err != nil {
		t.Fatalf("insert sibling task: %v", err)
	}
}

func assertWorkflowValidation(t *testing.T, err error, expected, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected WRKF_VALIDATION error")
	}
	detail, ok := AsErrorDetail(err)
	if !ok || detail.Code != wrkfCodeValidation || detail.Field != "workflow" || detail.Expected != expected {
		t.Fatalf("error detail = %+v (typed=%v), want workflow WRKF_VALIDATION expected %q", detail, ok, expected)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error = %q, want %q", err, contains)
	}
}
