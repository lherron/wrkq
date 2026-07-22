package store

import (
	"reflect"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/attribution"
	"github.com/lherron/wrkq/internal/db"
)

func TestCommentCreateEnforcesJudgmentKindVocabulary(t *testing.T) {
	const actorUUID = "00000000-0000-4000-8000-0000000000a0"
	dbPath := t.TempDir() + "/typed-comments.db"
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	defer func() { _ = database.Close() }()
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	s := New(database)
	project, err := s.Containers.Create(actorUUID, ContainerCreateParams{Slug: "typed-comments", Kind: "project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	task, err := s.Tasks.Create(actorUUID, CreateParams{
		Slug: "kind-target", Title: "Kind target", ProjectUUID: project.UUID, State: "open", Priority: 2,
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	attr := attribution.Attribution{PrincipalRef: "agent:comment-author"}

	for _, kind := range []string{"blocker", "decision", "postmortem", "digest"} {
		params := CommentCreateParams{TaskUUID: task.UUID, Body: kind}
		setCommentCreateKind(t, &params, kind)
		if _, err := s.Comments.CreateWithAttribution(attr, params); err != nil {
			t.Errorf("create kind %q: %v", kind, err)
		}
	}

	plain := CommentCreateParams{TaskUUID: task.UUID, Body: "[blocker] literal prose, not a marker"}
	if _, err := s.Comments.CreateWithAttribution(attr, plain); err != nil {
		t.Fatalf("create plain comment: %v", err)
	}
	var plainKind any
	if err := database.QueryRow("SELECT kind FROM comments WHERE body = ?", plain.Body).Scan(&plainKind); err != nil {
		t.Fatalf("load plain kind: %v", err)
	}
	if plainKind != nil {
		t.Fatalf("marker-like prose inferred kind %#v, want NULL", plainKind)
	}

	invalid := CommentCreateParams{TaskUUID: task.UUID, Body: "retired mechanical kind"}
	setCommentCreateKind(t, &invalid, "heartbeat")
	_, err = s.Comments.CreateWithAttribution(attr, invalid)
	if err == nil {
		t.Fatal("store accepted invalid comment kind heartbeat")
	}
	for _, want := range []string{"invalid comment kind", "blocker", "decision", "postmortem", "digest"} {
		if !strings.Contains(strings.ToLower(err.Error()), want) {
			t.Errorf("invalid-kind error %q does not contain %q", err, want)
		}
	}
}

func setCommentCreateKind(t *testing.T, params *CommentCreateParams, kind string) {
	t.Helper()
	field := reflect.ValueOf(params).Elem().FieldByName("Kind")
	if !field.IsValid() {
		t.Fatal("CommentCreateParams is missing Kind; typed comments are not implemented")
	}
	switch field.Kind() {
	case reflect.String:
		field.SetString(kind)
	case reflect.Pointer:
		value := kind
		field.Set(reflect.ValueOf(&value))
	default:
		t.Fatalf("CommentCreateParams.Kind has unsupported type %s", field.Type())
	}
}
