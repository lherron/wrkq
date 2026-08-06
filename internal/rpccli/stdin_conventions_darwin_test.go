//go:build darwin && wrkq_local

package rpccli

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

func TestDashStdinOnTTYErrorsWithoutBlocking(t *testing.T) {
	dbPath, taskID := migratedDBWithTask(t)
	master, slaveName := openPTY(t)
	defer func() { _ = master.Close() }()
	slave, err := os.OpenFile(slaveName, os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pty unavailable (open slave %s): %v", slaveName, err)
	}
	defer func() { _ = slave.Close() }()

	cmd := NewRootCmdFor("wrkq")
	cmd.SetArgs([]string{"--db", dbPath, "comment", "add", taskID, "-m", "-"})
	cmd.SetIn(slave)
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&out)

	err = cmd.Execute()
	if err == nil {
		t.Fatalf("expected TTY stdin error")
	}
	if !strings.Contains(err.Error(), "stdin is a terminal; pipe input or use a heredoc") {
		t.Fatalf("unexpected error: %v", err)
	}
}
