package nodeauth

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseSpecResolvesTokenToExactlyOneNode(t *testing.T) {
	reg, err := ParseSpec("max3=tok-max3,mini.svc=tok-svc")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if !reg.Enabled() {
		t.Fatal("registry should be enabled")
	}

	for token, want := range map[string]string{"tok-max3": "max3", "tok-svc": "mini.svc"} {
		got, ok := reg.Resolve(token)
		if !ok || got != want {
			t.Fatalf("Resolve(%q) = %q,%v; want %q,true", token, got, ok, want)
		}
	}
}

func TestResolveRefusesMissingAndUnknownTokens(t *testing.T) {
	reg, err := ParseSpec("max3=tok-max3")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	for _, token := range []string{"", "tok-other", "tok-max3 "} {
		if node, ok := reg.Resolve(token); ok {
			t.Fatalf("Resolve(%q) resolved to %q; want refusal", token, node)
		}
	}
}

func TestParseSpecRejectsTokenMappedToTwoNodes(t *testing.T) {
	_, err := ParseSpec("max3=shared,lab=shared")
	if err == nil {
		t.Fatal("expected duplicate-token config error")
	}
	if !strings.Contains(err.Error(), "already mapped") {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(err.Error(), "shared") {
		t.Fatalf("error text leaked the token: %v", err)
	}
}

func TestParseSpecAllowsSeveralTokensPerNode(t *testing.T) {
	reg, err := ParseSpec("max3=tok-old\nmax3=tok-new")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	for _, token := range []string{"tok-old", "tok-new"} {
		if node, ok := reg.Resolve(token); !ok || node != "max3" {
			t.Fatalf("Resolve(%q) = %q,%v; want max3,true", token, node, ok)
		}
	}
	if nodes := reg.Nodes(); len(nodes) != 1 || nodes[0] != "max3" {
		t.Fatalf("Nodes() = %v; want [max3]", nodes)
	}
}

func TestParseSpecRejectsBadEntries(t *testing.T) {
	cases := map[string]string{
		"missing separator": "max3",
		"empty token":       "max3=",
		"empty nodeId":      "=tok",
		"reserved nodeId":   "local=tok",
		"invalid character": "max 3=tok",
		"nothing at all":    "# only a comment",
	}
	for name, spec := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSpec(spec); err == nil {
				t.Fatalf("ParseSpec(%q) succeeded; want config error", spec)
			}
		})
	}
}

func TestParseSpecSkipsCommentsAndBlanks(t *testing.T) {
	reg, err := ParseSpec("# nodes\n\nmax3=tok-max3\n\n# trailing\n")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if node, ok := reg.Resolve("tok-max3"); !ok || node != "max3" {
		t.Fatalf("Resolve = %q,%v; want max3,true", node, ok)
	}
}

func TestValidateNodeIDLength(t *testing.T) {
	if err := ValidateNodeID(strings.Repeat("a", 64)); err != nil {
		t.Fatalf("64 chars should be valid: %v", err)
	}
	if err := ValidateNodeID(strings.Repeat("a", 65)); err == nil {
		t.Fatal("65 chars should be rejected")
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.tokens")
	if err := os.WriteFile(path, []byte("# federation nodes\nmax3=tok-max3\nmini.lab=tok-lab\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	reg, err := LoadFile(path)
	if err != nil {
		t.Fatalf("LoadFile: %v", err)
	}
	if node, ok := reg.Resolve("tok-lab"); !ok || node != "mini.lab" {
		t.Fatalf("Resolve = %q,%v; want mini.lab,true", node, ok)
	}

	if _, err := LoadFile(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("missing file should error")
	}
}

func TestDisabledRegistryResolvesNothing(t *testing.T) {
	var reg *Registry
	if reg.Enabled() {
		t.Fatal("nil registry should be disabled")
	}
	if node, ok := reg.Resolve("anything"); ok {
		t.Fatalf("nil registry resolved %q", node)
	}
	if nodes := reg.Nodes(); nodes != nil {
		t.Fatalf("Nodes() = %v; want nil", nodes)
	}
}

func TestContextRoundTrip(t *testing.T) {
	if node, ok := FromContext(context.Background()); ok {
		t.Fatalf("bare context resolved %q", node)
	}
	ctx := WithNode(context.Background(), "max3")
	if node, ok := FromContext(ctx); !ok || node != "max3" {
		t.Fatalf("FromContext = %q,%v; want max3,true", node, ok)
	}
	if node, ok := FromContext(WithNode(context.Background(), "")); ok {
		t.Fatalf("empty nodeId resolved %q", node)
	}
}
