package wrkqapi

// api_attribution_test.go — white-box coverage for the shared write-attribution
// helper attributionFor (daedalus #10261 / #10285, T-05119). The helper records
// the SAME caller-resolved principal legacy would record, falls back to the
// built-in wrkq-system actor ONLY on the truly-empty/default path, and REJECTS a
// non-empty invalid selector rather than silently coercing it to wrkq-system.

import (
	"path/filepath"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func newAttributionAPI(t *testing.T, defaultActor string) *API {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "attr.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	if err := database.Migrate(); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return New(database, nil, defaultActor, "", 0)
}

func TestAttributionFor_ResolvesAndRejects(t *testing.T) {
	noDefault := newAttributionAPI(t, "")

	// Empty selector + no default → wrkq-system via the EXPLICIT default path.
	if attr, err := noDefault.attributionFor(""); err != nil || attr.PrincipalRef != "agent:wrkq-system" {
		t.Errorf("empty selector: want agent:wrkq-system/nil, got %q/%v", attr.PrincipalRef, err)
	}

	// Empty selector + configured default_actor → honor it (NOT wrkq-system).
	withDefault := newAttributionAPI(t, "ops-bot")
	if attr, err := withDefault.attributionFor(""); err != nil || attr.PrincipalRef != "agent:ops-bot" {
		t.Errorf("default_actor honor: want agent:ops-bot/nil, got %q/%v", attr.PrincipalRef, err)
	}

	// Canonical principal ref with no legacy actor row → recorded exactly.
	if attr, err := noDefault.attributionFor("agent:flag-principal"); err != nil || attr.PrincipalRef != "agent:flag-principal" {
		t.Errorf("canonical principal: want agent:flag-principal/nil, got %q/%v", attr.PrincipalRef, err)
	}

	// Bare compat slug → agent:<slug>.
	if attr, err := noDefault.attributionFor("bareslug"); err != nil || attr.PrincipalRef != "agent:bareslug" {
		t.Errorf("bare slug: want agent:bareslug/nil, got %q/%v", attr.PrincipalRef, err)
	}

	// The built-in "system:" no-actor default sentinels map to the system actor
	// (the explicit intended-default path), NOT an error.
	for _, sentinel := range []string{"system:wrkq", "system:wrkf"} {
		if attr, err := noDefault.attributionFor(sentinel); err != nil || attr.PrincipalRef != "agent:wrkq-system" {
			t.Errorf("system sentinel %q: want agent:wrkq-system/nil, got %q/%v", sentinel, attr.PrincipalRef, err)
		}
	}

	// NON-EMPTY invalid selectors (e.g. a full scope ref) → error (WRKQ_VALIDATION at
	// the RPC boundary), NEVER a silent wrkq-system rewrite that would destroy audit
	// truth.
	for _, bad := range []string{"agent:cody:project:wrkq", "agent:cody:role:reviewer"} {
		if _, err := noDefault.attributionFor(bad); err == nil {
			t.Errorf("invalid actor %q: want error, got nil (must not coerce to wrkq-system)", bad)
		}
	}
}
