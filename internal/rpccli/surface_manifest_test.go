package rpccli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLISurfaceManifestMatchesCobraTree(t *testing.T) {
	// The checked-in artifact records canonical flag defaults, not values from a
	// developer's daemon environment. Earlier package tests execute commands that
	// call config.Load, whose dotenv loader can populate these variables for the
	// rest of the test process.
	for _, key := range []string{"WRKQD_ADDR", "WRKQD_UNIX", "WRKQD_TOKEN"} {
		t.Setenv(key, "")
	}

	generated, err := BuildCLISurfaceManifestJSON("wrkq")
	if err != nil {
		t.Fatalf("generate CLI surface manifest: %v", err)
	}
	generated = append(generated, '\n')

	root := repoRootFromTest(t)
	path := filepath.Join(root, "internal", "rpccli", "cli_surface_manifest.json")
	checkedIn, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read checked-in CLI surface manifest: %v", err)
	}

	if !bytes.Equal(checkedIn, generated) {
		t.Fatalf("checked-in CLI surface manifest is stale; regenerate with go generate ./internal/rpccli")
	}
	if !bytes.Equal(EmbeddedCLISurfaceManifestJSON, checkedIn) {
		t.Fatal("embedded CLI surface manifest does not match checked-in manifest")
	}
}

func TestCLISurfaceManifestRootHelpVisibility(t *testing.T) {
	var manifest CLISurfaceManifest
	if err := json.Unmarshal(EmbeddedCLISurfaceManifestJSON, &manifest); err != nil {
		t.Fatalf("parse embedded CLI surface manifest: %v", err)
	}

	root := NewRootCmdFor("wrkq")
	var stdout bytes.Buffer
	root.SetArgs([]string{"--help"})
	root.SetOut(&stdout)
	root.SetErr(&stdout)
	if err := root.Execute(); err != nil {
		t.Fatalf("root help returned error: %v", err)
	}
	output := stdout.String()

	for _, command := range manifest.Root.Commands {
		if command.Hidden || !command.Available {
			continue
		}
		if !strings.Contains(output, command.Name) {
			t.Fatalf("root help missing manifest command %q:\n%s", command.Name, output)
		}
	}
}
