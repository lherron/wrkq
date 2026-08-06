//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/lherron/wrkq/internal/db"
	"github.com/lherron/wrkq/internal/store"
	"github.com/lherron/wrkq/internal/webhooksub"
)

// The CLI is the only write path for webhook subscriptions, so it must be able
// to express BOTH stored forms — a bare URL and a class-narrowed
// {"url":...,"events":[...]} entry. Before T-06823 it unmarshalled []string
// only, which made the class-isolation feature unreachable from the CLI.

func newWebhookCLIFixture(t *testing.T) (dbPath, containerPath string) {
	t.Helper()
	dbPath = t.TempDir() + "/wrkq.db"
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	s := store.New(database)
	actor := "00000000-0000-4000-8000-0000000000a0" // wrkq-system actor seeded by migrations
	project, err := s.Containers.Create(actor, store.ContainerCreateParams{Slug: "hookproj", Kind: "project"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	if _, err := s.Containers.Create(actor, store.ContainerCreateParams{Slug: "wave", ParentUUID: &project.UUID}); err != nil {
		t.Fatalf("create container: %v", err)
	}
	return dbPath, "wave"
}

func runWebhookCLI(t *testing.T, dbPath string, args ...string) (string, error) {
	t.Helper()
	cmd := NewRootCmdFor("wrkq")
	// --project pins the project root so the ambient WRKQ_PROJECT_ROOT/ASP_PROJECT
	// of the developer shell cannot re-scope the fixture paths.
	cmd.SetArgs(append([]string{"--db", dbPath, "--principal-ref", "agent:webhook-test", "--project", "hookproj"}, args...))
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return output.String(), err
}

// storedSubscriptions reads webhook_urls back through `container cat`, which is
// the surface a caller uses to confirm what was written.
func storedSubscriptions(t *testing.T, dbPath, containerPath string) []webhooksub.Subscription {
	t.Helper()
	out, err := runWebhookCLI(t, dbPath, "container", "cat", containerPath, "--json")
	if err != nil {
		t.Fatalf("container cat failed: %v\n%s", err, out)
	}
	var got struct {
		WebhookURLs []webhooksub.Subscription `json:"webhook_urls"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode container cat %q: %v", out, err)
	}
	return got.WebhookURLs
}

// rawWebhookColumn asserts the STORED shape, not just the decoded one: a bare
// URL must stay a plain string so legacy readers keep working.
func rawWebhookColumn(t *testing.T, dbPath, containerPath string) string {
	t.Helper()
	out, err := runWebhookCLI(t, dbPath, "container", "cat", containerPath, "--json")
	if err != nil {
		t.Fatalf("container cat failed: %v\n%s", err, out)
	}
	var got struct {
		WebhookURLs json.RawMessage `json:"webhook_urls"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode container cat %q: %v", out, err)
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, got.WebhookURLs); err != nil {
		t.Fatalf("compact webhook_urls: %v", err)
	}
	return compact.String()
}

func TestContainerSetStructuredWebhookSubscriptions(t *testing.T) {
	t.Run("webhook-urls accepts mixed bare and structured entries", func(t *testing.T) {
		dbPath, path := newWebhookCLIFixture(t)
		payload := `["http://example.com/all",{"url":"http://example.com/containers","events":["container"]}]`
		if out, err := runWebhookCLI(t, dbPath, "container", "set", path, "--webhook-urls", payload); err != nil {
			t.Fatalf("container set --webhook-urls failed: %v\n%s", err, out)
		}
		got := storedSubscriptions(t, dbPath, path)
		want := []webhooksub.Subscription{
			{URL: "http://example.com/all"},
			{URL: "http://example.com/containers", Events: []string{"container"}},
		}
		if !webhooksub.Equal(got, want) {
			t.Fatalf("stored subscriptions = %+v, want %+v", got, want)
		}
		if raw := rawWebhookColumn(t, dbPath, path); raw != `["http://example.com/all",{"url":"http://example.com/containers","events":["container"]}]` {
			t.Fatalf("stored wire form = %s; bare entries must stay plain strings", raw)
		}
	})

	t.Run("webhook-events narrows the paired urls", func(t *testing.T) {
		dbPath, path := newWebhookCLIFixture(t)
		if out, err := runWebhookCLI(t, dbPath, "container", "set", path,
			"--webhook-url", "http://example.com/hook", "--webhook-events", "container,task"); err != nil {
			t.Fatalf("container set --webhook-events failed: %v\n%s", err, out)
		}
		got := storedSubscriptions(t, dbPath, path)
		want := []webhooksub.Subscription{{URL: "http://example.com/hook", Events: []string{"container", "task"}}}
		if !webhooksub.Equal(got, want) {
			t.Fatalf("stored subscriptions = %+v, want %+v", got, want)
		}
	})

	t.Run("add retargets an existing url instead of duplicating it", func(t *testing.T) {
		dbPath, path := newWebhookCLIFixture(t)
		if out, err := runWebhookCLI(t, dbPath, "container", "set", path,
			"--webhook-url", "http://example.com/hook"); err != nil {
			t.Fatalf("seed bare subscription failed: %v\n%s", err, out)
		}
		if out, err := runWebhookCLI(t, dbPath, "container", "set", path,
			"--add-webhook-url", "http://example.com/hook", "--webhook-events", "container"); err != nil {
			t.Fatalf("add with events failed: %v\n%s", err, out)
		}
		got := storedSubscriptions(t, dbPath, path)
		want := []webhooksub.Subscription{{URL: "http://example.com/hook", Events: []string{"container"}}}
		if !webhooksub.Equal(got, want) {
			t.Fatalf("stored subscriptions = %+v, want %+v", got, want)
		}
	})

	t.Run("remove matches a structured entry by url", func(t *testing.T) {
		dbPath, path := newWebhookCLIFixture(t)
		payload := `[{"url":"http://example.com/containers","events":["container"]},"http://example.com/all"]`
		if out, err := runWebhookCLI(t, dbPath, "container", "set", path, "--webhook-urls", payload); err != nil {
			t.Fatalf("seed subscriptions failed: %v\n%s", err, out)
		}
		if out, err := runWebhookCLI(t, dbPath, "container", "set", path,
			"--remove-webhook-url", "http://example.com/containers"); err != nil {
			t.Fatalf("remove failed: %v\n%s", err, out)
		}
		got := storedSubscriptions(t, dbPath, path)
		want := []webhooksub.Subscription{{URL: "http://example.com/all"}}
		if !webhooksub.Equal(got, want) {
			t.Fatalf("stored subscriptions = %+v, want %+v", got, want)
		}
	})

	t.Run("invalid entries are rejected before the rpc", func(t *testing.T) {
		dbPath, path := newWebhookCLIFixture(t)
		if _, err := runWebhookCLI(t, dbPath, "container", "set", path,
			"--webhook-urls", `[{"url":"ftp://example.com/hook","events":["container"]}]`); err == nil {
			t.Fatal("expected invalid scheme in structured entry to be rejected")
		}
		if _, err := runWebhookCLI(t, dbPath, "container", "set", path,
			"--webhook-urls", `[{"url":"http://example.com/hook","events":[" "]}]`); err == nil {
			t.Fatal("expected empty event name to be rejected")
		}
		if _, err := runWebhookCLI(t, dbPath, "container", "set", path,
			"--webhook-events", "container"); err == nil {
			t.Fatal("expected --webhook-events without a url flag to be rejected")
		}
	})
}