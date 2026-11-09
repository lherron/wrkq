//go:build wrkq_local

package rpccli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// T-12766: rewording a server refusal must keep its typed code. These are the
// shapes that turned a *Error into a plain error (so --json printed WRKQ_ERROR
// with the code folded into the message); use rpcRefusal / rewordRefusal.
var flattenedRefusal = regexp.MustCompile(
	`errors\.New\((rpcMessage\(|\w+\.Message\)|\w+\.Error\(\)\))` +
		`|fmt\.Errorf\("[^"]*", [^)]*\w+\.Message\)` +
		`|fmt\.Errorf\("[^"]*", rpcMessage\(`)

func TestNoFlattenedRPCRefusals(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(src), "\n") {
			if m := flattenedRefusal.FindString(line); m != "" {
				t.Errorf("%s:%d flattens an RPC refusal (%s); use rpcRefusal or rewordRefusal", f, i+1, m)
			}
		}
	}
}
