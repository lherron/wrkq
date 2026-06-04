package cli

import (
	"bytes"
	"testing"

	"github.com/lherron/wrkq/internal/config"
	"github.com/spf13/cobra"
)

func newOutputTestCommand(t *testing.T, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	cmd.Flags().Bool("json", false, "")
	cmd.Flags().Bool("ndjson", false, "")
	cmd.Flags().Bool("human", false, "")
	cmd.Flags().Bool("porcelain", false, "")
	cmd.Flags().Bool("yaml", false, "")
	cmd.Flags().Bool("tsv", false, "")
	cmd.Flags().String("output", "", "")
	if len(args) > 0 {
		if err := cmd.Flags().Parse(args); err != nil {
			t.Fatalf("parse flags: %v", err)
		}
	}
	return cmd
}

func TestResolveOutputModePrecedence(t *testing.T) {
	cmd := newOutputTestCommand(t, "--json", "--output", "human")
	sel, err := resolveOutputMode(cmd, &config.Config{Output: "ndjson"}, outputShapeList, outputResolveOptions{})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if sel.Mode != outputModeJSON {
		t.Fatalf("explicit alias should win, got %s", sel.Mode)
	}

	cmd = newOutputTestCommand(t, "--output", "human")
	sel, err = resolveOutputMode(cmd, &config.Config{Output: "ndjson"}, outputShapeList, outputResolveOptions{})
	if err != nil {
		t.Fatalf("resolve root output: %v", err)
	}
	if sel.Mode != outputModeHuman {
		t.Fatalf("--output should beat config, got %s", sel.Mode)
	}

	cmd = newOutputTestCommand(t)
	sel, err = resolveOutputMode(cmd, &config.Config{Output: "json"}, outputShapeList, outputResolveOptions{})
	if err != nil {
		t.Fatalf("resolve config: %v", err)
	}
	if sel.Mode != outputModeJSON {
		t.Fatalf("config output should beat shape default, got %s", sel.Mode)
	}
}

func TestResolveOutputModeDefaultsByShape(t *testing.T) {
	cmd := newOutputTestCommand(t)
	sel, err := resolveOutputMode(cmd, &config.Config{}, outputShapeList, outputResolveOptions{})
	if err != nil {
		t.Fatalf("resolve list: %v", err)
	}
	if sel.Mode != outputModeNDJSON {
		t.Fatalf("non-TTY list default = %s, want ndjson", sel.Mode)
	}

	cmd = newOutputTestCommand(t)
	sel, err = resolveOutputMode(cmd, &config.Config{}, outputShapeSingleton, outputResolveOptions{})
	if err != nil {
		t.Fatalf("resolve singleton: %v", err)
	}
	if sel.Mode != outputModeJSON {
		t.Fatalf("non-TTY singleton default = %s, want json", sel.Mode)
	}

	cmd = newOutputTestCommand(t)
	sel, err = resolveOutputMode(cmd, &config.Config{}, outputShapeContent, outputResolveOptions{})
	if err != nil {
		t.Fatalf("resolve content: %v", err)
	}
	if sel.Mode != outputModeRaw {
		t.Fatalf("content default = %s, want raw", sel.Mode)
	}
}

func TestResolveOutputModePorcelainIsModifier(t *testing.T) {
	cmd := newOutputTestCommand(t, "--json", "--porcelain")
	sel, err := resolveOutputMode(cmd, &config.Config{}, outputShapeList, outputResolveOptions{})
	if err != nil {
		t.Fatalf("resolve json porcelain: %v", err)
	}
	if sel.Mode != outputModeJSON || !sel.Stable {
		t.Fatalf("--json --porcelain = (%s,%t), want (json,true)", sel.Mode, sel.Stable)
	}

	cmd = newOutputTestCommand(t, "--output", "porcelain")
	sel, err = resolveOutputMode(cmd, &config.Config{}, outputShapeList, outputResolveOptions{})
	if err != nil {
		t.Fatalf("resolve output porcelain: %v", err)
	}
	if sel.Mode != outputModeNDJSON || !sel.Stable {
		t.Fatalf("--output porcelain list = (%s,%t), want (ndjson,true)", sel.Mode, sel.Stable)
	}
}

func TestResolveOutputModeConflictingAliases(t *testing.T) {
	cmd := newOutputTestCommand(t, "--json", "--ndjson")
	if _, err := resolveOutputMode(cmd, &config.Config{}, outputShapeList, outputResolveOptions{}); err == nil {
		t.Fatal("expected conflicting explicit aliases to fail")
	}
}
