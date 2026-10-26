package bulk

import (
	"bytes"
	"os"
	"testing"
	"time"
)

// A slow parallel operation must render progress at a bounded rate and stop
// its reporter when the workers finish. /dev/null exercises the existing
// character-device detection even in non-interactive CI.
func TestParallelProgressWaitsBetweenUpdates(t *testing.T) {
	terminal, err := os.OpenFile(os.DevNull, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = terminal.Close() }()
	capture, err := os.CreateTemp(t.TempDir(), "progress")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = capture.Close() }()
	stdout, stderr := os.Stdout, os.Stderr
	if !isatty(os.Stdout) {
		os.Stdout = terminal
	}
	os.Stderr = capture
	defer func() { os.Stdout, os.Stderr = stdout, stderr }()
	result := (&Operation{Jobs: 2, ShowProgress: true}).Execute([]string{"a", "b"}, func(string) error { time.Sleep(500 * time.Millisecond); return nil })
	if result.Succeeded != 2 {
		t.Fatalf("result: %+v", result)
	}
	data, err := os.ReadFile(capture.Name())
	if err != nil {
		t.Fatal(err)
	}
	updates := bytes.Count(data, []byte("Processing with"))
	if updates < 1 || updates > 10 {
		t.Fatalf("500ms operation rendered %d progress updates; want 1..10", updates)
	}
}
