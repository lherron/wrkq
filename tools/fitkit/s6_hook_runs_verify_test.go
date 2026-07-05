package fitkit

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestS6HookRunsVerifyUsesLocalProvenanceAndRealSmoke(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node is required to exercise the fitkit entrypoint: %v", err)
	}

	sourceRoot := repoRoot(t)
	tempRoot := t.TempDir()

	copyFile(t,
		filepath.Join(sourceRoot, "tools/fitkit/s6-hook-runs-verify.mjs"),
		filepath.Join(tempRoot, "tools/fitkit/s6-hook-runs-verify.mjs"),
	)
	writeFile(t, filepath.Join(tempRoot, "tools/fitkit/s6-hook-runs-verify.provenance.json"), `{
  "guardId": "fit:s6/hook-runs-verify",
  "ownerProject": "wrkq",
  "localSourceFiles": ["tools/fitkit/s6-hook-runs-verify.mjs"],
  "checkedSurfaces": ["Justfile", ".git/hooks/pre-push"],
  "reason": "first-party wrkq guard replacing the vendored archagent artifact"
}`)
	writeFile(t, filepath.Join(tempRoot, "Justfile"), "verify:\n  go test ./...\n")
	writeFile(t, filepath.Join(tempRoot, ".git/hooks/pre-push"), "#!/usr/bin/env bash\nset -euo pipefail\njust verify\n")

	// Regression bar for T-05623: the guard is now first-party wrkq code. A repo
	// with the local provenance manifest and a real installed hook should pass
	// without requiring the old vendored pin file.
	cmd := exec.Command("node", "tools/fitkit/s6-hook-runs-verify.mjs", "--root", tempRoot, "--json")
	cmd.Dir = tempRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected local-provenance guard to pass, got %v:\n%s", err, output)
	}

	var payload map[string]any
	if err := json.Unmarshal(output, &payload); err != nil {
		t.Fatalf("expected JSON output, got parse error %v:\n%s", err, output)
	}
	if payload["id"] != "fit:s6/hook-runs-verify" {
		t.Fatalf("expected guard id in JSON, got %#v", payload["id"])
	}
	result, ok := payload["result"].(map[string]any)
	if !ok || result["level"] != "PRESENT" || result["exercise"] != "EXERCISED" {
		t.Fatalf("expected passing exercised guard result, got %#v", payload["result"])
	}
	smoke, ok := payload["smoke"].(map[string]any)
	if !ok || smoke["expected"] != "FAIL" || smoke["observed"] != "FAIL" {
		t.Fatalf("expected real negative-smoke result in JSON, got %#v", payload["smoke"])
	}
	if payload["provenanceIdentifier"] != "fit:s6/hook-runs-verify@wrkq-local" {
		t.Fatalf("expected local provenance identifier, got %#v", payload["provenanceIdentifier"])
	}
	if _, hasPin := payload["pin"]; hasPin {
		t.Fatalf("expected local provenance JSON instead of vendored pin output, got pin=%#v", payload["pin"])
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

func writeFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func copyFile(t *testing.T, src string, dst string) {
	t.Helper()
	bytes, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, dst, strings.ReplaceAll(string(bytes), "\r\n", "\n"))
}
