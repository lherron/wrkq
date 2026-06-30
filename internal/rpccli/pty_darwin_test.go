//go:build darwin

package rpccli

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"unsafe"
)

// darwin /dev/ptmx ioctls (grantpt/unlockpt/ptsname equivalents). Used ONLY by
// the parity harness to attach a real terminal to a CLI's stdout so the legacy
// TTY-only render path (e.g. `relation ls` table) can be byte-compared against
// the mirror. No third-party pty dependency is pulled in for this.
const (
	tiocPtyGrant = 0x20007454 // TIOCPTYGRANT
	tiocPtyGname = 0x40807453 // TIOCPTYGNAME
	tiocPtyUnlk  = 0x20007452 // TIOCPTYUNLK
)

func ptyIoctl(fd, req, arg uintptr) syscall.Errno {
	_, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, req, arg)
	return e
}

// openPTY allocates a pseudo-terminal pair (master, slave-name).
func openPTY(t *testing.T) (master *os.File, slaveName string) {
	t.Helper()
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pty unavailable (open /dev/ptmx): %v", err)
	}
	if e := ptyIoctl(ptmx.Fd(), tiocPtyGrant, 0); e != 0 {
		_ = ptmx.Close()
		t.Skipf("pty unavailable (grantpt): %v", e)
	}
	if e := ptyIoctl(ptmx.Fd(), tiocPtyUnlk, 0); e != 0 {
		_ = ptmx.Close()
		t.Skipf("pty unavailable (unlockpt): %v", e)
	}
	buf := make([]byte, 128)
	if e := ptyIoctl(ptmx.Fd(), tiocPtyGname, uintptr(unsafe.Pointer(&buf[0]))); e != 0 {
		_ = ptmx.Close()
		t.Skipf("pty unavailable (ptsname): %v", e)
	}
	name := string(buf)
	for i, b := range buf {
		if b == 0 {
			name = string(buf[:i])
			break
		}
	}
	return ptmx, name
}

// runCLIOnTTY runs a CLI with stdout+stderr attached to a real pseudo-terminal,
// so isStdoutTTY(os.Stdout) reports true inside the process. Stdout and stderr
// share the pty (the legacy relation-ls table writes only stdout), and the
// captured bytes include the terminal's ONLCR \n→\r\n translation — identical
// for both binaries, so byte parity still holds. Returns the captured terminal
// output and the process exit code.
func runCLIOnTTY(t *testing.T, bin, dir string, args []string) (out string, exit int) {
	return runCLIOnTTYEnv(t, bin, dir, args, nil)
}

func runCLIOnTTYEnv(t *testing.T, bin, dir string, args []string, extraEnv []string) (out string, exit int) {
	t.Helper()
	master, slaveName := openPTY(t)
	defer func() { _ = master.Close() }()

	slave, err := os.OpenFile(slaveName, os.O_RDWR, 0)
	if err != nil {
		t.Skipf("pty unavailable (open slave %s): %v", slaveName, err)
	}

	full := append([]string{"--db", filepath.Join(dir, "wrkq.db"), "--as", "agent:local-human"}, args...)
	cmd := exec.Command(bin, full...)
	cmd.Dir = dir
	cmd.Env = append(hermeticEnv(), extraEnv...)
	// stdout+stderr on the pty slave is all that's needed: isStdoutTTY only checks
	// that the *os.File reports a char device, which any pty slave does. No
	// controlling-terminal session is required, so we avoid Setctty/Setsid (those
	// fail under the sandbox's fork/exec on a non-stdin pty fd).
	cmd.Stdin = slave
	cmd.Stdout = slave
	cmd.Stderr = slave

	var buf []byte
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); buf, _ = io.ReadAll(master) }()

	runErr := cmd.Run()
	_ = slave.Close()
	wg.Wait()

	exit = 0
	if runErr != nil {
		if ee, ok := runErr.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			t.Fatalf("run %s on tty: %v", bin, runErr)
		}
	}
	return string(buf), exit
}
