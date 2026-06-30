package wrkqapi

// api_attribution_test.go — white-box coverage for the shared write-attribution
// helper attributionFor (T-05381 principal-only). The helper records the EXACT
// agent:<id> principal ref via attribution.NormalizeCanonical: an empty selector
// uses the configured default_principal_ref (and fails when none is configured),
// and any non-canonical caller value — bare slug, system: sentinel, full scope
// ref — is REJECTED rather than silently coerced.

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

	// Empty selector + no default_principal_ref → error (no silent system fallback).
	if _, err := noDefault.attributionFor(""); err == nil {
		t.Errorf("empty selector + no default: want error, got nil")
	}

	// Empty selector + configured default_principal_ref → honor it.
	withDefault := newAttributionAPI(t, "agent:ops-bot")
	if attr, err := withDefault.attributionFor(""); err != nil || attr.PrincipalRef != "agent:ops-bot" {
		t.Errorf("default principal honor: want agent:ops-bot/nil, got %q/%v", attr.PrincipalRef, err)
	}

	// Canonical principal ref → recorded exactly.
	if attr, err := noDefault.attributionFor("agent:flag-principal"); err != nil || attr.PrincipalRef != "agent:flag-principal" {
		t.Errorf("canonical principal: want agent:flag-principal/nil, got %q/%v", attr.PrincipalRef, err)
	}

	// Bare compat slug → REJECTED (NormalizeCanonical does not accept bare slugs).
	if _, err := noDefault.attributionFor("bareslug"); err == nil {
		t.Errorf("bare slug: want rejection, got nil")
	}

	// The legacy "system:" sentinels are NOT valid caller principals → rejected.
	for _, sentinel := range []string{"system:wrkq", "system:wrkf"} {
		if _, err := noDefault.attributionFor(sentinel); err == nil {
			t.Errorf("system sentinel %q: want rejection, got nil", sentinel)
		}
	}

	// NON-EMPTY invalid selectors (full scope refs, actor UUIDs, A-*) → error
	// (WRKQ_VALIDATION at the RPC boundary), NEVER a silent rewrite.
	for _, bad := range []string{
		"agent:cody:project:wrkq", "agent:cody:role:reviewer",
		"A-00001", "00000000-0000-4000-8000-0000000000a0",
	} {
		if _, err := noDefault.attributionFor(bad); err == nil {
			t.Errorf("invalid actor %q: want error, got nil", bad)
		}
	}
}
