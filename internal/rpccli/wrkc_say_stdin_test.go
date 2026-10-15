//go:build wrkq_local

package rpccli

import (
	"bytes"
	"io"
	"strings"
	"testing"
	"time"
)

// TestWrkcSayRefusesABodyShapedRefBeforeReadingStdin: under an agent harness
// stdin is an open socket that never sends EOF, so a quoted body passed where
// the ref goes must be refused before the body read, never park in read().
func TestWrkcSayRefusesABodyShapedRefBeforeReadingStdin(t *testing.T) {
	f := newWrkcFixture(t)
	stdin, stdinWriter := io.Pipe()
	defer func() { _ = stdinWriter.Close() }()
	done := make(chan error, 1)
	go func() {
		cmd := NewWrkcRootCmd()
		cmd.SetArgs([]string{"--db", f.dbPath, "--principal-ref", "agent:alice",
			"say", "--to", "bob@wrkc-proj:primary", "--fyi", "a long quoted body"})
		cmd.SetIn(stdin)
		var output bytes.Buffer
		cmd.SetOut(&output)
		cmd.SetErr(&output)
		done <- cmd.Execute()
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "not a room ref") {
			t.Fatalf("want a not-a-room-ref refusal, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("say blocked reading stdin instead of refusing the body-shaped ref")
	}
}
