//go:build wrkq_local

package rpccli

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/lherron/wrkq/internal/db"
)

func TestWrkpLogTurnTypesE2E(t *testing.T) {
	dbPath, _ := migratedDBWithTask(t)
	run := func(args ...string) string {
		t.Helper()
		cmd := NewWrkpRootCmd()
		cmd.SetArgs(append([]string{"--db", dbPath, "--as", "agent:wrkp-test"}, args...))
		var out, stderr bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&stderr)
		if err := cmd.Execute(); err != nil {
			t.Fatalf("%v: %v (%s)", args, err, stderr.String())
		}
		return out.String()
	}
	for _, typ := range []string{"session.started", "turn.started", "turn.ended"} {
		run("post", "rpccli-test-proj", "--type", typ, "-m", typ, "--attr", "source=smoke")
	}
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE project_events SET created_at = '2025-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	for _, format := range []string{"--json", "--ndjson"} {
		for _, follow := range []bool{false, true} {
			for _, selection := range []struct {
				args []string
				want []string
			}{
				{nil, []string{"session.started"}},
				{[]string{"--type", "turn.*"}, []string{"turn.started", "turn.ended"}},
				{[]string{"--type", "turn.started"}, []string{"turn.started"}},
				{[]string{"--all-types"}, []string{"session.started", "turn.started", "turn.ended"}},
			} {
				args := []string{"log", "rpccli-test-proj", format}
				if follow {
					args = append(args, "--follow", "--since", "2020-01-01T00:00:00Z", "--before", "2026-01-01T00:00:00Z")
				}
				args = append(args, selection.args...)
				out := run(args...)
				var entries []timelineEntry
				if format == "--json" {
					decoder := json.NewDecoder(strings.NewReader(out))
					for {
						var page []timelineEntry
						err := decoder.Decode(&page)
						if err == io.EOF {
							break
						}
						if err != nil {
							t.Fatal(err)
						}
						entries = append(entries, page...)
					}
				} else {
					for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
						if line == "" {
							continue
						}
						var entry timelineEntry
						if err := json.Unmarshal([]byte(line), &entry); err != nil {
							t.Fatal(err)
						}
						entries = append(entries, entry)
					}
				}
				want := append([]string(nil), selection.want...)
				if !follow {
					for i, j := 0, len(want)-1; i < j; i, j = i+1, j-1 {
						want[i], want[j] = want[j], want[i]
					}
				}
				if len(entries) != len(want) {
					t.Fatalf("%v: got %s, want %v", args, out, want)
				}
				for i, entry := range entries {
					if entry.ProjectEvent == nil || entry.ProjectEvent.Type != want[i] {
						t.Fatalf("%v: got %s, want %v", args, out, want)
					}
				}
			}
		}
	}
	if out := run("types", "rpccli-test-proj", "--json"); !strings.Contains(out, "turn.started") || !strings.Contains(out, "turn.ended") {
		t.Fatalf("types lost turn facts: %s", out)
	}
}
