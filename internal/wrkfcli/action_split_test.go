package wrkfcli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestActionCommandBuilderIsSplitOutOfRoot(t *testing.T) {
	// T-05622 red bar: root.go should wire the action command, while the
	// action command builder implementation lives in a focused wrkfcli file.
	rootSource, err := os.ReadFile("root.go")
	if err != nil {
		t.Fatalf("read root.go: %v", err)
	}
	root := string(rootSource)

	if !strings.Contains(root, "rootCmd.AddCommand(actionCmd())") {
		t.Fatalf("root.go should keep root wiring for actionCmd")
	}
	if strings.Contains(root, "func actionCmd(") {
		t.Fatalf("root.go still defines actionCmd; move wrkf action command construction to a focused file")
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob wrkfcli sources: %v", err)
	}
	for _, file := range files {
		if file == "root.go" || strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if strings.Contains(string(source), "func actionCmd(") {
			return
		}
	}

	t.Fatalf("actionCmd should be defined in a non-root wrkfcli source file")
}
