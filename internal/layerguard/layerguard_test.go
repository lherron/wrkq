package layerguard_test

import (
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/layerguard"
)

// checkViolationMessage asserts §3 diagnostic requirements on a violation message.
func checkViolationMessage(t *testing.T, msg string) {
	t.Helper()
	if msg == "" {
		t.Error("violation Message must be non-empty")
		return
	}
	// Must carry the exact ARCH-EXCEPTION syntax so the reader knows what to write.
	if !strings.Contains(msg, "ARCH-EXCEPTION(T-") {
		t.Errorf("message must contain exact ARCH-EXCEPTION syntax; got: %s", msg)
	}
	// Must show fix direction.
	if !strings.Contains(msg, "FIX") && !strings.Contains(msg, "fix") {
		t.Errorf("message must contain FIX/fix guidance; got: %s", msg)
	}
	// Must explain WHY.
	if !strings.Contains(msg, "WHY") && !strings.Contains(msg, "why") && !strings.Contains(msg, "layer boundary") {
		t.Errorf("message must contain WHY rationale; got: %s", msg)
	}
	// Must include do-not-hide instruction.
	if !strings.Contains(msg, "do not hide") && !strings.Contains(msg, "do-not-hide") && !strings.Contains(msg, "DO NOT") {
		t.Errorf("message must include do-not-hide instruction; got: %s", msg)
	}
}

// TestCheckPackages_DirectEdge_DomainCobra verifies that a direct import from a governed
// domain package to a forbidden target (cobra) produces exactly one violation with a
// single-hop chain carrying file:line annotation.
func TestCheckPackages_DirectEdge_DomainCobra(t *testing.T) {
	// Fixture: domain imports cobra directly
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/domain", Dir: "testdata/domain-direct/domain"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "domain-no-cobra",
			Sources:   []string{"fixture/domain"},
			Forbidden: []string{"github.com/spf13/cobra"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(result.Violations), result.Violations)
	}
	v := result.Violations[0]
	// Chain must have exactly 1 hop (direct edge)
	if len(v.Chain) != 1 {
		t.Errorf("expected chain length 1 (direct edge), got %d: %v", len(v.Chain), v.Chain)
	}
	hop := v.Chain[0]
	if hop.File == "" || hop.Line <= 0 {
		t.Errorf("hop must carry file:line; got File=%q Line=%d", hop.File, hop.Line)
	}
	checkViolationMessage(t, v.Message)
}

// TestCheckPackages_TransitiveEdge_DomainIntermediateCobra verifies that a transitive
// import chain (domain→intermediate→cobra) produces one violation with a 2-hop chain.
// This is the barrel/bypass case: a direct-imports-only guard would miss it.
func TestCheckPackages_TransitiveEdge_DomainIntermediateCobra(t *testing.T) {
	// Fixture: domain imports intermediate, intermediate imports cobra
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/domain", Dir: "testdata/domain-transitive/domain"},
		{ImportPath: "fixture/intermediate", Dir: "testdata/domain-transitive/intermediate"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "domain-no-cobra",
			Sources:   []string{"fixture/domain"},
			Forbidden: []string{"github.com/spf13/cobra"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(result.Violations), result.Violations)
	}
	v := result.Violations[0]
	// Chain must have 2 hops (domain→intermediate→cobra) — FULL chain printed
	if len(v.Chain) != 2 {
		t.Errorf("expected chain length 2 (transitive chain), got %d: %v", len(v.Chain), v.Chain)
	}
	// Every hop must carry file:line
	for i, hop := range v.Chain {
		if hop.File == "" || hop.Line <= 0 {
			t.Errorf("chain[%d] must carry file:line; got File=%q Line=%d", i, hop.File, hop.Line)
		}
	}
	checkViolationMessage(t, v.Message)
}

// TestCheckPackages_TransitiveEdge_WorkflowToCli verifies that workflow→cli boundary
// violations are detected, including transitive chains.
func TestCheckPackages_TransitiveEdge_WorkflowToCli(t *testing.T) {
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/workflow", Dir: "testdata/workflow-cli/workflow"},
		{ImportPath: "fixture/cli", Dir: "testdata/workflow-cli/cli"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "workflow-no-cli",
			Sources:   []string{"fixture/workflow"},
			Forbidden: []string{"fixture/cli"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected 1 violation, got %d: %+v", len(result.Violations), result.Violations)
	}
	v := result.Violations[0]
	if len(v.Chain) < 1 {
		t.Errorf("expected chain with at least 1 hop, got %d", len(v.Chain))
	}
	for i, hop := range v.Chain {
		if hop.File == "" || hop.Line <= 0 {
			t.Errorf("chain[%d] must carry file:line; got File=%q Line=%d", i, hop.File, hop.Line)
		}
	}
	checkViolationMessage(t, v.Message)
}

// TestCheckPackages_WrkfrpcOwnership_UnauthorizedFails verifies that an unauthorized
// package importing wrkfrpc produces a violation.
func TestCheckPackages_WrkfrpcOwnership_UnauthorizedFails(t *testing.T) {
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/unauthorized", Dir: "testdata/wrkfrpc-ownership/unauthorized"},
		{ImportPath: "fixture/wrkfrpc", Dir: "testdata/wrkfrpc-ownership/wrkfrpc"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "wrkfrpc-ownership",
			Sources:   []string{"fixture"},
			Forbidden: []string{"fixture/wrkfrpc"},
			Except:    []string{"fixture/wrkfcli"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("expected 1 violation for unauthorized→wrkfrpc, got %d: %+v", len(result.Violations), result.Violations)
	}
	checkViolationMessage(t, result.Violations[0].Message)
}

// TestCheckPackages_WrkfrpcOwnership_WrkfcliPasses verifies that the excepted package
// (wrkfcli) importing wrkfrpc produces no violation.
func TestCheckPackages_WrkfrpcOwnership_WrkfcliPasses(t *testing.T) {
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/wrkfcli", Dir: "testdata/wrkfrpc-ownership/wrkfcli"},
		{ImportPath: "fixture/wrkfrpc", Dir: "testdata/wrkfrpc-ownership/wrkfrpc"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "wrkfrpc-ownership",
			Sources:   []string{"fixture"},
			Forbidden: []string{"fixture/wrkfrpc"},
			Except:    []string{"fixture/wrkfcli"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("wrkfcli→wrkfrpc must PASS (excepted), got violations: %+v", result.Violations)
	}
}

// TestCheckPackages_ArchException_ValidPasses verifies that a valid ARCH-EXCEPTION
// comment on the import line suppresses the violation and increments ExceptionsByRule.
func TestCheckPackages_ArchException_ValidPasses(t *testing.T) {
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/source", Dir: "testdata/exceptions/valid"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "source-no-cobra",
			Sources:   []string{"fixture/source"},
			Forbidden: []string{"github.com/spf13/cobra"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 0 {
		t.Errorf("valid ARCH-EXCEPTION must suppress violation, got: %+v", result.Violations)
	}
	if result.ExceptionsByRule["source-no-cobra"] != 1 {
		t.Errorf("ExceptionsByRule[source-no-cobra]: expected 1, got %d", result.ExceptionsByRule["source-no-cobra"])
	}
}

// TestCheckPackages_ArchException_MalformedTicketFails verifies that a malformed
// ARCH-EXCEPTION (missing ticket number) does not authorize the violation.
func TestCheckPackages_ArchException_MalformedTicketFails(t *testing.T) {
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/source", Dir: "testdata/exceptions/malformed-ticket"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "source-no-cobra",
			Sources:   []string{"fixture/source"},
			Forbidden: []string{"github.com/spf13/cobra"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("malformed ARCH-EXCEPTION must still be a violation, got %d violations", len(result.Violations))
	}
	checkViolationMessage(t, result.Violations[0].Message)
}

// TestCheckPackages_ArchException_EmptyReasonFails verifies that a malformed
// ARCH-EXCEPTION (empty reason) does not authorize the violation.
func TestCheckPackages_ArchException_EmptyReasonFails(t *testing.T) {
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/source", Dir: "testdata/exceptions/malformed-reason"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "source-no-cobra",
			Sources:   []string{"fixture/source"},
			Forbidden: []string{"github.com/spf13/cobra"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("empty-reason ARCH-EXCEPTION must still be a violation, got %d violations", len(result.Violations))
	}
	checkViolationMessage(t, result.Violations[0].Message)
}

// TestCheckPackages_ArchException_DownstreamOnlyFails verifies that an ARCH-EXCEPTION
// on a downstream hop (middle→cobra) does NOT authorize the governed source (source→middle→cobra).
func TestCheckPackages_ArchException_DownstreamOnlyFails(t *testing.T) {
	packages := []layerguard.PackageEntry{
		{ImportPath: "fixture/source", Dir: "testdata/exceptions/downstream-only/source"},
		{ImportPath: "fixture/middle", Dir: "testdata/exceptions/downstream-only/middle"},
	}
	cfg := layerguard.Config{
		Rules: []layerguard.Rule{{
			ID:        "source-no-cobra",
			Sources:   []string{"fixture/source"},
			Forbidden: []string{"github.com/spf13/cobra"},
		}},
	}
	result, err := layerguard.CheckPackages(packages, cfg)
	if err != nil {
		t.Fatalf("CheckPackages: %v", err)
	}
	if len(result.Violations) != 1 {
		t.Fatalf("downstream-only exception must NOT authorize source, got %d violations", len(result.Violations))
	}
	checkViolationMessage(t, result.Violations[0].Message)
}
